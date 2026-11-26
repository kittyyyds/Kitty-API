package service

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// projectGeminiNativeUsage changes only native usage counters for which the
// merger retained upstream evidence. In particular, billing's uncached input,
// combined output and any calculated total must never be reverse-engineered
// into upstream counters. All other JSON (including unknown extensions) stays
// intact. SSE callers only project existing usage objects; collected responses
// may attach counters observed in a later usage-only frame.
func projectGeminiNativeUsage(raw []byte, usage *ClaudeUsage, attach bool) []byte {
	if usage == nil || usage.geminiUsageEvidence == nil || !gjson.ValidBytes(raw) || !gjson.ParseBytes(raw).IsObject() {
		return raw
	}
	e := usage.geminiUsageEvidence
	fields := [...]struct {
		name  string
		value *int
	}{
		{"promptTokenCount", e.PromptTokens},
		{"candidatesTokenCount", e.CandidateTokens},
		{"thoughtsTokenCount", e.ThoughtTokens},
		{"cachedContentTokenCount", e.CacheReadTokens},
		{"totalTokenCount", e.TotalTokens},
	}
	observed := false
	for _, field := range fields {
		observed = observed || field.value != nil
	}
	if !observed {
		return raw
	}

	path := "usageMetadata"
	if gjson.GetBytes(raw, "response").IsObject() {
		path = "response.usageMetadata"
	}
	if !attach && !gjson.GetBytes(raw, path).IsObject() {
		return raw // Do not add usage to content-only, error, or finish frames.
	}

	out := raw
	for _, field := range fields {
		var err error
		if field.value != nil {
			out, err = sjson.SetBytes(out, path+"."+field.name, *field.value)
		} else {
			// A discarded all-zero placeholder can mention a counter never seen
			// in accepted evidence. Do not promote that zero into a known value.
			out, err = sjson.DeleteBytes(out, path+"."+field.name)
		}
		if err != nil {
			return raw // Never damage an otherwise forwardable native response.
		}
	}
	return out
}

// handleNativeStreamAsNonStreamingResponse uses the same SSE collector as the
// compat paths, but keeps the native response envelope/content. Only observed
// usage is projected back onto it, without replaying content or estimating any
// missing counters. Unknown usageMetadata fields from tail frames are retained.
func (s *GeminiMessagesCompatService) handleNativeStreamAsNonStreamingResponse(c *gin.Context, resp *http.Response, isOAuth bool, account *Account, requestID string) (*ClaudeUsage, error) {
	var best geminiResponseSignal
	envelope := &geminiNativeCollectedEnvelope{response: make(map[string]any)}
	_, usage, stats, err := collectGeminiSSEObserved(s.cfg, resp.Body, isOAuth, func(raw []byte) {
		if sig, ok := detectGeminiResponseSignal(raw); ok && sig.Kind > best.Kind {
			best = sig
		}
		envelope.observe(raw)
	})
	if err != nil {
		return nil, s.writeGoogleError(c, http.StatusBadGateway, "Failed to read upstream stream")
	}
	s.finalizeGeminiSSESignal(c, account, false, requestID, best, stats.dataEvents > 0, stats.fallback)
	body, err := json.Marshal(envelope.response)
	if err != nil {
		return nil, s.writeGoogleError(c, http.StatusBadGateway, "Failed to encode upstream response")
	}

	body = projectGeminiNativeUsage(body, usage, true)
	observer := upstreamResponseModelObserverFromContext(c)
	if observer == nil {
		observer = beginUpstreamResponseModelObservation(c)
	}
	observer.ObserveGemini(body)
	observeGeminiImageOutputs(c, body)
	c.Data(http.StatusOK, "application/json", body)
	return usage, nil
}

// geminiNativeCollectedEnvelope retains native metadata from usage-only and
// terminal frames without replaying their content. UseNumber is important here:
// an unknown extension (or tool argument) is not necessarily a float64-safe
// number. Content parts are appended once; only adjacent plain text deltas
// are joined, so thought signatures, tools and unknown part fields survive.
type geminiNativeCollectedEnvelope struct {
	response map[string]any
}

func (e *geminiNativeCollectedEnvelope) observe(raw []byte) {
	var frame map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if !gjson.ValidBytes(raw) || decoder.Decode(&frame) != nil || frame == nil {
		return
	}
	for key, value := range frame {
		switch key {
		case "usageMetadata":
			if fields, ok := value.(map[string]any); ok {
				previous, _ := e.response[key].(map[string]any)
				if previous == nil {
					previous = make(map[string]any)
				}
				for name, field := range fields {
					previous[name] = field
				}
				e.response[key] = previous
				continue
			}
		case "candidates":
			if candidates, ok := value.([]any); ok {
				previous, _ := e.response[key].([]any)
				for i, candidate := range candidates {
					if i >= len(previous) {
						previous = append(previous, candidate)
						continue
					}
					fields, isObject := candidate.(map[string]any)
					stored, wasObject := previous[i].(map[string]any)
					if !isObject || !wasObject {
						previous[i] = candidate
						continue
					}
					for name, field := range fields {
						if name == "content" {
							content, _ := field.(map[string]any)
							old, _ := stored[name].(map[string]any)
							if content != nil && old != nil {
								for k, v := range content {
									if parts, ok := v.([]any); k == "parts" && ok {
										storedParts, _ := old[k].([]any)
										old[k] = appendGeminiNativeContentParts(storedParts, parts)
										continue // An empty terminal parts array keeps the body.
									}
									old[k] = v
								}
								continue
							}
						}
						stored[name] = field
					}
				}
				e.response[key] = previous
				continue
			}
		}
		e.response[key] = value
	}
}

func appendGeminiNativeContentParts(previous, incoming []any) []any {
	for _, part := range incoming {
		if len(previous) > 0 {
			last, lastObject := previous[len(previous)-1].(map[string]any)
			next, nextObject := part.(map[string]any)
			if lastObject && nextObject && len(last) == 1 && len(next) == 1 {
				left, leftText := last["text"].(string)
				right, rightText := next["text"].(string)
				if leftText && rightText {
					last["text"] = left + right
					continue
				}
			}
		}
		previous = append(previous, part)
	}
	return previous
}
