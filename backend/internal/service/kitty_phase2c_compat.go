package service

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"unicode"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"
)

const scheduledTestPersistenceTimeout = 15 * time.Second

var scheduledTestCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

type accountTestMaxOutputTokensContextKey struct{}

func withAccountTestMaxOutputTokens(ctx context.Context, tokens int) context.Context {
	return context.WithValue(ctx, accountTestMaxOutputTokensContextKey{}, tokens)
}

func accountTestOutputTokenLimit(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	tokens, _ := ctx.Value(accountTestMaxOutputTokensContextKey{}).(int)
	return tokens
}

func (s *AccountTestService) RunTestBackgroundWithPromptAndReasoning(ctx context.Context, accountID int64, modelID, prompt, reasoningEffort string) (*ScheduledTestResult, error) {
	startedAt := time.Now()
	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	ginCtx.Request = (&http.Request{}).WithContext(withAccountTestReasoningEffort(ctx, reasoningEffort))
	ginCtx.Set(accountTestBackgroundKey, true)

	testErr := s.TestAccountConnection(ginCtx, accountID, modelID, prompt, AccountTestModeDefault)
	finishedAt := time.Now()
	responseText, errMsg := parseTestSSEOutput(w.Body.String())

	status := "success"
	if testErr != nil || errMsg != "" {
		status = "failed"
		if errMsg == "" && testErr != nil {
			errMsg = testErr.Error()
		}
	}
	return &ScheduledTestResult{
		Status:       status,
		ModelID:      modelID,
		ResponseText: responseText,
		ErrorMessage: errMsg,
		LatencyMs:    finishedAt.Sub(startedAt).Milliseconds(),
		StartedAt:    startedAt,
		FinishedAt:   finishedAt,
	}, nil
}

// KittyIntelligenceScheduler runs due per-account intelligence test schedules on a 1-minute tick.
type KittyIntelligenceScheduler struct {
	accountTestSvc *AccountTestService
	ctx            context.Context
	cancel         context.CancelFunc
	startOnce      sync.Once
	stopOnce       sync.Once
}

func NewKittyIntelligenceScheduler(accountTestSvc *AccountTestService) *KittyIntelligenceScheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &KittyIntelligenceScheduler{accountTestSvc: accountTestSvc, ctx: ctx, cancel: cancel}
}

func (s *KittyIntelligenceScheduler) Start() {
	if s == nil || s.accountTestSvc == nil {
		return
	}
	s.startOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-s.ctx.Done():
					return
				case now := <-ticker.C:
					s.runAccountIntelligenceSchedules(s.ctx, now)
				}
			}
		}()
	})
}

func (s *KittyIntelligenceScheduler) Stop() {
	if s != nil {
		s.stopOnce.Do(s.cancel)
	}
}

func (s *KittyIntelligenceScheduler) beginRunContext(parent context.Context) (context.Context, func(), error) {
	if s.ctx.Err() != nil {
		return nil, nil, s.ctx.Err()
	}
	ctx, cancel := context.WithCancel(parent)
	return ctx, cancel, nil
}

func opsNumber(v any) (float64, bool) {
	var n float64
	switch x := v.(type) {
	case float64:
		n = x
	case float32:
		n = float64(x)
	case int:
		n = float64(x)
	case int64:
		n = float64(x)
	case json.Number:
		var err error
		n, err = x.Float64()
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0)
}

func opsFresh(observed, until *time.Time, now time.Time) bool {
	return observed != nil && until != nil && !observed.IsZero() && !observed.After(now) && until.After(*observed) && until.After(now)
}

func sanitizeUpstreamBalanceText(value string, maxRunes int) string {
	kept := make([]rune, 0, maxRunes)
	for _, r := range strings.TrimSpace(value) {
		if r == unicode.ReplacementChar || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		if len(kept) == maxRunes {
			break
		}
		kept = append(kept, r)
	}
	return strings.TrimSpace(string(kept))
}
