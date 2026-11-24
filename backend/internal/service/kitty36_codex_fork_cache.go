package service

import (
	"strings"

	"github.com/gin-gonic/gin"
)

func codexClientSessionID(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	return extractClientSessionID(c.Request.Header)
}

// Ephemeral Codex forks name themselves in metadata but retain their parent's
// session header and cache key. Both carriers use the same credential namespace.
func codexPromptCacheKeyKind(cacheKey, bodySession string, clientSession ...string) string {
	if strings.TrimSpace(bodySession) != "" && cacheKey == bodySession {
		return "session"
	}
	for _, id := range clientSession {
		if strings.TrimSpace(id) != "" && cacheKey == id {
			return "session"
		}
	}
	return "prompt-cache"
}
