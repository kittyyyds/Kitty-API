//go:build unit

package service

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

type kitty36ConfiguredRepo struct {
	AccountRepository
	query func(context.Context, *int64, []string, bool) ([]Account, error)
}

func (r *kitty36ConfiguredRepo) ListSchedulerPoolCandidates(ctx context.Context, group *int64, platforms []string, includeGrouped bool) ([]Account, error) {
	return r.query(ctx, group, platforms, includeGrouped)
}

func TestKitty36FallbackCoalescesAndSurvivesCallerCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bucket := SchedulerBucket{GroupID: 36, Platform: PlatformOpenAI, Mode: SchedulerModeSingle}
		cache := newRetirementRaceCache()
		release := make(chan struct{})
		calls := 0
		repo := &kitty36ConfiguredRepo{query: func(ctx context.Context, group *int64, platforms []string, includeGrouped bool) ([]Account, error) {
			calls++
			if group == nil || *group != bucket.GroupID || len(platforms) != 1 || platforms[0] != bucket.Platform || includeGrouped {
				t.Error("shared read did not use the bucket's configured pool")
			}
			<-release
			return []Account{{ID: 11, Status: StatusActive, Schedulable: true}}, ctx.Err()
		}}
		svc := NewSchedulerSnapshotService(cache, nil, repo, nil, nil)
		type outcome struct {
			accounts []Account
			err      error
		}
		run := func(ctx context.Context, done chan<- outcome) {
			got, _, err := svc.ListSchedulableAccounts(ctx, &bucket.GroupID, bucket.Platform, false)
			done <- outcome{got, err}
		}
		first, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan outcome, 1)
		go run(first, done)
		synctest.Wait()
		waiters := make([]chan outcome, 10)
		for i := range waiters {
			waiters[i] = make(chan outcome, 1)
			go run(context.Background(), waiters[i])
		}
		synctest.Wait()
		cancel()
		require.ErrorIs(t, (<-done).err, context.Canceled)
		require.Equal(t, 1, calls)
		require.NoError(t, cache.RetireBucket(context.Background(), bucket))
		close(release)
		for _, waiter := range waiters {
			got := <-waiter
			require.NoError(t, got.err)
			require.Len(t, got.accounts, 1)
			require.EqualValues(t, 11, got.accounts[0].ID)
			got.accounts[0].ID = 1234
		}
		attempts, published := cache.counts(bucket)
		require.Equal(t, 1, attempts)
		require.Zero(t, published)
	})
}

type kitty36CoolingCache struct {
	*retirementRaceCache
	accounts []Account
}

func (c *kitty36CoolingCache) SetSnapshot(_ context.Context, _ SchedulerBucket, _ SchedulerBucketWriteToken, accounts []Account) error {
	c.accounts = append([]Account(nil), accounts...)
	return nil
}

func (c *kitty36CoolingCache) GetSnapshot(context.Context, SchedulerBucket) ([]*Account, bool, error) {
	if c.accounts == nil {
		return nil, false, nil
	}
	accounts := make([]*Account, len(c.accounts))
	for i := range c.accounts {
		row := c.accounts[i]
		accounts[i] = &row
	}
	return accounts, true, nil
}

func TestKitty36SnapshotRetainsCoolingRowsUntilNaturallyEligible(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		until := time.Now().Add(time.Second)
		calls := 0
		repo := &kitty36ConfiguredRepo{query: func(context.Context, *int64, []string, bool) ([]Account, error) {
			calls++
			return []Account{
				{ID: 1, Status: StatusActive, Schedulable: true, TempUnschedulableUntil: &until},
				{ID: 2, Status: StatusActive, Schedulable: true, RateLimitResetAt: &until},
				{ID: 3, Status: StatusActive, Schedulable: false},
			}, nil
		}}
		cache := &kitty36CoolingCache{retirementRaceCache: newRetirementRaceCache()}
		svc := NewSchedulerSnapshotService(cache, nil, repo, nil, nil)
		got, _, err := svc.ListSchedulableAccounts(context.Background(), nil, PlatformOpenAI, false)
		require.NoError(t, err)
		require.Empty(t, got)
		require.Len(t, cache.accounts, 3)
		time.Sleep(2 * time.Second)
		got, _, err = svc.ListSchedulableAccounts(context.Background(), nil, PlatformOpenAI, false)
		require.NoError(t, err)
		require.Len(t, got, 2)
		require.EqualValues(t, 1, got[0].ID)
		require.EqualValues(t, 2, got[1].ID)
		require.Equal(t, 1, calls)
		require.Equal(t, &until, got[0].TempUnschedulableUntil)
	})
}
