package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type geminiUpstreamSessionCtxKey struct{}
type geminiPoolRawClientBodyCtxKey struct{}
type geminiPoolAttemptSessionCtxKey struct{}

const (
	maxGeminiPoolDeadAccountRetries = 16
	geminiPoolRebindCacheTTL        = 2 * time.Hour
	geminiPoolRebindCacheMaxEntries = 8192
)

type geminiPoolRebindEntry struct {
	step      int
	expiresAt time.Time
}

var (
	geminiPoolRebindMu    sync.RWMutex
	geminiPoolRebindSteps = make(map[string]geminiPoolRebindEntry)
)

// WithGeminiUpstreamSession attaches a resolved gateway session identifier so
// Gemini API-key pool/relay upstreams can fall back to it when no header or
// stable message seed is present.
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

// WithGeminiPoolRawClientBody preserves the original client JSON payload (for
// example OpenAI /v1/chat/completions body containing prompt_cache_key or
// metadata.user_id) across intermediate protocol conversions.
func WithGeminiPoolRawClientBody(ctx context.Context, rawBody []byte) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(rawBody) == 0 {
		return ctx
	}
	return context.WithValue(ctx, geminiPoolRawClientBodyCtxKey{}, rawBody)
}

func geminiPoolRawClientBodyFromContext(ctx context.Context) []byte {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(geminiPoolRawClientBodyCtxKey{}).([]byte)
	return v
}

func withGeminiPoolAttemptSessionOverride(ctx context.Context, sessionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	cleaned := sanitizeSessionID(sessionID)
	if cleaned == "" {
		return ctx
	}
	return context.WithValue(ctx, geminiPoolAttemptSessionCtxKey{}, cleaned)
}

func geminiPoolAttemptSessionOverrideFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(geminiPoolAttemptSessionCtxKey{}).(string)
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

func accountShouldPreserveGeminiFunctionIDs(account *Account) bool {
	if account == nil || account.Type != AccountTypeAPIKey {
		return false
	}
	baseURL := account.GetGeminiBaseURL(geminicli.AIStudioBaseURL)
	return !isOfficialGoogleGeminiBaseURL(baseURL)
}

func resolveGeminiFunctionCallID(fc map[string]any) string {
	if fc != nil {
		if id, _ := fc["id"].(string); strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
	}
	return "toolu_" + randomHex(8)
}

func isGeminiUnknownFunctionIDFieldError(respBody []byte) bool {
	if len(respBody) == 0 {
		return false
	}
	lower := strings.ToLower(string(respBody))
	hasFieldErr := strings.Contains(lower, `unknown name "id"`) ||
		strings.Contains(lower, `unknown field "id"`) ||
		strings.Contains(lower, "cannot find field")
	hasFuncTarget := strings.Contains(lower, "functioncall") ||
		strings.Contains(lower, "function_call") ||
		strings.Contains(lower, "functionresponse") ||
		strings.Contains(lower, "function_response")
	return hasFieldErr && hasFuncTarget
}

func stripGeminiFunctionIDsBytes(raw []byte) ([]byte, bool) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return raw, false
	}
	contents, ok := payload["contents"].([]any)
	if !ok {
		return raw, false
	}
	changed := false
	for _, c := range contents {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		parts, ok := cm["parts"].([]any)
		if !ok {
			continue
		}
		for _, p := range parts {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if fc, ok := pm["functionCall"].(map[string]any); ok && fc != nil {
				if _, exists := fc["id"]; exists {
					delete(fc, "id")
					changed = true
				}
			}
			if fr, ok := pm["functionResponse"].(map[string]any); ok && fr != nil {
				if _, exists := fr["id"]; exists {
					delete(fr, "id")
					changed = true
				}
			}
		}
	}
	if !changed {
		return raw, false
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return raw, false
	}
	return out, true
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

func stripCacheControlDeep(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, item := range val {
			if k == "cache_control" || k == "cacheControl" {
				continue
			}
			out[k] = stripCacheControlDeep(item)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = stripCacheControlDeep(item)
		}
		return out
	default:
		return v
	}
}

func canonicalizeStickyJSONFragment(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return trimmed
	}
	cleaned := stripCacheControlDeep(parsed)
	b, err := json.Marshal(cleaned)
	if err != nil {
		return trimmed
	}
	return string(b)
}

func extractExplicitBodyStickyID(rawBody []byte) string {
	if len(rawBody) == 0 {
		return ""
	}
	if rawUID := strings.TrimSpace(gjson.GetBytes(rawBody, "metadata.user_id").String()); rawUID != "" {
		if parsed := ParseMetadataUserID(rawUID); parsed != nil && sanitizeSessionID(parsed.SessionID) != "" {
			return sanitizeSessionID(parsed.SessionID)
		}
		sum := sha256.Sum256([]byte("uid:" + rawUID))
		return hex.EncodeToString(sum[:16])
	}
	if pck := strings.TrimSpace(gjson.GetBytes(rawBody, "prompt_cache_key").String()); pck != "" {
		if clean := sanitizeSessionID(pck); clean != "" && len(clean) <= 64 {
			return clean
		}
		sum := sha256.Sum256([]byte("pck:" + pck))
		return hex.EncodeToString(sum[:16])
	}
	return ""
}

func extractCanonicalBodyConversationSeed(rawBody []byte) string {
	if len(rawBody) == 0 {
		return ""
	}
	var seed strings.Builder
	if sys := canonicalizeStickyJSONFragment(gjson.GetBytes(rawBody, "systemInstruction").Raw); sys != "" {
		seed.WriteString(sys)
	} else if sys := canonicalizeStickyJSONFragment(gjson.GetBytes(rawBody, "system").Raw); sys != "" {
		seed.WriteString(sys)
	}

	if firstTurn := canonicalizeStickyJSONFragment(gjson.GetBytes(rawBody, "contents.0").Raw); firstTurn != "" {
		seed.WriteByte('|')
		seed.WriteString(firstTurn)
	} else if firstMsgRaw := gjson.GetBytes(rawBody, "messages.0"); firstMsgRaw.Exists() {
		seed.WriteByte('|')
		seed.WriteString(canonicalizeStickyJSONFragment(firstMsgRaw.Raw))
		role0 := strings.ToLower(strings.TrimSpace(firstMsgRaw.Get("role").String()))
		if (role0 == "system" || role0 == "developer") && gjson.GetBytes(rawBody, "messages.1").Exists() {
			seed.WriteByte('|')
			seed.WriteString(canonicalizeStickyJSONFragment(gjson.GetBytes(rawBody, "messages.1").Raw))
		}
	}
	if seed.Len() == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte(seed.String()))
	return hex.EncodeToString(sum[:16])
}

func resolveGeminiPoolStickySessionID(ctx context.Context, c *gin.Context, rawBody []byte) string {
	if override := geminiPoolAttemptSessionOverrideFromContext(ctx); override != "" {
		return override
	}
	if c != nil && c.Request != nil {
		for _, hdr := range geminiPoolInboundSessionHeaders {
			if sid := sanitizeSessionID(c.GetHeader(hdr)); sid != "" {
				return sid
			}
		}
	}
	rawClientBody := geminiPoolRawClientBodyFromContext(ctx)
	if sid := extractExplicitBodyStickyID(rawClientBody); sid != "" {
		return sid
	}
	if sid := extractExplicitBodyStickyID(rawBody); sid != "" {
		return sid
	}
	if sid := extractCanonicalBodyConversationSeed(rawClientBody); sid != "" {
		return sid
	}
	if sid := extractCanonicalBodyConversationSeed(rawBody); sid != "" {
		return sid
	}
	if sid := geminiUpstreamSessionFromContext(ctx); sid != "" {
		return sid
	}
	return ""
}

func formatGeminiPoolReboundSessionID(baseSID string, step int) string {
	if baseSID == "" || step <= 0 {
		return baseSID
	}
	suffix := fmt.Sprintf("-r%d", step)
	maxBaseRunes := maxPersistedSessionIDLength - utf8.RuneCountInString(suffix)
	if maxBaseRunes <= 0 {
		return sanitizeSessionID(strings.TrimPrefix(suffix, "-"))
	}
	runes := []rune(baseSID)
	if len(runes) > maxBaseRunes {
		runes = runes[:maxBaseRunes]
	}
	return sanitizeSessionID(string(runes) + suffix)
}

func lookupGeminiPoolRebindStep(cacheKey string, now time.Time) int {
	if cacheKey == "" {
		return 0
	}
	geminiPoolRebindMu.RLock()
	entry, ok := geminiPoolRebindSteps[cacheKey]
	geminiPoolRebindMu.RUnlock()
	if !ok || !entry.expiresAt.After(now) {
		return 0
	}
	return entry.step
}

func storeGeminiPoolRebindStep(cacheKey string, step int, now time.Time) {
	if cacheKey == "" || step <= 0 {
		return
	}
	geminiPoolRebindMu.Lock()
	defer geminiPoolRebindMu.Unlock()
	if len(geminiPoolRebindSteps) >= geminiPoolRebindCacheMaxEntries {
		for k, v := range geminiPoolRebindSteps {
			if !v.expiresAt.After(now) {
				delete(geminiPoolRebindSteps, k)
			}
		}
		if len(geminiPoolRebindSteps) >= geminiPoolRebindCacheMaxEntries {
			for k := range geminiPoolRebindSteps {
				delete(geminiPoolRebindSteps, k)
				break
			}
		}
	}
	geminiPoolRebindSteps[cacheKey] = geminiPoolRebindEntry{
		step:      step,
		expiresAt: now.Add(geminiPoolRebindCacheTTL),
	}
}

type geminiPoolStickyAttemptState struct {
	cacheKey string
	baseSID  string
	step     int
}

func newGeminiPoolStickyAttemptState(ctx context.Context, c *gin.Context, rawBaseURL string, rawBody []byte) *geminiPoolStickyAttemptState {
	if isOfficialGoogleGeminiBaseURL(rawBaseURL) {
		return nil
	}
	baseSID := resolveGeminiPoolStickySessionID(ctx, c, rawBody)
	if baseSID == "" {
		baseSID = "pool-" + randomHex(8)
	}
	normKey := strings.ToLower(strings.TrimRight(strings.TrimSpace(rawBaseURL), "/")) + "|" + baseSID
	step := lookupGeminiPoolRebindStep(normKey, time.Now())
	return &geminiPoolStickyAttemptState{
		cacheKey: normKey,
		baseSID:  baseSID,
		step:     step,
	}
}

func (s *geminiPoolStickyAttemptState) currentSessionID() string {
	if s == nil {
		return ""
	}
	return formatGeminiPoolReboundSessionID(s.baseSID, s.step)
}

func (s *geminiPoolStickyAttemptState) withAttemptContext(ctx context.Context) context.Context {
	if s == nil {
		return ctx
	}
	sid := s.currentSessionID()
	if sid == "" {
		return ctx
	}
	return withGeminiPoolAttemptSessionOverride(ctx, sid)
}

func (s *geminiPoolStickyAttemptState) advanceOnDeadAccount() string {
	if s == nil {
		return ""
	}
	s.step++
	return s.currentSessionID()
}

func (s *geminiPoolStickyAttemptState) markHealthy() {
	if s == nil || s.step <= 0 {
		return
	}
	storeGeminiPoolRebindStep(s.cacheKey, s.step, time.Now())
}

func safeSessionPrefix(sid string) string {
	if len(sid) <= 16 {
		return sid
	}
	return sid[:16] + "..."
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
	override := geminiPoolAttemptSessionOverrideFromContext(ctx)
	sid := override
	if sid == "" {
		sid = resolveGeminiPoolStickySessionID(ctx, c, rawBody)
	}
	if sid == "" {
		return
	}
	for _, hdr := range []string{
		"x-sublink-session",
		"x-session-id",
		"x-session-affinity",
		"x-claude-code-session-id",
	} {
		if override != "" || req.Header.Get(hdr) == "" {
			req.Header.Set(hdr, sid)
		}
	}
}

type geminiCombinedReadCloser struct {
	io.Reader
	io.Closer
}

func isGeminiPoolDisabledAccountText(text string) bool {
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	return strings.Contains(lower, "disabled in this account") &&
		strings.Contains(lower, "terms of service")
}

func extractGeminiCandidateTextAndTokens(rawJSON []byte) (string, int64, int64, int64) {
	unwrapped := rawJSON
	if uw, err := unwrapGeminiResponse(rawJSON); err == nil && len(uw) > 0 {
		unwrapped = uw
	}
	var sb strings.Builder
	parts := gjson.GetBytes(unwrapped, "candidates.0.content.parts")
	if parts.IsArray() {
		for _, pt := range parts.Array() {
			if t := pt.Get("text").String(); t != "" {
				sb.WriteString(t)
			}
		}
	}
	prompt := gjson.GetBytes(unwrapped, "usageMetadata.promptTokenCount").Int()
	cand := gjson.GetBytes(unwrapped, "usageMetadata.candidatesTokenCount").Int()
	cached := gjson.GetBytes(unwrapped, "usageMetadata.cachedContentTokenCount").Int()
	return sb.String(), prompt, cand, cached
}

// inspectGeminiPoolFake200Response detects SubLink / Antigravity pool responses
// that return HTTP 200 with a Google ToS account-disabled notice and 0 tokens,
// while leaving healthy streaming and non-streaming bodies completely intact.
func inspectGeminiPoolFake200Response(resp *http.Response, isStream bool) (bool, *http.Response) {
	if resp == nil || resp.StatusCode != http.StatusOK || resp.Body == nil {
		return false, resp
	}
	sseMode := isStream || strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
	if !sseMode {
		bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		_ = resp.Body.Close()
		rebuilt := &http.Response{
			Status:        resp.Status,
			StatusCode:    resp.StatusCode,
			Proto:         resp.Proto,
			ProtoMajor:    resp.ProtoMajor,
			ProtoMinor:    resp.ProtoMinor,
			Header:        resp.Header.Clone(),
			Body:          io.NopCloser(bytes.NewReader(bodyBytes)),
			ContentLength: int64(len(bodyBytes)),
			Request:       resp.Request,
		}
		if err != nil || len(bodyBytes) == 0 {
			return false, rebuilt
		}
		text, prompt, cand, cached := extractGeminiCandidateTextAndTokens(bodyBytes)
		isDead := isGeminiPoolDisabledAccountText(text) && prompt == 0 && cand == 0 && cached == 0
		return isDead, rebuilt
	}

	origBody := resp.Body
	br := bufio.NewReaderSize(origBody, 16*1024)
	var peekBuf bytes.Buffer
	firstDataPayload := ""

	for peekBuf.Len() < 64*1024 {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			peekBuf.Write(line)
			trimmed := strings.TrimRight(string(line), "\r\n")
			if strings.HasPrefix(trimmed, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				if payload != "" && payload != "[DONE]" {
					firstDataPayload = payload
					break
				}
			}
		}
		if err != nil {
			break
		}
	}

	if firstDataPayload == "" || !strings.Contains(strings.ToLower(firstDataPayload), "disabled in this account") {
		resp.Body = geminiCombinedReadCloser{
			Reader: io.MultiReader(bytes.NewReader(peekBuf.Bytes()), br),
			Closer: origBody,
		}
		return false, resp
	}

	// Suspicious first SSE event: read remaining error events (typically <1KB) to confirm 0 tokens.
	rest, _ := io.ReadAll(io.LimitReader(br, 128*1024))
	if len(rest) > 0 {
		peekBuf.Write(rest)
	}
	_ = origBody.Close()

	allBytes := peekBuf.Bytes()
	rebuilt := &http.Response{
		Status:        resp.Status,
		StatusCode:    resp.StatusCode,
		Proto:         resp.Proto,
		ProtoMajor:    resp.ProtoMajor,
		ProtoMinor:    resp.ProtoMinor,
		Header:        resp.Header.Clone(),
		Body:          io.NopCloser(bytes.NewReader(allBytes)),
		ContentLength: int64(len(allBytes)),
		Request:       resp.Request,
	}

	var allText strings.Builder
	var maxPrompt, maxCand, maxCached int64
	for _, rawLine := range strings.Split(string(allBytes), "\n") {
		trimmed := strings.TrimRight(rawLine, "\r")
		if !strings.HasPrefix(trimmed, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		t, p, c, ch := extractGeminiCandidateTextAndTokens([]byte(payload))
		allText.WriteString(t)
		if p > maxPrompt {
			maxPrompt = p
		}
		if c > maxCand {
			maxCand = c
		}
		if ch > maxCached {
			maxCached = ch
		}
	}
	isDead := isGeminiPoolDisabledAccountText(allText.String()) && maxPrompt == 0 && maxCand == 0 && maxCached == 0
	return isDead, rebuilt
}

// mergeGeminiStreamUsage merges cumulative Gemini counters by field presence.
// The token-only evidence distinguishes absent usage from an explicit zero and
// prevents a partial/tool-only tail from erasing counters already observed.
func mergeGeminiStreamUsage(prev ClaudeUsage, rawData []byte) ClaudeUsage {
	return mergeGeminiUsageEvidence(prev, rawData)
}
