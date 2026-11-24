//go:build integration

package repository

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func kittyRollupFixture(t *testing.T) *sql.DB {
	t.Helper()
	useGroupUsageRepositoryTestTimezone(t, "UTC")
	ctx := context.Background()
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	u, err := url.Parse(os.Getenv("SUB2API_TEST_DATABASE_URL"))
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	q.Set("TimeZone", "UTC")
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	db.SetMaxOpenConns(5)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`INSERT INTO groups VALUES(10); INSERT INTO users VALUES(1);
		INSERT INTO usage_logs(id,user_id,group_id,actual_cost,created_at) VALUES
		(1,1,10,1.2345678901,'2026-01-01T12:00:00Z'),
		(2,1,10,2.0000000002,'2026-01-02T12:00:00Z'),
		(3,1,10,3.0000000003,'2026-01-03T12:00:00Z');
		UPDATE usage_group_rollup_state SET timezone_name='UTC',closed_before='1970-01-01'`)
	require.NoError(t, err)
	return db
}

func kittyRollupTransaction(t *testing.T, db *sql.DB, fn func(sqlExecutor) error) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	require.NoError(t, fn(tx))
	require.NoError(t, tx.Commit())
}

func assertKittyRollupMoney(t *testing.T, db *sql.DB, today time.Time) {
	t.Helper()
	// Compare as PostgreSQL NUMERIC text; do not hide precision drift with a float tolerance.
	var exact bool
	require.NoError(t, db.QueryRow(`SELECT
		(SELECT COALESCE(SUM(actual_cost),0) FROM usage_logs WHERE created_at < $1) =
		(SELECT COALESCE(SUM(actual_cost),0) FROM usage_group_daily_rollups)`, today).Scan(&exact))
	require.True(t, exact, "日桶必须与原始明细金额逐位一致")
	rows, err := newUsageLogRepositoryWithSQL(nil, db).GetAllGroupUsageSummary(context.Background(), today)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	var total float64
	require.NoError(t, db.QueryRow(`SELECT SUM(actual_cost) FROM usage_logs`).Scan(&total))
	require.InDelta(t, total, rows[0].TotalCost, 1e-10)
}

func TestKitty36GroupRollupBuildAllowsWritesAndRepairsDirtyRange(t *testing.T) {
	for _, mutation := range []string{"late_insert", "historical_delete", "historical_update", "today_insert"} {
		t.Run(mutation, func(t *testing.T) {
			db := kittyRollupFixture(t)
			ctx := context.Background()
			today := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
			var plan *groupUsageRollupBuildPlan
			kittyRollupTransaction(t, db, func(q sqlExecutor) (err error) {
				plan, err = beginGroupUsageRollupBuild(ctx, q, today)
				return
			})
			build, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer build.Rollback()
			require.NoError(t, buildGroupUsageRollups(ctx, build, plan))
			// Keep the calculation transaction open. A write must finish before it commits.
			writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			mutations := map[string]string{
				"late_insert":       `INSERT INTO usage_logs VALUES(4,1,10,4.0000000004,'2026-01-02T14:00:00Z')`,
				"historical_delete": `DELETE FROM usage_logs WHERE id=2`,
				"historical_update": `UPDATE usage_logs SET actual_cost=9.0000000009 WHERE id=2`,
				"today_insert":      `INSERT INTO usage_logs VALUES(4,1,10,4.0000000004,'2026-01-03T14:00:00Z')`,
			}
			_, err = db.ExecContext(writeCtx, mutations[mutation])
			require.NoError(t, err, "计算阶段不能持有阻塞正常用量写入的状态锁")
			require.NoError(t, build.Commit())
			var dirty bool
			kittyRollupTransaction(t, db, func(q sqlExecutor) (err error) {
				dirty, err = publishGroupUsageRollups(ctx, q, plan)
				return
			})
			require.Equal(t, mutation != "today_insert", dirty)
			var closed string
			require.NoError(t, db.QueryRow(`SELECT closed_before::text FROM usage_group_rollup_state`).Scan(&closed))
			if dirty {
				require.Equal(t, "2026-01-02", closed)
			} else {
				require.Equal(t, "2026-01-03", closed)
			}
			// Even before repair, readers combine only published buckets with the raw tail.
			rows, err := newUsageLogRepositoryWithSQL(nil, db).GetAllGroupUsageSummary(ctx, today)
			require.NoError(t, err)
			var total float64
			require.NoError(t, db.QueryRow(`SELECT SUM(actual_cost) FROM usage_logs`).Scan(&total))
			require.InDelta(t, total, rows[0].TotalCost, 1e-10)
			require.NoError(t, newDashboardAggregationRepositoryWithSQL(db).SyncGroupUsageRollups(ctx, today))
			assertKittyRollupMoney(t, db, today)
		})
	}
}

func TestKitty36GroupRollupResumesInterruptedBuild(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "calculation_rolled_back", true: "calculation_committed"}[committed], func(t *testing.T) {
			db := kittyRollupFixture(t)
			ctx := context.Background()
			today := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
			var plan *groupUsageRollupBuildPlan
			kittyRollupTransaction(t, db, func(q sqlExecutor) (err error) { plan, err = beginGroupUsageRollupBuild(ctx, q, today); return })
			tx, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			require.NoError(t, buildGroupUsageRollups(ctx, tx, plan))
			if committed {
				require.NoError(t, tx.Commit())
			} else {
				require.NoError(t, tx.Rollback())
			}
			// No publish occurred: emulate a process restart with a new repository.
			require.NoError(t, newDashboardAggregationRepositoryWithSQL(db).SyncGroupUsageRollups(ctx, today))
			assertKittyRollupMoney(t, db, today)
			var idle bool
			require.NoError(t, db.QueryRow(`SELECT building_until IS NULL AND build_dirty_from IS NULL FROM usage_group_rollup_state`).Scan(&idle))
			require.True(t, idle)
		})
	}
}
