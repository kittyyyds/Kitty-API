package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Runtime transition coverage is also reproducible with the isolated PostgreSQL
// WASM runner in tools/kitty/verify_account_lifetime_usage.mjs (no production DB).
func TestAccountLifetimeUsageMigrationLedgerUniverseAndSafety(t *testing.T) {
	body, err := FS.ReadFile("955_kitty_account_lifetime_usage.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(body))
	require.Contains(t, sql, "set local lock_timeout = '5s'")
	require.Contains(t, sql, "pg_advisory_xact_lock(955949, 955)")
	require.Contains(t, sql, "lock table kitty_permanent_usage, kitty_procurement_receipts, kitty_account_cost_legacy")
	require.NotContains(t, sql, "lock table usage_logs")
	require.Contains(t, sql, "primary key (api_key_id, request_key)")
	require.Contains(t, sql, "unknown_requests <= requests")
	require.Contains(t, sql, "where (excluded.source = 'receipt' or s.source = 'legacy')")
	require.Contains(t, sql, "not exists (select from kitty_procurement_receipts")
	require.Contains(t, sql, "count(*) filter (where tokens is null)")
	require.Contains(t, sql, "(p.api_key_id, p.request_key) = (l.api_key_id, l.request_key)")
	for _, forbidden := range []string{
		"update kitty_account_lifetime_costs", "insert into kitty_account_lifetime_costs",
		"delete from kitty_account_lifetime_costs", "function kitty_account_cost_capture",
		"function kitty_adjust_account_lifetime(", "delete from kitty_account_usage_snapshots",
	} {
		require.NotContains(t, sql, forbidden)
	}
	require.Contains(t, sql, "create trigger kitty_account_usage_receipt_write after insert or update of account_id, measurements")
	require.Contains(t, sql, "create trigger kitty_account_usage_legacy_write after insert or update of account_id")
	require.Contains(t, sql, "cache_read_tokens, cache_creation_tokens, invalid_metrics on kitty_permanent_usage")
	require.Contains(t, sql, "coalesce(new.tokens, 0) - coalesce(old.tokens, 0)")
	require.Contains(t, sql, "new_unknown - old_unknown")
	require.Contains(t, sql, "old.account_id < new.account_id")
}

func TestAccountLifetimeUsageMigrationTokenValidationAndIdempotentBackfill(t *testing.T) {
	body, err := FS.ReadFile("955_kitty_account_lifetime_usage.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(body))
	start := strings.Index(sql, "function kitty_account_usage_tokens(")
	end := strings.Index(sql[start:], "\n$$;")
	require.Greater(t, start, 0)
	require.Greater(t, end, 0)
	helper := sql[start : start+end]
	require.Contains(t, helper, "array['input', 'output', 'cache_read', 'cache_write']")
	require.Contains(t, helper, "jsonb_typeof(m->k) is distinct from 'number'")
	require.Contains(t, helper, "v <> trunc(v)", "100.0 is a valid integer, not an integer-string regex match")
	require.Contains(t, helper, "v < 0")
	require.NotContains(t, helper, "cache_write_5m")
	require.NotContains(t, helper, "cache_write_1h")
	require.NotContains(t, helper, "coalesce(m->>", "missing token fields are never invented as zero")
	require.Contains(t, sql, "p.invalid_metrics is false")
	require.Contains(t, sql, "create table if not exists kitty_account_usage_events")
	require.Contains(t, sql, "delete from kitty_account_usage_events where id = any(captured_ids)")
	require.Contains(t, sql, "drop trigger if exists kitty_account_usage_snapshot_change")
	require.Contains(t, sql, "delete from kitty_account_lifetime_usage")
	require.Contains(t, sql, "from kitty_account_usage_snapshots group by account_id")
}

func accountUsageFunction(t *testing.T, sql, name string) string {
	t.Helper()
	start := strings.Index(sql, "function "+name+"(")
	require.Greater(t, start, 0, name)
	end := strings.Index(sql[start:], "\n$$;")
	require.Greater(t, end, 0, name)
	return sql[start : start+end]
}

func TestAccountLifetimeUsageMigrationLiveTriggersOnlyAppendIdentityEvents(t *testing.T) {
	body, err := FS.ReadFile("955_kitty_account_lifetime_usage.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(body))
	require.NotContains(t, sql, "from kitty_account_lifetime_costs")
	require.NotContains(t, sql, "for update")
	require.Contains(t, sql, "id bigserial primary key")
	require.Contains(t, sql, "on kitty_account_usage_events(account_id)")
	for _, name := range []string{"kitty_account_usage_receipt_capture", "kitty_account_usage_legacy_capture", "kitty_account_usage_permanent_capture"} {
		body := accountUsageFunction(t, sql, name)
		require.Contains(t, body, "insert into kitty_account_usage_events")
		for _, forbidden := range []string{"kitty_upsert_account_usage_snapshot", "kitty_account_lifetime_usage", "kitty_account_usage_snapshots", "advisory", "on conflict", "measurements"} {
			require.NotContains(t, body, forbidden, name)
		}
	}
	helper := accountUsageFunction(t, sql, "kitty_upsert_account_usage_snapshot")
	require.Contains(t, helper, "insert into kitty_account_usage_snapshots")
	require.NotContains(t, helper, "kitty_account_lifetime_costs")
}

func TestAccountLifetimeUsageMigrationDrainIsBoundedCanonicalAndLateCommitSafe(t *testing.T) {
	body, err := FS.ReadFile("955_kitty_account_lifetime_usage.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(body))
	drain := accountUsageFunction(t, sql, "kitty_drain_account_usage_events")
	require.Contains(t, drain, "batch_size integer default 2048")
	require.Contains(t, drain, "batch_size > 2048")
	require.Contains(t, drain, "pg_try_advisory_xact_lock(955949, 955)")
	capture := strings.Index(drain, "into captured_ids")
	canonical := strings.Index(drain, "for item in")
	deleted := strings.Index(drain, "delete from kitty_account_usage_events where id = any(captured_ids)")
	require.Greater(t, capture, 0)
	require.Greater(t, canonical, capture)
	require.Greater(t, deleted, canonical)
	require.Contains(t, drain, "order by id limit batch_size")
	require.Contains(t, drain, "where id = any(captured_ids)")
	require.NotContains(t, drain, "where id <=")
	require.Contains(t, drain, "select distinct api_key_id, request_key")
	require.Contains(t, drain, "case when r.api_key_id is not null then kitty_account_usage_tokens(r.measurements)")
	require.Contains(t, drain, "and r.api_key_id is null")
	require.NotContains(t, drain, "for update")
	require.Contains(t, drain, "return cardinality(captured_ids)")
	require.Contains(t, sql, "select pg_advisory_xact_lock(955949, 955)")
}
