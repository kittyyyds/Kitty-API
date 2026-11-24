package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Cyber session block (ported from ranxi v2.21.6). When an upstream account refuses a
// request with `cyber_policy`, the conversation can be blocked locally for a while so
// that retries never reach an upstream account again. Everything here is inert until an
// administrator enables the cyber session block switch (default off).

const (
	openAICyberIdentityMetricsLogInterval = 1024
)

var openAICyberIdentityMetricsLogCounter atomic.Uint64

// 双语单串：网关客户端面向中英用户，且本错误无 i18n 协商通道。
const cyberSessionBlockedClientMsg = "该会话已被网络安全策略屏蔽，请开启新会话 / This session is blocked by cyber-security policy, please start a new session"

// cyberSessionBlockFormat selects the per-endpoint error envelope for a locally
// blocked session (each compatible endpoint keeps its own error shape).
type cyberSessionBlockFormat int

const (
	cyberBlockFormatResponses cyberSessionBlockFormat = iota
	cyberBlockFormatChat
	cyberBlockFormatAnthropic
)

// rejectIfCyberSessionBlocked resolves and observes the request identity before
// account selection. When the default-off strict gate is enabled, untrusted
// identities are rejected before routing. Exact session blocks are then checked
// only while the main cyber session block switch is enabled.
func (h *OpenAIGatewayHandler) rejectIfCyberSessionBlocked(c *gin.Context, apiKey *service.APIKey, body []byte, model string, format cyberSessionBlockFormat) bool {
	if h == nil || h.gatewayService == nil || apiKey == nil {
		return false
	}
	identity := service.ResolveCyberSessionIdentity(apiKey.ID, c, body)
	enabled, _ := h.gatewayService.CyberSessionBlockRuntime(c.Request.Context())
	strictRejected := enabled && h.gatewayService.CyberSessionIdentityStrictEnabled(c.Request.Context()) && !identity.Resolved()
	observeOpenAICyberSessionIdentity(identity, strictRejected, false)
	if strictRejected {
		h.writeCyberSessionIdentityRejected(c, apiKey, model, format, identity.Metadata)
		return true
	}
	if !enabled {
		return false
	}
	key := h.findBlockedCyberSessionForRequest(c, apiKey, identity, body)
	if key == "" {
		return false
	}
	// A body-signal compact heartbeat may already have committed 200 (this check runs
	// after the long user-slot wait): report it as a response.failed terminal event;
	// when nothing was committed, stop the heartbeat and write the usual JSON.
	if service.StopOpenAICompactSSEKeepaliveCommitted(c) {
		service.MarkOpsStreamError(c, "permission_error", cyberSessionBlockedClientMsg, http.StatusForbidden)
		if writeResponsesFailedSSE(c, "permission_error", "", cyberSessionBlockedClientMsg) {
			h.enqueueCyberSessionBlockedOpsEntry(c, apiKey, model, key)
			return true
		}
	}
	switch format {
	case cyberBlockFormatAnthropic:
		c.JSON(http.StatusForbidden, gin.H{"type": "error", "error": gin.H{
			"type":    "permission_error",
			"message": cyberSessionBlockedClientMsg,
		}})
	default: // Responses and Chat share the OpenAI error envelope.
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
			"type":    "permission_error",
			"code":    "session_blocked_by_cyber_policy",
			"message": cyberSessionBlockedClientMsg,
		}})
	}
	h.enqueueCyberSessionBlockedOpsEntry(c, apiKey, model, key)
	return true
}

func observeOpenAICyberSessionIdentity(identity service.CyberSessionIdentityResolution, strictRejected, wsIdentitySwap bool) {
	service.ObserveOpenAICyberSessionIdentity(identity.Metadata, identity.Inherited, strictRejected, wsIdentitySwap)
	if openAICyberIdentityMetricsLogCounter.Add(1)%openAICyberIdentityMetricsLogInterval != 0 {
		return
	}
	metrics := service.SnapshotOpenAICyberSessionIdentityMetrics()
	logger.L().Info("openai.cyber_session_identity_metrics",
		zap.Uint64("total", metrics.Total),
		zap.Uint64("resolved", metrics.Resolved),
		zap.Uint64("missing", metrics.Missing),
		zap.Uint64("conflict", metrics.Conflict),
		zap.Uint64("invalid", metrics.Invalid),
		zap.Uint64("inherited", metrics.Inherited),
		zap.Uint64("strict_rejected", metrics.StrictRejected),
		zap.Uint64("ws_identity_swap", metrics.WSIdentitySwap),
	)
}

func cyberSessionIdentityError(metadata service.OpenAIClientSessionIdentityMetadata) (string, string) {
	switch metadata.Status {
	case service.OpenAIClientSessionIdentityConflict:
		return "cyber_session_identity_conflict", "Conflicting session identities were provided; send one stable thread_id or session_id"
	case service.OpenAIClientSessionIdentityInvalid:
		return "cyber_session_identity_invalid", "The session identity is invalid; send a valid thread_id or session_id"
	default:
		return "cyber_session_identity_required", "A stable thread_id or session_id is required"
	}
}

func (h *OpenAIGatewayHandler) writeCyberSessionIdentityRejected(c *gin.Context, apiKey *service.APIKey, model string, format cyberSessionBlockFormat, metadata service.OpenAIClientSessionIdentityMetadata) {
	code, message := cyberSessionIdentityError(metadata)
	if format == cyberBlockFormatResponses && service.StopOpenAICompactSSEKeepaliveCommitted(c) {
		service.MarkOpsStreamError(c, "invalid_request_error", message, http.StatusBadRequest)
		if writeResponsesFailedSSE(c, "invalid_request_error", code, message) {
			h.enqueueCyberSessionIdentityRejectedOpsEntry(c, apiKey, model, metadata)
			return
		}
	}
	switch format {
	case cyberBlockFormatAnthropic:
		c.JSON(http.StatusBadRequest, gin.H{"type": "error", "error": gin.H{
			"type":    "invalid_request_error",
			"message": message,
		}})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"type":    "invalid_request_error",
			"code":    code,
			"message": message,
		}})
	}
	h.enqueueCyberSessionIdentityRejectedOpsEntry(c, apiKey, model, metadata)
}

// enqueueCyberSessionBlockedOpsEntry captures request meta and enqueues the
// ops_error_logs entry for a locally blocked request.
func (h *OpenAIGatewayHandler) enqueueCyberSessionBlockedOpsEntry(c *gin.Context, apiKey *service.APIKey, model string, sessionBlockKey string) {
	if h.opsService == nil {
		return
	}
	// The dedicated cyber_session_blocked entry owns Ops semantics for this
	// request; suppress the generic middleware record of the same 403 response.
	c.Set(opsDedicatedErrorRecordedKey, true)
	meta := cyberPolicyOpsErrorMeta{Model: model, InboundEndpoint: GetInboundEndpoint(c), CreatedAt: time.Now(), SessionBlockKey: sessionBlockKey}
	meta.RequestID = c.Writer.Header().Get("X-Request-Id")
	if c.Request != nil && c.Request.URL != nil {
		meta.RequestPath = c.Request.URL.Path
	}
	if v, ok := c.Get(opsStreamKey); ok {
		if b, ok := v.(bool); ok {
			meta.Stream = b
		}
	}
	requestCtx := context.Background()
	if c.Request != nil {
		requestCtx = c.Request.Context()
	}
	meta.Platform = resolveOpsPlatform(requestCtx, apiKey, guessPlatformFromPath(meta.RequestPath))
	if c.Request != nil {
		meta.ClientRequestID, _ = c.Request.Context().Value(ctxkey.ClientRequestID).(string)
		meta.UserAgent = c.GetHeader("User-Agent")
		meta.ClientIP = strings.TrimSpace(ip.GetClientIP(c))
	}
	meta.APIKeyID = apiKey.ID
	meta.GroupID = apiKey.GroupID
	meta.APIKeyPrefix = keyPrefix(apiKey.Key, 8)
	if apiKey.User != nil {
		meta.UserID = apiKey.User.ID
	}
	enqueueOpsErrorLog(h.opsService, buildCyberSessionBlockedOpsEntry(meta))
}

func (h *OpenAIGatewayHandler) enqueueCyberSessionIdentityRejectedOpsEntry(c *gin.Context, apiKey *service.APIKey, model string, metadata service.OpenAIClientSessionIdentityMetadata) {
	if h.opsService == nil || c == nil || apiKey == nil {
		return
	}
	c.Set(opsDedicatedErrorRecordedKey, true)
	meta := cyberPolicyOpsErrorMeta{
		Model:           model,
		InboundEndpoint: GetInboundEndpoint(c),
		CreatedAt:       time.Now(),
		IdentityStatus:  string(metadata.Status),
		IdentityKind:    metadata.Kind,
		IdentitySource:  metadata.Source,
	}
	_, meta.WSTurnAdmission = c.Get(securityAuditWSTurnContextKey)
	meta.RequestID = c.Writer.Header().Get("X-Request-Id")
	if c.Request != nil && c.Request.URL != nil {
		meta.RequestPath = c.Request.URL.Path
	}
	if v, ok := c.Get(opsStreamKey); ok {
		meta.Stream, _ = v.(bool)
	}
	requestCtx := context.Background()
	if c.Request != nil {
		requestCtx = c.Request.Context()
		meta.ClientRequestID, _ = requestCtx.Value(ctxkey.ClientRequestID).(string)
		meta.UserAgent = c.GetHeader("User-Agent")
		meta.ClientIP = strings.TrimSpace(ip.GetClientIP(c))
	}
	meta.Platform = resolveOpsPlatform(requestCtx, apiKey, guessPlatformFromPath(meta.RequestPath))
	meta.APIKeyID = apiKey.ID
	meta.GroupID = apiKey.GroupID
	meta.APIKeyPrefix = keyPrefix(apiKey.Key, 8)
	if apiKey.User != nil {
		meta.UserID = apiKey.User.ID
	}
	enqueueOpsErrorLog(h.opsService, buildCyberSessionIdentityRejectedOpsEntry(meta))
}

// buildCyberSessionBlockedOpsEntry builds the ops_error_logs entry for a request
// rejected locally by the cyber session block. Distinct error_type from upstream
// `cyber_policy`; it never feeds moderation logs or violation counting (the request
// never reached upstream).
func buildCyberSessionBlockedOpsEntry(meta cyberPolicyOpsErrorMeta) *service.OpsInsertErrorLogInput {
	rt := int16(service.RequestTypeCyberBlocked)
	entry := &service.OpsInsertErrorLogInput{
		RequestID:         meta.RequestID,
		ClientRequestID:   meta.ClientRequestID,
		Platform:          meta.Platform,
		Model:             meta.Model,
		RequestPath:       meta.RequestPath,
		Stream:            meta.Stream,
		InboundEndpoint:   meta.InboundEndpoint,
		RequestType:       &rt,
		UserAgent:         meta.UserAgent,
		APIKeyPrefix:      meta.APIKeyPrefix,
		ErrorPhase:        "request",
		ErrorType:         "cyber_policy_session_blocked",
		Severity:          "P3",
		StatusCode:        http.StatusForbidden,
		IsBusinessLimited: true,
		ErrorMessage:      "cyber_policy_session_blocked: request rejected locally by session block",
		ErrorSource:       "gateway_local",
		ErrorOwner:        "platform",
		CreatedAt:         meta.CreatedAt,
		// AccountID is deliberately unset: the request was rejected before account selection.
	}
	if meta.SessionBlockKey != "" {
		entry.ErrorBody = "session_block_key=" + meta.SessionBlockKey
	}
	if meta.UserID > 0 {
		entry.UserID = &meta.UserID
	}
	if meta.APIKeyID > 0 {
		entry.APIKeyID = &meta.APIKeyID
	}
	entry.GroupID = meta.GroupID
	if meta.ClientIP != "" {
		entry.ClientIP = &meta.ClientIP
	}
	return entry
}

func buildCyberSessionIdentityRejectedOpsEntry(meta cyberPolicyOpsErrorMeta) *service.OpsInsertErrorLogInput {
	message := "cyber_session_identity_rejected: request rejected before account selection"
	if meta.WSTurnAdmission {
		message = "cyber_session_identity_rejected: websocket turn rejected during session identity admission"
	}
	rt := int16(service.RequestTypeCyberBlocked)
	entry := &service.OpsInsertErrorLogInput{
		RequestID:         meta.RequestID,
		ClientRequestID:   meta.ClientRequestID,
		Platform:          meta.Platform,
		Model:             meta.Model,
		RequestPath:       meta.RequestPath,
		Stream:            meta.Stream,
		InboundEndpoint:   meta.InboundEndpoint,
		RequestType:       &rt,
		UserAgent:         meta.UserAgent,
		APIKeyPrefix:      meta.APIKeyPrefix,
		ErrorPhase:        "request",
		ErrorType:         "cyber_session_identity_rejected",
		Severity:          "P3",
		StatusCode:        http.StatusBadRequest,
		IsBusinessLimited: true,
		ErrorMessage:      appendCyberIdentityMetadata(message, meta),
		ErrorSource:       "gateway_local",
		ErrorOwner:        "client",
		CreatedAt:         meta.CreatedAt,
	}
	if meta.UserID > 0 {
		entry.UserID = &meta.UserID
	}
	if meta.APIKeyID > 0 {
		entry.APIKeyID = &meta.APIKeyID
	}
	entry.GroupID = meta.GroupID
	if meta.ClientIP != "" {
		entry.ClientIP = &meta.ClientIP
	}
	return entry
}

func appendCyberIdentityMetadata(message string, meta cyberPolicyOpsErrorMeta) string {
	status := strings.TrimSpace(meta.IdentityStatus)
	if status == "" {
		return message
	}
	message += "; session_identity_status=" + status
	if kind := strings.TrimSpace(meta.IdentityKind); kind != "" {
		message += "; session_identity_kind=" + kind
	}
	if source := strings.TrimSpace(meta.IdentitySource); source != "" {
		message += "; session_identity_source=" + source
	}
	if meta.IdentityInherited {
		message += "; session_identity_inherited=true"
	}
	return message
}

type openAIWSCyberTurnContext struct {
	body     []byte
	identity service.CyberSessionIdentityResolution
}

type openAIWSCyberIdentityBinding struct {
	mu                       sync.Mutex
	bound                    service.CyberSessionIdentityResolution
	connectionHeaderIdentity service.CyberSessionIdentityResolution
	headersCaptured          bool
}

type openAIWSCyberIdentityDecision struct {
	observed     service.CyberSessionIdentityResolution
	effective    service.CyberSessionIdentityResolution
	reject       bool
	identitySwap bool
}

func (b *openAIWSCyberIdentityBinding) resolve(apiKeyID int64, c *gin.Context, payload []byte, enabled, strict bool) openAIWSCyberIdentityDecision {
	observed := service.ResolveCyberSessionIdentity(apiKeyID, c, payload)
	decision := openAIWSCyberIdentityDecision{observed: observed, effective: observed}
	if !enabled {
		return decision
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.headersCaptured {
		b.connectionHeaderIdentity = service.ResolveCyberSessionIdentity(apiKeyID, c, nil)
		b.headersCaptured = true
	}

	switch observed.Metadata.Status {
	case service.OpenAIClientSessionIdentityResolved:
		if !b.bound.Resolved() {
			b.bound = observed
			return decision
		}
		if b.bound.BlockKey != observed.BlockKey {
			// Upgrade headers are connection-static. If the first frame bound a
			// higher-priority body thread and a later frame omits it, the lower-
			// priority static session header is not an explicit identity switch.
			if observed.Metadata.Source == service.OpenAIClientSessionIdentitySourceHeader &&
				b.connectionHeaderIdentity.Resolved() &&
				observed.BlockKey == b.connectionHeaderIdentity.BlockKey {
				decision.effective = service.InheritCyberSessionIdentity(b.bound)
				return decision
			}
			decision.reject = true
			decision.identitySwap = true
		}
		return decision
	case service.OpenAIClientSessionIdentityMissing:
		if b.bound.Resolved() {
			decision.effective = service.InheritCyberSessionIdentity(b.bound)
			return decision
		}
	case service.OpenAIClientSessionIdentityConflict, service.OpenAIClientSessionIdentityInvalid:
		// Once a connection has a trusted identity, an explicitly ambiguous
		// follow-up must never silently inherit that identity.
		if b.bound.Resolved() {
			decision.reject = true
			return decision
		}
	}
	decision.reject = strict
	return decision
}

func cyberIdentityRejectionMetadata(decision openAIWSCyberIdentityDecision) service.OpenAIClientSessionIdentityMetadata {
	if !decision.identitySwap {
		return decision.observed.Metadata
	}
	return service.OpenAIClientSessionIdentityMetadata{
		Status: service.OpenAIClientSessionIdentityConflict,
		Kind:   decision.observed.Metadata.Kind,
		Source: service.OpenAIClientSessionIdentitySourceConnection,
	}
}

// advanceOpenAIWSCyberBlockState tracks whether this WebSocket connection is blocked.
// A marked turn blocks the connection unless it failed over to another account, in
// which case the decision waits for the end of the failover chain.
func advanceOpenAIWSCyberBlockState(blocked, pending, marked bool, turnErr error) (bool, bool) {
	var failoverErr *service.UpstreamFailoverError
	isFailover := errors.As(turnErr, &failoverErr)
	if marked {
		if isFailover {
			return false, true
		}
		return true, false
	}
	if pending && !isFailover {
		return true, false
	}
	return blocked, pending
}

func writeOpenAIWSErrorFrame(ctx context.Context, conn *coderws.Conn, payload []byte) {
	if conn == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = conn.Write(writeCtx, coderws.MessageText, payload)
}

// writeCyberSessionBlockedWSError sends an error frame telling the client this
// session is blocked by the cyber session block before closing.
func writeCyberSessionBlockedWSError(ctx context.Context, conn *coderws.Conn) {
	payload, err := json.Marshal(gin.H{
		"event_id": "evt_cyber_session_blocked",
		"type":     "error",
		"error": gin.H{
			"type":    "permission_error",
			"code":    "session_blocked_by_cyber_policy",
			"message": cyberSessionBlockedClientMsg,
		},
	})
	if err != nil {
		payload = []byte(`{"event_id":"evt_cyber_session_blocked","type":"error","error":{"type":"permission_error","code":"session_blocked_by_cyber_policy","message":"This session is blocked by cyber-security policy, please start a new session"}}`)
	}
	writeOpenAIWSErrorFrame(ctx, conn, payload)
}

func writeCyberSessionIdentityRejectedWSError(ctx context.Context, conn *coderws.Conn, metadata service.OpenAIClientSessionIdentityMetadata) {
	code, message := cyberSessionIdentityError(metadata)
	payload, err := json.Marshal(gin.H{
		"event_id": "evt_cyber_session_identity_rejected",
		"type":     "error",
		"error": gin.H{
			"type":    "invalid_request_error",
			"code":    code,
			"message": message,
		},
	})
	if err != nil {
		payload = []byte(`{"event_id":"evt_cyber_session_identity_rejected","type":"error","error":{"type":"invalid_request_error","code":"cyber_session_identity_required","message":"A stable thread_id or session_id is required"}}`)
	}
	writeOpenAIWSErrorFrame(ctx, conn, payload)
}

func newOpenAIWSLocalAdmissionCloseError(reason string) error {
	return service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, reason, errOpenAIWSLocalAdmissionRejected)
}
