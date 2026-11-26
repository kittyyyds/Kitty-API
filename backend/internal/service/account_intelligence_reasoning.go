package service

import "github.com/Wei-Shaw/sub2api/internal/pkg/openai"

// Production returned HTTP 400 for gpt-6.1-sol + minimal on 2026-11-26:
// supported efforts are low, medium, high, xhigh and max. Apply this known
// capability rule to the mapped model in both manual and scheduled runs, not
// to unrelated models. Never change an explicit valid effort or the model.
func normalizeAccountIntelligenceReasoning(account *Account, in *AccountIntelligenceInput) {
	if account == nil || account.Platform != PlatformOpenAI || in.ReasoningEffort != "minimal" {
		return
	}
	model := account.GetMappedModel(in.Model)
	if !openai.IsGPT61SolModelSpelling(model) {
		return
	}
	in.RequestedReasoningEffort = in.ReasoningEffort
	in.ReasoningEffort = "low"
}
