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
		admin.GET("/channels/context-rule-models", h.Admin.Channel.ContextRuleModels)
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
		adminTraffic.GET("/connections", h.Admin.Usage.Connections)
		adminTraffic.GET("/audit/stats", h.Admin.Usage.AuditStats)
		adminTraffic.GET("/audit/export", h.Admin.Usage.AuditExport)
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


	// QQ Bot public, user, and admin routes (910/911/922)
	if k.QQBot != nil {
		v1.POST("/qqbot/webhook", k.QQBot.Webhook)
		v1.GET("/settings/community", k.QQBot.CommunityGuide)

		qqBinding := v1.Group("/qqbot/email-binding")
		if panelRateLimiter != nil {
			qqBinding.Use(panelRateLimiter.Heavy())
		}
		qqBinding.POST("/preview", k.QQBot.PreviewEmailBinding)
		qqBinding.POST("/confirm", k.QQBot.ConfirmEmailBinding)

		qqTransfer := v1.Group("/qqbot/transfer")
		if panelRateLimiter != nil {
			qqTransfer.Use(panelRateLimiter.Heavy())
		}
		qqTransfer.POST("/preview", k.QQBot.PreviewTransfer)
		qqTransfer.POST("/confirm", k.QQBot.ConfirmTransfer)

		userQQBot := authenticated.Group("/user/qqbot")
		userQQBot.GET("/binding", k.QQBot.Binding)
		if panelRateLimiter != nil {
			userQQBot.POST("/binding-code", panelRateLimiter.Heavy(), k.QQBot.CreateBindingCode)
		} else {
			userQQBot.POST("/binding-code", k.QQBot.CreateBindingCode)
		}
		userQQBot.POST("/binding-confirm", k.QQBot.ConfirmBinding)

		bot := admin.Group("/qqbot")
		bot.GET("/config", k.QQBot.GetConfig)
		bot.GET("/setup/options", k.QQBot.SetupOptions)
		bot.GET("/setup/checks", k.QQBot.SetupChecks)
		bot.PUT("/setup", k.QQBot.SaveSetup)
		bot.POST("/setup/probe/:kind", k.QQBot.SetupProbe)
		bot.GET("/activity-templates", k.QQBot.ActivityTemplates)
		bot.POST("/activity-templates", k.QQBot.ApplyActivityTemplate)
		bot.PUT("/config", k.QQBot.SaveConfig)
		bot.GET("/runtime", k.QQBot.Runtime)
		bot.GET("/members", k.QQBot.ListMembers)
		bot.PUT("/config/:section", k.QQBot.SaveConfigSection)
		bot.GET("/config-history", k.QQBot.ConfigHistory)
		bot.GET("/overview", k.QQBot.Overview)
		bot.GET("/jobs", k.QQBot.ListJobs)
		bot.GET("/moderation-records", k.QQBot.ListModerationRecords)
		bot.GET("/diagnosis", k.QQBot.Diagnose)
		bot.GET("/commands", k.QQBot.Commands)
		bot.GET("/activity-preview", k.QQBot.PreviewActivities)
		bot.DELETE("/groups", k.QQBot.DeleteDiscoveredGroup)
		bot.GET("/groups/:group_id/info", k.QQBot.GroupInfo)
		bot.GET("/records/:kind", k.QQBot.ListActivityRecords)
		bot.GET("/checkin", k.QQBot.GetCheckinConfig)
		bot.PUT("/checkin", k.QQBot.SaveCheckinConfig)
		bot.GET("/rankings", k.QQBot.GetRankingConfig)
		bot.PUT("/rankings", k.QQBot.SaveRankingConfig)
		bot.GET("/invite", k.QQBot.GetInviteConfig)
		bot.PUT("/invite", k.QQBot.SaveInviteConfig)
		bot.GET("/transfer", k.QQBot.GetTransferConfig)
		bot.GET("/transfers", k.QQBot.TransferHistory)
		bot.PUT("/transfer", k.QQBot.SaveTransferConfig)
		bot.GET("/lottery", k.QQBot.GetLotteryConfig)
		bot.PUT("/lottery", k.QQBot.SaveLotteryConfig)
		bot.GET("/lottery/draws", k.QQBot.ListLotteryDraws)
		bot.GET("/lottery/users/:user_id", k.QQBot.LotteryState)
		bot.GET("/reward-codes", k.QQBot.ListRewardCodes)
		bot.PATCH("/reward-codes/:id", k.QQBot.UpdateRewardCode)
		bot.GET("/bindings", k.QQBot.ListBindings)
		bot.DELETE("/bindings/:user_id", k.QQBot.Unbind)
		bot.POST("/test/:kind", k.QQBot.Probe)
		bot.POST("/chat-preview", k.QQBot.PreviewChat)
	}


	// Operations Dashboard, Pricing Audit, Provider Hub, CS1 Providers, User Cleanup, Account Intelligence & Ops Stats
	if h.Admin != nil {
		if h.Admin.Ops != nil {
			admin.POST("/ops/account-request-stats/batch", h.Admin.Ops.GetAccountRequestStatsBatch)
		}
		if k.UserCleanup != nil {
			admin.GET("/users/cleanup/summary", k.UserCleanup.Summary)
			admin.POST("/users/cleanup/preview", k.UserCleanup.Preview)
			admin.POST("/users/cleanup/execute", k.UserCleanup.Execute)
			admin.GET("/users/:id/cleanup-guard", k.UserCleanup.GetGuard)
			admin.PUT("/users/:id/cleanup-guard", k.UserCleanup.UpdateGuard)
		}
		if h.Admin.Dashboard != nil {
			admin.GET("/dashboard/operations", h.Admin.Dashboard.GetOperationsDashboard)
			admin.GET("/dashboard/operations/recharge-ranking", h.Admin.Dashboard.GetOperationsRechargeRanking)
			admin.GET("/dashboard/operations/users", h.Admin.Dashboard.GetOperationsUsers)
			admin.GET("/dashboard/operations/users/suggest", h.Admin.Dashboard.GetOperationsUserSuggestions)
		}
		if h.Admin.Account != nil {
			admin.GET("/accounts/intelligence-tests/recent", h.Admin.Account.IntelligenceRecent)
			admin.GET("/accounts/:id/intelligence-tests", h.Admin.Account.IntelligenceHistory)
			admin.POST("/accounts/:id/intelligence-tests", h.Admin.Account.StartIntelligenceTest)
			admin.GET("/accounts/:id/intelligence-schedule", h.Admin.Account.GetIntelligenceSchedule)
			admin.PUT("/accounts/:id/intelligence-schedule", h.Admin.Account.SaveIntelligenceSchedule)

			p := admin.Group("/providers")
			p.GET("", h.Admin.Account.ListProviders)
			p.GET("/options", h.Admin.Account.ListProviderOptions)
			p.GET("/user-balance", h.Admin.Account.ProviderUserBalance)
			p.POST("/probe", h.Admin.Account.ProbeProvider)
			p.POST("", h.Admin.Account.CreateProvider)
			p.PUT("/:id", h.Admin.Account.UpdateProvider)
			p.DELETE("/:id", h.Admin.Account.DeleteProvider)
			p.POST("/:id/refresh", h.Admin.Account.RefreshProvider)
			p.GET("/:id/keys", h.Admin.Account.ListProviderKeys)
			p.GET("/:id/groups", h.Admin.Account.ListProviderKeyGroups)
			p.POST("/:id/keys", h.Admin.Account.CreateProviderKey)
		}
		if h.Admin.Channel != nil {
			admin.GET("/channels/pricing-audit", h.Admin.Channel.PricingAudit)
			admin.GET("/operations", h.Admin.Channel.ProviderOperations)
			providers := admin.Group("/provider-hub")
			providers.GET("", h.Admin.Channel.ProviderHubState)
			providers.POST("/suppliers", h.Admin.Channel.ProviderHubMutation("supplier"))
			providers.PUT("/suppliers/:id", h.Admin.Channel.ProviderHubMutation("supplier"))
			providers.POST("/suppliers/:id/archive", h.Admin.Channel.ProviderHubMutation("archive_supplier"))
			providers.POST("/wallets", h.Admin.Channel.ProviderHubMutation("wallet"))
			providers.PUT("/wallets/:id", h.Admin.Channel.ProviderHubMutation("wallet"))
			providers.POST("/wallets/:id/archive", h.Admin.Channel.ProviderHubMutation("archive_wallet"))
			providers.PUT("/wallets/:id/authorization", h.Admin.Channel.ProviderHubMutation("authorization"))
			providers.PUT("/wallets/:id/bindings", h.Admin.Channel.ProviderHubMutation("bindings"))
			providers.POST("/bindings/:id/unbind", h.Admin.Channel.ProviderHubMutation("unbind"))
			providers.POST("/wallets/:id/purchases", h.Admin.Channel.ProviderHubMutation("purchase"))
			providers.GET("/wallets/:id/purchases", h.Admin.Channel.ProviderHubPurchases)
			providers.POST("/purchases/:id/void", h.Admin.Channel.ProviderHubMutation("void_purchase"))
			providers.POST("/wallets/:id/rules", h.Admin.Channel.ProviderHubMutation("rule"))
			providers.POST("/rules/:id/close", h.Admin.Channel.ProviderHubMutation("close_rule"))
			providers.POST("/adjustments", h.Admin.Channel.ProviderHubMutation("adjustment"))
			providers.POST("/wallets/:id/sync", h.Admin.Channel.ProviderHubSync)
			providers.GET("/wallets/:id/keys", h.Admin.Channel.ProviderHubKeys)
			providers.GET("/wallets/:id/groups", h.Admin.Channel.ProviderHubGroups)
			providers.POST("/wallets/:id/keys", h.Admin.Channel.ProviderHubCreateKey)
			providers.POST("/wallets/:id/import", h.Admin.Channel.ProviderHubImportKeys)
		}
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
