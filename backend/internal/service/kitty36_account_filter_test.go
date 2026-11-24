//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestKitty36PassthroughModelFilterIsOptIn(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		for _, flag := range []any{nil, false, "true", 1, true} {
			a := &Account{Platform: PlatformOpenAI, Type: accountType,
				Credentials: map[string]any{"model_mapping": map[string]any{"gpt-6.2": "other-model", "custom-*": "target"}},
				Extra:       map[string]any{"openai_passthrough": true, "openai_passthrough_model_filter": flag}}
			require.True(t, a.IsModelSupported("gpt-6.2"))
			require.True(t, a.IsModelSupported("custom-a"))
			require.Equal(t, flag != true, a.IsModelSupported("unknown-model"))
			// A mapping target never becomes an implicit allowed source name.
			require.Equal(t, flag != true, a.IsModelSupported("other-model"))
			a.Credentials["model_mapping"] = map[string]any{}
			require.True(t, a.IsModelSupported("unknown-model"))
		}
	}
	a := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: map[string]any{
		"openai_passthrough": true, "openai_passthrough_model_filter": true}}
	require.False(t, a.IsOpenAIPassthroughModelFilterEnabled())
}

func TestKitty36PassthroughCatalogUsesSourceNames(t *testing.T) {
	a := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-6.2": "target-only", "custom-*": "ignored"}},
		Extra:       map[string]any{"openai_passthrough": true, "openai_passthrough_model_filter": true}}
	for _, codex := range []bool{false, true} {
		body := []byte(`{"object":"list","data":[{"id":"gpt-6.2","marker":42},{"id":"target-only"},{"id":"custom-a"},{"id":"unsupported"}],"extra":"kept"}`)
		path := "data"
		if codex {
			body = bytes.ReplaceAll(body, []byte(`"data"`), []byte(`"models"`))
			body = bytes.ReplaceAll(body, []byte(`"id"`), []byte(`"slug"`))
			path = "models"
		}
		projected, err := projectAccountModelsBody(body, &a, nil, codex)
		require.NoError(t, err)
		require.Len(t, gjson.GetBytes(projected, path).Array(), 2)
		require.Equal(t, int64(42), gjson.GetBytes(projected, path+".0.marker").Int())
		require.Equal(t, "kept", gjson.GetBytes(projected, "extra").String())
		require.NotContains(t, string(projected), "target-only")
		// An unfiltered account in the same group retains the other live names.
		unfiltered := a
		unfiltered.Extra = map[string]any{"openai_passthrough": true}
		mixed, changed, err := filterOpenAIPassthroughCatalog(body, []Account{a, unfiltered}, codex)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, body, mixed)
	}

	groupID := int64(99)
	repo := &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {a}}}
	svc := &GatewayService{accountRepo: repo}
	models := svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI)
	require.Contains(t, models, "gpt-6.2")
	require.NotContains(t, models, "gpt-6-astra")
	require.NotContains(t, models, "target-only")
	require.NotContains(t, models, "custom-*")
	// A pattern with no known concrete model must not resurrect the full
	// default catalog, and a later ordinary account may still serve defaults.
	a.Credentials = map[string]any{"model_mapping": map[string]any{"custom-*": "ignored"}}
	repo.byGroup[groupID] = []Account{a}
	models = svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI)
	require.NotNil(t, models)
	require.Empty(t, models)
}

func TestKitty36PassthroughFilterNeverRewritesWireModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		t.Run(accountType, func(t *testing.T) {
			body := []byte(`{"model":"gpt-6.2","stream":false,"instructions":"fixture","input":"hi"}`)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK,
				Header: http.Header{"Content-Type": []string{"application/json"}},
				Body:   io.NopCloser(strings.NewReader(`{"id":"resp_source","model":"gpt-6.2","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":1}}`))}}
			a := &Account{ID: 1, Platform: PlatformOpenAI, Type: accountType, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"access_token": "fixture", "chatgpt_account_id": "owner-fixture", "api_key": "fixture",
					"model_mapping": map[string]any{"gpt-6.2": "target-must-not-be-sent"}},
				Extra: map[string]any{"openai_passthrough": true, "openai_passthrough_model_filter": true}}
			svc := &OpenAIGatewayService{httpUpstream: upstream}
			result, err := svc.Forward(context.Background(), c, a, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, "gpt-6.2", gjson.GetBytes(upstream.lastBody, "model").String())
			require.NotContains(t, string(upstream.lastBody), "target-must-not-be-sent")
		})
	}
}

func TestKitty36GeminiStickySwitchPreservesRealUsage(t *testing.T) {
	for _, platform := range []string{PlatformGemini, PlatformAntigravity, PlatformAnthropic} {
		usage := ClaudeUsage{InputTokens: 20, CacheReadInputTokens: 7, CacheCreationInputTokens: 3}
		changed := applyForceCacheBilling(&usage, &Account{Platform: platform, Type: AccountTypeOAuth}, true)
		if platform == PlatformAnthropic {
			require.True(t, changed)
			require.Equal(t, 27, usage.CacheReadInputTokens)
		} else {
			require.False(t, changed)
			require.Equal(t, ClaudeUsage{InputTokens: 20, CacheReadInputTokens: 7, CacheCreationInputTokens: 3}, usage)
		}
	}
}
