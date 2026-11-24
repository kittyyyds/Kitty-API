//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestKitty36UserCacheDiagnosticsIsScopedBoundedAndEphemeral(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		svc := &OpenAIGatewayService{}
		body := []byte(`{"prompt_cache_key":"private-marker","input":"private-request"}`)
		_, diag := svc.BeginUserCacheDiagnostic(ctx, 31, "mock-gpt", "responses", body)
		require.Nil(t, diag)
		cfg := OpenAICacheDiagnosticConfig{UserID: 31, Model: "mock-gpt", Minutes: 1, MaxSamples: 2}
		require.NoError(t, svc.SetUserCacheDiagnostic(cfg))
		for _, wrong := range []struct {
			id    int64
			model string
		}{{0, "mock-gpt"}, {32, "mock-gpt"}, {31, "other"}} {
			_, rejected := svc.BeginUserCacheDiagnostic(ctx, wrong.id, wrong.model, "responses", body)
			require.Nil(t, rejected)
		}
		observedCtx, observed := svc.BeginUserCacheDiagnostic(ctx, 31, "mock-gpt", "responses", body)
		require.NotNil(t, observed)
		req, err := http.NewRequestWithContext(observedCtx, http.MethodPost, "https://example.invalid/v1/responses", bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer local-test-marker")
		for range 8 {
			observeUserCacheDiagnosticHTTP(req, 5)
		}
		untouched, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.Equal(t, body, untouched)
		observed.ObserveResult(4, nil, errors.New("arbitrary upstream private error"))
		observed.ObserveResult(5, &OpenAIForwardResult{Usage: OpenAIUsage{CacheReadKnown: true, CacheReadInputTokens: 51}}, nil)
		observed.Finish(false)
		observed.Finish(false)
		_, second := svc.BeginUserCacheDiagnostic(ctx, 31, "mock-gpt", "responses", make([]byte, openAICacheDiagnosticMaxBody+1))
		require.NotNil(t, second)
		second.Finish(true)
		_, overBudget := svc.BeginUserCacheDiagnostic(ctx, 31, "mock-gpt", "responses", body)
		require.Nil(t, overBudget)
		status := svc.UserCacheDiagnosticStatus()
		require.False(t, status.Active)
		require.Len(t, status.Samples, 2)
		got := status.Samples[0]
		require.Len(t, got.Egress, openAICacheDiagnosticMaxEgress)
		require.Equal(t, got.Ingress, *got.Egress[0].Body)
		require.Len(t, got.Ingress.Digest, 64)
		require.Equal(t, 2, got.ForwardAttempts)
		require.Equal(t, "forward_completed", got.Outcome)
		require.True(t, got.CacheReadKnown)
		require.Equal(t, 51, got.Usage.CacheReadInputTokens)
		require.Empty(t, status.Samples[1].Ingress.Digest)
		require.Equal(t, "request_canceled", status.Samples[1].Outcome)
		raw, err := json.Marshal(status)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "private-")
		require.NotContains(t, string(raw), "local-test-marker")
		require.NotContains(t, string(raw), "arbitrary upstream")
		time.Sleep(61 * time.Second)
		require.Nil(t, svc.userCacheDiagnostic.current.Load())
		require.Empty(t, svc.UserCacheDiagnosticStatus().Samples)
	})
}

func TestKitty36UserCacheDiagnosticsConcurrentBudgetAndReplacement(t *testing.T) {
	svc := &OpenAIGatewayService{}
	t.Cleanup(svc.StopUserCacheDiagnostic)
	cfg := OpenAICacheDiagnosticConfig{UserID: 7, Model: "test", Minutes: 1, MaxSamples: 5}
	require.NoError(t, svc.SetUserCacheDiagnostic(cfg))
	ctx := context.Background()
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() {
			_, d := svc.BeginUserCacheDiagnostic(ctx, 7, "test", "chat_completions", nil)
			d.Finish(false)
		})
	}
	wg.Wait()
	require.Len(t, svc.UserCacheDiagnosticStatus().Samples, 5)
	require.NoError(t, svc.SetUserCacheDiagnostic(cfg))
	_, d := svc.BeginUserCacheDiagnostic(ctx, 7, "test", "chat_completions", nil)
	require.NotNil(t, d)
	require.NoError(t, svc.SetUserCacheDiagnostic(cfg))
	d.Finish(false)
	require.Empty(t, svc.UserCacheDiagnosticStatus().Samples)
}
