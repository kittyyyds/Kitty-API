package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

const (
	SettingKeyRegistrationEmailBlacklist = "registration_email_blacklist"
	SettingKeySupportTicketEnabled       = "support_ticket_enabled"
	accountTestBackgroundKey             = "account_test_background"
)

type accountTestReasoningEffortContextKey struct{}

func withAccountTestReasoningEffort(ctx context.Context, effort string) context.Context {
	return context.WithValue(ctx, accountTestReasoningEffortContextKey{}, strings.ToLower(strings.TrimSpace(effort)))
}

func parseTestSSEOutput(body string) (responseText, errMsg string) {
	var texts []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		jsonStr := strings.TrimPrefix(line, "data: ")
		var event TestEvent
		if err := json.Unmarshal([]byte(jsonStr), &event); err != nil {
			continue
		}
		switch event.Type {
		case "content":
			if event.Text != "" {
				texts = append(texts, event.Text)
			}
		case "error":
			errMsg = event.Error
		}
	}
	return strings.Join(texts, ""), errMsg
}

type ScheduledTestResult struct {
	ID            int64     `json:"id"`
	Status        string    `json:"status"`
	ModelID       string    `json:"model_id"`
	UpstreamModel string    `json:"upstream_model,omitempty"`
	ResponseText  string    `json:"response_text,omitempty"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	LatencyMs     int64     `json:"latency_ms"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	FinishedAt    time.Time `json:"finished_at,omitempty"`
}

func startBackgroundService(cfg *config.Config, job interface{ Start() }) {
	if job == nil {
		return
	}
	if cfg == nil || cfg.RunMode == "" || cfg.RunMode == config.RunModeStandard {
		job.Start()
	}
}

func IsAdminRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case RoleAdmin, "super_admin", "superadmin":
		return true
	default:
		return false
	}
}
