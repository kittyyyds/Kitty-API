package repository

import (
	"context"
	"database/sql"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Match the established ledger formula: NULL account_stats_cost means no
// account-price override, not a missing charge. Fall back to the saved total_cost
// and apply the request-time account multiplier exactly once. Never use current
// group/account prices. If both saved prices are missing the result is unknown.
const accountTodaySummaryStatsSQL = `
 SELECT account_id, COUNT(*),
  SUM(input_tokens::bigint + output_tokens + cache_creation_tokens + cache_read_tokens),
  CASE WHEN BOOL_AND(COALESCE(account_stats_cost, total_cost) IS NOT NULL)
   THEN SUM(COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1)) END,
  CASE WHEN BOOL_AND(total_cost IS NOT NULL) THEN SUM(total_cost) END,
  CASE WHEN BOOL_AND(actual_cost IS NOT NULL) THEN SUM(actual_cost) END
 FROM usage_logs
 WHERE account_id = ANY($1) AND created_at >= $2
 GROUP BY account_id
`

func (r *usageLogRepository) GetAccountTodaySummaryStatsBatch(ctx context.Context, ids []int64, start time.Time) (map[int64]*service.AccountUsageSummaryStats, error) {
	result := make(map[int64]*service.AccountUsageSummaryStats, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := r.sql.QueryContext(ctx, accountTodaySummaryStatsSQL, pq.Array(ids), start)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, requests, tokens int64
		var cost, standardCost, userCost sql.NullFloat64
		if err := rows.Scan(&id, &requests, &tokens, &cost, &standardCost, &userCost); err != nil {
			return nil, err
		}
		result[id] = &service.AccountUsageSummaryStats{Requests: &requests, Tokens: &tokens,
			Cost: accountSummaryCost(cost), StandardCost: accountSummaryCost(standardCost), UserCost: accountSummaryCost(userCost)}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// A successful bounded query establishes zero usage for missing IDs. Query
	// failures return no results, so they cannot be mistaken for empty accounts.
	for _, id := range ids {
		if _, found := result[id]; !found {
			var count int64
			var cost float64
			result[id] = &service.AccountUsageSummaryStats{Requests: &count, Tokens: &count,
				Cost: &cost, StandardCost: &cost, UserCost: &cost}
		}
	}
	return result, nil
}

// This permanent rollup survives usage log retention and already contains the
// frozen historical account multiplier and actual user charges. Do not multiply
// it again, join current account/group pricing, or scan all retained logs.
const accountLifetimeSummaryStatsSQL = `
 SELECT account_id, account_cost, user_cost
 FROM kitty_account_lifetime_costs
 WHERE account_id = ANY($1)
`

func (r *usageLogRepository) GetAccountLifetimeSummaryStatsBatch(ctx context.Context, ids []int64) (map[int64]*service.AccountUsageSummaryStats, error) {
	result := make(map[int64]*service.AccountUsageSummaryStats, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := r.sql.QueryContext(ctx, accountLifetimeSummaryStatsSQL, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var cost, userCost sql.NullFloat64
		if err := rows.Scan(&id, &cost, &userCost); err != nil {
			return nil, err
		}
		// Keep money independent of the separate permanent request/token rollup.
		result[id] = &service.AccountUsageSummaryStats{LifetimeCost: accountSummaryCost(cost), LifetimeUserCost: accountSummaryCost(userCost)}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func accountSummaryCost(value sql.NullFloat64) *float64 {
	if !value.Valid || math.IsNaN(value.Float64) || math.IsInf(value.Float64, 0) {
		return nil
	}
	return &value.Float64
}
