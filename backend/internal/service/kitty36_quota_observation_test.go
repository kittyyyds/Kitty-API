//go:build unit

package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestKitty36QuotaPreservesObservationAndUnknown(t *testing.T) {
	observed := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	for _, value := range []string{`0`, `null`, `99.5`, `"invalid"`} {
		t.Run(value, func(t *testing.T) {
			var usage OpenAIQuotaUsage
			raw := `{"fetched_at":1794996000,"rate_limit":{"primary_window":{"used_percent":` + value + `,"limit_window_seconds":18000,"reset_after_seconds":60}},"additional_rate_limits":[{"metered_feature":"codex_bengalfox","rate_limit":{"primary_window":{"used_percent":` + value + `,"limit_window_seconds":18000,"reset_after_seconds":60}}}]}`
			err := json.Unmarshal([]byte(raw), &usage)
			if value == `"invalid"` {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			usage.FetchedAt = observed.Unix()
			for _, updates := range []map[string]any{buildOpenAIAutoResetUsageUpdates(&usage, observed.Add(2*time.Hour)), buildCodexSparkWindowExtraUpdates(&usage, observed.Add(2*time.Hour))} {
				if value == `null` {
					require.Empty(t, updates)
					continue
				}
				require.Equal(t, observed.Format(time.RFC3339), updates["codex_usage_updated_at"])
				require.Equal(t, observed.Add(time.Minute).Format(time.RFC3339), updates["codex_5h_reset_at"])
				if value == `0` {
					require.Equal(t, float64(0), updates["codex_5h_used_percent"])
				}
			}
		})
	}
	var missing OpenAIQuotaUsage
	require.NoError(t, json.Unmarshal([]byte(`{"rate_limit":{"primary_window":{"limit_window_seconds":18000}},"additional_rate_limits":[{"metered_feature":"codex_bengalfox","rate_limit":{"primary_window":{}}}]}`), &missing))
	require.Empty(t, buildOpenAIAutoResetUsageUpdates(&missing, observed))
	require.Empty(t, buildCodexSparkWindowExtraUpdates(&missing, observed))
}

func TestKitty36QuotaAbsoluteResetDoesNotDrift(t *testing.T) {
	observed := time.Unix(1794996000, 0).UTC()
	usage := &OpenAIQuotaUsage{FetchedAt: observed.Unix(), RateLimit: &OpenAIRateLimit{PrimaryWindow: &OpenAIRateLimitWindow{UsedPercent: 100, LimitWindowSeconds: 18000, ResetAt: observed.Add(90 * time.Second).Unix(), ResetAfterSeconds: 3600}}}
	updates := buildOpenAIAutoResetUsageUpdates(usage, observed.Add(time.Hour))
	require.Equal(t, observed.Add(90*time.Second).Format(time.RFC3339), updates["codex_5h_reset_at"])
}
