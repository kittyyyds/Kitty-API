//go:build unit

package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKittyRouteParityAndRetirementContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")

	noop := func(c *gin.Context) { c.Next() }
	h := &handler.Handlers{
		APIKey:  &handler.APIKeyHandler{},
		Usage:   &handler.UsageHandler{},
		Payment: &handler.PaymentHandler{},
		Gateway: &handler.GatewayHandler{},
		OpenAIGateway: &handler.OpenAIGatewayHandler{},
		Admin: &handler.AdminHandlers{
			Group:     &admin.GroupHandler{},
			User:      &admin.UserHandler{},
			Account:   &admin.AccountHandler{},
			Channel:   &admin.ChannelHandler{},
			Dashboard: &admin.DashboardHandler{},
			Ops:       &admin.OpsHandler{},
			Setting:   &admin.SettingHandler{},
			Usage:     &admin.UsageHandler{},
			Payment:   &admin.PaymentHandler{},
		},
		Kitty: &handler.KittyModules{
			BillingExport:      &admin.BillingExportHandler{},
			RechargeBonus:      &handler.KittyRechargeBonusHandler{},
			AdminRechargeBonus: &admin.KittyRechargeBonusHandler{},
			SupportTicket:      &handler.SupportTicketHandler{},
			BillingPreview:     &handler.BillingPreviewHandler{},
			ChannelMonitorV3:   &handler.ChannelMonitorV3Handler{},
			UserCleanup:        &admin.UserCleanupHandler{},
			QQBot:              &handler.QQBotHandler{},
		},
	}

	RegisterUserRoutes(v1, h, middleware.JWTAuthMiddleware(noop), nil, nil, nil)
	RegisterAPIKeyTestRoutes(r, h, middleware.JWTAuthMiddleware(noop), noop, nil, nil, nil, nil)
	RegisterAdminRoutes(v1, h, middleware.AdminAuthMiddleware(noop), nil, middleware.StepUpAuthMiddleware(noop), nil, nil)
	RegisterPaymentRoutes(v1, h.Payment, &handler.PaymentWebhookHandler{}, h.Admin.Payment, middleware.JWTAuthMiddleware(noop), middleware.AdminAuthMiddleware(noop), nil, nil, nil, nil)
	RegisterKittyRoutes(v1, r, h, middleware.JWTAuthMiddleware(noop), middleware.OptionalJWTAuthMiddleware(noop), middleware.AdminAuthMiddleware(noop), nil, nil, nil, nil)

	registered := make(map[string]bool)
	for _, rt := range r.Routes() {
		registered[rt.Method+" "+rt.Path] = true
	}

	requiredKittyRoutes := []string{
		"POST /api/v1/keys/:id/regenerate",
		"GET /api/v1/billing/statement",
		"GET /api/v1/billing/export",
		"GET /api/v1/billing/catalog",
		"POST /api/v1/billing/preview",
		"GET /api/v1/payment/invoices/config",
		"GET /api/v1/payment/invoices",
		"POST /api/v1/payment/webhook/dujiaopay",
		"POST /api/v1/payment/webhook/paypal",
		"GET /api/v1/recharge-bonus/rules",
		"GET /api/v1/recharge-bonus/summary",
		"GET /api/v1/usage/traffic",
		"GET /api/v1/usage/traffic/keys",
		"GET /api/v1/support-tickets",
		"GET /api/v1/channel-monitor-v3/status",
		"GET /api/v1/user/qqbot/binding",
		"POST /api/v1/qqbot/webhook",
		"GET /api/v1/settings/community",
		"GET /api/v1/admin/dashboard/operations",
		"GET /api/v1/admin/channels/context-rules",
		"GET /api/v1/admin/channels/context-rule-models",
		"GET /api/v1/admin/channels/pricing-audit",
		"GET /api/v1/admin/provider-hub",
		"GET /api/v1/admin/providers",
		"GET /api/v1/admin/users/cleanup/summary",
		"GET /api/v1/admin/accounts/intelligence-tests/recent",
		"POST /api/v1/admin/ops/account-request-stats/batch",
		"GET /api/v1/admin/usage/traffic",
		"GET /api/v1/admin/usage/connections",
		"GET /api/v1/admin/usage/audit/stats",
		"GET /api/v1/admin/settings/registration-email-blacklist",
		"POST /api/v1/admin/users/batch-disable",
		"GET /api/v1/admin/qqbot/overview",
		"GET /api/v1/admin/support-tickets",
		"PUT /api/v1/admin/channel-monitor-mode",
		// Kitty.3 restored A-level routes
		"GET /api/v1/keys/concurrency",
		"GET /api/v1/key-tests/:key_id/models",
		"POST /api/v1/key-tests/:key_id",
		"GET /api/v1/admin/accounts/platforms",
		"GET /api/v1/admin/accounts/management-capabilities",
		"POST /api/v1/admin/accounts/performance",
		"PUT /api/v1/admin/accounts/:id/performance",
		"POST /api/v1/admin/accounts/:id/performance/release",
		"GET /api/v1/admin/accounts/:id/performance/events",
		"GET /api/v1/admin/accounts/:id/upstream-billing-probe/config",
		"POST /api/v1/admin/accounts/:id/upstream-billing-probe/config/preview",
		"PUT /api/v1/admin/accounts/:id/upstream-billing-probe/config",
		"DELETE /api/v1/admin/accounts/:id/upstream-billing-probe/config",
		"POST /api/v1/admin/accounts/models/sync-upstream-batch",
		"GET /api/v1/admin/usage/:id/timing",
		"GET /api/v1/admin/payment/recharge-activity",
		"GET /api/v1/admin/payment/recharge-activity/summary",
		"GET /api/v1/admin/payment/recharge-risks",
		"GET /api/v1/admin/groups/:id/smart-ws",
		"PUT /api/v1/admin/groups/:id/smart-ws",
		"GET /api/v1/admin/groups/:id/available-models",
		"PUT /api/v1/admin/groups/:id/user-denied-models",
		"DELETE /api/v1/admin/groups/:id/user-denied-models",
		"GET /api/v1/admin/settings/openai-response-header-timeout",
		"PUT /api/v1/admin/settings/openai-response-header-timeout",
	}
	for _, route := range requiredKittyRoutes {
		require.True(t, registered[route], "missing required Kitty route: %s", route)
	}

	// Retired 950/952 routes MUST return 404 (matches live .24 extra_checks.py contract)
	retiredEndpoints := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/admin/security-policy/config"},
		{http.MethodGet, "/api/v1/admin/security-policy/logs"},
		{http.MethodGet, "/api/v1/admin/content-moderation/config"},
		{http.MethodGet, "/api/v1/admin/intelligent-test/config"},
		{http.MethodPost, "/api/v1/admin/users/1/vip"},
	}
	for _, ep := range retiredEndpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusNotFound, w.Code, "retired endpoint must return 404: %s %s", ep.method, ep.path)
	}
}
