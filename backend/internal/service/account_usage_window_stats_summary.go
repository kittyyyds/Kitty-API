package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

// AccountUsageSummaryStats is the paginated account list's today + lifetime
// contract. Nil means unknown, not zero. It deliberately does not change quota
// window statistics or recalculate any historical pricing.
type AccountUsageSummaryStats struct {
	Requests         *int64   `json:"requests"`
	Tokens           *int64   `json:"tokens"`
	Cost             *float64 `json:"cost"`
	StandardCost     *float64 `json:"standard_cost"`
	UserCost         *float64 `json:"user_cost"`
	LifetimeRequests *int64   `json:"lifetime_requests"`
	LifetimeTokens   *int64   `json:"lifetime_tokens"`
	LifetimeCost     *float64 `json:"lifetime_cost"`
	LifetimeUserCost *float64 `json:"lifetime_user_cost"`
}

type accountTodaySummaryStatsReader interface {
	GetAccountTodaySummaryStatsBatch(context.Context, []int64, time.Time) (map[int64]*AccountUsageSummaryStats, error)
}

type accountLifetimeUsageStatsReader interface {
	GetAccountLifetimeUsageStatsBatch(context.Context, []int64) (map[int64]*AccountUsageSummaryStats, error)
}

type accountLifetimeSummaryStatsReader interface {
	GetAccountLifetimeSummaryStatsBatch(context.Context, []int64) (map[int64]*AccountUsageSummaryStats, error)
}

// GetAccountUsageSummaryBatch keeps the existing today-stats/batch endpoint
// compatible while adding permanent totals. Each source fails independently;
// an unavailable batch remains null and never falls back to N single queries.
func (s *AccountUsageService) GetAccountUsageSummaryBatch(ctx context.Context, accountIDs []int64) (map[int64]*AccountUsageSummaryStats, error) {
	ids := make([]int64, 0, len(accountIDs))
	result := make(map[int64]*AccountUsageSummaryStats, len(accountIDs))
	for _, id := range accountIDs {
		if id <= 0 {
			continue
		}
		if _, exists := result[id]; exists {
			continue
		}
		ids = append(ids, id)
		result[id] = &AccountUsageSummaryStats{}
	}
	todayReader, hasToday := s.usageLogRepo.(accountTodaySummaryStatsReader)
	lifetimeReader, hasLifetime := s.usageLogRepo.(accountLifetimeSummaryStatsReader)
	usageReader, hasUsage := s.usageLogRepo.(accountLifetimeUsageStatsReader)
	start := timezone.Today()
	for offset := 0; offset < len(ids); offset += AccountWindowStatsBatchSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch := ids[offset:min(offset+AccountWindowStatsBatchSize, len(ids))]
		if hasToday {
			if stats, err := todayReader.GetAccountTodaySummaryStatsBatch(ctx, batch, start); err == nil {
				for _, id := range batch {
					if value := stats[id]; value != nil {
						target := result[id]
						target.Requests, target.Tokens = value.Requests, value.Tokens
						target.Cost, target.StandardCost, target.UserCost = value.Cost, value.StandardCost, value.UserCost
					}
				}
			}
		}
		if hasLifetime {
			if stats, err := lifetimeReader.GetAccountLifetimeSummaryStatsBatch(ctx, batch); err == nil {
				for _, id := range batch {
					if value := stats[id]; value != nil {
						target := result[id]
						target.LifetimeCost, target.LifetimeUserCost = value.LifetimeCost, value.LifetimeUserCost
					}
				}
			}
		}
		// Counts/tokens have their own availability boundary. A missing new
		// migration or failed counter query must never hide permanent money.
		if hasUsage {
			if stats, err := usageReader.GetAccountLifetimeUsageStatsBatch(ctx, batch); err == nil {
				for _, id := range batch {
					if value := stats[id]; value != nil {
						target := result[id]
						target.LifetimeRequests, target.LifetimeTokens = value.LifetimeRequests, value.LifetimeTokens
					}
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
