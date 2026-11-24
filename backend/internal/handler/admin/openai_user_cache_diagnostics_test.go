//go:build unit

package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKitty36DiagnosticsAdminScopeAndValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &service.OpenAIGatewayService{}
	t.Cleanup(svc.StopUserCacheDiagnostic)
	h := &AccountHandler{openAIGatewayService: svc}
	call := func(method, role, body string, restricted bool) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(method, "/api/v1/admin/accounts/user-cache-diagnostics", strings.NewReader(body))
		if role != "" {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
			c.Set(string(middleware.ContextKeyUserRole), role)
		}
		if restricted {
			c.Request = c.Request.WithContext(adminaccess.WithPrincipal(c.Request.Context(), &adminaccess.Principal{Policy: adminaccess.Policy{GlobalPermissions: []string{"accounts.read", "accounts.write"}}}))
		}
		h.UserCacheDiagnostics(c)
		return w
	}
	config := `{"user_id":3,"model":"fixture-gpt","minutes":1,"max_samples":2}`
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		for _, role := range []string{"", "user", "observer"} {
			require.Equal(t, 403, call(method, role, config, false).Code)
		}
		require.Equal(t, 403, call(method, "admin", config, true).Code)
	}
	require.False(t, svc.UserCacheDiagnosticStatus().Active)
	for _, bad := range []string{`{}`, config + `{}`, `{"user_id":3,"model":"*","minutes":1,"max_samples":2}`, strings.Replace(config, `"minutes":1`, `"minutes":31`, 1), strings.Replace(config, `"max_samples":2`, `"max_samples":65`, 1), strings.Replace(config, `"user_id":3`, `"user_id":3,"capture_text":true`, 1)} {
		require.Equal(t, 400, call(http.MethodPut, "admin", bad, false).Code)
	}
	w := call(http.MethodPut, "admin", config, false)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.True(t, svc.UserCacheDiagnosticStatus().Active)
	require.Equal(t, 200, call(http.MethodDelete, "admin", "", false).Code)
	require.Nil(t, svc.UserCacheDiagnosticStatus().Config)
}
