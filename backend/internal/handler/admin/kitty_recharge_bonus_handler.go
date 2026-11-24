package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type KittyRechargeBonusHandler struct {
	store *service.KittyRechargeBonusAdminStore
}

func NewKittyRechargeBonusHandler(store *service.KittyRechargeBonusAdminStore) *KittyRechargeBonusHandler {
	return &KittyRechargeBonusHandler{store: store}
}

func (h *KittyRechargeBonusHandler) ListRules(c *gin.Context) {
	rules, err := h.store.ListRules(c.Request.Context())
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, rules)
}

func (h *KittyRechargeBonusHandler) CreateRule(c *gin.Context) {
	var in service.KittyRechargeBonusRuleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid recharge bonus rule")
		return
	}
	rule, err := h.store.CreateRule(c.Request.Context(), in)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Created(c, rule)
}

func (h *KittyRechargeBonusHandler) UpdateRule(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid rule ID")
		return
	}
	var in service.KittyRechargeBonusRuleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid recharge bonus rule")
		return
	}
	rule, err := h.store.UpdateRule(c.Request.Context(), id, in)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, rule)
}

func (h *KittyRechargeBonusHandler) DeleteRule(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid rule ID")
		return
	}
	if err := h.store.DeleteRule(c.Request.Context(), id); response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *KittyRechargeBonusHandler) Overview(c *gin.Context) {
	data, err := h.store.Overview(c.Request.Context())
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, data)
}
