package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type geminiUpstreamSessionCtxKey struct{}

// WithGeminiUpstreamSession attaches a resolved gateway session identifier so
// Gemini API-key pool/relay upstreams can preserve sticky routing and implicit
// prefix caching across multi-turn conversations.
func WithGeminiUpstreamSession(ctx context.Context, sessionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	cleaned := strings.TrimPrefix(strings.TrimSpace(sessionID), "gemini:")
	cleaned = sanitizeSessionID(cleaned)
	if cleaned == "" {
		return ctx
	}
	return context.WithValue(ctx, geminiUpstreamSessionCtxKey{}, cleaned)
}

func geminiUpstreamSessionFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(geminiUpstreamSessionCtxKey{}).(string)
	return v
}

func isOfficialGoogleGeminiBaseURL(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return true
	}
	host := strings.ToLower(u.Hostname())
	return host == "generativelanguage.googleapis.com" ||
		host == "aiplatform.googleapis.com" ||
		strings.HasSuffix(host, ".googleapis.com")
}

var geminiPoolInboundSessionHeaders = []string{
	"x-sublink-session",
	"x-claude-code-session-id",
	"x-session-affinity",
	"x-session-id",
	"session-id",
	"session_id",
	"x-conversation-id",
	"conversation_id",
}

func resolveGeminiPoolStickySessionID(ctx context.Context, c *gin.Context, rawBody []byte) string {
	if c != nil && c.Request != nil {
		for _, hdr := range geminiPoolInboundSessionHeaders {
			if sid := sanitizeSessionID(c.GetHeader(hdr)); sid != "" {
				return sid
			}
		}
	}
	if sid := geminiUpstreamSessionFromContext(ctx); sid != "" {
		return sid
	}
	if len(rawBody) > 0 {
		if rawUID := strings.TrimSpace(gjson.GetBytes(rawBody, "metadata.user_id").String()); rawUID != "" {
			if parsed := ParseMetadataUserID(rawUID); parsed != nil && sanitizeSessionID(parsed.SessionID) != "" {
				return sanitizeSessionID(parsed.SessionID)
			}
			sum := sha256.Sum256([]byte("uid:" + rawUID))
			return hex.EncodeToString(sum[:16])
		}
		var seed strings.Builder
		if sys := strings.TrimSpace(gjson.GetBytes(rawBody, "systemInstruction").Raw); sys != "" {
			seed.WriteString(sys)
		} else if sys := strings.TrimSpace(gjson.GetBytes(rawBody, "system").Raw); sys != "" {
			seed.WriteString(sys)
		}
		if firstTurn := strings.TrimSpace(gjson.GetBytes(rawBody, "contents.0").Raw); firstTurn != "" {
			seed.WriteByte('|')
			seed.WriteString(firstTurn)
		} else if firstMsg := strings.TrimSpace(gjson.GetBytes(rawBody, "messages.0").Raw); firstMsg != "" {
			seed.WriteByte('|')
			seed.WriteString(firstMsg)
		}
		if seed.Len() > 0 {
			sum := sha256.Sum256([]byte(seed.String()))
			return hex.EncodeToString(sum[:16])
		}
	}
	return ""
}

func applyGeminiAPIKeyPoolSessionHeaders(
	ctx context.Context,
	c *gin.Context,
	req *http.Request,
	normalizedBaseURL string,
	rawBody []byte,
) {
	if req == nil || isOfficialGoogleGeminiBaseURL(normalizedBaseURL) {
		return
	}
	sid := resolveGeminiPoolStickySessionID(ctx, c, rawBody)
	if sid == "" {
		return
	}
	for _, hdr := range []string{
		"x-sublink-session",
		"x-session-id",
		"x-session-affinity",
		"x-claude-code-session-id",
	} {
		if req.Header.Get(hdr) == "" {
			req.Header.Set(hdr, sid)
		}
	}
}

// mergeGeminiStreamUsage updates streaming usage from a Gemini SSE chunk while
// preserving previously observed cachedContentTokenCount / promptTokenCount if
// a trailing finish chunk omits those fields.
func mergeGeminiStreamUsage(prev ClaudeUsage, rawData []byte) ClaudeUsage {
	u := extractGeminiUsage(rawData)
	if u == nil {
		return prev
	}
	usageMeta := gjson.GetBytes(rawData, "usageMetadata")
	hasPrompt := usageMeta.Get("promptTokenCount").Exists()
	hasCached := usageMeta.Get("cachedContentTokenCount").Exists()

	if !hasCached && prev.CacheReadInputTokens > 0 {
		u.CacheReadInputTokens = prev.CacheReadInputTokens
		if hasPrompt {
			prompt := int(usageMeta.Get("promptTokenCount").Int())
			u.InputTokens = max(prompt-u.CacheReadInputTokens, 0)
		} else {
			u.InputTokens = prev.InputTokens
		}
	} else if !hasPrompt && !hasCached && u.InputTokens == 0 && prev.InputTokens > 0 {
		u.InputTokens = prev.InputTokens
	}
	if u.OutputTokens == 0 && prev.OutputTokens > 0 && !usageMeta.Get("candidatesTokenCount").Exists() {
		u.OutputTokens = prev.OutputTokens
	}
	return *u
}
