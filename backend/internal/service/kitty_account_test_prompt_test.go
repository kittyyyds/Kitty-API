//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountTestPayloadPromptReasoningAndMaxOutputTokens(t *testing.T) {
	candy := CandyPrompt + "\n只输出最终整数，不要解释。"
	ctx := withAccountTestReasoningEffort(withAccountTestMaxOutputTokens(context.Background(), 8192), "high")

	oai := createOpenAITestPayload("gpt-6.1-sol", false, candy)
	applyAccountTestOutputTokenLimit(ctx, oai, "max_output_tokens")
	applyAccountTestReasoningEffort(oai, accountTestReasoningEffort(ctx))
	inputs := oai["input"].([]map[string]any)
	content := inputs[0]["content"].([]map[string]any)
	require.Equal(t, candy, content[0]["text"])
	require.Equal(t, 8192, oai["max_output_tokens"])
	require.Equal(t, map[string]any{"effort": "high"}, oai["reasoning"])

	claudePayload, err := createTestPayloadForAccount("claude-sonnet-4-6", nil, candy)
	require.NoError(t, err)
	applyAccountTestOutputTokenLimit(ctx, claudePayload, "max_tokens")
	cMsgs := claudePayload["messages"].([]map[string]any)
	cContent := cMsgs[0]["content"].([]map[string]any)
	require.Equal(t, candy, cContent[0]["text"])
	require.Equal(t, 8192, claudePayload["max_tokens"])

	gemBytes := createGeminiTestPayload("gemini-2.5-pro", candy, accountTestOutputTokenLimit(ctx))
	var gem map[string]any
	require.NoError(t, json.Unmarshal(gemBytes, &gem))
	genCfg := gem["generationConfig"].(map[string]any)
	require.Equal(t, float64(8192), genCfg["maxOutputTokens"])
}
