package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// accountLifetimeUsageEventDrainer is deliberately separate from the broad
// UsageLogRepository interface so constructors and unrelated repository doubles
// do not acquire a background-work requirement. Each call uses its own transaction.
type accountLifetimeUsageEventDrainer interface {
	DrainAccountLifetimeUsageEvents(context.Context, int) (int, error)
}

const accountLifetimeUsageBatchSize = 2048

type accountLifetimeUsageWorkerConfig struct {
	pollInterval     time.Duration
	catchupInterval  time.Duration
	batchTimeout     time.Duration
	cycleTimeout     time.Duration
	maxBatches       int
	errorLogInterval time.Duration
}

func defaultAccountLifetimeUsageWorkerConfig() accountLifetimeUsageWorkerConfig {
	return accountLifetimeUsageWorkerConfig{
		pollInterval:     time.Second,
		catchupInterval:  25 * time.Millisecond,
		batchTimeout:     3 * time.Second,
		cycleTimeout:     5 * time.Second,
		maxBatches:       8,
		errorLogInterval: time.Minute,
	}
}

type accountLifetimeUsageWorker struct {
	drainer accountLifetimeUsageEventDrainer
	config  accountLifetimeUsageWorkerConfig
	logger  *slog.Logger
	cancel  context.CancelFunc
	done    chan struct{}
	// Only the worker goroutine accesses this field.
	lastErrorLog time.Time
}

// StartAccountLifetimeUsageWorker is explicitly enabled only by the Kitty server;
// constructors and isolated rehearsals remain side-effect free. Start and Stop are idempotent,
// including concurrent calls; a stopped service cannot restart during shutdown.
func (s *AccountUsageService) StartAccountLifetimeUsageWorker() {
	if s == nil {
		return
	}
	s.lifetimeUsageWorkerMu.Lock()
	defer s.lifetimeUsageWorkerMu.Unlock()
	if s.lifetimeUsageWorkerStopped || s.lifetimeUsageWorker != nil {
		return
	}
	drainer, ok := s.usageLogRepo.(accountLifetimeUsageEventDrainer)
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker := &accountLifetimeUsageWorker{
		drainer: drainer,
		config:  defaultAccountLifetimeUsageWorkerConfig(),
		logger:  slog.Default(),
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	s.lifetimeUsageWorker = worker
	go worker.run(ctx)
}

// StopAccountLifetimeUsageWorker cancels both database work and timer waits,
// then joins the goroutine before infrastructure cleanup closes the database.
func (s *AccountUsageService) StopAccountLifetimeUsageWorker() {
	if s == nil {
		return
	}
	s.lifetimeUsageWorkerMu.Lock()
	s.lifetimeUsageWorkerStopped = true
	worker := s.lifetimeUsageWorker
	if worker != nil {
		worker.cancel()
	}
	s.lifetimeUsageWorkerMu.Unlock()
	if worker != nil {
		<-worker.done
	}
}

func (w *accountLifetimeUsageWorker) run(ctx context.Context) {
	defer close(w.done)
	for waitAccountLifetimeUsageWorker(ctx, w.config.pollInterval) {
		w.drainCycle(ctx)
	}
}

func (w *accountLifetimeUsageWorker) drainCycle(ctx context.Context) {
	cycleCtx, cancel := context.WithTimeout(ctx, w.config.cycleTimeout)
	defer cancel()
	for batch := 0; batch < w.config.maxBatches; batch++ {
		if cycleCtx.Err() != nil {
			return
		}
		batchCtx, cancelBatch := context.WithTimeout(cycleCtx, w.config.batchTimeout)
		processed, err := w.drainer.DrainAccountLifetimeUsageEvents(batchCtx, accountLifetimeUsageBatchSize)
		cancelBatch()
		if ctx.Err() != nil {
			return // Normal shutdown is not a failed drain.
		}
		if err != nil {
			w.logFailure(err, time.Now())
			return // Retry on the regular poll, never in a hot failure loop.
		}
		// -1 means another processor holds the try advisory lock; it is a
		// normal outcome and ends catch-up immediately.
		if processed == -1 {
			return
		}
		if processed < 0 || processed > accountLifetimeUsageBatchSize {
			w.logFailure(fmt.Errorf("journal drain returned invalid batch size %d", processed), time.Now())
			return
		}
		// An empty/partial batch means catch-up is complete for this cycle.
		if processed < accountLifetimeUsageBatchSize {
			return
		}
		if batch+1 < w.config.maxBatches && !waitAccountLifetimeUsageWorker(cycleCtx, w.config.catchupInterval) {
			return
		}
	}
}

func (w *accountLifetimeUsageWorker) logFailure(err error, now time.Time) {
	if !w.lastErrorLog.IsZero() && now.Sub(w.lastErrorLog) < w.config.errorLogInterval {
		return
	}
	w.lastErrorLog = now
	w.logger.Warn("account lifetime usage journal drain failed; retrying on next poll", "error", err)
}

func waitAccountLifetimeUsageWorker(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}
