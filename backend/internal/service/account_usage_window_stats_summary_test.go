//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type summaryStatsRepoStub struct {
	UsageLogRepository
	todayBatches, lifetimeBatches, usageBatches [][]int64
	todayErr, lifetimeErr, usageErr             error
	start                                       time.Time
}

func summaryValue[T any](value T) *T { return &value }

func (r *summaryStatsRepoStub) GetAccountTodaySummaryStatsBatch(_ context.Context, ids []int64, start time.Time) (map[int64]*AccountUsageSummaryStats, error) {
	r.todayBatches = append(r.todayBatches, append([]int64(nil), ids...))
	r.start = start
	if r.todayErr != nil {
		return nil, r.todayErr
	}
	result := map[int64]*AccountUsageSummaryStats{}
	for _, id := range ids {
		if id == 2 {
			continue
		} // A missing result is unknown, never a synthesized zero.
		result[id] = &AccountUsageSummaryStats{Requests: summaryValue(id), Tokens: summaryValue(id * 100), Cost: summaryValue(102.30), UserCost: summaryValue(17.28)}
	}
	result[99999] = &AccountUsageSummaryStats{Requests: summaryValue(int64(99999))}
	return result, nil
}

func (r *summaryStatsRepoStub) GetAccountLifetimeSummaryStatsBatch(_ context.Context, ids []int64) (map[int64]*AccountUsageSummaryStats, error) {
	r.lifetimeBatches = append(r.lifetimeBatches, append([]int64(nil), ids...))
	if r.lifetimeErr != nil {
		return nil, r.lifetimeErr
	}
	result := map[int64]*AccountUsageSummaryStats{}
	for _, id := range ids {
		result[id] = &AccountUsageSummaryStats{LifetimeCost: summaryValue(float64(id) * 200), LifetimeUserCost: summaryValue(float64(id) * 30)}
	}
	return result, nil
}

func (r *summaryStatsRepoStub) GetAccountLifetimeUsageStatsBatch(_ context.Context, ids []int64) (map[int64]*AccountUsageSummaryStats, error) {
	r.usageBatches = append(r.usageBatches, append([]int64(nil), ids...))
	if r.usageErr != nil {
		return nil, r.usageErr
	}
	result := map[int64]*AccountUsageSummaryStats{}
	for _, id := range ids {
		result[id] = &AccountUsageSummaryStats{LifetimeRequests: summaryValue(id * 10)}
		if id != 2 {
			result[id].LifetimeTokens = summaryValue(id * 1000)
		}
	}
	result[99999] = &AccountUsageSummaryStats{LifetimeRequests: summaryValue(int64(99999))}
	return result, nil
}
func TestAccountUsageSummaryBatchBoundsIDsAndPermanentCosts(t *testing.T) {
	repo := &summaryStatsRepoStub{}
	svc := &AccountUsageService{usageLogRepo: repo}
	ids := []int64{0, -1, 1, 1}
	for id := int64(2); id <= 260; id++ {
		ids = append(ids, id)
	}
	got, err := svc.GetAccountUsageSummaryBatch(context.Background(), ids)
	require.NoError(t, err)
	require.Len(t, got, 260)
	require.NotContains(t, got, int64(99999))
	require.Equal(t, []int{128, 128, 4}, []int{len(repo.todayBatches[0]), len(repo.todayBatches[1]), len(repo.todayBatches[2])})
	require.Equal(t, repo.todayBatches, repo.lifetimeBatches)
	require.Equal(t, repo.todayBatches, repo.usageBatches)
	require.EqualValues(t, 260, *got[260].Requests)
	require.Equal(t, 52000.0, *got[260].LifetimeCost)
	require.Nil(t, got[2].Requests)
	require.Equal(t, 400.0, *got[2].LifetimeCost, "missing today metrics must not hide the permanent rollup")
	require.EqualValues(t, 10, *got[1].LifetimeRequests)
	require.EqualValues(t, 1000, *got[1].LifetimeTokens)
	require.EqualValues(t, 20, *got[2].LifetimeRequests)
	require.Nil(t, got[2].LifetimeTokens, "incomplete tokens do not hide a known request count")
	require.Zero(t, repo.start.Hour())
	require.Zero(t, repo.start.Minute())
}

func TestAccountUsageSummaryIndependentFailureIsUnknownNotZero(t *testing.T) {
	// Every combination of the three sources has an independent availability
	// boundary, including a not-yet-installed request/token migration.
	for mask := 0; mask < 8; mask++ {
		t.Run(fmt.Sprintf("sources_%03b", mask), func(t *testing.T) {
			repo := &summaryStatsRepoStub{}
			if mask&1 != 0 {
				repo.todayErr = errors.New("today unavailable")
			}
			if mask&2 != 0 {
				repo.lifetimeErr = errors.New("permanent money unavailable")
			}
			if mask&4 != 0 {
				repo.usageErr = errors.New("permanent counters unavailable")
			}
			got, err := (&AccountUsageService{usageLogRepo: repo}).GetAccountUsageSummaryBatch(context.Background(), []int64{1})
			require.NoError(t, err)
			require.Len(t, repo.todayBatches, 1)
			require.Len(t, repo.lifetimeBatches, 1)
			require.Len(t, repo.usageBatches, 1)
			if repo.todayErr != nil {
				require.Nil(t, got[1].Requests)
				require.Nil(t, got[1].Cost)
			} else {
				require.Equal(t, 102.30, *got[1].Cost)
			}
			if repo.lifetimeErr != nil {
				require.Nil(t, got[1].LifetimeCost)
			} else {
				require.Equal(t, 200.0, *got[1].LifetimeCost)
			}
			if repo.usageErr != nil {
				require.Nil(t, got[1].LifetimeRequests)
				require.Nil(t, got[1].LifetimeTokens)
			} else {
				require.EqualValues(t, 10, *got[1].LifetimeRequests)
				require.EqualValues(t, 1000, *got[1].LifetimeTokens)
			}
		})
	}
}
func TestAccountUsageSummaryUnsupportedReaderEmptyAndCancellation(t *testing.T) {
	svc := &AccountUsageService{}
	got, err := svc.GetAccountUsageSummaryBatch(context.Background(), []int64{1})
	require.NoError(t, err)
	require.Nil(t, got[1].Requests)
	require.Nil(t, got[1].LifetimeCost)
	repo := &summaryStatsRepoStub{}
	svc.usageLogRepo = repo
	got, err = svc.GetAccountUsageSummaryBatch(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, got)
	require.Empty(t, repo.todayBatches)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = svc.GetAccountUsageSummaryBatch(ctx, []int64{1})
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, repo.todayBatches)
	require.Empty(t, repo.lifetimeBatches)
	require.Empty(t, repo.usageBatches)
}
