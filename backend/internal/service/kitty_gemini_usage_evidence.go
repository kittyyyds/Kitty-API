package service

import (
	"math"

	"github.com/tidwall/gjson"
)

// GeminiUsageEvidence is bounded, token-only telemetry. No prompt, response text,
// tool arguments, tool IDs, credentials, or request headers are captured.
// Pointers deliberately distinguish an omitted counter from an explicit zero.
type GeminiUsageEvidence struct {
	Frames            int  `json:"frames"`
	UsageFrames       int  `json:"usage_frames"`
	ToolCallEvents    int  `json:"tool_call_events"`
	ZeroResetsIgnored int  `json:"zero_resets_ignored"`
	CacheRegressions  int  `json:"cache_regressions"`
	InvalidCounters   int  `json:"invalid_counters"`
	PromptTokens      *int `json:"prompt_tokens,omitempty"`
	CandidateTokens   *int `json:"candidate_tokens,omitempty"`
	ThoughtTokens     *int `json:"thought_tokens,omitempty"`
	CacheReadTokens   *int `json:"cache_read_tokens,omitempty"`
	TotalTokens       *int `json:"total_tokens,omitempty"`
}

// usage_logs stores individual token counters as PostgreSQL INT. Reject values
// that cannot be persisted instead of wrapping, clamping or billing invented data.
const maxGeminiStoredTokenCount = math.MaxInt32

func geminiReportedCounter(meta gjson.Result, name string) (int, bool) {
	value := meta.Get(name)
	if value.Type != gjson.Number || value.Num < 0 || math.IsNaN(value.Num) || math.IsInf(value.Num, 0) || math.Trunc(value.Num) != value.Num || value.Num > float64(maxGeminiStoredTokenCount) {
		return 0, false
	}
	return int(value.Num), true
}

func mergeGeminiUsageEvidence(prev ClaudeUsage, raw []byte) ClaudeUsage {
	var evidence GeminiUsageEvidence
	if prev.geminiUsageEvidence != nil {
		evidence = *prev.geminiUsageEvidence
	}
	prev.geminiUsageEvidence = &evidence
	evidence.Frames++

	// Wrappers occur on custom API-key pools as well as OAuth relays.
	if inner, err := unwrapGeminiResponse(raw); err == nil {
		raw = inner
	}
	for _, candidate := range gjson.GetBytes(raw, "candidates").Array() {
		for _, part := range candidate.Get("content.parts").Array() {
			if part.Get("functionCall").IsObject() {
				evidence.ToolCallEvents++
			}
		}
	}
	meta := gjson.GetBytes(raw, "usageMetadata")
	if !meta.IsObject() {
		return prev
	}
	evidence.UsageFrames++
	prompt, hasPrompt := geminiReportedCounter(meta, "promptTokenCount")
	candidates, hasCandidates := geminiReportedCounter(meta, "candidatesTokenCount")
	cached, hasCached := geminiReportedCounter(meta, "cachedContentTokenCount")
	thoughts, hasThoughts := geminiReportedCounter(meta, "thoughtsTokenCount")
	total, hasTotal := geminiReportedCounter(meta, "totalTokenCount")
	for _, name := range []string{"promptTokenCount", "candidatesTokenCount", "cachedContentTokenCount", "thoughtsTokenCount", "totalTokenCount"} {
		value := meta.Get(name)
		if value.Exists() && value.Type != gjson.Null {
			if _, valid := geminiReportedCounter(meta, name); !valid {
				evidence.InvalidCounters++
			}
		}
	}
	// Candidate and thought counters are individually valid yet their combined
	// output may overflow the persisted counter. Reject the invalid pair update,
	// retaining any earlier valid evidence; never clamp it to an invented value.
	nextCandidates, nextThoughts := candidates, thoughts
	if !hasCandidates && evidence.CandidateTokens != nil {
		nextCandidates = *evidence.CandidateTokens
	}
	if !hasThoughts && evidence.ThoughtTokens != nil {
		nextThoughts = *evidence.ThoughtTokens
	}
	if nextCandidates > maxGeminiStoredTokenCount-nextThoughts {
		evidence.InvalidCounters++
		hasCandidates, hasThoughts = false, false
	}
	hasCounter := hasPrompt || hasCandidates || hasCached || hasThoughts || hasTotal
	zeroOnly := hasCounter && hasPrompt && hasCandidates && prompt == 0 && candidates == 0 && cached == 0 && thoughts == 0 && total == 0
	hadTokens := prev.InputTokens > 0 || prev.OutputTokens > 0 || prev.CacheReadInputTokens > 0 || prev.ImageOutputTokens > 0

	// Gemini usage is cumulative within a response. A subsequent all-zero
	// placeholder containing both primary counters is not a new generation and
	// must not wipe billable evidence. A partial field update of zero is valid.
	// This does NOT maximize or invent cache usage: an explicit cache=0 in a
	// meaningful, nonzero usage frame remains authoritative below.
	if zeroOnly && hadTokens {
		evidence.ZeroResetsIgnored++
		return prev
	}
	if hasPrompt {
		evidence.PromptTokens = usageAuditIntPtr(prompt)
	}
	if hasCandidates {
		evidence.CandidateTokens = usageAuditIntPtr(candidates)
	}
	if hasThoughts {
		evidence.ThoughtTokens = usageAuditIntPtr(thoughts)
	}
	if hasTotal {
		evidence.TotalTokens = usageAuditIntPtr(total)
	}
	if hasCached {
		if prev.CacheReadInputTokens > cached {
			evidence.CacheRegressions++
		}
		evidence.CacheReadTokens = usageAuditIntPtr(cached)
		prev.CacheReadInputTokens = cached
	}

	if evidence.PromptTokens != nil {
		prev.InputTokens = max(*evidence.PromptTokens-prev.CacheReadInputTokens, 0)
	}
	if evidence.CandidateTokens != nil || evidence.ThoughtTokens != nil {
		output := 0
		if evidence.CandidateTokens != nil {
			output += *evidence.CandidateTokens
		}
		if evidence.ThoughtTokens != nil {
			output += *evidence.ThoughtTokens
		}
		prev.OutputTokens = output
	}
	details := meta.Get("candidatesTokensDetails")
	if details.IsArray() {
		for _, detail := range details.Array() {
			if detail.Get("modality").String() == "IMAGE" {
				if n, ok := geminiReportedCounter(detail, "tokenCount"); ok {
					prev.ImageOutputTokens = n
				}
			}
		}
	}
	return prev
}

func buildGeminiUsageAudit(usage ClaudeUsage) *UsageAudit {
	e := usage.geminiUsageEvidence
	if e == nil {
		return nil // Historical / non-Gemini paths are not guessed from model names.
	}
	snapshot := UsageAuditTokenSnapshot{
		TotalInputTokens: e.PromptTokens,
		CacheReadTokens:  e.CacheReadTokens,
	}
	if e.PromptTokens != nil {
		snapshot.InputTokens = usageAuditIntPtr(usage.InputTokens)
	}
	if e.CandidateTokens != nil {
		snapshot.OutputTokens = usageAuditIntPtr(usage.OutputTokens)
	}
	audit := NewUnverifiableUsageAudit("gemini_generate_content", snapshot, "local_estimate_disabled")
	copy := *e
	audit.GeminiUsage = &copy
	audit.EvidenceSource = UsageAuditEvidenceUpstreamRaw
	switch {
	case e.PromptTokens == nil && e.CandidateTokens == nil && e.ThoughtTokens == nil && e.CacheReadTokens == nil && e.TotalTokens == nil:
		audit.Reasons = append(audit.Reasons, "gemini_usage_missing")
	case e.PromptTokens == nil || e.CandidateTokens == nil:
		audit.Reasons = append(audit.Reasons, "gemini_usage_partial")
	case *e.PromptTokens == 0 && *e.CandidateTokens == 0 && usage.OutputTokens == 0 && usage.CacheReadInputTokens == 0:
		audit.Reasons = append(audit.Reasons, "gemini_usage_explicit_zero")
	default:
		audit.Reasons = append(audit.Reasons, "gemini_usage_reported")
	}
	if e.InvalidCounters > 0 {
		audit.Reasons = append(audit.Reasons, "gemini_usage_invalid_counters")
		audit.Status = UsageAuditStatusWarning
	}
	if e.ZeroResetsIgnored > 0 {
		audit.Reasons = append(audit.Reasons, "gemini_usage_zero_reset_ignored")
		audit.Status = UsageAuditStatusWarning
	}
	if e.CacheRegressions > 0 {
		audit.Reasons = append(audit.Reasons, "gemini_cache_counter_regressed")
		audit.Status = UsageAuditStatusWarning
	}
	if e.PromptTokens != nil && *e.PromptTokens > 0 && e.CacheReadTokens != nil {
		audit.CacheHitRate = roundedFloatPtr(float64(usage.CacheReadInputTokens) / float64(*e.PromptTokens) * 100)
	}
	return audit
}
