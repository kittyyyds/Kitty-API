package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// RegisterKittyRoutes registers all reconstructed Kitty custom user, public, and admin routes.
func RegisterKittyRoutes(
	v1 *gin.RouterGroup,
	r *gin.Engine,
	h *handler.Handlers,
	jwtAuth middleware.JWTAuthMiddleware,
	optionalJWTAuth middleware.OptionalJWTAuthMiddleware,
	adminAuth middleware.AdminAuthMiddleware,
	auditLog middleware.AuditLogMiddleware,
	settingService *service.SettingService,
	panelRateLimiter *middleware.PanelRateLimiter,
	redisClient *redis.Client,
) {
	if h == nil || h.Kitty == nil {
		return
	}
	k := h.Kitty

	authenticated := v1.Group("")
	authenticated.Use(gin.HandlerFunc(jwtAuth))

	admin := v1.Group("/admin")
	admin.Use(gin.HandlerFunc(adminAuth))
	if auditLog != nil {
		admin.Use(gin.HandlerFunc(auditLog))
	}

	// API Key regeneration (POST /api/v1/keys/:id/regenerate)
	if h.APIKey != nil {
		authenticated.POST("/keys/:id/regenerate", h.APIKey.KittyRegenerate)
	}

	// Billing statement & CSV export (GET /api/v1/billing/statement, /export)
	if k.BillingExport != nil {
		billing := authenticated.Group("/billing")
		billing.GET("/statement", k.BillingExport.Statement)
		billing.GET("/export", k.BillingExport.Export)

		adminBilling := admin.Group("/billing")
		adminBilling.GET("/users/:userId/statement", k.BillingExport.AdminStatement)
		adminBilling.GET("/users/:userId/export", k.BillingExport.AdminExport)
	}

	// Global context pricing rules (901_kitty_billing_rules.sql)
	if h.Admin != nil && h.Admin.Channel != nil {
		ctxRules := admin.Group("/channels/context-rules")
		ctxRules.GET("", h.Admin.Channel.ListContextRules)
		ctxRules.POST("", h.Admin.Channel.SaveContextRule)
		ctxRules.PUT("/:ruleID", h.Admin.Channel.SaveContextRule)
		ctxRules.DELETE("/:ruleID", h.Admin.Channel.DeleteContextRule)
		ctxRules.GET("/models", h.Admin.Channel.ContextRuleModels)
	}

	// Expirable Recharge Bonus (902_kitty_recharge_bonus.sql)
	if k.RechargeBonus != nil {
		rb := authenticated.Group("/recharge-bonus")
		rb.GET("/rules", k.RechargeBonus.ListRules)
		rb.GET("/summary", k.RechargeBonus.GetSummary)
	}
	if k.AdminRechargeBonus != nil {
		arb := admin.Group("/recharge-bonus")
		arb.GET("/rules", k.AdminRechargeBonus.ListRules)
		arb.POST("/rules", k.AdminRechargeBonus.CreateRule)
		arb.PUT("/rules/:id", k.AdminRechargeBonus.UpdateRule)
		arb.DELETE("/rules/:id", k.AdminRechargeBonus.DeleteRule)
		arb.GET("/overview", k.AdminRechargeBonus.Overview)
	}
}
