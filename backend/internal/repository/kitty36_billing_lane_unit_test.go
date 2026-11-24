//go:build unit

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestKitty36BillingBatchUnknownCommitRequiresEveryReceipt(t *testing.T) {
	for _, state := range []string{"applied", "complete", "pending", "missing", "invalid_result", "null_result"} {
		t.Run(state, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			r := &usageBillingRepository{db: db}
			keys := []service.UsageBillingEventKey{{RequestID: "a", APIKeyID: 2}, {RequestID: "b", APIKeyID: 2}}
			mock.ExpectBegin()
			for _, key := range keys {
				cmd := service.UsageBillingCommand{RequestID: key.RequestID, APIKeyID: 2, UserID: 1, AccountID: 3, BalanceCost: 1}
				cmd.Normalize()
				e := service.UsageBillingEvent{Version: 1, Command: cmd, Usage: service.UsageLog{RequestID: key.RequestID, APIKeyID: 2, UserID: 1, AccountID: 3, CreatedAt: time.Now()}}
				payload, err := json.Marshal(e)
				require.NoError(t, err)
				mock.ExpectQuery("SELECT schema_version,payload,state,result,request_fingerprint").WithArgs(key.RequestID, key.APIKeyID).WillReturnRows(sqlmock.NewRows([]string{"schema_version", "payload", "state", "result", "request_fingerprint"}).AddRow(1, payload, "pending", nil, cmd.RequestFingerprint))
				mock.ExpectQuery("INSERT INTO usage_billing_dedup").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
				mock.ExpectQuery("SELECT request_fingerprint.*FROM usage_billing_dedup_archive").WillReturnRows(sqlmock.NewRows([]string{"request_fingerprint"}))
				mock.ExpectQuery("SELECT EXISTS.*FROM usage_logs").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
				mock.ExpectQuery(conditionalBalanceDeductSQL).WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(99))
				mock.ExpectExec("INSERT INTO usage_logs").WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectExec("UPDATE usage_billing_events SET state='applied'").WillReturnResult(sqlmock.NewResult(0, 1))
			}
			mock.ExpectCommit().WillReturnError(errors.New("lost response"))
			mock.ExpectQuery("SELECT state,result FROM usage_billing_events").WithArgs("a", 2).WillReturnRows(sqlmock.NewRows([]string{"state", "result"}).AddRow("applied", `{"Applied":true}`))
			last := mock.ExpectQuery("SELECT state,result FROM usage_billing_events").WithArgs("b", 2)
			switch state {
			case "missing":
				last.WillReturnError(sql.ErrNoRows)
			case "invalid_result":
				last.WillReturnRows(sqlmock.NewRows([]string{"state", "result"}).AddRow("applied", `{broken`))
			case "null_result":
				last.WillReturnRows(sqlmock.NewRows([]string{"state", "result"}).AddRow("applied", `null`))
			default:
				last.WillReturnRows(sqlmock.NewRows([]string{"state", "result"}).AddRow(state, `{"Applied":true}`))
			}
			results, err := r.applyStagedUsageBillingBatch(context.Background(), keys, 1)
			if state == "applied" || state == "complete" {
				require.NoError(t, err)
				require.Len(t, results, 2)
				require.Equal(t, state == "applied", results[1].NeedsCache)
			} else {
				require.ErrorIs(t, err, service.ErrUsageBillingOutcomeUnknown)
				require.Nil(t, results)
			}
			require.NoError(t, mock.ExpectationsWereMet(), "no individual debit or legacy fallback after unknown commit")
		})
	}
}

func TestKitty36BillingLaneBoundsAndPreExecutionCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &usageBillingRepository{}
	_, err := r.ApplyStagedUsageBillingForUser(ctx, service.UsageBillingEventKey{}, 1)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, r.lanes)
	for _, global := range []bool{true, false} {
		r = &usageBillingRepository{lanes: map[int64]*kittyUsageBillingLane{1: {pending: make([]*kittyUsageBillingCall, kittyUsageBillingMaxQueuedPerUser)}}}
		if global {
			r.lanePending = kittyUsageBillingMaxQueued
		}
		_, err = r.ApplyStagedUsageBillingForUser(context.Background(), service.UsageBillingEventKey{RequestID: "kept-in-journal", APIKeyID: 2}, 1)
		require.ErrorIs(t, err, errKittyUsageBillingLaneFull)
		require.Len(t, r.lanes[1].pending, kittyUsageBillingMaxQueuedPerUser)
	}
	call := &kittyUsageBillingCall{ctx: ctx, done: make(chan kittyUsageBillingOutcome, 1)}
	lane := &kittyUsageBillingLane{pending: []*kittyUsageBillingCall{call}}
	r = &usageBillingRepository{lanes: map[int64]*kittyUsageBillingLane{1: lane}, lanePending: 1}
	r.drainKittyUsageBillingLane(1, lane)
	require.ErrorIs(t, (<-call.done).err, context.Canceled)
	require.Empty(t, r.lanes)
	require.Zero(t, r.lanePending)
}
