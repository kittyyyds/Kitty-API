//go:build unit

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type queueSpilloverCache struct {
	fakeConcurrencyCache
	freeAfterEnqueue int64
	availability     int64
	waits, releases  []int64
	decrements       []int64
	allocatedInQueue bool
}

func (c *queueSpilloverCache) GetAccountsLoadBatch(_ context.Context, accounts []service.AccountWithConcurrency) (map[int64]*service.AccountLoadInfo, error) {
	loads := make(map[int64]*service.AccountLoadInfo, len(accounts))
	for _, item := range accounts {
		load := &service.AccountLoadInfo{AccountID: item.ID}
		if item.ID != c.availability {
			load.LoadRate, load.CurrentConcurrency = 100, item.MaxConcurrency
		}
		loads[item.ID] = load
	}
	return loads, nil
}

func (c *queueSpilloverCache) AcquireAccountSlot(_ context.Context, id int64, _ int, _ string) (bool, error) {
	if id != c.availability {
		return false, nil
	}
	c.allocatedInQueue = len(c.waits) == 1 && len(c.decrements) == 0
	c.availability = 0
	return true, nil
}

func (c *queueSpilloverCache) ReleaseAccountSlot(_ context.Context, id int64, _ string) error {
	c.releases = append(c.releases, id)
	return nil
}

func (c *queueSpilloverCache) IncrementAccountWaitCount(_ context.Context, id int64, _ int) (bool, error) {
	c.waits = append(c.waits, id)
	c.availability = c.freeAfterEnqueue
	return true, nil
}

func (c *queueSpilloverCache) DecrementAccountWaitCount(_ context.Context, id int64) error {
	c.decrements = append(c.decrements, id)
	return nil
}

func makeQueueSpilloverTestHandler(t *testing.T, cache *queueSpilloverCache, wait time.Duration) (*OpenAIGatewayHandler, *openAIQueueSuccessUpstream, *openAIWSUsageHandlerUsageLogRepoStub) {
	t.Helper()
	accounts := make([]service.Account, 2)
	for i := range accounts {
		accounts[i] = service.Account{
			ID: int64(i + 1), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: i,
			GroupIDs: []int64{3131}, Credentials: map[string]any{"api_key": "test-key", "base_url": "https://example.invalid"},
		}
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.Scheduling = config.GatewaySchedulingConfig{LoadBatchEnabled: true, StickySessionMaxWaiting: 3,
		StickySessionWaitTimeout: wait, FallbackMaxWaiting: 100, FallbackWaitTimeout: wait}
	upstream := &openAIQueueSuccessUpstream{}
	usage := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 8)}
	slots := service.NewConcurrencyService(cache)
	gateway := service.NewOpenAIGatewayService(openAIImagesFailoverAccountRepo{accounts: accounts}, usage, nil, nil,
		nil, nil, nil, cfg, nil, slots, service.NewBillingService(cfg, nil), nil, nil, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	h := NewOpenAIGatewayHandler(gateway, slots, billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, cfg)
	h.maxAccountSwitches = 0
	h.concurrencyHelper.pingInterval = 8 * time.Millisecond
	return h, upstream, usage
}

func TestOpenAIQueueSpilloverUsesActualAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, path, body string
		run              func(*OpenAIGatewayHandler, *gin.Context)
	}{
		{"responses", "/v1/responses", `{"model":"gpt-5.1","input":"hello"}`, (*OpenAIGatewayHandler).Responses},
		{"stream", "/v1/responses", `{"model":"gpt-5.1","input":"hello","stream":true}`, (*OpenAIGatewayHandler).Responses},
		{"chat", "/v1/chat/completions", `{"model":"gpt-5.1","messages":[{"role":"user","content":"hello"}]}`, (*OpenAIGatewayHandler).ChatCompletions},
		{"messages", "/v1/messages", `{"model":"gpt-5.1","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`, (*OpenAIGatewayHandler).Messages},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := &queueSpilloverCache{freeAfterEnqueue: 2}
			h, upstream, usage := makeQueueSpilloverTestHandler(t, cache, 600*time.Millisecond)
			c, rec := openAIQueueRequestContext(t, tc.path, tc.body)
			tc.run(h, c)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), "ok")
			if tc.name == "stream" {
				require.Contains(t, rec.Body.String(), SSEPingFormatComment)
				require.Contains(t, rec.Body.String(), "response.completed")
			}
			require.Equal(t, []int64{2}, upstream.accountIDs)
			require.Equal(t, []int64{1}, cache.waits)
			require.Equal(t, []int64{1}, cache.decrements)
			require.Equal(t, []int64{2}, cache.releases)
			require.True(t, cache.allocatedInQueue)
			opsAccount, exists := c.Get(opsAccountIDKey)
			require.True(t, exists)
			require.Equal(t, int64(2), opsAccount)
			require.Len(t, usage.created, 1, "exactly one usage row must be written")
			log := <-usage.created
			require.Equal(t, int64(2), log.AccountID)
			require.Equal(t, int64(99), log.APIKeyID)
			require.Equal(t, int64(3131), *log.GroupID)
		})
	}
}

func TestOpenAIQueueSpilloverAllBusyStillReturns429(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &queueSpilloverCache{}
	h, upstream, usage := makeQueueSpilloverTestHandler(t, cache, 80*time.Millisecond)
	c, rec := openAIQueueRequestContext(t, "/v1/responses", `{"model":"gpt-5.1","input":"hello"}`)
	h.Responses(c)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Equal(t, gatewayConcurrencyLimitCode, gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
	require.True(t, isOpsLocalAdmissionError(c, gatewayConcurrencyLimitCode))
	require.Empty(t, upstream.accountIDs)
	require.Empty(t, cache.releases)
	require.Empty(t, usage.created)
	require.Equal(t, []int64{1}, cache.waits)
	require.Equal(t, []int64{1}, cache.decrements)
}

func TestOpenAIQueueSpilloverStillAcquiresOriginalAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	h := NewConcurrencyHelper(nil, SSEPingFormatNone, 0)
	streamStarted, probed, released := false, false, 0
	release, alternative, err := h.waitForSlotWithQueueAlternative(c, "account", 1200*time.Millisecond, false, &streamStarted, true,
		func(context.Context) (*service.AcquireResult, error) {
			if !probed {
				return &service.AcquireResult{}, nil
			}
			return &service.AcquireResult{Acquired: true, ReleaseFunc: func() { released++ }}, nil
		},
		func(context.Context) *service.AccountSelectionResult {
			probed = true
			return nil
		})
	require.NoError(t, err)
	require.Nil(t, alternative)
	require.True(t, probed)
	require.NotNil(t, release)
	release()
	require.Equal(t, 1, released)
}

func TestOpenAIQueueSpilloverReturnsLateLease(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, cancelClient := range []bool{false, true} {
		name := "wait deadline"
		if cancelClient {
			name = "client cancellation"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
			h := NewConcurrencyHelper(nil, SSEPingFormatNone, 0)
			streamStarted, released, probed := false, 0, 0
			release, alternative, err := h.waitForSlotWithQueueAlternative(c, "account", 120*time.Millisecond, false, &streamStarted, true,
				func(context.Context) (*service.AcquireResult, error) { return &service.AcquireResult{}, nil },
				func(probeCtx context.Context) *service.AccountSelectionResult {
					probed++
					if cancelClient {
						cancel()
					}
					<-probeCtx.Done()
					return &service.AccountSelectionResult{Account: &service.Account{ID: 2}, Acquired: true,
						ReleaseFunc: func() { released++ }}
				})
			require.Nil(t, release)
			require.Nil(t, alternative)
			require.Equal(t, 1, probed)
			require.Equal(t, 1, released)
			if cancelClient {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				var concurrencyErr *ConcurrencyError
				require.ErrorAs(t, err, &concurrencyErr)
				require.True(t, concurrencyErr.IsTimeout)
			}
		})
	}
}
