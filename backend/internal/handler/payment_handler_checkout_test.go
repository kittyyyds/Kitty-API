//go:build unit

package handler

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCheckoutInfoResponseIncludesRechargeBonusFields(t *testing.T) {
	resp := checkoutInfoResponse{
		Methods:              map[string]service.MethodLimits{},
		Plans:                []checkoutPlan{},
		RechargeBonusTiers:   []service.RechargeBonusTier{},
		RechargeBonusRules:   []service.RechargeBonusRule{},
		RechargeBonusSummary: service.RechargeBonusSummary{},
	}
	raw, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	rules, ok := decoded["recharge_bonus_rules"].([]any)
	require.True(t, ok, "recharge_bonus_rules must marshal as JSON array")
	require.Empty(t, rules)
	summary, ok := decoded["recharge_bonus_summary"].(map[string]any)
	require.True(t, ok, "recharge_bonus_summary must marshal as JSON object")
	require.Equal(t, float64(0), summary["available_amount"])
	require.Equal(t, float64(0), summary["active_grants"])
}
