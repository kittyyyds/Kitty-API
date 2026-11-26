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
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
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


type kittyPoolFuncUpstream struct {
	fn func(req *http.Request) (*http.Response, error)
}

func (u *kittyPoolFuncUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.fn(req)
}

func (u *kittyPoolFuncUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.fn(req)
}

func TestKitty6_PreservesToolCallIDsForPoolAndStripsForOfficial(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 1. Streaming response from SubLink preserves upstream fc.id instead of randomizing
	sseStream := `data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"toolu_01SubLinkExactID999","name":"get_node_status","args":{"node_id":20}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":32393,"candidatesTokenCount":25}}` + "\n\n"
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	svc := &GeminiMessagesCompatService{cfg: &config.Config{}}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(bytes.NewBufferString(sseStream)),
	}
	_, err := svc.handleStreamingResponse(c, resp, time.Now(), "gemini-4-neon")
	require.NoError(t, err)
	require.Contains(t, rec.Body.String(), `"id":"toolu_01SubLinkExactID999"`)

	// 2. Non-streaming response preserves upstream fc.id
	rawJSON := []byte(`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"toolu_01NonStreamExactID","name":"get_node_status","args":{"node_id":20}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10}}`)
	var parsedResp map[string]any
	require.NoError(t, json.Unmarshal(rawJSON, &parsedResp))
	claudeMsg, _ := convertGeminiToClaudeMessage(parsedResp, "gemini-4-neon", rawJSON, false)
	blocks := claudeMsg["content"].([]any)
	require.Equal(t, "toolu_01NonStreamExactID", blocks[0].(map[string]any)["id"])

	// 3. Turn 2 conversion keeps functionCall.id + functionResponse.id for custom pool, strips for official Google
	turn2ClaudeReq := map[string]any{
		"model": "gemini-4-neon",
		"messages": []any{
			map[string]any{"role": "user", "content": "Check node 20"},
			map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{
						"type":  "tool_use",
						"id":    "toolu_01SubLinkExactID999",
						"name":  "get_node_status",
						"input": map[string]any{"node_id": 20},
					},
				},
			},
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type":        "tool_result",
						"tool_use_id": "toolu_01SubLinkExactID999",
						"content":     "healthy",
					},
				},
			},
		},
	}
	turn2Bytes, err := json.Marshal(turn2ClaudeReq)
	require.NoError(t, err)

	poolConverted, err := convertClaudeMessagesToGeminiGenerateContentWithOptions(turn2Bytes, "gemini-4-neon", true)
	require.NoError(t, err)
	require.Contains(t, string(poolConverted), `"functionCall":{"args":{"node_id":20},"id":"toolu_01SubLinkExactID999","name":"get_node_status"}`)
	require.Contains(t, string(poolConverted), `"functionResponse":{"id":"toolu_01SubLinkExactID999","name":"get_node_status"`)

	officialConverted, err := convertClaudeMessagesToGeminiGenerateContent(turn2Bytes, "gemini-2.5-flash")
	require.NoError(t, err)
	require.NotContains(t, string(officialConverted), "toolu_01SubLinkExactID999")
}

func TestKitty6_HeaderlessStickySeedIgnoresMovingCacheControlAndDriftingContextHash(t *testing.T) {
	gin.SetMode(gin.TestMode)

	turn1Body := []byte(`{
		"system": [{"type":"text","text":"Repo instructions","cache_control":{"type":"ephemeral"}}],
		"messages": [
			{"role":"user","content":[{"type":"text","text":"First prompt","cache_control":{"type":"ephemeral"}}]}
		]
	}`)
	turn2Body := []byte(`{
		"system": [{"type":"text","text":"Repo instructions"}],
		"messages": [
			{"role":"user","content":[{"type":"text","text":"First prompt"}]},
			{"role":"assistant","content":[{"type":"text","text":"First answer"}]},
			{"role":"user","content":[{"type":"text","text":"Second prompt","cache_control":{"type":"ephemeral"}}]}
		]
	}`)

	c1, _ := gin.CreateTestContext(httptest.NewRecorder())
	c1.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ctx1 := WithGeminiUpstreamSession(context.Background(), "gemini:hash-from-turn1-breakpoint")
	sid1 := resolveGeminiPoolStickySessionID(ctx1, c1, turn1Body)

	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ctx2 := WithGeminiUpstreamSession(context.Background(), "gemini:different-hash-from-turn2-breakpoint")
	sid2 := resolveGeminiPoolStickySessionID(ctx2, c2, turn2Body)

	require.NotEmpty(t, sid1)
	require.Equal(t, sid1, sid2, "sticky session ID must remain invariant when cache_control moves across turns")
}

func TestKitty6_DeadAccountFake200RebindsAndPersistsAcrossTurns(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var seenSessions []string
	upstream := &kittyPoolFuncUpstream{
		fn: func(req *http.Request) (*http.Response, error) {
			sid := req.Header.Get("x-sublink-session")
			seenSessions = append(seenSessions, sid)

			// Simulate first two sticky bindings landing on banned Google accounts (fake HTTP 200 SSE)
			if sid == "sess-dead-test" || sid == "sess-dead-test-r1" {
				deadSSE := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"⚠ This service has been disabled in this account for violation of Terms of Service."}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":0,"totalTokenCount":0}}` + "\n\n"
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       io.NopCloser(bytes.NewBufferString(deadSSE)),
				}, nil
			}

			// Rebound session -r2 lands on healthy account and hits cache on Turn 2!
			cached := 0
			if len(seenSessions) > 3 {
				cached = 29800
			}
			healthySSE := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Healthy answer"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":32000,"cachedContentTokenCount":` + jsonNumberString(cached) + `,"candidatesTokenCount":8}}` + "\n\n"
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(bytes.NewBufferString(healthySSE)),
			}, nil
		},
	}

	svc := &GeminiMessagesCompatService{
		cfg:          &config.Config{},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          1000000040,
		Platform:    PlatformGemini,
		Type:        AccountTypeAPIKey,
		Concurrency: 10,
		Credentials: map[string]any{
			"api_key":  "sk-sublink-test",
			"base_url": "https://sublink.45-77-28-117.sslip.io",
		},
	}
	reqBody := []byte(`{"model":"gemini-4-neon","stream":true,"messages":[{"role":"user","content":"Hello"}]}`)

	// Turn 1: tries sess-dead-test (dead) -> sess-dead-test-r1 (dead) -> sess-dead-test-r2 (healthy!)
	rec1 := httptest.NewRecorder()
	c1, _ := gin.CreateTestContext(rec1)
	c1.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(reqBody))
	c1.Request.Header.Set("X-Claude-Code-Session-Id", "sess-dead-test")

	res1, err := svc.Forward(context.Background(), c1, account, reqBody)
	require.NoError(t, err)
	require.Equal(t, 32000, res1.Usage.InputTokens)
	require.Contains(t, rec1.Body.String(), "Healthy answer")
	require.NotContains(t, rec1.Body.String(), "disabled in this account")
	require.Equal(t, []string{"sess-dead-test", "sess-dead-test-r1", "sess-dead-test-r2"}, seenSessions)

	// Turn 2: same client session header jumps straight to sess-dead-test-r2 on attempt 1 and gets cache hit!
	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	c2.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(reqBody))
	c2.Request.Header.Set("X-Claude-Code-Session-Id", "sess-dead-test")

	res2, err := svc.Forward(context.Background(), c2, account, reqBody)
	require.NoError(t, err)
	require.Equal(t, 29800, res2.Usage.CacheReadInputTokens)
	require.Equal(t, 2200, res2.Usage.InputTokens)
	require.Equal(t, []string{"sess-dead-test", "sess-dead-test-r1", "sess-dead-test-r2", "sess-dead-test-r2"}, seenSessions)
}

func jsonNumberString(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}


func TestKitty6_LowercaseToolSchemaTypesForPoolAndUppercaseForOfficial(t *testing.T) {
	claudeBody, _ := json.Marshal(map[string]any{
		"model":      "gemini-4-neon",
		"max_tokens": 256,
		"tools": []any{
			map[string]any{
				"name":        "get_node_live_status",
				"description": "Check live node status",
				"input_schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"node_id": map[string]any{"type": "integer"},
						"mode":    map[string]any{"const": "FAST"},
					},
					"required": []any{"node_id"},
				},
			},
		},
		"messages": []any{
			map[string]any{"role": "user", "content": "check node 20"},
		},
	})

	poolOut, err := convertClaudeMessagesToGeminiGenerateContentWithOptions(claudeBody, "gemini-4-neon", true)
	require.NoError(t, err)
	require.Equal(t, "object", gjson.GetBytes(poolOut, "tools.0.functionDeclarations.0.parameters.type").String())
	require.Equal(t, "integer", gjson.GetBytes(poolOut, "tools.0.functionDeclarations.0.parameters.properties.node_id.type").String())
	require.Equal(t, "string", gjson.GetBytes(poolOut, "tools.0.functionDeclarations.0.parameters.properties.mode.type").String())
	require.Equal(t, "FAST", gjson.GetBytes(poolOut, "tools.0.functionDeclarations.0.parameters.properties.mode.enum.0").String())

	officialOut, err := convertClaudeMessagesToGeminiGenerateContentWithOptions(claudeBody, "gemini-2.5-pro", false)
	require.NoError(t, err)
	require.Equal(t, "OBJECT", gjson.GetBytes(officialOut, "tools.0.functionDeclarations.0.parameters.type").String())
	require.Equal(t, "INTEGER", gjson.GetBytes(officialOut, "tools.0.functionDeclarations.0.parameters.properties.node_id.type").String())
	require.Equal(t, "STRING", gjson.GetBytes(officialOut, "tools.0.functionDeclarations.0.parameters.properties.mode.type").String())

	nativeUpper := []byte(`{"tools":[{"functionDeclarations":[{"name":"get_node_live_status","parameters":{"type":"OBJECT","properties":{"node_id":{"type":"INTEGER"}}}}]}]}`)
	nativePool := normalizeGeminiPoolToolSchemasInBody(nativeUpper)
	require.Equal(t, "object", gjson.GetBytes(nativePool, "tools.0.functionDeclarations.0.parameters.type").String())
	require.Equal(t, "integer", gjson.GetBytes(nativePool, "tools.0.functionDeclarations.0.parameters.properties.node_id.type").String())
}
