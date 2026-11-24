package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKitty36CacheWriteExplicitZeroAndNullAlias(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{`{"cache_write_tokens":0,"cache_creation_tokens":7}`, 0},
		{`{"cache_write_tokens":null,"cache_creation_tokens":7}`, 7},
		{`{"cache_creation_tokens":7}`, 7},
		{`{"cache_write_tokens":-4,"cache_creation_tokens":7}`, 0},
	} {
		var details ChatTokenDetails
		require.NoError(t, json.Unmarshal([]byte(tc.raw), &details))
		u := &ChatUsage{PromptTokens: 30, PromptTokensDetails: &details}
		require.Equal(t, tc.want, chatUsageToAnthropicUsage(u).CacheCreationInputTokens)
		require.Equal(t, tc.want, ChatUsageToResponsesUsage(u).CacheCreationInputTokens)
		body, err := json.Marshal(details)
		require.NoError(t, err)
		if details.cacheWriteTokensPresent && details.CacheWriteTokens == 0 {
			require.Contains(t, string(body), `"cache_write_tokens":0`)
		}
	}
}

func TestKitty36AnthropicCacheOnlyDeltaRetainsOtherUsageAndTTL(t *testing.T) {
	state := NewAnthropicEventToResponsesState()
	state.InputTokens, state.OutputTokens, state.CacheReadInputTokens = 100, 12, 20
	var u AnthropicUsage
	require.NoError(t, json.Unmarshal([]byte(`{"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":0}}`), &u))
	state.mergeCacheCreationUsage(u)
	// A later cache-only message delta must not erase the prior output count.
	anthToResHandleMessageDelta(&AnthropicStreamEvent{Usage: &u}, state)
	require.Equal(t, 12, state.OutputTokens)
	require.Equal(t, 100, state.InputTokens)
	require.Equal(t, 20, state.CacheReadInputTokens)
	require.Equal(t, 5, state.CacheCreationInputTokens)
	completed := makeResponsesCompletedEvent(state, "completed", nil)
	require.NotNil(t, completed.Response.Usage.CacheCreation)
	require.Equal(t, 0, *completed.Response.Usage.CacheCreation.Ephemeral1hInputTokens)
	chat := chatUsageFromResponsesUsage(completed.Response.Usage)
	ant := chatUsageToAnthropicUsage(chat)
	require.Equal(t, 5, ant.CacheCreationInputTokens)
	require.NotNil(t, ant.CacheCreation)
	require.Equal(t, 5, *ant.CacheCreation.Ephemeral5mInputTokens)
	require.Equal(t, 0, *ant.CacheCreation.Ephemeral1hInputTokens)
	*ant.CacheCreation.Ephemeral5mInputTokens = 99
	require.Equal(t, 5, *state.CacheCreation.Ephemeral5mInputTokens)

	require.NoError(t, json.Unmarshal([]byte(`{"cache_creation_input_tokens":0,"cache_creation":{"ephemeral_1h_input_tokens":7}}`), &u))
	require.True(t, u.HasCacheCreationInputTokens())
	require.Zero(t, anthropicCacheCreationInputTokens(u))
	state.mergeCacheCreationUsage(u)
	require.Zero(t, state.CacheCreationInputTokens)
	body, err := json.Marshal(u)
	require.NoError(t, err)
	require.Contains(t, string(body), `"cache_creation_input_tokens":0`)
	completed = makeResponsesCompletedEvent(state, "completed", nil)
	body, err = json.Marshal(completed.Response.Usage)
	require.NoError(t, err)
	require.Contains(t, string(body), `"cache_creation_input_tokens":0`)
	var wire ResponsesUsage
	require.NoError(t, json.Unmarshal(body, &wire))
	require.True(t, wire.CacheCreationUsageReported())
	chat = chatUsageFromResponsesUsage(&wire)
	require.Zero(t, chatUsageToAnthropicUsage(chat).CacheCreationInputTokens)
}
