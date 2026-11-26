//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAccountIntelligenceReasoningCompatibility(t *testing.T) {
	for _, effort := range []string{"", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"} {
		t.Run(effort, func(t *testing.T) {
			a := &Account{Platform: PlatformOpenAI}
			in := AccountIntelligenceInput{Model: "gpt-6.1-sol", ReasoningEffort: effort}
			normalizeAccountIntelligenceReasoning(a, &in)
			want := effort
			if effort == "minimal" {
				want = "low"
				require.Equal(t, "minimal", in.RequestedReasoningEffort)
			} else {
				require.Empty(t, in.RequestedReasoningEffort)
			}
			require.Equal(t, want, in.ReasoningEffort)
			require.Equal(t, "gpt-6.1-sol", in.Model)
		})
	}
	t.Run("mapped alias", func(t *testing.T) {
		a := &Account{Platform: PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"saved-alias": "gpt-6.1-sol"}}}
		in := AccountIntelligenceInput{Model: "saved-alias", ReasoningEffort: "minimal"}
		normalizeAccountIntelligenceReasoning(a, &in)
		require.Equal(t, "low", in.ReasoningEffort)
		require.Equal(t, "saved-alias", in.Model)
	})
	for _, model := range []string{"gpt-6-astra", "unknown-model"} {
		in := AccountIntelligenceInput{Model: model, ReasoningEffort: "minimal"}
		normalizeAccountIntelligenceReasoning(&Account{Platform: PlatformOpenAI}, &in)
		require.Equal(t, "minimal", in.ReasoningEffort)
	}
	in := AccountIntelligenceInput{Model: "gpt-6.1-sol", ReasoningEffort: "minimal"}
	normalizeAccountIntelligenceReasoning(&Account{Platform: PlatformAnthropic}, &in)
	require.Equal(t, "minimal", in.ReasoningEffort)
}

func TestAccountIntelligenceResultNeverScoresErrors(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		result     *ScheduledTestResult
		err        error
		status     string
	}{
		{"correct", "candy", &ScheduledTestResult{Status: "success", ResponseText: "21"}, nil, "correct"},
		{"incorrect", "candy", &ScheduledTestResult{Status: "success", ResponseText: "29"}, nil, "incorrect"},
		{"ambiguous", "candy", &ScheduledTestResult{Status: "success", ResponseText: "21 or 29"}, nil, "incorrect"},
		{"custom unscored", "custom", &ScheduledTestResult{Status: "success", ResponseText: "21"}, nil, "completed"},
		{"failed with correct partial", "candy", &ScheduledTestResult{Status: "failed", ResponseText: "21", ErrorMessage: "upstream failed"}, nil, "error"},
		{"success and error", "candy", &ScheduledTestResult{Status: "success", ResponseText: "21", ErrorMessage: "stream interrupted"}, nil, "error"},
		{"execution error keeps evidence", "candy", &ScheduledTestResult{Status: "success", ResponseText: "21", LatencyMs: 123, UpstreamModel: "actual-model"}, errors.New("deadline exceeded"), "error"},
		{"empty", "candy", &ScheduledTestResult{Status: "success", ResponseText: " \n"}, nil, "error"},
		{"missing", "candy", nil, nil, "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := newAccountIntelligenceTest(1, AccountIntelligenceInput{Kind: tc.kind})
			finishAccountIntelligenceResult(item, tc.result, tc.err)
			require.Equal(t, tc.status, item.Status)
			if tc.result != nil {
				require.Equal(t, tc.result.ResponseText, item.Response)
				require.Equal(t, tc.result.UpstreamModel, item.UpstreamModel)
				require.Equal(t, tc.result.LatencyMs, item.LatencyMs)
			}
			if tc.status == "error" {
				require.NotEmpty(t, item.Error)
			}
		})
	}
}

type accountIntelligenceRegressionRepo struct {
	openAIAccountTestRepo
	finished chan AccountIntelligenceTest
	plan     *AccountIntelligenceSchedule
}

func (r *accountIntelligenceRegressionRepo) IntelligenceFind(context.Context, int64, string) (*AccountIntelligenceTest, error) {
	return nil, nil
}
func (r *accountIntelligenceRegressionRepo) IntelligenceClaim(_ context.Context, item *AccountIntelligenceTest) (*AccountIntelligenceTest, bool, error) {
	saved := *item
	return &saved, true, nil
}
func (r *accountIntelligenceRegressionRepo) IntelligenceFinish(_ context.Context, item *AccountIntelligenceTest) error {
	r.finished <- *item
	return nil
}
func (r *accountIntelligenceRegressionRepo) IntelligenceHistory(context.Context, []int64, int, int) ([]AccountIntelligenceTest, error) {
	return nil, nil
}
func (r *accountIntelligenceRegressionRepo) IntelligenceScheduleGet(context.Context, int64) (*AccountIntelligenceSchedule, error) {
	return r.plan, nil
}
func (r *accountIntelligenceRegressionRepo) IntelligenceScheduleSave(_ context.Context, plan *AccountIntelligenceSchedule, _ int64) (bool, error) {
	copy := *plan
	r.plan = &copy
	return true, nil
}
func (r *accountIntelligenceRegressionRepo) IntelligenceSchedules(context.Context) ([]AccountIntelligenceSchedule, error) {
	return nil, nil
}
func (r *accountIntelligenceRegressionRepo) IntelligenceScheduleClaim(context.Context, *AccountIntelligenceSchedule, time.Time, time.Time) (bool, error) {
	return false, nil
}
func (r *accountIntelligenceRegressionRepo) IntelligenceScheduleError(context.Context, *AccountIntelligenceSchedule, time.Time, string) error {
	return nil
}
func (r *accountIntelligenceRegressionRepo) IntelligencePruneScheduled(context.Context, int64, int) error {
	return nil
}

func accountIntelligenceRegressionService(responses ...*http.Response) (*AccountTestService, *accountIntelligenceRegressionRepo, *queuedHTTPUpstream) {
	account := &Account{ID: 27, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1, Credentials: map[string]any{"access_token": "test-token"}}
	repo := &accountIntelligenceRegressionRepo{finished: make(chan AccountIntelligenceTest, 2)}
	repo.accountsByID = map[int64]*Account{27: account}
	upstream := &queuedHTTPUpstream{responses: responses}
	return &AccountTestService{accountRepo: repo, httpUpstream: upstream}, repo, upstream
}
func accountIntelligenceRegressionInput() AccountIntelligenceInput {
	return AccountIntelligenceInput{RequestID: uuid.NewString(), Kind: "candy", Model: "gpt-6.1-sol", ReasoningEffort: "minimal", Prompt: "stale saved prompt must not be used"}
}

const accountIntelligenceRegressionAnswer = `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"21"}]}]}}`

func TestAccountIntelligenceManualAndScheduledCompatibilityParity(t *testing.T) {
	svc, repo, upstream := accountIntelligenceRegressionService(newJSONResponse(200, accountIntelligenceRegressionAnswer), newJSONResponse(200, accountIntelligenceRegressionAnswer))
	_, err := svc.StartAccountIntelligence(context.Background(), 27, accountIntelligenceRegressionInput())
	require.NoError(t, err)
	var manual AccountIntelligenceTest
	select {
	case manual = <-repo.finished:
	case <-time.After(5 * time.Second):
		t.Fatal("manual run did not finish")
	}
	scheduled, err := svc.runScheduledAccountIntelligence(context.Background(), 27, accountIntelligenceRegressionInput())
	require.NoError(t, err)
	require.Equal(t, "manual", manual.Source)
	require.Equal(t, "scheduled", scheduled.Source)
	for _, item := range []*AccountIntelligenceTest{&manual, scheduled} {
		require.Equal(t, "correct", item.Status)
		require.Equal(t, "21", item.Response)
		require.Equal(t, "21", item.ExpectedAnswer)
		require.Equal(t, "low", item.ReasoningEffort)
		require.Equal(t, "minimal", item.RequestedReasoningEffort)
		require.Equal(t, "gpt-6.1-sol", item.Model)
	}
	require.Len(t, upstream.requests, 2, "do not retry or pick another model to obtain a correct answer")
	manualPayload := decodeCodexProbeTestBody(t, upstream.requests[0])
	scheduledPayload := decodeCodexProbeTestBody(t, upstream.requests[1])
	// Each Codex call intentionally gets fresh session/turn/cache identifiers.
	// Compare the task-bearing fields, not that per-request identity metadata.
	for _, field := range []string{"model", "input", "reasoning"} {
		require.Equal(t, gjson.GetBytes(manualPayload, field).Raw, gjson.GetBytes(scheduledPayload, field).Raw, field)
	}
	require.Equal(t, "low", gjson.GetBytes(manualPayload, "reasoning.effort").String())
	require.Equal(t, "gpt-6.1-sol", gjson.GetBytes(manualPayload, "model").String())
	prompt := gjson.GetBytes(manualPayload, `input.#(role=="user").content.0.text`).String()
	require.Equal(t, CandyPrompt+"\n只输出最终整数，不要解释。", prompt)
	require.NotContains(t, prompt, "stale saved prompt")
}

func TestAccountIntelligenceSaveScheduleUsesSameCompatibility(t *testing.T) {
	svc, repo, _ := accountIntelligenceRegressionService()
	for _, effort := range []string{"minimal", "", "low", "medium", "high", "xhigh", "max"} {
		t.Run(effort, func(t *testing.T) {
			plan, err := svc.SaveIntelligenceSchedule(context.Background(), 27, AccountIntelligenceSchedule{Kind: "candy", Model: "gpt-6.1-sol", ReasoningEffort: effort, Enabled: true, CronExpression: "*/2 * * * *", MaxResults: 100})
			require.NoError(t, err)
			want := effort
			if effort == "minimal" {
				want = "low"
			}
			require.Equal(t, want, plan.ReasoningEffort)
			require.Equal(t, want, repo.plan.ReasoningEffort)
			require.Equal(t, "gpt-6.1-sol", plan.Model)
		})
	}
}

func TestAccountIntelligenceProductionManualDefaultUnchanged(t *testing.T) {
	svc, _, upstream := accountIntelligenceRegressionService(newJSONResponse(200, accountIntelligenceRegressionAnswer), newJSONResponse(200, accountIntelligenceRegressionAnswer))
	in := accountIntelligenceRegressionInput()
	in.Model = "gpt-6-astra"
	in.ReasoningEffort = ""
	item, err := svc.runScheduledAccountIntelligence(context.Background(), 27, in)
	require.NoError(t, err)
	require.Equal(t, "correct", item.Status)
	require.Empty(t, item.ReasoningEffort)
	require.Empty(t, item.RequestedReasoningEffort)
	body := decodeCodexProbeTestBody(t, upstream.requests[0])
	// The shared Codex adapter supplies its own default. The IQ fix must
	// leave the empty user choice alone, rather than force another effort.
	_, err = svc.RunTestBackgroundWithPromptAndReasoning(context.Background(), 27, in.Model, item.Prompt, "")
	require.NoError(t, err)
	ordinaryBody := decodeCodexProbeTestBody(t, upstream.requests[1])
	require.Equal(t, gjson.GetBytes(ordinaryBody, "reasoning").Raw, gjson.GetBytes(body, "reasoning").Raw)
}

func TestAccountIntelligenceScheduledRealFailureIsReturnedWithoutRetry(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			svc, repo, upstream := accountIntelligenceRegressionService(newJSONResponse(status, `{"error":{"message":"real upstream failure"}}`))
			item, err := svc.runScheduledAccountIntelligence(context.Background(), 27, accountIntelligenceRegressionInput())
			require.Error(t, err)
			require.Equal(t, "error", item.Status)
			require.Contains(t, err.Error(), "real upstream failure")
			require.Equal(t, item.Error, err.Error(), "scheduler must persist the actual last_error")
			require.Equal(t, item.ID, (<-repo.finished).ID)
			require.Empty(t, item.Response)
			require.Len(t, upstream.requests, 1)
		})
	}
}
