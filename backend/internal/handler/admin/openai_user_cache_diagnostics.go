package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// UserCacheDiagnostics is registered inside the normal admin and audit groups.
func (h *AccountHandler) UserCacheDiagnostics(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !ipAuditAdministrator(c) {
		return
	}
	if adminaccess.RestrictedFromContext(c.Request.Context()) != nil {
		response.Error(c, http.StatusForbidden, "Customer request diagnostics require full administrator access")
		return
	}
	gateway := h.openAIGatewayService
	if gateway == nil {
		response.Error(c, http.StatusServiceUnavailable, "Gateway unavailable")
		return
	}
	switch c.Request.Method {
	case http.MethodPut:
		var cfg service.OpenAICacheDiagnosticConfig
		decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 4096))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&cfg) != nil || decoder.Decode(new(any)) != io.EOF {
			response.BadRequest(c, "Invalid cache diagnostics configuration")
			return
		}
		if err := gateway.SetUserCacheDiagnostic(cfg); err != nil {
			if errors.Is(err, service.ErrInvalidOpenAICacheDiagnostic) {
				response.BadRequest(c, err.Error())
			} else {
				response.Error(c, http.StatusServiceUnavailable, "Failed to start cache diagnostics")
			}
			return
		}
	case http.MethodDelete:
		gateway.StopUserCacheDiagnostic()
	}
	response.Success(c, gateway.UserCacheDiagnosticStatus())
}
