package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type KittyRechargeBonusHandler struct {
	paymentService *service.PaymentService
}

func NewKittyRechargeBonusHandler(paymentService *service.PaymentService) *KittyRechargeBonusHandler {
	return &KittyRechargeBonusHandler{paymentService: paymentService}
}

func (h *KittyRechargeBonusHandler) ListRules(c *gin.Context) {
	rules, err := h.paymentService.ListRechargeBonusRules(c.Request.Context())
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, rules)
}

func (h *KittyRechargeBonusHandler) GetSummary(c *gin.Context) {
	subject, ok := requireAuth(c)
	if !ok {
		return
	}
	summary, err := h.paymentService.GetRechargeBonusSummary(c.Request.Context(), subject.UserID)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, summary)
}
