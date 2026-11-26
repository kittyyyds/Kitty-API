//go:build unit

package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestAccountLifetimeUsageStatsRepositoryBoundedRollupAndUnknowns(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &usageLogRepository{sql: db}
	mock.ExpectQuery(regexp.QuoteMeta(drainAccountLifetimeUsageEventsSQL)).WithArgs(2048).WillReturnRows(sqlmock.NewRows([]string{"processed"}).AddRow(0))
	mock.ExpectQuery(regexp.QuoteMeta(accountLifetimeUsageStatsSQL)).WithArgs("{20,10,99}").
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "requests", "tokens"}).
			AddRow(10, 2, 1135).AddRow(20, 2, nil))
	got, err := repo.GetAccountLifetimeUsageStatsBatch(context.Background(), []int64{20, 10, 99})
	require.NoError(t, err)
	require.EqualValues(t, 2, *got[10].LifetimeRequests)
	require.EqualValues(t, 1135, *got[10].LifetimeTokens)
	require.EqualValues(t, 2, *got[20].LifetimeRequests)
	require.Nil(t, got[20].LifetimeTokens, "one unknown request must not look like a complete partial total")
	require.Zero(t, *got[99].LifetimeRequests, "no ledger identities after a successful query is recorded zero")
	require.Zero(t, *got[99].LifetimeTokens)
	require.Nil(t, got[10].LifetimeCost, "the independent count reader never supplies money")
	for _, forbidden := range []string{"usage_logs", "kitty_procurement_receipts", "kitty_account_cost_legacy", "kitty_permanent_usage", "SUM("} {
		require.NotContains(t, accountLifetimeUsageStatsSQL, forbidden)
	}
	require.Contains(t, accountLifetimeUsageStatsSQL, "COALESCE(u.unknown_requests, 0) = 0")
	require.Contains(t, accountLifetimeUsageStatsSQL, "COALESCE(u.token_sum, 0) <= 9223372036854775807")
	require.Contains(t, accountLifetimeUsageStatsSQL, "account_id = ANY($1)")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountLifetimeUsageStatsRepositoryFailureDoesNotBecomeZero(t *testing.T) {
	for _, mode := range []string{"query", "scan", "rows"} {
		t.Run(mode, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			repo := &usageLogRepository{sql: db}
			mock.ExpectQuery(regexp.QuoteMeta(drainAccountLifetimeUsageEventsSQL)).WithArgs(2048).WillReturnRows(sqlmock.NewRows([]string{"processed"}).AddRow(0))
			query := mock.ExpectQuery(regexp.QuoteMeta(accountLifetimeUsageStatsSQL))
			switch mode {
			case "query":
				query.WillReturnError(errors.New("rollup absent"))
			case "scan":
				query.WillReturnRows(sqlmock.NewRows([]string{"id", "requests", "tokens"}).AddRow(1, "invalid", 5))
			case "rows":
				query.WillReturnRows(sqlmock.NewRows([]string{"id", "requests", "tokens"}).AddRow(1, 1, 5).RowError(0, errors.New("interrupted")))
			}
			got, err := repo.GetAccountLifetimeUsageStatsBatch(context.Background(), []int64{1, 2})
			require.Error(t, err)
			require.Nil(t, got)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAccountLifetimeUsageStatsRepositoryEmptyDoesNotQuery(t *testing.T) {
	got, err := (&usageLogRepository{}).GetAccountLifetimeUsageStatsBatch(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestAccountLifetimeUsageStatsPendingNeverLooksLikeZero(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &usageLogRepository{sql: db}
	mock.ExpectQuery(regexp.QuoteMeta(drainAccountLifetimeUsageEventsSQL)).WithArgs(2048).WillReturnRows(sqlmock.NewRows([]string{"processed"}).AddRow(-1))
	mock.ExpectQuery(regexp.QuoteMeta(accountLifetimeUsageStatsSQL)).WithArgs("{10,20}").WillReturnRows(sqlmock.NewRows([]string{"account_id", "requests", "tokens"}).AddRow(10, nil, nil).AddRow(20, 0, 0))
	got, err := repo.GetAccountLifetimeUsageStatsBatch(context.Background(), []int64{10, 20})
	require.NoError(t, err)
	require.Nil(t, got[10].LifetimeRequests)
	require.Nil(t, got[10].LifetimeTokens)
	require.Zero(t, *got[20].LifetimeRequests)
	require.Zero(t, *got[20].LifetimeTokens)
	require.Contains(t, accountLifetimeUsageStatsSQL, "JOIN kitty_account_usage_snapshots s USING (api_key_id, request_key)")
	require.Contains(t, accountLifetimeUsageStatsSQL, "p.account_id IS NULL")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountLifetimeUsageStatsDrainFailureIsUnknown(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &usageLogRepository{sql: db}
	mock.ExpectQuery(regexp.QuoteMeta(drainAccountLifetimeUsageEventsSQL)).WithArgs(2048).WillReturnError(errors.New("journal processor absent"))
	got, err := repo.GetAccountLifetimeUsageStatsBatch(context.Background(), []int64{10})
	require.Error(t, err)
	require.Nil(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
	processed, err := repo.DrainAccountLifetimeUsageEvents(context.Background(), 0)
	require.NoError(t, err)
	require.Zero(t, processed)
}
