//go:build integration

package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func kitty36StageBills(t *testing.T, r *usageBillingRepository, cmd service.UsageBillingCommand, u service.UsageLog, n int) []service.UsageBillingEventKey {
	t.Helper()
	keys := make([]service.UsageBillingEventKey, n)
	for i := range n {
		c, usage := cmd, u
		c.RequestID = uuid.NewString()
		c.RequestFingerprint = ""
		c.BalanceCost = .25
		c.APIKeyQuotaCost = .25
		usage.RequestID = c.RequestID
		usage.ActualCost = .25
		usage.TotalCost = .25
		require.NoError(t, r.StageUsageBilling(context.Background(), &c, &usage))
		keys[i] = service.UsageBillingEventKey{RequestID: c.RequestID, APIKeyID: c.APIKeyID}
	}
	return keys
}

func TestKitty36BillingLaneCombinesDurableReceiptsAndKeepsExactMoney(t *testing.T) {
	ctx := context.Background()
	r, cmd, usage, _ := durableBillingFixture(t)
	keys := kitty36StageBills(t, r, cmd, usage, 40)
	name := "lane_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := r.db.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE %s(request_id text,txn bigint);
	CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
	 IF NEW.user_id=%d THEN INSERT INTO %s VALUES(NEW.request_id,txid_current());END IF;RETURN NEW;END;$$;
	CREATE TRIGGER %s AFTER INSERT ON usage_logs FOR EACH ROW EXECUTE FUNCTION %s()`, name, name, cmd.UserID, name, name, name))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = r.db.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON usage_logs;DROP FUNCTION IF EXISTS %s();DROP TABLE IF EXISTS %s", name, name, name))
	})
	block, err := r.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer block.Rollback()
	_, err = block.ExecContext(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", cmd.UserID)
	require.NoError(t, err)
	errs := make(chan error, len(keys))
	go func() { _, err := r.ApplyStagedUsageBillingForUser(ctx, keys[0], cmd.UserID); errs <- err }()
	require.Eventually(t, func() bool {
		r.laneMu.Lock()
		defer r.laneMu.Unlock()
		return r.lanes[cmd.UserID] != nil && r.lanePending == 0
	}, 2*time.Second, time.Millisecond)
	for _, key := range keys[1:] {
		go func(k service.UsageBillingEventKey) {
			_, err := r.ApplyStagedUsageBillingForUser(ctx, k, cmd.UserID)
			errs <- err
		}(key)
	}
	require.Eventually(t, func() bool { r.laneMu.Lock(); defer r.laneMu.Unlock(); return r.lanePending == 39 }, 2*time.Second, time.Millisecond)
	require.NoError(t, block.Commit())
	for range keys {
		select {
		case err := <-errs:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("billing lane did not finish")
		}
	}
	var balance, quota string
	var logs, txns int
	require.NoError(t, r.db.QueryRowContext(ctx, "SELECT balance::text FROM users WHERE id=$1", cmd.UserID).Scan(&balance))
	require.NoError(t, r.db.QueryRowContext(ctx, "SELECT quota_used::text FROM api_keys WHERE id=$1", cmd.APIKeyID).Scan(&quota))
	require.NoError(t, r.db.QueryRowContext(ctx, fmt.Sprintf("SELECT count(*),count(DISTINCT txn) FROM %s", name)).Scan(&logs, &txns))
	require.Equal(t, "90.00000000", balance)
	require.Equal(t, "10.00000000", quota)
	require.Equal(t, 40, logs)
	require.LessOrEqual(t, txns, 3, "hot-user receipts share transactions")
	for _, key := range keys {
		recovered, err := r.ApplyStagedUsageBillingForUser(ctx, key, cmd.UserID)
		require.NoError(t, err)
		require.False(t, recovered.Result.Applied)
	}
	require.NoError(t, r.db.QueryRowContext(ctx, "SELECT balance::text FROM users WHERE id=$1", cmd.UserID).Scan(&balance))
	require.Equal(t, "90.00000000", balance)
	wrong, err := r.applyStagedUsageBillingBatch(ctx, keys[:2], cmd.UserID+1)
	require.ErrorIs(t, err, service.ErrUsageBillingRequestConflict)
	require.Nil(t, wrong)
}

func TestKitty36BillingBatchFailureRollsBackEveryReceiptAndRecovers(t *testing.T) {
	ctx := context.Background()
	r, cmd, usage, _ := durableBillingFixture(t)
	keys := kitty36StageBills(t, r, cmd, usage, 2)
	name := "lane_fail_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := r.db.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN IF NEW.request_id='%s' THEN RAISE EXCEPTION 'injected second receipt failure';END IF;RETURN NEW;END;$$;
	CREATE TRIGGER %s BEFORE INSERT ON usage_logs FOR EACH ROW EXECUTE FUNCTION %s()`, name, keys[1].RequestID, name, name))
	require.NoError(t, err)
	cleanup := func() {
		_, _ = r.db.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON usage_logs;DROP FUNCTION IF EXISTS %s()", name, name))
	}
	t.Cleanup(cleanup)
	_, err = r.applyStagedUsageBillingBatch(ctx, keys, cmd.UserID)
	require.Error(t, err)
	var balance string
	var logs, claims int
	require.NoError(t, r.db.QueryRowContext(ctx, "SELECT balance::text FROM users WHERE id=$1", cmd.UserID).Scan(&balance))
	require.Equal(t, "100.00000000", balance)
	require.NoError(t, r.db.QueryRowContext(ctx, "SELECT count(*) FROM usage_logs WHERE user_id=$1", cmd.UserID).Scan(&logs))
	require.Zero(t, logs)
	require.NoError(t, r.db.QueryRowContext(ctx, "SELECT count(*) FROM usage_billing_dedup WHERE request_id IN ($1,$2)", keys[0].RequestID, keys[1].RequestID).Scan(&claims))
	require.Zero(t, claims)
	cleanup()
	results, err := r.applyStagedUsageBillingBatch(ctx, keys, cmd.UserID)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.True(t, results[0].Result.Applied)
	require.True(t, results[1].Result.Applied)
	require.NoError(t, r.db.QueryRowContext(ctx, "SELECT balance::text FROM users WHERE id=$1", cmd.UserID).Scan(&balance))
	require.Equal(t, "99.50000000", balance)
	results, err = r.applyStagedUsageBillingBatch(ctx, keys, cmd.UserID)
	require.NoError(t, err)
	require.False(t, results[0].Result.Applied)
	require.False(t, results[1].Result.Applied)
}

func TestKitty36BillingLaneCancellationAndOtherUserProgress(t *testing.T) {
	ctx := context.Background()
	r, cmd, usage, client := durableBillingFixture(t)
	keys := kitty36StageBills(t, r, cmd, usage, 2)
	other := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.invalid", PasswordHash: "hash", Balance: 100})
	otherKey := mustCreateApiKey(t, client, &service.APIKey{UserID: other.ID, Key: "sk-fixture-" + uuid.NewString(), Name: "independent", Quota: 100})
	c, u := cmd, usage
	c.UserID, c.APIKeyID, c.RequestID, c.RequestFingerprint = other.ID, otherKey.ID, uuid.NewString(), ""
	u.UserID, u.APIKeyID, u.RequestID = c.UserID, c.APIKeyID, c.RequestID
	require.NoError(t, r.StageUsageBilling(ctx, &c, &u))
	block, err := r.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer block.Rollback()
	_, err = block.ExecContext(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", cmd.UserID)
	require.NoError(t, err)
	first := make(chan error, 1)
	go func() { _, err := r.ApplyStagedUsageBillingForUser(ctx, keys[0], cmd.UserID); first <- err }()
	require.Eventually(t, func() bool {
		r.laneMu.Lock()
		defer r.laneMu.Unlock()
		return r.lanes[cmd.UserID] != nil && r.lanePending == 0
	}, 2*time.Second, time.Millisecond)
	canceled, cancel := context.WithCancel(ctx)
	second := make(chan error, 1)
	go func() { _, err := r.ApplyStagedUsageBillingForUser(canceled, keys[1], cmd.UserID); second <- err }()
	require.Eventually(t, func() bool { r.laneMu.Lock(); defer r.laneMu.Unlock(); return r.lanePending == 1 }, 2*time.Second, time.Millisecond)
	cancel()
	otherCtx, done := context.WithTimeout(ctx, 2*time.Second)
	defer done()
	_, err = r.ApplyStagedUsageBillingForUser(otherCtx, service.UsageBillingEventKey{RequestID: c.RequestID, APIKeyID: c.APIKeyID}, other.ID)
	require.NoError(t, err, "an unrelated user must not wait for the blocked lane")
	require.NoError(t, block.Commit())
	require.NoError(t, <-first)
	require.ErrorIs(t, <-second, context.Canceled)
	var state string
	var logs int
	require.NoError(t, r.db.QueryRowContext(ctx, "SELECT state FROM usage_billing_events WHERE request_id=$1 AND api_key_id=$2", keys[1].RequestID, keys[1].APIKeyID).Scan(&state))
	require.Equal(t, "pending", state)
	require.NoError(t, r.db.QueryRowContext(ctx, "SELECT count(*) FROM usage_logs WHERE request_id=$1 AND api_key_id=$2", keys[1].RequestID, keys[1].APIKeyID).Scan(&logs))
	require.Zero(t, logs)
	_, err = r.ApplyStagedUsageBillingForUser(ctx, keys[1], cmd.UserID)
	require.NoError(t, err, "canceled staged receipt remains recoverable")
}
