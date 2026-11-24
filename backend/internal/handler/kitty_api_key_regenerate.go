package handler

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// KittyRegenerate handles POST /api/v1/keys/:id/regenerate
func (h *APIKeyHandler) KittyRegenerate(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid key ID")
		return
	}
	key, err := h.apiKeyService.KittyRegenerate(c.Request.Context(), id, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := dto.APIKeyFromService(key)
	out.Key = key.Key
	response.Success(c, out)
}
