package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const kittyUsageBillingMaxBatch = 32
const kittyUsageBillingMaxQueuedPerUser = 256
const kittyUsageBillingMaxQueued = 4096

var errKittyUsageBillingLaneFull = errors.New("durable billing lane full; staged receipt remains recoverable")

type kittyUsageBillingLane struct{ pending []*kittyUsageBillingCall }
type kittyUsageBillingCall struct {
	ctx  context.Context
	key  service.UsageBillingEventKey
	done chan kittyUsageBillingOutcome
}
type kittyUsageBillingOutcome struct {
	recovery *service.UsageBillingRecovery
	err      error
}

// Only staged receipts enter a lane. Every caller still waits for its own durable
// result. There is no legacy-money fallback, including on an uncertain COMMIT.
func (r *usageBillingRepository) ApplyStagedUsageBillingForUser(ctx context.Context, key service.UsageBillingEventKey, userID int64) (*service.UsageBillingRecovery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, service.ErrUsageBillingRequestConflict
	}
	call := &kittyUsageBillingCall{ctx: ctx, key: key, done: make(chan kittyUsageBillingOutcome, 1)}
	r.laneMu.Lock()
	if r.lanes == nil {
		r.lanes = make(map[int64]*kittyUsageBillingLane)
	}
	lane := r.lanes[userID]
	if r.lanePending >= kittyUsageBillingMaxQueued || (lane != nil && len(lane.pending) >= kittyUsageBillingMaxQueuedPerUser) {
		r.laneMu.Unlock()
		return nil, errKittyUsageBillingLaneFull
	}
	created := lane == nil
	if created {
		lane = &kittyUsageBillingLane{}
		r.lanes[userID] = lane
	}
	lane.pending = append(lane.pending, call)
	r.lanePending++
	r.laneMu.Unlock()
	if created {
		go r.drainKittyUsageBillingLane(userID, lane)
	}
	// Do not return an ambiguous cancellation and then run an individual debit.
	// The bounded database call (or the untouched journal on pre-queue expiry)
	// resolves each receipt before releasing the caller's accounting reservation.
	out := <-call.done
	return out.recovery, out.err
}

func (r *usageBillingRepository) drainKittyUsageBillingLane(userID int64, lane *kittyUsageBillingLane) {
	for {
		r.laneMu.Lock()
		if len(lane.pending) == 0 {
			delete(r.lanes, userID)
			r.laneMu.Unlock()
			return
		}
		n := min(len(lane.pending), kittyUsageBillingMaxBatch)
		batch := append([]*kittyUsageBillingCall(nil), lane.pending[:n]...)
		clear(lane.pending[:n])
		lane.pending = lane.pending[n:]
		r.lanePending -= n
		r.laneMu.Unlock()
		active := make([]*kittyUsageBillingCall, 0, n)
		for _, call := range batch {
			if err := call.ctx.Err(); err != nil {
				call.done <- kittyUsageBillingOutcome{err: err}
			} else {
				active = append(active, call)
			}
		}
		if len(active) == 0 {
			continue
		}
		ctx, cancel := context.WithTimeout(active[0].ctx, 15*time.Second)
		keys := make([]service.UsageBillingEventKey, len(active))
		for i, call := range active {
			keys[i] = call.key
		}
		results, err := r.applyStagedUsageBillingBatch(ctx, keys, userID)
		cancel()
		for i, call := range active {
			var recovery *service.UsageBillingRecovery
			if err == nil {
				recovery = results[i]
			}
			call.done <- kittyUsageBillingOutcome{recovery: recovery, err: err}
		}
	}
}

// Shared transaction for one user's receipts, preserving every original effect.
// A statement failure rolls the entire batch back. Failed/unknown batches stay
// in the journal for recovery; they are never replayed immediately as singles.
func (r *usageBillingRepository) applyStagedUsageBillingBatch(ctx context.Context, keys []service.UsageBillingEventKey, userID int64) ([]*service.UsageBillingRecovery, error) {
	if len(keys) == 0 || len(keys) > kittyUsageBillingMaxBatch || userID <= 0 {
		return nil, service.ErrUsageBillingRequestConflict
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	results := make([]*service.UsageBillingRecovery, len(keys))
	for i, key := range keys {
		results[i], err = r.applyStagedUsageBillingTx(ctx, tx, key, userID)
		if err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err == nil {
		return results, nil
	}
	// Query a new connection for all durable receipts before acknowledging any
	// part of an ambiguous transaction. Never infer rollback from a network error.
	check, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for i, key := range keys {
		var state string
		var raw []byte
		checkErr := r.db.QueryRowContext(check, `SELECT state,result FROM usage_billing_events WHERE request_id=$1 AND api_key_id=$2`, key.RequestID, key.APIKeyID).Scan(&state, &raw)
		if checkErr != nil || (state != "applied" && state != "complete" && state != "legacy") {
			return nil, errors.Join(service.ErrUsageBillingOutcomeUnknown, err, checkErr)
		}
		if state != "legacy" && (len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))) {
			return nil, errors.Join(service.ErrUsageBillingOutcomeUnknown, err)
		}
		if len(raw) > 0 {
			if checkErr = json.Unmarshal(raw, &results[i].Result); checkErr != nil {
				return nil, errors.Join(service.ErrUsageBillingOutcomeUnknown, err, checkErr)
			}
		}
		results[i].NeedsCache = state == "applied"
	}
	return results, nil
}
