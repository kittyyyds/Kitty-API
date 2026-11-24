//go:build unit

package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

const (
	groupRollupPrecheckQuery = `SELECT closed_before::text, timezone_name, building_until IS NULL`
	groupRollupLockQuery     = `SELECT pg_try_advisory_lock\(hashtext\('usage_group_daily_rollups:build'\)\)`
	groupRollupUnlockQuery   = `SELECT pg_advisory_unlock\(hashtext\('usage_group_daily_rollups:build'\)\)`
	groupRollupBeginQuery    = `(?s)SELECT closed_before::text, retained_from, timezone_name, building_until::text.*FOR UPDATE`
	groupRollupBuildQuery    = `(?s)SELECT building_until::text, timezone_name.*FOR KEY SHARE`
	groupRollupPublishQuery  = `(?s)SELECT closed_before::text, building_until::text, build_dirty_from::text.*FOR UPDATE`
	groupRollupRegisterExec  = `(?s)UPDATE usage_group_rollup_state.*building_until = \$4::date`
	groupRollupPublishExec   = `(?s)UPDATE usage_group_rollup_state.*SET closed_before = \$1::date,\s+building_until = NULL`
	groupRollupExpireExec    = `(?s)UPDATE usage_group_rollup_state.*expired_before = GREATEST\(expired_before, \$1\)`
	groupRollupInvalidateSQL = `SELECT invalidate_group_usage_rollups_at\(\$1, \$2\)`
)

var groupRollupEpoch = time.Unix(0, 0).UTC()

func expectGroupRollupPrecheck(mock sqlmock.Sqlmock, closedBefore, timezoneName string, idle bool) {
	mock.ExpectQuery(groupRollupPrecheckQuery).
		WillReturnRows(sqlmock.NewRows([]string{"closed_before", "timezone_name", "idle"}).
			AddRow(closedBefore, timezoneName, idle))
}

func expectGroupRollupLock(mock sqlmock.Sqlmock, locked bool) {
	mock.ExpectQuery(groupRollupLockQuery).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(locked))
}

func expectGroupRollupUnlock(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(groupRollupUnlockQuery).
		WillReturnRows(sqlmock.NewRows([]string{"unlocked"}).AddRow(true))
}

func groupRollupBeginRows(closedBefore string, retainedFrom time.Time, timezoneName string, expiredBefore time.Time, buildingUntil any) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"closed_before", "retained_from", "timezone_name", "building_until"}).
		AddRow(closedBefore, retainedFrom, timezoneName, buildingUntil)
}

// expectGroupRollupRound 期望一轮完整的登记、计算、发布。
func expectGroupRollupRound(
	mock sqlmock.Sqlmock,
	begin *sqlmock.Rows,
	minCreatedAt *sqlmock.Rows,
	minCreatedAfter time.Time,
	register []any,
	deleteArgs []any,
	insertArgs []any,
	publishRows *sqlmock.Rows,
	publishBefore string,
) {
	mock.ExpectBegin()
	mock.ExpectQuery(groupRollupBeginQuery).WillReturnRows(begin)
	if minCreatedAt == nil {
		minCreatedAt = sqlmock.NewRows([]string{"min"}).AddRow(register[1])
	}
	mock.ExpectQuery(`SELECT MIN\(created_at\) FROM usage_logs`).WillReturnRows(minCreatedAt)

	mock.ExpectExec(groupRollupRegisterExec).
		WithArgs(toDriverValues(register)...).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	mock.ExpectBegin()
	mock.ExpectQuery(groupRollupBuildQuery).
		WillReturnRows(sqlmock.NewRows([]string{"building_until", "timezone_name"}).AddRow(register[3], register[2]))
	mock.ExpectExec(`DELETE FROM usage_group_daily_rollups`).
		WithArgs(toDriverValues(deleteArgs)...).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO usage_group_daily_rollups`).
		WithArgs(toDriverValues(append(append([]any{}, insertArgs...), register[0]))...).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	mock.ExpectBegin()
	mock.ExpectQuery(groupRollupPublishQuery).WillReturnRows(publishRows)
	mock.ExpectExec(groupRollupPublishExec).
		WithArgs(publishBefore).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

func toDriverValues(values []any) []driver.Value {
	args := make([]driver.Value, 0, len(values))
	for _, value := range values {
		args = append(args, sqlmockValue{value: value})
	}
	return args
}

// sqlmockValue 按时刻比较 time.Time（忽略 Location），其余值按相等比较。
type sqlmockValue struct{ value any }

func (v sqlmockValue) Match(actual driver.Value) bool {
	if expectedTime, ok := v.value.(time.Time); ok {
		actualTime, ok := actual.(time.Time)
		return ok && actualTime.Equal(expectedTime)
	}
	return actual == v.value
}

func publishRows(closedBefore, buildingUntil string, dirtyFrom any) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"closed_before", "building_until", "build_dirty_from"}).
		AddRow(closedBefore, buildingUntil, dirtyFrom)
}

func TestDashboardAggregationRepositorySyncGroupUsageRollupsNoopsWithoutLockingAtCurrentDate(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	todayStart := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)

	expectGroupRollupPrecheck(mock, "2026-08-14", "Asia/Shanghai", true)

	require.NoError(t, repo.SyncGroupUsageRollups(context.Background(), todayStart))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDashboardAggregationRepositorySyncGroupUsageRollupsSkipsWhenAnotherBuildHoldsLock(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	todayStart := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)

	expectGroupRollupPrecheck(mock, "2026-08-13", "Asia/Shanghai", true)
	expectGroupRollupLock(mock, false)

	require.NoError(t, repo.SyncGroupUsageRollups(context.Background(), todayStart))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDashboardAggregationRepositorySyncGroupUsageRollupsBuildsAndPublishesInSeparateTransactions(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	todayStart := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)
	retainedFrom := time.Date(2026, 5, 1, 3, 0, 0, 0, time.UTC)
	rebuildStart := time.Date(2026, 8, 12, 16, 0, 0, 0, time.UTC)

	expectGroupRollupPrecheck(mock, "2026-08-13", "Asia/Shanghai", true)
	expectGroupRollupLock(mock, true)
	expectGroupRollupRound(mock,
		groupRollupBeginRows("2026-08-13", retainedFrom, "Asia/Shanghai", groupRollupEpoch, nil),
		nil, time.Time{},
		[]any{"2026-08-13", retainedFrom, "Asia/Shanghai", "2026-08-14"},
		[]any{"2026-05-01", "2026-08-13"},
		[]any{rebuildStart, todayStart, "Asia/Shanghai"},
		publishRows("2026-08-13", "2026-08-14", nil),
		"2026-08-14",
	)
	expectGroupRollupUnlock(mock)

	require.NoError(t, repo.SyncGroupUsageRollups(context.Background(), todayStart))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDashboardAggregationRepositorySyncGroupUsageRollupsRebuildsDirtyRangeBeforeFullPublish(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	todayStart := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)
	retainedFrom := time.Date(2026, 5, 1, 3, 0, 0, 0, time.UTC)

	expectGroupRollupPrecheck(mock, "2026-08-11", "Asia/Shanghai", true)
	expectGroupRollupLock(mock, true)
	// 第一轮计算期间有 08-12 的迟到写入：只发布到 08-12。
	expectGroupRollupRound(mock,
		groupRollupBeginRows("2026-08-11", retainedFrom, "Asia/Shanghai", groupRollupEpoch, nil),
		nil, time.Time{},
		[]any{"2026-08-11", retainedFrom, "Asia/Shanghai", "2026-08-14"},
		[]any{"2026-05-01", "2026-08-11"},
		[]any{time.Date(2026, 8, 10, 16, 0, 0, 0, time.UTC), todayStart, "Asia/Shanghai"},
		publishRows("2026-08-11", "2026-08-14", "2026-08-12"),
		"2026-08-12",
	)
	// 第二轮只重算脏日期之后的区间。
	expectGroupRollupRound(mock,
		groupRollupBeginRows("2026-08-12", retainedFrom, "Asia/Shanghai", groupRollupEpoch, nil),
		nil, time.Time{},
		[]any{"2026-08-12", retainedFrom, "Asia/Shanghai", "2026-08-14"},
		[]any{"2026-05-01", "2026-08-12"},
		[]any{time.Date(2026, 8, 11, 16, 0, 0, 0, time.UTC), todayStart, "Asia/Shanghai"},
		publishRows("2026-08-12", "2026-08-14", nil),
		"2026-08-14",
	)
	expectGroupRollupUnlock(mock)

	require.NoError(t, repo.SyncGroupUsageRollups(context.Background(), todayStart))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDashboardAggregationRepositorySyncGroupUsageRollupsStopsAfterMaxRoundsWithoutOverPublishing(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	todayStart := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)
	retainedFrom := time.Date(2026, 5, 1, 3, 0, 0, 0, time.UTC)

	expectGroupRollupPrecheck(mock, "2026-08-12", "Asia/Shanghai", true)
	expectGroupRollupLock(mock, true)
	for range groupUsageRollupBuildMaxRounds {
		expectGroupRollupRound(mock,
			groupRollupBeginRows("2026-08-12", retainedFrom, "Asia/Shanghai", groupRollupEpoch, nil),
			nil, time.Time{},
			[]any{"2026-08-12", retainedFrom, "Asia/Shanghai", "2026-08-14"},
			[]any{"2026-05-01", "2026-08-12"},
			[]any{time.Date(2026, 8, 11, 16, 0, 0, 0, time.UTC), todayStart, "Asia/Shanghai"},
			publishRows("2026-08-12", "2026-08-14", "2026-08-12"),
			"2026-08-12",
		)
	}
	expectGroupRollupUnlock(mock)

	require.NoError(t, repo.SyncGroupUsageRollups(context.Background(), todayStart))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDashboardAggregationRepositorySyncGroupUsageRollupsPublishKeepsUnmarkedInvalidation(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	todayStart := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)
	retainedFrom := time.Date(2026, 5, 1, 3, 0, 0, 0, time.UTC)

	expectGroupRollupPrecheck(mock, "2026-08-13", "Asia/Shanghai", true)
	expectGroupRollupLock(mock, true)
	// 计算期间水位被后退到 08-10 而没有记脏：发布不越过 08-10。
	expectGroupRollupRound(mock,
		groupRollupBeginRows("2026-08-13", retainedFrom, "Asia/Shanghai", groupRollupEpoch, nil),
		nil, time.Time{},
		[]any{"2026-08-13", retainedFrom, "Asia/Shanghai", "2026-08-14"},
		[]any{"2026-05-01", "2026-08-13"},
		[]any{time.Date(2026, 8, 12, 16, 0, 0, 0, time.UTC), todayStart, "Asia/Shanghai"},
		publishRows("2026-08-10", "2026-08-14", nil),
		"2026-08-10",
	)
	expectGroupRollupRound(mock,
		groupRollupBeginRows("2026-08-10", retainedFrom, "Asia/Shanghai", groupRollupEpoch, nil),
		nil, time.Time{},
		[]any{"2026-08-10", retainedFrom, "Asia/Shanghai", "2026-08-14"},
		[]any{"2026-05-01", "2026-08-10"},
		[]any{time.Date(2026, 8, 9, 16, 0, 0, 0, time.UTC), todayStart, "Asia/Shanghai"},
		publishRows("2026-08-10", "2026-08-14", nil),
		"2026-08-14",
	)
	expectGroupRollupUnlock(mock)

	require.NoError(t, repo.SyncGroupUsageRollups(context.Background(), todayStart))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDashboardAggregationRepositorySyncGroupUsageRollupsRebuildsWhenTimezoneChanges(t *testing.T) {
	useGroupUsageRepositoryTestTimezone(t, "America/New_York")
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	todayStart := time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC)
	retainedFrom := time.Date(2026, 3, 1, 5, 0, 0, 0, time.UTC)

	expectGroupRollupPrecheck(mock, "2026-03-09", "Asia/Shanghai", true)
	expectGroupRollupLock(mock, true)
	expectGroupRollupRound(mock,
		groupRollupBeginRows("2026-03-09", groupRollupEpoch, "Asia/Shanghai", groupRollupEpoch, nil),
		sqlmock.NewRows([]string{"min"}).AddRow(retainedFrom), groupRollupEpoch,
		[]any{"2026-03-01", retainedFrom, "America/New_York", "2026-03-09"},
		[]any{"2026-03-01", "2026-03-01"},
		[]any{retainedFrom, todayStart, "America/New_York"},
		publishRows("2026-03-01", "2026-03-09", nil),
		"2026-03-09",
	)
	expectGroupRollupUnlock(mock)

	require.NoError(t, repo.SyncGroupUsageRollups(context.Background(), todayStart))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDashboardAggregationRepositorySyncGroupUsageRollupsClearsStaleBuildMarkWhenPublished(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	todayStart := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)

	expectGroupRollupPrecheck(mock, "2026-08-14", "Asia/Shanghai", false)
	expectGroupRollupLock(mock, true)
	mock.ExpectBegin()
	mock.ExpectQuery(groupRollupBeginQuery).
		WillReturnRows(groupRollupBeginRows("2026-08-14", groupRollupEpoch, "Asia/Shanghai", groupRollupEpoch, "2026-08-14"))
	mock.ExpectExec(`(?s)UPDATE usage_group_rollup_state\s+SET building_until = NULL`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	expectGroupRollupUnlock(mock)

	require.NoError(t, repo.SyncGroupUsageRollups(context.Background(), todayStart))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDashboardAggregationRepositorySyncGroupUsageRollupsRejectsFutureWatermark(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	todayStart := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)

	expectGroupRollupPrecheck(mock, "2026-08-15", "Asia/Shanghai", true)
	expectGroupRollupLock(mock, true)
	mock.ExpectBegin()
	mock.ExpectQuery(groupRollupBeginQuery).
		WillReturnRows(groupRollupBeginRows("2026-08-15", groupRollupEpoch, "Asia/Shanghai", groupRollupEpoch, nil))
	mock.ExpectRollback()
	expectGroupRollupUnlock(mock)

	err := repo.SyncGroupUsageRollups(context.Background(), todayStart)
	require.ErrorContains(t, err, "未来")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDashboardAggregationRepositorySyncGroupUsageRollupsAbortsWhenBuildMarkReplaced(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	todayStart := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)
	retainedFrom := time.Date(2026, 5, 1, 3, 0, 0, 0, time.UTC)

	expectGroupRollupPrecheck(mock, "2026-08-13", "Asia/Shanghai", true)
	expectGroupRollupLock(mock, true)
	mock.ExpectBegin()
	mock.ExpectQuery(groupRollupBeginQuery).
		WillReturnRows(groupRollupBeginRows("2026-08-13", retainedFrom, "Asia/Shanghai", groupRollupEpoch, nil))
	mock.ExpectQuery(`SELECT MIN\(created_at\) FROM usage_logs`).WillReturnRows(sqlmock.NewRows([]string{"min"}).AddRow(retainedFrom))
	mock.ExpectExec(groupRollupRegisterExec).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectQuery(groupRollupBuildQuery).
		WillReturnRows(sqlmock.NewRows([]string{"building_until", "timezone_name"}).AddRow(nil, "Asia/Shanghai"))
	mock.ExpectRollback()
	expectGroupRollupUnlock(mock)

	err := repo.SyncGroupUsageRollups(context.Background(), todayStart)
	require.ErrorIs(t, err, errGroupUsageRollupBuildPreempted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDashboardAggregationRepositoryRecomputeRangeInvalidatesGroupRollupsBeforeDashboardRebuild(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	start := time.Date(2026, 8, 1, 3, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id FROM usage_group_rollup_state.*FOR UPDATE`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectExec(groupRollupInvalidateSQL).
		WithArgs(start, "Asia/Shanghai").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM usage_dashboard_hourly`).
		WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()

	err := repo.RecomputeRange(context.Background(), start, end)
	require.ErrorIs(t, err, sql.ErrConnDone)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDashboardAggregationRepositoryRecomputeRangeRebuildsGroupRollupsBeforeCommit(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	start := time.Date(2026, 8, 1, 3, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	fixedNow := time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
	repo.clock = func() time.Time { return fixedNow }
	todayStart := service.GroupUsageTodayStart(fixedNow)
	startDate := service.GroupUsageDate(start)
	rebuildStart, err := service.ParseGroupUsageDate(startDate)
	require.NoError(t, err)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id FROM usage_group_rollup_state.*FOR UPDATE`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectExec(groupRollupInvalidateSQL).
		WithArgs(start, "Asia/Shanghai").
		WillReturnResult(sqlmock.NewResult(0, 1))
	for _, query := range []string{
		`DELETE FROM usage_dashboard_hourly WHERE`,
		`DELETE FROM usage_dashboard_hourly_users WHERE`,
		`DELETE FROM usage_dashboard_daily WHERE`,
		`DELETE FROM usage_dashboard_daily_users WHERE`,
		`INSERT INTO usage_dashboard_hourly_users`,
		`INSERT INTO usage_dashboard_daily_users`,
		`INSERT INTO usage_dashboard_hourly`,
		`INSERT INTO usage_dashboard_daily`,
	} {
		mock.ExpectExec(query).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectQuery(groupRollupBeginQuery).WillReturnRows(groupRollupBeginRows(startDate, groupRollupEpoch, "Asia/Shanghai", groupRollupEpoch, nil))
	mock.ExpectQuery(`SELECT MIN\(created_at\) FROM usage_logs`).WillReturnRows(sqlmock.NewRows([]string{"min"}).AddRow(start))
	mock.ExpectExec(groupRollupRegisterExec).WithArgs(startDate, start, "Asia/Shanghai", service.GroupUsageDate(todayStart)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(groupRollupBuildQuery).WillReturnRows(sqlmock.NewRows([]string{"building_until", "timezone_name"}).AddRow(service.GroupUsageDate(todayStart), "Asia/Shanghai"))
	mock.ExpectExec(`DELETE FROM usage_group_daily_rollups`).WithArgs(startDate, startDate).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO usage_group_daily_rollups`).WithArgs(rebuildStart.UTC(), todayStart.UTC(), "Asia/Shanghai", startDate).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(groupRollupPublishQuery).WillReturnRows(publishRows(startDate, service.GroupUsageDate(todayStart), nil))
	mock.ExpectExec(groupRollupPublishExec).WithArgs(service.GroupUsageDate(todayStart)).WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectCommit()

	require.NoError(t, repo.RecomputeRange(context.Background(), start, end))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDashboardAggregationRepositoryCleanupUsageLogsNonPartitionedInvalidatesEachBatchAndSyncs(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	cutoff := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	earliestDeletedAt := time.Date(2026, 5, 3, 2, 0, 0, 0, time.UTC)
	fixedNow := time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
	repo.clock = func() time.Time { return fixedNow }
	todayStart := service.GroupUsageTodayStart(fixedNow)

	mock.ExpectQuery(`SELECT EXISTS`).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id FROM usage_group_rollup_state.*FOR UPDATE`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`(?s)DELETE FROM usage_logs.*RETURNING created_at`).
		WithArgs(cutoff, usageLogsCleanupBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).
			AddRow(earliestDeletedAt.Add(time.Hour)).
			AddRow(earliestDeletedAt))
	mock.ExpectExec(groupRollupInvalidateSQL).
		WithArgs(earliestDeletedAt, "Asia/Shanghai").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	expectGroupRollupPrecheck(mock, service.GroupUsageDate(todayStart), "Asia/Shanghai", true)

	require.NoError(t, repo.CleanupUsageLogs(context.Background(), cutoff))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDashboardAggregationRepositoryCleanupUsageLogsPartitionedSortsAndInvalidatesEachDropBeforeSync(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	cutoff := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	aprilStart := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	juneStart := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	fixedNow := time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
	repo.clock = func() time.Time { return fixedNow }
	todayStart := service.GroupUsageTodayStart(fixedNow)

	mock.ExpectQuery(`SELECT EXISTS`).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(`SELECT c.relname`).
		WillReturnRows(sqlmock.NewRows([]string{"relname"}).
			AddRow("usage_logs_202606").
			AddRow("usage_logs_invalid").
			AddRow("usage_logs_202604").
			AddRow("usage_logs_202607"))

	for _, partition := range []struct {
		name  string
		start time.Time
	}{
		{name: "usage_logs_202604", start: aprilStart},
		{name: "usage_logs_202606", start: juneStart},
	} {
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT id FROM usage_group_rollup_state.*FOR UPDATE`).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
		mock.ExpectExec(groupRollupInvalidateSQL).
			WithArgs(partition.start, "Asia/Shanghai").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(`DROP TABLE IF EXISTS "` + partition.name + `"`).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectCommit()
	}
	// The boundary partition is pruned by exact timestamp. tableoid is required
	// because ctid alone can also identify a retained row in another partition.
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id FROM usage_group_rollup_state.*FOR UPDATE`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`(?s)SELECT tableoid, ctid.*WHERE created_at < \$1.*WHERE \(tableoid, ctid\) IN.*RETURNING created_at`).
		WithArgs(cutoff, usageLogsCleanupBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(cutoff.Add(-time.Hour)))
	mock.ExpectExec(groupRollupInvalidateSQL).
		WithArgs(cutoff.Add(-time.Hour), "Asia/Shanghai").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	expectGroupRollupPrecheck(mock, service.GroupUsageDate(todayStart), "Asia/Shanghai", true)

	require.NoError(t, repo.CleanupUsageLogs(context.Background(), cutoff))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDashboardAggregationRepositoryCleanupUsageLogsNonPartitionedFailureRollsBackWithoutSync(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	cutoff := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	deletedAt := time.Date(2026, 5, 3, 2, 0, 0, 0, time.UTC)

	mock.ExpectQuery(`SELECT EXISTS`).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id FROM usage_group_rollup_state.*FOR UPDATE`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`(?s)SELECT tableoid, ctid.*ORDER BY created_at ASC, id ASC.*DELETE FROM usage_logs.*RETURNING created_at`).
		WithArgs(cutoff, usageLogsCleanupBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(deletedAt))
	mock.ExpectExec(groupRollupInvalidateSQL).
		WithArgs(deletedAt, "Asia/Shanghai").
		WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()

	err := repo.CleanupUsageLogs(context.Background(), cutoff)
	require.ErrorIs(t, err, sql.ErrConnDone)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDashboardAggregationRepositoryCleanupUsageLogsPartitionFailureRollsBackAndStops(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	cutoff := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	aprilStart := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	dropErr := errors.New("drop partition failed")

	mock.ExpectQuery(`SELECT EXISTS`).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(`SELECT c.relname`).
		WillReturnRows(sqlmock.NewRows([]string{"relname"}).
			AddRow("usage_logs_202606").
			AddRow("usage_logs_202604"))
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id FROM usage_group_rollup_state.*FOR UPDATE`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectExec(groupRollupInvalidateSQL).
		WithArgs(aprilStart, "Asia/Shanghai").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DROP TABLE IF EXISTS "usage_logs_202604"`).
		WillReturnError(dropErr)
	mock.ExpectRollback()

	err := repo.CleanupUsageLogs(context.Background(), cutoff)
	require.ErrorIs(t, err, dropErr)
	require.NoError(t, mock.ExpectationsWereMet())
}

func setGroupUsageRollupTestTimezone(t *testing.T) {
	t.Helper()
	useGroupUsageRepositoryTestTimezone(t, "Asia/Shanghai")
}
