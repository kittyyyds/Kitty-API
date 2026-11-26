//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKittyGeminiUsageEvidence_MissingVersusExplicitZero(t *testing.T) {
	cases := []struct{ name, raw, reason string }{
		{"tool without usage", `{"candidates":[{"content":{"parts":[{"functionCall":{"id":"toolu-private","name":"lookup","args":{"secret":"DO-NOT-PERSIST"}}}]},"finishReason":"STOP"}]}`, "gemini_usage_missing"},
		{"null metadata", `{"usageMetadata":null}`, "gemini_usage_missing"},
		{"null counters", `{"usageMetadata":{"promptTokenCount":null,"candidatesTokenCount":null}}`, "gemini_usage_missing"},
		{"invalid counters", `{"usageMetadata":{"promptTokenCount":-1,"candidatesTokenCount":"10"}}`, "gemini_usage_missing"},
		{"explicit zero", `{"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":0}}`, "gemini_usage_explicit_zero"},
		{"partial", `{"usageMetadata":{"promptTokenCount":100}}`, "gemini_usage_partial"},
		{"reported", `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":2,"cachedContentTokenCount":50}}`, "gemini_usage_reported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := mergeGeminiStreamUsage(ClaudeUsage{}, []byte(tc.raw))
			a := buildGeminiUsageAudit(u)
			require.NotNil(t, a)
			require.Contains(t, a.Reasons, tc.reason)
			raw, err := json.Marshal(a)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "DO-NOT-PERSIST")
			require.NotContains(t, string(raw), "toolu-private")
			if tc.reason == "gemini_usage_missing" {
				require.Nil(t, a.Upstream.InputTokens)
				require.Nil(t, a.Upstream.OutputTokens)
			}
			if tc.reason == "gemini_usage_explicit_zero" {
				require.NotNil(t, a.Upstream.InputTokens)
				require.Zero(t, *a.Upstream.InputTokens)
			}
		})
	}
	require.Nil(t, buildGeminiUsageAudit(ClaudeUsage{}))
}

func TestKittyGeminiUsageEvidence_MergesPartialCountersIndependently(t *testing.T) {
	u := mergeGeminiStreamUsage(ClaudeUsage{}, []byte(`{"usageMetadata":{"promptTokenCount":20000,"cachedContentTokenCount":15000,"candidatesTokenCount":12,"thoughtsTokenCount":30}}`))
	// Output-only tail must not erase prompt/cache/thought counters.
	u = mergeGeminiStreamUsage(u, []byte(`{"usageMetadata":{"candidatesTokenCount":20}}`))
	require.Equal(t, 5000, u.InputTokens)
	require.Equal(t, 15000, u.CacheReadInputTokens)
	require.Equal(t, 50, u.OutputTokens)
	u = mergeGeminiStreamUsage(u, []byte(`{"usageMetadata":{"thoughtsTokenCount":45}}`))
	require.Equal(t, 65, u.OutputTokens)
	// Cache-only update adjusts uncached input against the known total prompt.
	u = mergeGeminiStreamUsage(u, []byte(`{"usageMetadata":{"cachedContentTokenCount":16000}}`))
	require.Equal(t, 4000, u.InputTokens)
	require.Equal(t, 16000, u.CacheReadInputTokens)
	require.Equal(t, 80.0, *buildGeminiUsageAudit(u).CacheHitRate)
}

func TestKittyGeminiUsageEvidence_ZeroPlaceholderCannotEraseObservedTokens(t *testing.T) {
	u := mergeGeminiStreamUsage(ClaudeUsage{}, []byte(`{"usageMetadata":{"promptTokenCount":20000,"cachedContentTokenCount":15000,"candidatesTokenCount":12}}`))
	before := u
	u = mergeGeminiStreamUsage(u, []byte(`{"usageMetadata":{"promptTokenCount":0,"cachedContentTokenCount":0,"candidatesTokenCount":0}}`))
	require.Equal(t, before.InputTokens, u.InputTokens)
	require.Equal(t, before.CacheReadInputTokens, u.CacheReadInputTokens)
	require.Equal(t, before.OutputTokens, u.OutputTokens)
	a := buildGeminiUsageAudit(u)
	require.Contains(t, a.Reasons, "gemini_usage_zero_reset_ignored")
	require.Equal(t, 1, a.GeminiUsage.ZeroResetsIgnored)
	// Previous snapshots must not be mutated when merging a subsequent frame.
	require.Zero(t, before.geminiUsageEvidence.ZeroResetsIgnored)
}

func TestKittyGeminiUsageEvidence_ExplicitCacheZeroInNonzeroFinalIsNotInvented(t *testing.T) {
	u := mergeGeminiStreamUsage(ClaudeUsage{}, []byte(`{"usageMetadata":{"promptTokenCount":20000,"cachedContentTokenCount":15000,"candidatesTokenCount":12}}`))
	u = mergeGeminiStreamUsage(u, []byte(`{"usageMetadata":{"promptTokenCount":20000,"cachedContentTokenCount":0,"candidatesTokenCount":20}}`))
	require.Equal(t, 20000, u.InputTokens)
	require.Zero(t, u.CacheReadInputTokens)
	require.Equal(t, 20, u.OutputTokens)
	require.Contains(t, buildGeminiUsageAudit(u).Reasons, "gemini_cache_counter_regressed")
}

func TestKittyGeminiUsageEvidence_ToolOnlyUsageTailAndMissingRemainSuccessful(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tool := `data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"toolu-original","name":"lookup","args":{}}}]},"finishReason":"STOP"}]}` + "\n\n"
	for _, withUsage := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "reported"}[withUsage], func(t *testing.T) {
			stream := tool
			if withUsage {
				stream += `data: {"usageMetadata":{"promptTokenCount":20000,"cachedContentTokenCount":15000,"candidatesTokenCount":12}}` + "\n\n"
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			svc := &GeminiMessagesCompatService{cfg: &config.Config{}}
			result, err := svc.handleStreamingResponse(c, &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(stream))}, time.Now(), "gemini-4-carbon")
			require.NoError(t, err)
			require.Contains(t, rec.Body.String(), `"stop_reason":"tool_use"`)
			require.Contains(t, rec.Body.String(), "toolu-original")
			a := buildGeminiUsageAudit(*result.usage)
			if withUsage {
				require.Equal(t, 15000, result.usage.CacheReadInputTokens)
				require.Equal(t, 5000, result.usage.InputTokens)
				require.Contains(t, a.Reasons, "gemini_usage_reported")
			} else {
				require.Contains(t, a.Reasons, "gemini_usage_missing")
				require.Zero(t, result.usage.InputTokens)
			}
		})
	}
}

func TestKittyGeminiUsageEvidence_EmptyStreamIsUnknown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	svc := &GeminiMessagesCompatService{cfg: &config.Config{}}
	result, err := svc.handleStreamingResponse(c, &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(nil))}, time.Now(), "gemini-4-carbon")
	require.NoError(t, err)
	require.Contains(t, buildGeminiUsageAudit(*result.usage).Reasons, "gemini_usage_missing")
}

func TestKittyGeminiUsageEvidence_BillingLogCarriesEvidenceWithoutInventingFees(t *testing.T) {
	usage := mergeGeminiStreamUsage(ClaudeUsage{}, []byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{}}}]}}]}`))
	svc := &GatewayService{}
	log := svc.buildRecordUsageLog(context.Background(), &recordUsageCoreInput{}, &ForwardResult{Usage: usage, Model: "gemini-4-carbon"}, &APIKey{ID: 1}, &User{ID: 1}, &Account{ID: 1, Platform: PlatformGemini}, nil, "gemini-4-carbon", 0.5, 0.5, 1, 0, false, nil)
	require.NotNil(t, log.UsageAudit)
	require.Contains(t, log.UsageAudit.Reasons, "gemini_usage_missing")
	require.Zero(t, log.ActualCost)
	require.Zero(t, log.TotalCost)
	require.Zero(t, log.CacheReadTokens)
}

func TestKittyGeminiUsageEvidence_CustomPoolWrappersAcrossChatAndCollect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	raw := `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":2,"cachedContentTokenCount":80}}}`
	sse := "data: " + raw + "\n\n"
	svc := &GeminiMessagesCompatService{cfg: &config.Config{}}
	t.Run("chat nonstream", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		u, err := svc.handleChatCompletionsNonStreamingResponseFromGemini(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(raw))}, "gemini-4-carbon", false)
		require.NoError(t, err)
		require.Equal(t, 80, u.CacheReadInputTokens)
		require.Contains(t, rec.Body.String(), "OK")
	})
	t.Run("chat stream", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		r, err := svc.handleChatCompletionsStreamingResponseFromGemini(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(sse))}, time.Now(), "gemini-4-carbon", false, true)
		require.NoError(t, err)
		require.Equal(t, 80, r.usage.CacheReadInputTokens)
		require.Contains(t, rec.Body.String(), "OK")
	})
	t.Run("collect", func(t *testing.T) {
		m, u, err := collectGeminiSSE(&config.Config{}, strings.NewReader(sse), false)
		require.NoError(t, err)
		require.Equal(t, 80, u.CacheReadInputTokens)
		require.NotEmpty(t, extractGeminiParts(m))
	})
}

func TestKittyGeminiUsageEvidence_PartialExplicitZeroIsNotAReset(t *testing.T) {
	u := mergeGeminiStreamUsage(ClaudeUsage{}, []byte(`{"usageMetadata":{"promptTokenCount":20000,"cachedContentTokenCount":15000,"candidatesTokenCount":12,"thoughtsTokenCount":3}}`))
	u = mergeGeminiStreamUsage(u, []byte(`{"usageMetadata":{"cachedContentTokenCount":0}}`))
	require.Zero(t, u.CacheReadInputTokens)
	require.Equal(t, 20000, u.InputTokens)
	require.Zero(t, u.geminiUsageEvidence.ZeroResetsIgnored)
	u = mergeGeminiStreamUsage(u, []byte(`{"usageMetadata":{"thoughtsTokenCount":0}}`))
	require.Equal(t, 12, u.OutputTokens)
}

func TestKittyGeminiUsageEvidence_CollectedUsageReturnedToClient(t *testing.T) {
	sse := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"OK\"}]},\"finishReason\":\"STOP\"}]}\n\n" +
		"data: {\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":10,\"cachedContentTokenCount\":80}}\n\n"
	collected, u, err := collectGeminiSSE(&config.Config{}, strings.NewReader(sse), false)
	require.NoError(t, err)
	raw, err := json.Marshal(collected)
	require.NoError(t, err)
	response, billed := convertGeminiToClaudeMessageWithUsage(collected, "gemini-4-carbon", raw, false, u)
	values := response["usage"].(map[string]any)
	require.Equal(t, 20, values["input_tokens"])
	require.Equal(t, 10, values["output_tokens"])
	require.Equal(t, 80, values["cache_read_input_tokens"])
	require.Same(t, u, billed)
	chat, chatBilled, err := geminiResponseToChatCompletions(collected, "gemini-4-carbon", raw, u)
	require.NoError(t, err)
	require.Same(t, u, chatBilled)
	require.NotNil(t, chat.Usage)
	require.Equal(t, 100, chat.Usage.PromptTokens)
}

func TestKittyGeminiUsageEvidence_MergedCountersChargeGroupHalfOnce(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)
	pricing := &PricingService{}
	pricing.pricingData, err = pricing.parsePricingData(data)
	require.NoError(t, err)
	billing := NewBillingService(&config.Config{}, pricing)
	resolver := NewModelPricingResolver(nil, billing)
	group := &Group{ID: 73, Platform: PlatformGemini, RateMultiplier: 0.5}
	u := mergeGeminiStreamUsage(ClaudeUsage{}, []byte(`{"usageMetadata":{"promptTokenCount":20000,"cachedContentTokenCount":15000,"candidatesTokenCount":5,"thoughtsTokenCount":3}}`))
	u = mergeGeminiStreamUsage(u, []byte(`{"usageMetadata":{"candidatesTokenCount":12}}`))
	for _, tc := range []struct {
		model                string
		input, output, cache float64
	}{
		{"gemini-4-carbon", 4, 20, 0.20},
		{"gemini-4-hydrogen", 1.50, 7.50, 0.07},
	} {
		t.Run(tc.model, func(t *testing.T) {
			cost, err := billing.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: tc.model, GroupID: &group.ID, Group: group,
				Tokens: UsageTokens{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadInputTokens}, RateMultiplier: 0.5, Resolver: resolver})
			require.NoError(t, err)
			want := (5000*tc.input + 15*tc.output + 15000*tc.cache) / 1_000_000
			require.InDelta(t, want, cost.TotalCost, 1e-12)
			require.InDelta(t, want*0.5, cost.ActualCost, 1e-12)
			log := (&GatewayService{}).buildRecordUsageLog(context.Background(), &recordUsageCoreInput{}, &ForwardResult{Usage: u, Model: tc.model},
				&APIKey{ID: 1}, &User{ID: 1}, &Account{ID: 1, Platform: PlatformGemini}, nil, tc.model, 0.5, 0.5, 2, 0, false, cost)
			require.InDelta(t, want*0.5, log.ActualCost, 1e-12, "account multiplier must not be applied to user charge")
			require.InDelta(t, want, log.TotalCost, 1e-12)
			require.Equal(t, 2.0, *log.AccountRateMultiplier)
			require.Equal(t, 15000, log.CacheReadTokens)
			require.Equal(t, 75.0, *log.UsageAudit.CacheHitRate)
		})
	}
}

func TestKittyGeminiUsageEvidence_RejectsCounterAndCombinedOutputOverflow(t *testing.T) {
	for _, raw := range []string{
		`{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":4611686018427387904,"thoughtsTokenCount":4611686018427387904}}`,
		`{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":2147483647,"thoughtsTokenCount":1}}`,
	} {
		u := mergeGeminiStreamUsage(ClaudeUsage{}, []byte(raw))
		require.Zero(t, u.OutputTokens, "unusable counters must not wrap or be clamped into charges")
		a := buildGeminiUsageAudit(u)
		require.Contains(t, a.Reasons, "gemini_usage_invalid_counters")
		require.Contains(t, a.Reasons, "gemini_usage_partial")
		require.Equal(t, UsageAuditStatusWarning, a.Status)
		require.Nil(t, a.Upstream.OutputTokens)
		prior := mergeGeminiStreamUsage(ClaudeUsage{}, []byte(`{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":5,"thoughtsTokenCount":3}}`))
		merged := mergeGeminiStreamUsage(prior, []byte(raw))
		require.Equal(t, 8, merged.OutputTokens, "an invalid later update cannot erase valid earlier evidence")
		require.Zero(t, prior.geminiUsageEvidence.InvalidCounters, "snapshots must not mutate")
	}
	boundary := mergeGeminiStreamUsage(ClaudeUsage{}, []byte(`{"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":2147483646,"thoughtsTokenCount":1}}`))
	require.Equal(t, math.MaxInt32, boundary.OutputTokens)
	require.Zero(t, boundary.geminiUsageEvidence.InvalidCounters)
}
