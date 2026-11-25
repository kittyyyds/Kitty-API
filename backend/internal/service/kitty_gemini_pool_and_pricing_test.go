//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeminiPoolStickyHeaders_CustomPoolVsOfficialGoogle(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("X-Claude-Code-Session-Id", "sess-cc-12345")

	ctx := WithGeminiUpstreamSession(context.Background(), "gemini:fallback-hash")

	// 1. Custom pool (SubLink) receives sticky headers
	poolReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://sublink.45-77-28-117.sslip.io/v1beta/models/gemini-4-neon:streamGenerateContent?alt=sse", nil)
	require.NoError(t, err)
	applyGeminiAPIKeyPoolSessionHeaders(ctx, c, poolReq, "https://sublink.45-77-28-117.sslip.io", nil)
	require.Equal(t, "sess-cc-12345", poolReq.Header.Get("x-sublink-session"))
	require.Equal(t, "sess-cc-12345", poolReq.Header.Get("x-session-id"))
	require.Equal(t, "sess-cc-12345", poolReq.Header.Get("x-session-affinity"))
	require.Equal(t, "sess-cc-12345", poolReq.Header.Get("x-claude-code-session-id"))

	// 2. Official Google endpoint receives NO custom sticky headers
	googReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse", nil)
	require.NoError(t, err)
	applyGeminiAPIKeyPoolSessionHeaders(ctx, c, googReq, "https://generativelanguage.googleapis.com", nil)
	require.Empty(t, googReq.Header.Get("x-sublink-session"))
	require.Empty(t, googReq.Header.Get("x-session-id"))

	// 3. Fallback to gateway sessionHash when client sent no explicit header
	cNoHdr, _ := gin.CreateTestContext(httptest.NewRecorder())
	cNoHdr.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	poolReq2, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://sublink.45-77-28-117.sslip.io/v1beta/models/gemini-4-neon:streamGenerateContent?alt=sse", nil)
	require.NoError(t, err)
	applyGeminiAPIKeyPoolSessionHeaders(ctx, cNoHdr, poolReq2, "https://sublink.45-77-28-117.sslip.io", nil)
	require.Equal(t, "fallback-hash", poolReq2.Header.Get("x-sublink-session"))
}

func TestGeminiStreamUsagePreservesCacheReadAndEmitsToClaudeClient(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sseStream := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]}}],"usageMetadata":{"promptTokenCount":20000,"cachedContentTokenCount":15000,"candidatesTokenCount":5}}` + "\n\n" +
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello world"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":20000,"candidatesTokenCount":12}}` + "\n\n"

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	svc := &GeminiMessagesCompatService{cfg: &config.Config{}}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(sseStream)),
	}
	res, err := svc.handleStreamingResponse(c, resp, time.Now(), "gemini-4-neon")
	require.NoError(t, err)
	require.Equal(t, 5000, res.usage.InputTokens)
	require.Equal(t, 15000, res.usage.CacheReadInputTokens)
	require.Equal(t, 12, res.usage.OutputTokens)
	require.Contains(t, rec.Body.String(), `"cache_read_input_tokens":15000`)
	require.Contains(t, rec.Body.String(), `"input_tokens":5000`)
}

func TestKitty20ModelsBase1xAndGroupHalfRateBilling(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)

	pricingSvc := &PricingService{}
	pricingSvc.pricingData, err = pricingSvc.parsePricingData(data)
	require.NoError(t, err)

	billingSvc := NewBillingService(&config.Config{}, pricingSvc)
	resolver := NewModelPricingResolver(nil, billingSvc)

	cases := []struct {
		model string
		in1M  float64
		out1M float64
		cr1M  float64
		cw1M  float64
	}{
		{"ag-claude-opus-4-6-thinking", 5.00, 25.00, 0.50, 6.25},
		{"ag-claude-opus-5-5", 4.00, 20.00, 0.20, 5.00},
		{"ag-claude-sonnet-4-6", 3.00, 15.00, 0.30, 3.75},
		{"ag-claude-sonnet-5-5", 2.00, 10.00, 0.10, 2.50},
		{"ag-gpt-oss-120b", 0.50, 2.00, 0.05, 0.50},
		{"gemini-2.5-flash", 0.30, 2.50, 0.03, 0.30},
		{"gemini-2.5-flash-lite", 0.30, 2.50, 0.03, 0.30},
		{"gemini-2.5-flash-thinking", 0.30, 2.50, 0.03, 0.30},
		{"gemini-3-flash", 0.50, 3.00, 0.05, 0.50},
		{"gemini-3.1-flash-image", 0.50, 3.00, 0.05, 0.50},
		{"gemini-3.1-flash-lite", 0.50, 3.00, 0.05, 0.50},
		{"gemini-3.5-flash-lite", 0.50, 3.00, 0.05, 0.50},
		{"gemini-3.8-flash", 0.50, 3.00, 0.05, 0.50},
		{"gemini-3.8-flash-tiered", 0.50, 3.00, 0.05, 0.50},
		{"gemini-4-argon", 4.00, 20.00, 0.20, 4.00},
		{"gemini-4-carbon", 4.00, 20.00, 0.20, 4.00},
		{"gemini-4-hydrogen", 1.50, 7.50, 0.07, 1.50},
		{"gemini-4-hydrogen-sonar", 1.50, 7.50, 0.07, 1.50},
		{"gemini-4-hydrogen-tiered", 1.50, 7.50, 0.07, 1.50},
		{"gemini-4-neon", 3.00, 15.00, 0.15, 3.00},
	}

	halfGroup := &Group{ID: 73, Name: "Gemini - 高缓稳定号池", Platform: PlatformGemini, RateMultiplier: 0.5}
	gid := int64(73)
	tokens := UsageTokens{
		InputTokens:         1_000_000,
		OutputTokens:        1_000_000,
		CacheReadTokens:     1_000_000,
		CacheCreationTokens: 1_000_000,
	}

	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			// 1. Plaza schedule resolves 1x base price (so Plaza UI multiplies by 0.5 once -> 0.5x)
			sched, err := billingSvc.ResolveContextPricingSchedule(context.Background(), resolver, ContextPricingScheduleInput{
				Model:    tc.model,
				Group:    halfGroup,
				Platform: PlatformGemini,
			})
			require.NoError(t, err)
			require.NotEmpty(t, sched.Tiers)
			tier0 := sched.Tiers[0]
			require.InDelta(t, tc.in1M*1e-6, *tier0.Input, 1e-12)
			require.InDelta(t, tc.out1M*1e-6, *tier0.Output, 1e-12)
			require.InDelta(t, tc.cr1M*1e-6, *tier0.CacheRead, 1e-12)
			require.InDelta(t, tc.cw1M*1e-6, *tier0.CacheWrite, 1e-12)

			// 2. Billing at 0.5x group rate: TotalCost = 1x, ActualCost = 0.5x!
			bd, err := billingSvc.CalculateCostUnified(CostInput{
				Ctx:            context.Background(),
				Model:          tc.model,
				GroupID:        &gid,
				Group:          halfGroup,
				Tokens:         tokens,
				RateMultiplier: 0.5,
				Resolver:       resolver,
			})
			require.NoError(t, err)
			want1x := tc.in1M + tc.out1M + tc.cr1M + tc.cw1M
			require.InDelta(t, want1x, bd.TotalCost, 1e-9, "TotalCost must equal 1x base cost")
			require.InDelta(t, want1x*0.5, bd.ActualCost, 1e-9, "ActualCost must equal 0.5x group charged cost")
		})
	}

	// Verify non-Stream Claude message conversion includes cache_read_input_tokens
	rawResp, _ := json.Marshal(map[string]any{
		"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": "ok"}}}, "finishReason": "STOP"}},
		"usageMetadata": map[string]any{"promptTokenCount": 1000, "cachedContentTokenCount": 600, "candidatesTokenCount": 20},
	})
	var parsed map[string]any
	_ = json.Unmarshal(rawResp, &parsed)
	claudeMsg, u := convertGeminiToClaudeMessage(parsed, "gemini-4-neon", rawResp, false)
	require.Equal(t, 400, u.InputTokens)
	require.Equal(t, 600, u.CacheReadInputTokens)
	usageMap := claudeMsg["usage"].(map[string]any)
	require.Equal(t, 600, usageMap["cache_read_input_tokens"])
}
