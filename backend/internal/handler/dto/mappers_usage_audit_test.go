//go:build unit

package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageLogFromServiceAdmin_UsageAuditJSONRoundTrip(t *testing.T) {
	t.Parallel()
	zero := 0
	audit := &service.UsageAudit{
		Version: service.UsageAuditVersion, Status: service.UsageAuditStatusUnverifiable,
		Confidence: service.UsageAuditConfidenceLow, EvidenceSource: service.UsageAuditEvidenceNormalized,
		Protocol: "gemini_generate_content", Reasons: []string{"upstream_usage_missing", "tool_call_without_usage"},
		Upstream:         service.UsageAuditTokenSnapshot{CacheReadTokens: &zero},
		GeminiUsage:      &service.GeminiUsageEvidence{Frames: 2, ToolCallEvents: 1, CacheReadTokens: &zero},
		UpstreamRawUsage: json.RawMessage(`{"cachedContentTokenCount":0}`),
	}
	for _, evidence := range []*service.UsageAudit{nil, audit} {
		log := &service.UsageLog{ID: 42, Model: "gemini-test", UsageAudit: evidence}
		mapped := UsageLogFromServiceAdmin(log)
		require.Same(t, evidence, mapped.UsageAudit)
		payload, err := json.Marshal(mapped)
		require.NoError(t, err)
		var object map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(payload, &object))
		if evidence == nil {
			require.NotContains(t, object, "usage_audit", "historical rows must not acquire synthetic evidence")
		} else {
			var decoded service.UsageAudit
			require.NoError(t, json.Unmarshal(object["usage_audit"], &decoded))
			require.Equal(t, *evidence, decoded)
			require.Contains(t, string(object["usage_audit"]), `"reasons":["upstream_usage_missing","tool_call_without_usage"]`)
			require.Contains(t, string(object["usage_audit"]), `"cache_read_tokens":0`, "explicit zero is evidence, not an absent counter")
		}
		var roundTrip AdminUsageLog
		require.NoError(t, json.Unmarshal(payload, &roundTrip))
		require.Equal(t, evidence, roundTrip.UsageAudit)
		require.EqualValues(t, 42, roundTrip.ID)
	}
	require.Nil(t, UsageLogFromServiceAdmin(nil))
}

func TestUsageLogFromService_UsageAuditNotVisibleToNonAdmin(t *testing.T) {
	t.Parallel()
	log := &service.UsageLog{
		ID: 42, Model: "gemini-test",
		UsageAudit: &service.UsageAudit{
			Status: service.UsageAuditStatusAnomaly, Reasons: []string{"admin-only-audit-reason"},
			GeminiUsage:      &service.GeminiUsageEvidence{Frames: 99},
			UpstreamRawUsage: json.RawMessage(`{"admin-only-raw-usage":123}`),
		},
	}
	payload, err := json.Marshal(UsageLogFromService(log))
	require.NoError(t, err)
	var object map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload, &object))
	require.NotContains(t, object, "usage_audit")
	for _, hidden := range []string{"admin-only-audit-reason", "gemini_usage", "admin-only-raw-usage", "upstream_raw_usage"} {
		require.NotContains(t, string(payload), hidden)
	}
	require.JSONEq(t, `42`, string(object["id"]))
	require.Equal(t, []string{"admin-only-audit-reason"}, log.UsageAudit.Reasons, "projection must not mutate the service record")
	require.Nil(t, UsageLogFromService(nil))
}
