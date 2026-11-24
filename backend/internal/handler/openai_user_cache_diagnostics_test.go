//go:build unit

package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKitty36DiagnosticsThreeHTTPEntrances(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"responses", "chat_completions", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			svc := &service.OpenAIGatewayService{}
			t.Cleanup(svc.StopUserCacheDiagnostic)
			require.NoError(t, svc.SetUserCacheDiagnostic(service.OpenAICacheDiagnosticConfig{UserID: 4348, Model: "gemini-2.5-pro", Minutes: 1, MaxSamples: 1}))
			h := &OpenAIGatewayHandler{gatewayService: svc, billingCacheService: &service.BillingCacheService{}, apiKeyService: &service.APIKeyService{}, concurrencyHelper: &ConcurrencyHelper{concurrencyService: &service.ConcurrencyService{}}}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, bytes.NewBufferString(`{"model":"gemini-2.5-pro","stream":false,"messages":[],"input":"private-marker"}`))
			key := &service.APIKey{ID: 4348, UserID: 4348, User: &service.User{ID: 4348}, Group: &service.Group{Platform: service.PlatformComposite, AllowMessagesDispatch: true}}
			c.Set(string(middleware.ContextKeyAPIKey), key)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 4348, Concurrency: 1})
			switch protocol {
			case "responses":
				h.Responses(c)
			case "chat_completions":
				h.ChatCompletions(c)
			case "messages":
				h.Messages(c)
			}
			require.Equal(t, 400, w.Code, w.Body.String())
			status := svc.UserCacheDiagnosticStatus()
			require.Len(t, status.Samples, 1)
			require.Equal(t, protocol, status.Samples[0].Protocol)
			require.Equal(t, "no_forward_attempt", status.Samples[0].Outcome)
			require.Zero(t, status.Samples[0].ForwardAttempts)
			require.Empty(t, status.Samples[0].Egress)
		})
	}
}
