//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type lifetimeUsageDrainerStub struct {
	UsageLogRepository
	drain func(context.Context, int) (int, error)
}

func (r *lifetimeUsageDrainerStub) DrainAccountLifetimeUsageEvents(ctx context.Context, limit int) (int, error) {
	return r.drain(ctx, limit)
}

func newLifetimeUsageTestWorker(drainer accountLifetimeUsageEventDrainer) (*accountLifetimeUsageWorker, *bytes.Buffer) {
	output := &bytes.Buffer{}
	worker := &accountLifetimeUsageWorker{
		drainer: drainer,
		config:  defaultAccountLifetimeUsageWorkerConfig(),
		logger:  slog.New(slog.NewTextHandler(output, nil)),
		done:    make(chan struct{}),
	}
	worker.config.catchupInterval = time.Millisecond
	return worker, output
}

func TestAccountLifetimeUsageWorkerConstructorDoesNotStart(t *testing.T) {
	var calls atomic.Int32
	repo := &lifetimeUsageDrainerStub{drain: func(context.Context, int) (int, error) {
		calls.Add(1)
		return 0, nil
	}}
	s := NewAccountUsageService(nil, repo, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	require.Nil(t, s.lifetimeUsageWorker)
	require.Zero(t, calls.Load())
	s.StopAccountLifetimeUsageWorker()
}

func TestAccountLifetimeUsageWorkerStartsOnceAndStops(t *testing.T) {
	repo := &lifetimeUsageDrainerStub{drain: func(context.Context, int) (int, error) { return 0, nil }}
	s := NewAccountUsageService(nil, repo, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	require.Nil(t, s.lifetimeUsageWorker, "constructing an account service must not start a database writer")
	s.StartAccountLifetimeUsageWorker()
	t.Cleanup(s.StopAccountLifetimeUsageWorker)
	worker := s.lifetimeUsageWorker
	require.NotNil(t, worker)
	require.Equal(t, time.Second, worker.config.pollInterval)
	require.Equal(t, 2048, accountLifetimeUsageBatchSize)
	require.Equal(t, 8, worker.config.maxBatches)
	require.Equal(t, 25*time.Millisecond, worker.config.catchupInterval)
	require.Equal(t, 3*time.Second, worker.config.batchTimeout)
	require.Equal(t, 5*time.Second, worker.config.cycleTimeout)
	require.Equal(t, time.Minute, worker.config.errorLogInterval)
	s.StartAccountLifetimeUsageWorker()
	require.Same(t, worker, s.lifetimeUsageWorker)
	s.StopAccountLifetimeUsageWorker()
	s.StopAccountLifetimeUsageWorker()
	select {
	case <-worker.done:
	default:
		t.Fatal("Stop returned before the worker exited")
	}
	s.StartAccountLifetimeUsageWorker()
	require.Same(t, worker, s.lifetimeUsageWorker, "shutdown must not restart the worker")
}

func TestAccountLifetimeUsageWorkerLifecycleConcurrentAndOptional(t *testing.T) {
	var nilService *AccountUsageService
	nilService.StartAccountLifetimeUsageWorker()
	nilService.StopAccountLifetimeUsageWorker()
	unsupported := &AccountUsageService{}
	unsupported.StartAccountLifetimeUsageWorker()
	require.Nil(t, unsupported.lifetimeUsageWorker)

	s := &AccountUsageService{usageLogRepo: &lifetimeUsageDrainerStub{drain: func(context.Context, int) (int, error) { return 0, nil }}}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.StartAccountLifetimeUsageWorker()
			s.StopAccountLifetimeUsageWorker()
		}()
	}
	wg.Wait()
	require.True(t, s.lifetimeUsageWorkerStopped)
	if s.lifetimeUsageWorker != nil {
		select {
		case <-s.lifetimeUsageWorker.done:
		default:
			t.Fatal("concurrent Stop left a running worker")
		}
	}
	stopped := &AccountUsageService{usageLogRepo: s.usageLogRepo}
	stopped.StopAccountLifetimeUsageWorker()
	stopped.StartAccountLifetimeUsageWorker()
	require.Nil(t, stopped.lifetimeUsageWorker)
}

func TestAccountLifetimeUsageWorkerBoundedCatchup(t *testing.T) {
	for _, tc := range []struct {
		name      string
		results   []int
		wantCalls int
	}{
		{"empty", []int{0}, 1},
		{"partial", []int{12}, 1},
		{"busy", []int{-1}, 1},
		{"invalid_negative", []int{-2}, 1},
		{"invalid_oversized", []int{2049}, 1},
		{"full_then_partial", []int{2048, 2}, 2},
		{"full_then_busy", []int{2048, -1}, 2},
		{"continuous_backlog", []int{2048}, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			repo := &lifetimeUsageDrainerStub{drain: func(ctx context.Context, limit int) (int, error) {
				require.Equal(t, 2048, limit)
				deadline, ok := ctx.Deadline()
				require.True(t, ok)
				require.True(t, time.Until(deadline) <= 3*time.Second)
				result := tc.results[min(calls, len(tc.results)-1)]
				calls++
				return result, nil
			}}
			worker, output := newLifetimeUsageTestWorker(repo)
			worker.drainCycle(context.Background())
			require.Equal(t, tc.wantCalls, calls)
			if tc.name == "invalid_negative" || tc.name == "invalid_oversized" {
				require.Contains(t, output.String(), "invalid batch size")
			} else {
				require.Empty(t, output.String(), "busy is not an error")
			}
		})
	}
}

func TestAccountLifetimeUsageWorkerHonorsBatchAndCycleDeadlines(t *testing.T) {
	t.Run("batch_deadline", func(t *testing.T) {
		calls := 0
		worker, output := newLifetimeUsageTestWorker(&lifetimeUsageDrainerStub{drain: func(ctx context.Context, _ int) (int, error) {
			calls++
			<-ctx.Done()
			return 0, ctx.Err()
		}})
		worker.config.batchTimeout = 15 * time.Millisecond
		worker.drainCycle(context.Background())
		require.Equal(t, 1, calls)
		require.Contains(t, output.String(), "deadline exceeded")
	})
	t.Run("cycle_deadline_cancels_catchup_wait", func(t *testing.T) {
		calls := 0
		worker, _ := newLifetimeUsageTestWorker(&lifetimeUsageDrainerStub{drain: func(context.Context, int) (int, error) {
			calls++
			return 2048, nil
		}})
		worker.config.catchupInterval = time.Hour
		worker.config.cycleTimeout = 15 * time.Millisecond
		done := make(chan struct{})
		go func() { worker.drainCycle(context.Background()); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("catch-up was not bounded by the cycle deadline")
		}
		require.Equal(t, 1, calls)
	})
	t.Run("already_cancelled", func(t *testing.T) {
		worker, output := newLifetimeUsageTestWorker(&lifetimeUsageDrainerStub{drain: func(context.Context, int) (int, error) {
			t.Fatal("drain called with cancelled parent")
			return 0, nil
		}})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		worker.drainCycle(ctx)
		require.Empty(t, output.String())
	})
}

func TestAccountLifetimeUsageWorkerFailureLoggingIsThrottled(t *testing.T) {
	calls := 0
	worker, output := newLifetimeUsageTestWorker(&lifetimeUsageDrainerStub{drain: func(context.Context, int) (int, error) {
		calls++
		return 0, errors.New("drain unavailable")
	}})
	for i := 0; i < 10; i++ {
		worker.drainCycle(context.Background())
	}
	require.Equal(t, 10, calls, "an error must end each catch-up round")
	require.Equal(t, 1, strings.Count(output.String(), "drain unavailable"))
	worker.logFailure(errors.New("drain unavailable"), worker.lastErrorLog.Add(time.Minute))
	require.Equal(t, 2, strings.Count(output.String(), "drain unavailable"))
}

func TestAccountLifetimeUsageWorkerPollsInsteadOfSpinning(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result int
		err    error
	}{
		{"empty", 0, nil}, {"busy", -1, nil}, {"failure", 0, errors.New("unavailable")},
		{"full_backlog", 2048, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := make(chan time.Time, 4)
			worker, _ := newLifetimeUsageTestWorker(&lifetimeUsageDrainerStub{drain: func(context.Context, int) (int, error) {
				called <- time.Now()
				return tc.result, tc.err
			}})
			worker.config.pollInterval = 20 * time.Millisecond
			worker.config.maxBatches = 1
			ctx, cancel := context.WithCancel(context.Background())
			worker.cancel = cancel
			s := &AccountUsageService{lifetimeUsageWorker: worker}
			go worker.run(ctx)
			t.Cleanup(s.StopAccountLifetimeUsageWorker)
			var first, second time.Time
			select {
			case first = <-called:
			case <-time.After(time.Second):
				t.Fatal("first poll missing")
			}
			select {
			case second = <-called:
			case <-time.After(time.Second):
				t.Fatal("second poll missing")
			}
			require.GreaterOrEqual(t, second.Sub(first), worker.config.pollInterval)
			s.StopAccountLifetimeUsageWorker()
		})
	}
}

func TestAccountLifetimeUsageWorkerStopCancelsWaitsAndJoins(t *testing.T) {
	for _, mode := range []string{"poll", "catchup", "query"} {
		t.Run(mode, func(t *testing.T) {
			called := make(chan struct{})
			cancelled := make(chan struct{})
			release := make(chan struct{})
			worker, output := newLifetimeUsageTestWorker(&lifetimeUsageDrainerStub{drain: func(ctx context.Context, _ int) (int, error) {
				close(called)
				if mode == "query" {
					<-ctx.Done()
					close(cancelled)
					<-release // Stop must join, not just signal cancellation.
					return 0, ctx.Err()
				}
				return 2048, nil
			}})
			worker.config.pollInterval = time.Millisecond
			worker.config.catchupInterval = time.Hour
			if mode == "poll" {
				worker.config.pollInterval = time.Hour
			}
			ctx, cancel := context.WithCancel(context.Background())
			worker.cancel = cancel
			s := &AccountUsageService{lifetimeUsageWorker: worker}
			go worker.run(ctx)
			defer cancel()
			if mode != "poll" {
				select {
				case <-called:
				case <-time.After(time.Second):
					t.Fatal("worker did not drain")
				}
			}
			stopped := make(chan struct{})
			go func() { s.StopAccountLifetimeUsageWorker(); close(stopped) }()
			if mode == "query" {
				select {
				case <-cancelled:
				case <-time.After(time.Second):
					t.Fatal("query was not cancelled")
				}
				select {
				case <-stopped:
					t.Fatal("Stop returned before the query exited")
				default:
				}
				close(release)
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("Stop did not interrupt worker wait")
			}
			require.Empty(t, output.String(), "shutdown cancellation should not produce warning logs")
		})
	}
}
