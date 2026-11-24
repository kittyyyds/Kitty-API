package service

import (
	"context"
	"fmt"
	"time"
)

// Optional extension retains compatibility with repository decorators. The
// production repository returns configured candidates, including cooling rows.
type SchedulerConfiguredPoolRepository interface {
	ListSchedulerPoolCandidates(context.Context, *int64, []string, bool) ([]Account, error)
}

func kitty36CurrentSchedulableAccounts(accounts []Account) []Account {
	if accounts == nil {
		return nil
	}
	out := make([]Account, 0, len(accounts))
	for i := range accounts {
		if accounts[i].IsSchedulable() {
			out = append(out, accounts[i])
		}
	}
	return out
}

func kitty36SharedFallbackTimeout(s *SchedulerSnapshotService) time.Duration {
	if s.cfg != nil && s.cfg.Gateway.Scheduling.DbFallbackTimeoutSeconds > 0 {
		return time.Duration(s.cfg.Gateway.Scheduling.DbFallbackTimeoutSeconds) * time.Second
	}
	return 30 * time.Second
}

func (s *SchedulerSnapshotService) kitty36LoadFallback(ctx context.Context, bucket SchedulerBucket, mixed bool) ([]Account, error) {
	// A caller cancellation ends only its own wait. Shared work is bounded and
	// still uses the normal fallback limiter and bucket retirement fence.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// The bucket, not the first caller's identity or transaction, defines
	// this shared database read.
	sharedCtx := context.Background()
	ch := s.fallbackLoads.DoChan(bucket.String(), func() (out any, err error) {
		defer func() {
			if recover() != nil {
				out = nil
				err = fmt.Errorf("scheduler fallback failed for bucket %s", bucket.String())
			}
		}()
		bounded, cancel := context.WithTimeout(sharedCtx, kitty36SharedFallbackTimeout(s))
		defer cancel()
		return s.kitty36LoadAndPublish(bounded, bucket, mixed)
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-ch:
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if result.Err != nil {
			return nil, result.Err
		}
		accounts, _ := result.Val.([]Account)
		// Return each caller its own backing slice before any filtering.
		return append([]Account(nil), accounts...), nil
	}
}
