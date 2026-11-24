package service

import (
	"context"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

var errOpenAIWSOutgoingFrameTooLarge = errors.New("serialized websocket frame exceeds HTTP bridge threshold before submission")

// This is a transport choice before submission, not a retry after model work.
// Native WS, explicit/inferred continuation and VM transports keep their existing contract.
func (s *OpenAIGatewayService) checkOpenAIWSFramePreflight(ctx context.Context, c *gin.Context, account *Account, previousResponseID string, frame []byte) error {
	if !s.openAIWSHTTPBridgeEnabled() || int64(len(frame)) < s.openAIWSHTTPBridgeThresholdBytes() ||
		strings.TrimSpace(previousResponseID) != "" || !openAIWSUnsubmittedHTTPFallbackEligible(ctx, c, account, nil) {
		return nil
	}
	return wrapOpenAIWSFallback("payload_too_large", errOpenAIWSOutgoingFrameTooLarge)
}

func openAIWSUnsubmittedHTTPFallbackEligible(ctx context.Context, c *gin.Context, account *Account, body []byte) bool {
	return ctx.Err() == nil && c != nil && c.Writer != nil && !c.Writer.Written() &&
		GetOpenAIClientTransport(c) == OpenAIClientTransportHTTP && isCodexOAuthAccount(account) &&
		!account.IsOpenAIAgentIdentity() && !account.IsOpenAIPersonalAccessToken() &&
		!account.IsOpenAIPassthroughEnabled() && !IsResponseCommitted(c) &&
		strings.TrimSpace(gjson.GetBytes(body, "previous_response_id").String()) == "" &&
		!openAIStreamClientOutputStarted(c, false) && (&openAIResponseReplayGuard{}).CanReplay(c)
}
