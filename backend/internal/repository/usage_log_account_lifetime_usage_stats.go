package repository

import (
	"context"
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Read the small per-account rollup and pending identity journal. Missing evidence in any recorded
// request makes the token total unknown, not a sum of only the known requests.
// Numeric aggregation cannot overflow; totals outside the API's int64 contract
// are also unknown rather than failing the entire batch (including request counts).
const accountLifetimeUsageStatsSQL = `
 WITH pending_accounts AS (
  SELECT e.account_id FROM kitty_account_usage_events e WHERE e.account_id = ANY($1)
  UNION
  SELECT s.account_id FROM kitty_account_usage_events e
   JOIN kitty_account_usage_snapshots s USING (api_key_id, request_key)
   WHERE s.account_id = ANY($1)
 )
 SELECT wanted.account_id,
  CASE WHEN p.account_id IS NULL THEN COALESCE(u.requests, 0) END AS requests,
  CASE WHEN p.account_id IS NULL AND COALESCE(u.unknown_requests, 0) = 0
   AND COALESCE(u.token_sum, 0) <= 9223372036854775807
   THEN COALESCE(u.token_sum, 0)::bigint END AS tokens
 FROM unnest($1::bigint[]) AS wanted(account_id)
 LEFT JOIN kitty_account_lifetime_usage u USING (account_id)
 LEFT JOIN pending_accounts p USING (account_id)
`

const drainAccountLifetimeUsageEventsSQL = `SELECT kitty_drain_account_usage_events($1)`

// DrainAccountLifetimeUsageEvents processes a bounded journal batch in its own
// autocommit transaction. Live billing writers only append journal identities;
// they never wait for account counters, snapshots, or this processor's lock.
func (r *usageLogRepository) DrainAccountLifetimeUsageEvents(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	rows, err := r.sql.QueryContext(ctx, drainAccountLifetimeUsageEventsSQL, limit)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, err
		}
		return 0, sql.ErrNoRows
	}
	var processed int
	if err := rows.Scan(&processed); err != nil {
		return 0, err
	}
	return processed, rows.Err()
}

func (r *usageLogRepository) GetAccountLifetimeUsageStatsBatch(ctx context.Context, ids []int64) (map[int64]*service.AccountUsageSummaryStats, error) {
	result := make(map[int64]*service.AccountUsageSummaryStats, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	// This finishes before the SELECT starts, so its new statement snapshot sees
	// committed counters. If another processor is active, pending accounts below
	// remain unknown instead of showing a stale value or a made-up zero.
	if _, err := r.DrainAccountLifetimeUsageEvents(ctx, 2048); err != nil {
		return nil, err
	}
	rows, err := r.sql.QueryContext(ctx, accountLifetimeUsageStatsSQL, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var requests, tokens sql.NullInt64
		if err := rows.Scan(&id, &requests, &tokens); err != nil {
			return nil, err
		}
		stats := &service.AccountUsageSummaryStats{}
		if requests.Valid {
			stats.LifetimeRequests = &requests.Int64
		}
		if tokens.Valid {
			stats.LifetimeTokens = &tokens.Int64
		}
		result[id] = stats
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// A successful query after the atomic migration establishes that these IDs
	// have no recorded permanent identities. This is zero within the ledger's
	// coverage, not a claim about requests lost before permanent capture began.
	for _, id := range ids {
		if _, found := result[id]; !found {
			var zero int64
			result[id] = &service.AccountUsageSummaryStats{LifetimeRequests: &zero, LifetimeTokens: &zero}
		}
	}
	return result, nil
}
