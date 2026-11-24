//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

type kitty36AccessRepo struct {
	*openAIRecoveryRepo
	clears    int
	rejects   int
	beforeCAS func()
}

func (r *kitty36AccessRepo) UpdateOpenAIAccessProofStatusIfUnchanged(_ context.Context, expected *Account, status, message string) (bool, error) {
	if r.beforeCAS != nil && status == StatusActive {
		r.beforeCAS()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	left, _ := json.Marshal(expected.Credentials)
	right, _ := json.Marshal(r.account.Credentials)
	if expected.ID != r.account.ID || string(left) != string(right) || !r.account.Schedulable ||
		r.account.Status != expected.Status || r.account.ErrorMessage != expected.ErrorMessage ||
		!expected.UpdatedAt.Equal(r.account.UpdatedAt) || !sameOpenAIOAuthProxy(expected, r.account) {
		return false, nil
	}
	r.account = snapshotOAuthRefreshAccount(r.account)
	r.account.Status = status
	r.account.ErrorMessage = message
	r.account.UpdatedAt = time.Now()
	if status == StatusActive {
		r.clears++
	} else {
		r.rejects++
	}
	return true, nil
}

func TestKitty36VerifiedAccessBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Account)
		want   bool
	}{
		{"same state", nil, true},
		{"new access", func(a *Account) { a.Credentials["access_token"] = "new" }, false},
		{"new refresh", func(a *Account) { a.Credentials["refresh_token"] = "new" }, false},
		{"other workspace", func(a *Account) { a.Credentials["chatgpt_account_id"] = "other" }, false},
		{"other user", func(a *Account) { a.Credentials["chatgpt_user_id"] = "other" }, false},
		{"proxy changed", func(a *Account) { v := int64(99); a.ProxyID = &v }, false},
		{"credentials revision", func(a *Account) { a.Credentials["id_token"] = "changed" }, false},
		{"performance pause", func(a *Account) { a.PerformancePaused = true }, false},
		{"proxy pool", func(a *Account) { a.ProxyIDs = []int64{1, 2} }, false},
		{"manual stop", func(a *Account) { a.Schedulable = false }, false},
		{"disabled", func(a *Account) { a.Status = "disabled" }, false},
		{"other error", func(a *Account) { a.Status = StatusError; a.ErrorMessage = "admin hold" }, false},
		{"expired", func(a *Account) { a.Credentials["expires_at"] = time.Now().Add(-time.Second).Format(time.RFC3339) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, r, ex, p := newOpenAIRecoveryFixture()
			api := p.refreshAPI
			rejection := &openai.RefreshTokenError{Permanent: true, Code: "refresh_token_reused"}
			api.recordOpenAIRefreshFailure(a, rejection)
			before := api.openAIRejectionForAccount(a)
			if tc.change != nil {
				tc.change(r.account)
			}
			require.Equal(t, tc.want, api.verifyOpenAIAccessAfterTest(context.Background(), a, before, "Bearer "+a.GetOpenAIAccessToken()))
			require.Same(t, rejection, api.openAIRefreshFailure(a))
			require.Zero(t, ex.calls.Load())
			if tc.want {
				tok, err := p.GetAccessToken(context.Background(), a)
				require.NoError(t, err)
				require.Equal(t, a.GetOpenAIAccessToken(), tok)
			}
		})
	}
}

func TestKitty36VerifiedAccessDoesNotClearCooldowns(t *testing.T) {
	a, r, _, p := newOpenAIRecoveryFixture()
	r.account.Status = StatusError
	failure := &openai.RefreshTokenError{Permanent: true, Code: "refresh_token_reused"}
	r.account.ErrorMessage = openAIRefreshErrorPrefix + failure.Error()
	future := time.Now().Add(time.Hour)
	r.account.TempUnschedulableUntil = &future
	r.account.RateLimitResetAt = &future
	repo := &kitty36AccessRepo{openAIRecoveryRepo: r}
	p.accountRepo = repo
	p.refreshAPI.accountRepo = repo
	p.refreshAPI.recordOpenAIRefreshFailure(a, failure)
	require.True(t, p.refreshAPI.verifyOpenAIAccessAfterTest(context.Background(), a, p.refreshAPI.openAIRejectionForAccount(a), "Bearer "+a.GetOpenAIAccessToken()))
	require.Equal(t, 1, repo.clears)
	require.False(t, r.account.IsSchedulable())
	require.Equal(t, &future, r.account.TempUnschedulableUntil)
	require.Equal(t, &future, r.account.RateLimitResetAt)
}

func TestKitty36New401AndExpiryInvalidateProof(t *testing.T) {
	a, repo, _, p := newOpenAIRecoveryFixture()
	api := p.refreshAPI
	fail := &openai.RefreshTokenError{Permanent: true, Code: "refresh_token_expired"}
	api.recordOpenAIRefreshFailure(a, fail)
	before := api.openAIRejectionForAccount(a)
	require.True(t, api.verifyOpenAIAccessAfterTest(context.Background(), a, before, "Bearer "+a.GetOpenAIAccessToken()))
	proof := api.openAIRejectionForAccount(a)
	require.False(t, proof.accessVerifiedAt(a, proof.verifiedAccessUntil))
	api.rejectVerifiedOpenAIAccess(context.Background(), a, "Bearer "+a.GetOpenAIAccessToken())
	api.recordOpenAIRefreshFailure(a, fail)
	require.False(t, api.verifyOpenAIAccessAfterTest(context.Background(), a, before, "Bearer "+a.GetOpenAIAccessToken()))
	require.False(t, api.verifyOpenAIAccessAfterTest(context.Background(), a, api.openAIRejectionForAccount(a), "Bearer "+a.GetOpenAIAccessToken()))
	repo.account.Credentials["id_token"] = "changed"
	_, err := p.GetAccessToken(context.Background(), a)
	require.Error(t, err)
}

func TestKitty36CompleteTextAndFinalRequestProof(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		mutate     func(*http.Request)
		reject     bool
		want       bool
	}{
		{"complete text", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"42\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", nil, false, true},
		{"scheduled", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"42\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", nil, false, false},
		{"unterminated SSE event", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"42\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n", nil, false, false},
		{"bare terminal", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", nil, false, false},
		{"EOF after text", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"42\"}\n\n", nil, false, false},
		{"wrong bearer", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"42\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", func(r *http.Request) { r.Header.Set("Authorization", "Bearer other") }, false, false},
		{"redirected", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"42\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", func(r *http.Request) { r.URL.Path = "/other" }, false, false},
		{"newer 401", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"42\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", nil, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, r, _, p := newOpenAIRecoveryFixture()
			api := p.refreshAPI
			api.recordOpenAIRefreshFailure(a, &openai.RefreshTokenError{Permanent: true, Code: "refresh_token_reused"})
			up := &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
				resp := newJSONResponse(200, tc.body)
				resp.Request = req.Clone(req.Context())
				if tc.mutate != nil {
					tc.mutate(resp.Request)
				}
				if tc.reject {
					api.rejectVerifiedOpenAIAccess(context.Background(), a, "Bearer "+a.GetOpenAIAccessToken())
				}
				return resp, nil
			}}
			svc := &AccountTestService{accountRepo: r, httpUpstream: up, openaiGatewayService: &OpenAIGatewayService{openAITokenProvider: p}}
			ctx, _ := newTestContext()
			if tc.name == "scheduled" {
				_ = svc.TestAccountConnection(ctx, a.ID, "gpt-6.2-sol", "", AccountTestModeDefault)
			} else {
				_ = svc.TestAccountConnectionForAdmin(ctx, a.ID, "gpt-6.2-sol", "", AccountTestModeDefault)
			}
			require.True(t, AccountTestOwnsOpenAIAccessRecovery(ctx))
			require.Equal(t, tc.want, api.openAIRejectionForAccount(a).accessVerifiedAt(a, time.Now()))
		})
	}
	for _, bad := range []string{`{"response":{"status":"failed"}}`, `{"response":{"status":"completed","output":[]}}`, `{"response":{"status":"completed","output":[{"type":"function_call"}]}}`, `{"response":{"status":"completed","error":{}}}`} {
		var v map[string]any
		require.NoError(t, json.Unmarshal([]byte(bad), &v))
		require.False(t, openAICompletedTextProof(v, strings.Repeat("a", 2)))
	}
}

func TestKitty36ConcurrentDenialFencesPersistedProof(t *testing.T) {
	ctx := context.Background()
	a, original, _, p := newOpenAIRecoveryFixture()
	repo := &kitty36AccessRepo{openAIRecoveryRepo: original}
	p.refreshAPI.accountRepo = repo
	p.accountRepo = repo
	cause := &openai.RefreshTokenError{Permanent: true, Code: "refresh_token_reused"}
	repo.account.Status = StatusError
	repo.account.ErrorMessage = openAIRefreshErrorPrefix + cause.Error()
	api := p.refreshAPI
	api.recordOpenAIRefreshFailure(a, cause)
	before := api.openAIRejectionForAccount(a)
	denied := make(chan struct{})
	repo.beforeCAS = func() {
		go func() {
			api.rejectVerifiedOpenAIAccess(ctx, a, "Bearer "+a.GetOpenAIAccessToken())
			close(denied)
		}()
		require.Eventually(t, func() bool {
			return api.openAIRejectionForAccount(a).accessRejected
		}, time.Second, time.Millisecond)
	}
	require.False(t, api.verifyOpenAIAccessAfterTest(ctx, a, before, "Bearer "+a.GetOpenAIAccessToken()))
	select {
	case <-denied:
	case <-time.After(3 * time.Second):
		t.Fatal("denial did not finish")
	}
	latest, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, StatusError, latest.Status)
	require.Equal(t, openAIVerifiedAccessRejectedMessage, latest.ErrorMessage)
	require.Equal(t, 1, repo.rejects)
	_, err = p.GetAccessToken(ctx, a)
	require.Error(t, err)
}
