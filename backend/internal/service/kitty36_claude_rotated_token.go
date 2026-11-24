package service

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// Reload an already-rotated token once after a 401. This path never refreshes,
// mutates credentials, changes proxies or clears any account cooldown.
func (s *GatewayService) rotatedClaudeAccessToken(ctx context.Context, account *Account, rejectedToken string) string {
	if account == nil || account.Platform != PlatformAnthropic || account.Type != AccountTypeOAuth ||
		s.accountRepo == nil || rejectedToken == "" || ctx.Err() != nil {
		return ""
	}
	latest, err := s.accountRepo.GetByID(ctx, account.ID)
	if err != nil || latest == nil || latest.ID != account.ID || latest.Platform != account.Platform || latest.Type != account.Type ||
		!latest.IsSchedulable() || !sameOpenAIOAuthProxy(account, latest) {
		return ""
	}
	for _, field := range []string{"org_uuid", "account_uuid", "email", "scope", "scopes"} {
		if strings.TrimSpace(account.GetCredential(field)) != strings.TrimSpace(latest.GetCredential(field)) {
			return ""
		}
	}
	for _, field := range []string{"org_uuid", "account_uuid", "email"} {
		if strings.TrimSpace(account.GetExtraString(field)) != strings.TrimSpace(latest.GetExtraString(field)) {
			return ""
		}
	}
	now := time.Now()
	if expiry := latest.GetCredentialAsTime("expires_at"); expiry != nil && !expiry.After(now) {
		return ""
	}
	token := strings.TrimSpace(latest.GetCredential("access_token"))
	if token == "" || token == rejectedToken {
		return ""
	}
	if p := s.claudeTokenProvider; p != nil && p.tokenCache != nil {
		if err := p.tokenCache.DeleteAccessToken(ctx, ClaudeTokenCacheKey(latest)); err != nil {
			slog.Warn("claude_rotated_token_cache_invalidation_failed", "account_id", account.ID)
		}
	}
	return token
}
