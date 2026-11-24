package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const statusClientClosedRequest = 499

const (
	gatewayQueueFullCode          = "gateway_queue_full"
	gatewayConcurrencyLimitCode   = "gateway_concurrency_limit"
	gatewayAccountUnavailableCode = "gateway_account_unavailable"
	opsLocalAdmissionErrorCodeKey = "ops_local_admission_error_code"
)

// Only a terminal local rejection sets this marker. An upstream response can
// have the same error code, so the wire error alone is not local evidence.
func markOpsLocalAdmissionError(c *gin.Context, code string) {
	if c != nil && isOpsGatewayAdmissionCode(code) {
		c.Set(opsLocalAdmissionErrorCodeKey, code)
	}
}

func isOpsGatewayAdmissionCode(code string) bool {
	return code == gatewayQueueFullCode || code == gatewayConcurrencyLimitCode
}

func isOpsLocalAdmissionError(c *gin.Context, code string) bool {
	return c != nil && isOpsGatewayAdmissionCode(code) && c.GetString(opsLocalAdmissionErrorCodeKey) == code
}

func concurrencyErrorResponse(err error, slotType string) (int, string, string, string) {
	if errors.Is(err, service.ErrAccountAdmissionDenied) {
		return http.StatusServiceUnavailable, "api_error", gatewayAccountUnavailableCode,
			"Account temporarily unavailable due to scheduling or quality protection, please retry later"
	}

	var waitQueueFullErr *WaitQueueFullError
	if errors.As(err, &waitQueueFullErr) {
		return http.StatusTooManyRequests, "rate_limit_error", gatewayQueueFullCode,
			"Too many pending requests, please retry later"
	}

	var concurrencyErr *ConcurrencyError
	if errors.As(err, &concurrencyErr) {
		if concurrencyErr.SlotType != "" {
			slotType = concurrencyErr.SlotType
		}
		return http.StatusTooManyRequests, "rate_limit_error", gatewayConcurrencyLimitCode,
			fmt.Sprintf("Concurrency limit exceeded for %s, please retry later", slotType)
	}

	if errors.Is(err, context.Canceled) {
		return statusClientClosedRequest, "api_error", "", "context canceled"
	}

	return http.StatusServiceUnavailable, "api_error", "", "Service temporarily unavailable, please retry later"
}
