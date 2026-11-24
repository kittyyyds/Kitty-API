package apicompat

import "encoding/json"

func cacheUsagePresence(present bool) *bool { return &present }

func chatCacheCreationReported(d *ChatTokenDetails) bool {
	return d != nil && (d.cacheWriteTokensPresent || d.cacheCreationTokensPresent || d.CacheWriteTokens != 0 || d.CacheCreationTokens != 0)
}

func (u *ResponsesUsage) CacheCreationUsageReported() bool {
	if u == nil {
		return false
	}
	if u.cacheCreationReported != nil {
		return *u.cacheCreationReported
	}
	return u.CacheCreationInputTokens != 0
}

func (u ResponsesUsage) MarshalJSON() ([]byte, error) {
	type alias ResponsesUsage
	var creation *int
	if u.CacheCreationUsageReported() {
		creation = &u.CacheCreationInputTokens
	}
	return json.Marshal(struct {
		alias
		CacheCreationInputTokens *int `json:"cache_creation_input_tokens,omitempty"`
	}{alias(u), creation})
}

// Copy token pointers so conversion/state merging cannot mutate a caller's
// usage snapshot. No prompt content is retained or inferred here.
func cloneAnthropicCacheCreation(in *AnthropicCacheCreation) *AnthropicCacheCreation {
	if in == nil || (in.Ephemeral5mInputTokens == nil && in.Ephemeral1hInputTokens == nil) {
		return nil
	}
	out := &AnthropicCacheCreation{}
	if in.Ephemeral5mInputTokens != nil {
		n := *in.Ephemeral5mInputTokens
		out.Ephemeral5mInputTokens = &n
	}
	if in.Ephemeral1hInputTokens != nil {
		n := *in.Ephemeral1hInputTokens
		out.Ephemeral1hInputTokens = &n
	}
	return out
}

func chatCacheCreation(details *ChatTokenDetails) *AnthropicCacheCreation {
	if details == nil {
		return nil
	}
	return cloneAnthropicCacheCreation(details.CacheCreation)
}
