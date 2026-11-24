//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestKitty36CompositeReportsPublicNameAndKeepsUpstreamObservation(t *testing.T) {
	ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{
		Matched: true, PublicModel: "kitty-composite", TargetPlatform: PlatformAnthropic, UpstreamModel: "configured",
	})
	service := &GatewayService{cfg: rawChatCompletionsTestConfig(), rateLimitService: &RateLimitService{}}
	account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}
	t.Run("streaming", func(t *testing.T) {
		c, recorder := newTestContext()
		resp := nativeAnthropicStreamResponse()
		defer resp.Body.Close()
		result, err := service.handleStreamingResponse(ctx, resp, c, account, time.Now(), "configured", "configured", false)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Contains(t, recorder.Body.String(), `"model":"kitty-composite"`)
		require.Equal(t, "k3", upstreamResponseModelObserverFromContext(c).Model())
		require.Equal(t, 93, result.usage.InputTokens)
	})
	t.Run("buffered", func(t *testing.T) {
		c, recorder := newTestContext()
		resp := &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"model":"dated-model","content":[{"text":"hello","type":"text"}],"usage":{"input_tokens":12,"output_tokens":7}}`)),
		}
		defer resp.Body.Close()
		usage, err := service.handleNonStreamingResponse(ctx, resp, c, account, "configured", "configured")
		require.NoError(t, err)
		require.Equal(t, "kitty-composite", gjson.Get(recorder.Body.String(), "model").String())
		require.Equal(t, "dated-model", upstreamResponseModelObserverFromContext(c).Model())
		require.Equal(t, 7, usage.OutputTokens)
	})
}

func TestKitty36CodexForkScopesParentCacheKeyByOwnerAndKey(t *testing.T) {
	parent := "31a3f935-aab5-4448-83ca-7624cf41b383"
	fork := "bae24fab-5809-4c97-90b5-7a1c8009bdf4"
	body := []byte(`{"prompt_cache_key":"` + parent + `","client_metadata":{"session_id":"` + fork + `"}}`)
	account := newTestOAuthAccount(36, nil)
	account.Credentials = map[string]any{"chatgpt_account_id": "test-workspace"}
	mapping := func(a *Account, id int64) string {
		next, changed, err := applyCodexAccountIdentityClientMetadataRaw(body, a, id, parent)
		require.NoError(t, err)
		require.True(t, changed)
		header := http.Header{}
		header.Set("session-id", parent)
		applyCodexAccountIdentityHeaders(header, a, id)
		key := gjson.GetBytes(next, "prompt_cache_key").String()
		require.Equal(t, header.Get("session-id"), key)
		require.NotEqual(t, key, gjson.GetBytes(next, "client_metadata.session_id").String())
		return key
	}
	first := mapping(account, 1)
	require.Equal(t, first, mapping(account, 1))
	require.NotEqual(t, first, mapping(account, 2))
	other := newTestOAuthAccount(37, nil)
	other.Credentials = map[string]any{"chatgpt_account_id": "other-workspace"}
	require.NotEqual(t, first, mapping(other, 1))
}

func TestKitty36AdditionalToolsKeepDeclarationAndCallIDs(t *testing.T) {
	raw := []byte(`{"input":[{"type":"additional_tools","id":"declaration","tools":[{"type":"function","name":"python"}]},{"type":"function_call","name":"python","id":"fc_message","call_id":"call_invoke","arguments":"{}"},{"type":"function_call_output","call_id":"call_invoke","output":"ok"}]}`)
	out, reverse, changed, err := aliasOpenAIOAuthReservedToolNamesBody(raw)
	require.NoError(t, err)
	require.True(t, changed)
	alias := gjson.GetBytes(out, "input.0.tools.0.name").String()
	require.Equal(t, "python", reverse[alias])
	require.Equal(t, alias, gjson.GetBytes(out, "input.1.name").String())
	require.Equal(t, "declaration", gjson.GetBytes(out, "input.0.id").String())
	require.Equal(t, "fc_message", gjson.GetBytes(out, "input.1.id").String())
	require.Equal(t, "call_invoke", gjson.GetBytes(out, "input.1.call_id").String())
	require.Equal(t, "call_invoke", gjson.GetBytes(out, "input.2.call_id").String())
}
