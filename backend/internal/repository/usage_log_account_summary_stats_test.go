//go:build unit

package repository

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestAccountUsageSummaryRepositoryHistoricalPricingAndUnknowns(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta(accountTodaySummaryStatsSQL)).WithArgs("{42,7,9}", start).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "requests", "tokens", "cost", "standard_cost", "user_cost"}).
			AddRow(7, 2, 300, 6, 99, 1.25).AddRow(42, 2300, 285100000, nil, 102.30, 17.28))
	today, err := repo.GetAccountTodaySummaryStatsBatch(context.Background(), []int64{42, 7, 9}, start)
	require.NoError(t, err)
	require.Equal(t, 6.0, *today[7].Cost)
	require.Equal(t, 99.0, *today[7].StandardCost)
	require.Nil(t, today[42].Cost, "only when both saved prices are unavailable is the aggregate unknown")
	require.EqualValues(t, 2300, *today[42].Requests)
	require.Equal(t, 17.28, *today[42].UserCost)
	require.Zero(t, *today[9].Requests)
	require.Zero(t, *today[9].Cost)
	// Permanent costs are already multiplied. Neither the SQL nor the caller may
	// multiply 6 again or reinterpret the 1.25 actual debit as customer list price.
	mock.ExpectQuery(regexp.QuoteMeta(accountLifetimeSummaryStatsSQL)).WithArgs("{42,7,9}").
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "account_cost", "user_cost"}).AddRow(7, 600, 125).AddRow(42, 0, 0))
	lifetime, err := repo.GetAccountLifetimeSummaryStatsBatch(context.Background(), []int64{42, 7, 9})
	require.NoError(t, err)
	require.Equal(t, 600.0, *lifetime[7].LifetimeCost)
	require.Equal(t, 125.0, *lifetime[7].LifetimeUserCost)
	require.Zero(t, *lifetime[42].LifetimeCost)
	require.NotContains(t, lifetime, int64(9), "missing permanent row is unknown, not zero")
	require.Nil(t, lifetime[7].LifetimeRequests)
	require.Nil(t, lifetime[7].LifetimeTokens)
	require.NotContains(t, accountLifetimeSummaryStatsSQL, "usage_logs")
	require.NotContains(t, accountLifetimeSummaryStatsSQL, "multiplier")
	require.Contains(t, accountTodaySummaryStatsSQL, "SUM(COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1))")
	require.Contains(t, accountTodaySummaryStatsSQL, "BOOL_AND(COALESCE(account_stats_cost, total_cost) IS NOT NULL)")
	require.NotContains(t, accountTodaySummaryStatsSQL, "NULLIF(account_rate_multiplier", "an explicit zero account multiplier must remain zero")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountUsageSummaryRepositoryErrorsDoNotBecomeEmptySuccess(t *testing.T) {
	for _, mode := range []string{"query", "scan", "rows"} {
		t.Run(mode, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			repo := &usageLogRepository{sql: db}
			q := mock.ExpectQuery(regexp.QuoteMeta(accountTodaySummaryStatsSQL))
			switch mode {
			case "query":
				q.WillReturnError(errors.New("unavailable"))
			case "scan":
				q.WillReturnRows(sqlmock.NewRows([]string{"id", "requests", "tokens", "cost", "standard", "user"}).AddRow(1, "broken", 1, 1, 1, 1))
			case "rows":
				q.WillReturnRows(sqlmock.NewRows([]string{"id", "requests", "tokens", "cost", "standard", "user"}).AddRow(1, 1, 1, 1, 1, 1).RowError(0, errors.New("read interrupted")))
			}
			got, err := repo.GetAccountTodaySummaryStatsBatch(context.Background(), []int64{1}, time.Now())
			require.Error(t, err)
			require.Nil(t, got)
			mock.ExpectQuery(regexp.QuoteMeta(accountLifetimeSummaryStatsSQL)).WillReturnError(errors.New("relation absent"))
			got, err = repo.GetAccountLifetimeSummaryStatsBatch(context.Background(), []int64{1})
			require.Error(t, err)
			require.Nil(t, got)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAccountUsageSummaryRepositoryEmptyAndNonFiniteCosts(t *testing.T) {
	repo := &usageLogRepository{}
	got, err := repo.GetAccountTodaySummaryStatsBatch(context.Background(), nil, time.Now())
	require.NoError(t, err)
	require.Empty(t, got)
	got, err = repo.GetAccountLifetimeSummaryStatsBatch(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, got)
	for _, value := range []sql.NullFloat64{{}, {Float64: math.NaN(), Valid: true}, {Float64: math.Inf(1), Valid: true}} {
		require.Nil(t, accountSummaryCost(value))
	}
	require.Zero(t, *accountSummaryCost(sql.NullFloat64{Float64: 0, Valid: true}))
}
