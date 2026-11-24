package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"github.com/gin-gonic/gin"
)

const openAIAccessRecoveryHandledKey = "kitty.openai_access_recovery_handled"
const openAIManualAccountTestKey = "kitty.openai_manual_account_test"
const openAIRefreshErrorPrefix = "Token refresh failed (non-retryable): "
const openAIVerifiedAccessRejectedMessage = "OpenAI OAuth access rejected after refresh failure; sign in again"

// AccountTestOwnsOpenAIAccessRecovery prevents a broad successful-test reset
// from clearing unrelated cooldown/quality state after this narrow recovery.
func AccountTestOwnsOpenAIAccessRecovery(c *gin.Context) bool {
	return c != nil && c.GetBool(openAIAccessRecoveryHandledKey)
}

type OpenAIOAuthAccessProofRepository interface {
	UpdateOpenAIAccessProofStatusIfUnchanged(context.Context, *Account, string, string) (bool, error)
}

func (api *OAuthRefreshAPI) openAIRefreshFailure(account *Account) *openai.RefreshTokenError {
	if r := api.openAIRejectionForAccount(account); r != nil {
		return r.err
	}
	return nil
}

func openAIAccessProofVersion(account *Account) [sha256.Size]byte {
	b, _ := json.Marshal(account.Credentials)
	return sha256.Sum256(b)
}

func (r *openAIRefreshRejection) canVerifyAccess() bool {
	if r == nil || r.err == nil || !r.err.Permanent || r.accessRejected {
		return false
	}
	// Only a diagnosed refresh-token fault is independent evidence. Generic
	// invalid_grant can also mean revocation of the underlying grant.
	return r.err.Code == "refresh_token_reused" || r.err.Code == "refresh_token_expired"
}

func openAIAccessProxyVersion(account *Account) (digest [sha256.Size]byte, ok bool) {
	if account.ProxyID == nil {
		return digest, account.Proxy == nil
	}
	proxy := account.Proxy
	if proxy == nil || *account.ProxyID != proxy.ID || !proxy.IsActive() || proxy.IsExpired(time.Now()) {
		return digest, false
	}
	// Keep the digest in process. The proxy URL can include user info.
	return sha256.Sum256([]byte(proxy.URL() + "\x00" + proxy.UpdatedAt.UTC().Format(time.RFC3339Nano))), true
}

func (r *openAIRefreshRejection) accessVerifiedAt(account *Account, now time.Time) bool {
	if !r.canVerifyAccess() || !canRecoverOpenAIAccess(account) || r.credentials != openAIRefreshFingerprint(account) ||
		!now.Before(r.verifiedAccessUntil) || r.proofCredentials != openAIAccessProofVersion(account) {
		return false
	}
	proxy, ok := openAIAccessProxyVersion(account)
	expiry := openAIAccessTokenExpiresAt(account)
	return ok && proxy == r.proofProxy && expiry != nil && now.Before(*expiry)
}

func canRecoverOpenAIAccess(account *Account) bool {
	return canRecoverOpenAIOAuth(account) && !account.IsOpenAIVMBound() && !account.IsRetiredBPSCredential() &&
		len(account.ProxyIDs) <= 1 && account.SelectedProxyID == 0 &&
		strings.TrimSpace(account.GetChatGPTAccountID()) != ""
}

func openAITestResponseAuthorization(account *Account, sent *http.Request) string {
	if account == nil || sent == nil || sent.URL == nil || sent.Method != http.MethodPost || sent.URL.String() != chatgptCodexAPIURL {
		return ""
	}
	if (sent.Host != "" && !strings.EqualFold(sent.Host, sent.URL.Host)) || sent.Header.Get("chatgpt-account-id") != account.GetChatGPTAccountID() {
		return ""
	}
	return sent.Header.Get("Authorization")
}

func sentOpenAIAccessTokenMatches(account *Account, authorization string) bool {
	return account != nil && account.GetOpenAIAccessToken() != "" && authorization == "Bearer "+account.GetOpenAIAccessToken()
}

// A complete nonempty text and a successful terminal must agree. HTTP 200,
// a bare completed event, tool-only output, and a truncated stream are no proof.
func openAICompletedTextProof(data map[string]any, streamed string) bool {
	r, _ := data["response"].(map[string]any)
	if r == nil || r["status"] != "completed" || r["error"] != nil || r["incomplete_details"] != nil || data["error"] != nil {
		return false
	}
	var final strings.Builder
	output, hasOutput := r["output"].([]any)
	if _, present := r["output"]; present && !hasOutput {
		return false
	}
	for _, raw := range output {
		item, _ := raw.(map[string]any)
		if item["type"] == "reasoning" {
			continue
		}
		if item["type"] != "message" || (item["status"] != nil && item["status"] != "completed") {
			return false
		}
		content, _ := item["content"].([]any)
		for _, rawBlock := range content {
			block, _ := rawBlock.(map[string]any)
			if block["type"] != "output_text" {
				return false
			}
			text, _ := block["text"].(string)
			if final.Len()+len(text) > 2<<20 {
				return false
			}
			final.WriteString(text)
		}
	}
	if hasOutput {
		return strings.TrimSpace(final.String()) != "" && (streamed == "" || streamed == final.String())
	}
	return strings.TrimSpace(streamed) != ""
}

// Proof is scoped to an immutable rejection generation. A later 401 replaces
// that generation, so a late successful probe cannot resurrect rejected access.
func (api *OAuthRefreshAPI) verifyOpenAIAccessAfterTest(ctx context.Context, account *Account, before *openAIRefreshRejection, authorization string) bool {
	if api == nil || api.accountRepo == nil || !before.canVerifyAccess() || !canRecoverOpenAIAccess(account) ||
		!sentOpenAIAccessTokenMatches(account, authorization) || before.credentials != openAIRefreshFingerprint(account) || ctx.Err() != nil {
		return false
	}
	testProxy, ok := openAIAccessProxyVersion(account)
	if !ok {
		return false
	}
	mu := api.getLocalLock(OpenAITokenCacheKey(account))
	if mu.Lock(ctx) != nil {
		return false
	}
	defer mu.Unlock()
	latest, err := api.accountRepo.GetByID(ctx, account.ID)
	if err != nil || latest == nil || latest.ID != account.ID || !canRecoverOpenAIAccess(latest) || !sameOpenAIOAuthOwner(account, latest) ||
		!sameOpenAIOAuthProxy(account, latest) || !latest.Schedulable || latest.PerformancePaused ||
		openAIAccessProofVersion(latest) != openAIAccessProofVersion(account) || api.openAIRejectionForAccount(latest) != before {
		return false
	}
	latestProxy, ok := openAIAccessProxyVersion(latest)
	if !ok || latestProxy != testProxy {
		return false
	}
	expiry := openAIAccessTokenExpiresAt(latest)
	if expiry == nil || !time.Now().Before(*expiry) {
		return false
	}
	if !latest.IsActive() {
		// Only our own persisted refresh rejection may be cleared, with full
		// credential/proxy/message/status CAS; never a manual or other error.
		if latest.Status != StatusError || latest.ErrorMessage != openAIRefreshErrorPrefix+logredact.RedactText(before.err.Error()) {
			return false
		}
		repo, ok := api.accountRepo.(OpenAIOAuthAccessProofRepository)
		if !ok {
			return false
		}
		applied, err := repo.UpdateOpenAIAccessProofStatusIfUnchanged(ctx, latest, StatusActive, "")
		if err != nil || !applied {
			return false
		}
	}
	verified := *before
	verified.verifiedAccessUntil = *expiry
	verified.proofCredentials = openAIAccessProofVersion(latest)
	verified.proofProxy = latestProxy
	return api.openAIFailures.CompareAndSwap(account.ID, before, &verified)
}

// Record denial before waiting for the refresh lock. A probe already writing its
// CAS may finish that write, but cannot publish proof, and the denial is fenced
// durably against the account revision read after that write.
func (api *OAuthRefreshAPI) rejectVerifiedOpenAIAccess(ctx context.Context, account *Account, authorization string) {
	var before *openAIRefreshRejection
	for {
		before = api.openAIRejectionForAccount(account)
		if !before.canVerifyAccess() || !sentOpenAIAccessTokenMatches(account, authorization) {
			return
		}
		rejected := &openAIRefreshRejection{credentials: before.credentials, err: before.err, accessRejected: true}
		if api.openAIFailures.CompareAndSwap(account.ID, before, rejected) {
			break
		}
	}
	repo, ok := api.accountRepo.(OpenAIOAuthAccessProofRepository)
	if !ok {
		return
	}
	// The result is credential state, independent of whether the caller is
	// still waiting for a response. Bound the lock and database work.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	mu := api.getLocalLock(OpenAITokenCacheKey(account))
	if mu.Lock(ctx) != nil {
		return
	}
	defer mu.Unlock()
	latest, err := api.accountRepo.GetByID(ctx, account.ID)
	if err != nil || latest == nil || !canRecoverOpenAIAccess(latest) || !latest.Schedulable ||
		openAIAccessProofVersion(latest) != openAIAccessProofVersion(account) || !sameOpenAIOAuthProxy(latest, account) {
		return
	}
	if !latest.IsActive() && (latest.Status != StatusError || latest.ErrorMessage != openAIRefreshErrorPrefix+logredact.RedactText(before.err.Error())) {
		return
	}
	if _, err := repo.UpdateOpenAIAccessProofStatusIfUnchanged(ctx, latest, StatusError, openAIVerifiedAccessRejectedMessage); err != nil {
		slog.Warn("openai_access_proof_denial_write_failed", "account_id", latest.ID)
	}
}

func (s *TokenRefreshService) handleRejectedOpenAIRefresh(ctx context.Context, attempted *Account, cause *openai.RefreshTokenError, message string) error {
	mu := s.refreshAPI.getLocalLock(OpenAITokenCacheKey(attempted))
	if err := mu.Lock(ctx); err != nil {
		return err
	}
	defer mu.Unlock()
	latest, err := s.accountRepo.GetByID(ctx, attempted.ID)
	if err != nil {
		return &providerCycleContainmentRefreshError{err: fmt.Errorf("reread OpenAI refresh state: %w", err)}
	}
	if latest == nil || !latest.IsActive() || !latest.Schedulable || !sameOpenAIOAuthOwner(attempted, latest) || !sameOpenAIOAuthProxy(attempted, latest) {
		return errRefreshSkipped
	}
	failure := s.refreshAPI.openAIRejectionForAccount(latest)
	if failure == nil || failure.err != cause || failure.credentials != openAIRefreshFingerprint(attempted) || failure.accessVerifiedAt(latest, time.Now()) {
		return errRefreshSkipped
	}
	if failure.accessRejected {
		message = openAIVerifiedAccessRejectedMessage
	}
	repo, ok := s.accountRepo.(OpenAIOAuthRefreshFailureRepository)
	if !ok {
		return openAIRefreshCASNotConfigured()
	}
	applied, err := repo.SetOpenAIOAuthRefreshErrorIfUnchanged(ctx, latest.ID, latest.Credentials, latest.ProxyID, message)
	if err != nil {
		return &providerCycleContainmentRefreshError{err: errors.New("failed to conditionally persist OpenAI refresh rejection")}
	}
	if !applied {
		return errRefreshSkipped
	}
	s.notifyAccountSchedulingBlocked(latest, time.Time{}, "token_refresh_non_retryable")
	invalidated := s.cacheInvalidator != nil && s.cacheInvalidator.InvalidateToken(ctx, latest) == nil
	return &accountPermanentRefreshError{err: cause, persistentlyBlocked: true, cacheInvalidationFailed: !invalidated}
}
