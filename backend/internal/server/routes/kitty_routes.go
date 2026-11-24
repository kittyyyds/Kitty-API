package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
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

	// Billing statement, CSV export & Billing Preview
	if k.BillingExport != nil || k.BillingPreview != nil {
		billing := authenticated.Group("/billing")
		if k.BillingExport != nil {
			billing.GET("/statement", k.BillingExport.Statement)
			billing.GET("/export", k.BillingExport.Export)
			adminBilling := admin.Group("/billing")
			adminBilling.GET("/users/:userId/statement", k.BillingExport.AdminStatement)
			adminBilling.GET("/users/:userId/export", k.BillingExport.AdminExport)
		}
		if k.BillingPreview != nil {
			billing.GET("/catalog", k.BillingPreview.Catalog)
			billing.POST("/preview", k.BillingPreview.Preview)
		}
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
	}

	// User & Admin Traffic Analytics (906_user_traffic_analytics.sql)
	if h.Usage != nil {
		usageTraffic := authenticated.Group("/usage")
		if panelRateLimiter != nil {
			usageTraffic.Use(panelRateLimiter.Heavy())
		}
		usageTraffic.GET("/traffic", h.Usage.Traffic)
		usageTraffic.GET("/traffic/keys", h.Usage.TrafficKeys)
	}
	if h.Admin != nil && h.Admin.Usage != nil {
		adminTraffic := admin.Group("/usage")
		adminTraffic.GET("/traffic", h.Admin.Usage.Traffic)
		adminTraffic.GET("/traffic/keys", h.Admin.Usage.TrafficKeys)
		adminTraffic.GET("/traffic/health", h.Admin.Usage.TrafficHealth)
	}

	// Support Tickets (904_support_tickets.sql)
	if k.SupportTicket != nil {
		tickets := authenticated.Group("/support-tickets")
		tickets.GET("", k.SupportTicket.List)
		tickets.POST("", k.SupportTicket.Create)
		tickets.GET("/summary", k.SupportTicket.Summary)
		tickets.GET("/:id", k.SupportTicket.Get)
		tickets.POST("/:id/messages", k.SupportTicket.Reply)
		tickets.POST("/:id/close", k.SupportTicket.Close)
		tickets.POST("/:id/reopen", k.SupportTicket.Reopen)

		adminTickets := admin.Group("/support-tickets")
		adminTickets.GET("", k.SupportTicket.AdminList)
		adminTickets.GET("/summary", k.SupportTicket.AdminSummary)
		adminTickets.GET("/:id", k.SupportTicket.AdminGet)
		adminTickets.POST("/:id/messages", k.SupportTicket.AdminReply)
		adminTickets.POST("/:id/status", k.SupportTicket.AdminSetStatus)
		adminTickets.DELETE("/:id", k.SupportTicket.AdminDelete)
	}

	// Channel Monitor V3 (907_channel_monitor_v3.sql)
	if k.ChannelMonitorV3 != nil {
		monitorV3 := authenticated.Group("/channel-monitor-v3")
		if panelRateLimiter != nil {
			monitorV3.Use(panelRateLimiter.Heavy())
		}
		monitorV3.Use(kittyChannelMonitorV3Guard(settingService))
		monitorV3.GET("/status", k.ChannelMonitorV3.Status)
		monitorV3.GET("/incidents", k.ChannelMonitorV3.Incidents)

		admin.PUT("/channel-monitor-mode", k.ChannelMonitorV3.SetMode)
		adminV3 := admin.Group("/channel-monitor-v3")
		adminV3.Use(kittyChannelMonitorAdminFeatureGuard(settingService))
		adminV3.GET("/settings", k.ChannelMonitorV3.GetSettings)
		adminV3.PUT("/config", k.ChannelMonitorV3.UpdateConfig)
		adminV3.PUT("/idle-probe", k.ChannelMonitorV3.UpdateIdleProbe)
		adminV3.POST("/categories", k.ChannelMonitorV3.CreateCategory)
		adminV3.PUT("/categories/:id", k.ChannelMonitorV3.UpdateCategory)
		adminV3.DELETE("/categories/:id", k.ChannelMonitorV3.DeleteCategory)
		adminV3.POST("/components", k.ChannelMonitorV3.CreateComponent)
		adminV3.PUT("/components/:id", k.ChannelMonitorV3.UpdateComponent)
		adminV3.DELETE("/components/:id", k.ChannelMonitorV3.DeleteComponent)
		adminV3.POST("/reorder", k.ChannelMonitorV3.Reorder)
		adminV3.GET("/status", k.ChannelMonitorV3.Status)
		adminV3.GET("/incidents", k.ChannelMonitorV3.Incidents)
	}

	// Registration Email Blacklist & Batch Disable
	if h.Admin != nil && h.Admin.Setting != nil {
		admin.GET("/settings/registration-email-blacklist", h.Admin.Setting.GetRegistrationEmailBlacklist)
		admin.PUT("/settings/registration-email-blacklist", h.Admin.Setting.UpdateRegistrationEmailBlacklist)
	}
	if h.Admin != nil && h.Admin.User != nil {
		admin.POST("/users/batch-disable", h.Admin.User.BatchDisable)
		admin.POST("/users/blacklist-matches", h.Admin.User.FindBlacklistedEmailUsers)
	}
}

func kittyChannelMonitorAdminFeatureGuard(settingService *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if settingService != nil && settingService.GetChannelMonitorRuntime(c.Request.Context()).Enabled {
			c.Next()
			return
		}
		response.ErrorFrom(c, service.ErrChannelMonitorDisabled)
		c.Abort()
	}
}

func kittyChannelMonitorV3Guard(settingService *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if settingService != nil {
			rt := settingService.GetChannelMonitorRuntime(c.Request.Context())
			if rt.Enabled && rt.Mode == service.ChannelMonitorModeV3 {
				c.Next()
				return
			}
		}
		response.ErrorFrom(c, service.ErrChannelMonitorDisabled)
		c.Abort()
	}
}
