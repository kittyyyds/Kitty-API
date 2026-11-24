package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestKitty36PricingGPT62CardsAndRequestTime(t *testing.T) {
	s := NewBillingService(nil, nil)
	cutoff := time.Date(2027, time.March, 1, 8, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		model               string
		input, output, read float64
	}{
		{"gpt-6.2-sol", 2e-6, 10e-6, .1e-6},
		{"gpt-6.2-astra", 8e-6, 40e-6, .4e-6},
	} {
		t.Run(tc.model, func(t *testing.T) {
			for _, at := range []time.Time{cutoff.Add(-time.Nanosecond), cutoff, cutoff.In(time.FixedZone("UTC+8", 8*3600))} {
				p, err := s.getModelPricingAt(tc.model, at)
				require.NoError(t, err)
				require.InDelta(t, tc.input, p.InputPricePerToken, 1e-15)
				require.InDelta(t, tc.output, p.OutputPricePerToken, 1e-15)
				require.InDelta(t, tc.read, p.CacheReadPricePerToken, 1e-15)
				require.InDelta(t, tc.input*1.25, p.CacheCreationPricePerToken, 1e-15)
				require.NotNil(t, p.CacheCreation12hPricePerToken)
				require.InDelta(t, tc.input*2.5, *p.CacheCreation12hPricePerToken, 1e-15)
				want := 6.0
				if at.Before(cutoff) {
					want = 3
				}
				require.Equal(t, want, configuredServiceTierMultiplier("ultrafast", p))
				cost, err := s.CalculateCostUnified(CostInput{Model: tc.model, PricingAt: at, Tokens: UsageTokens{InputTokens: 1000}, ServiceTier: "ultrafast", RateMultiplier: 1})
				require.NoError(t, err)
				require.InDelta(t, tc.input*1000*want, cost.TotalCost, 1e-12)
			}
			for _, alias := range []string{tc.model, "openai/" + tc.model, tc.model + "-max"} {
				p, err := s.GetModelPricing(alias)
				require.NoError(t, err, alias)
				require.InDelta(t, tc.input, p.InputPricePerToken, 1e-15)
			}
			for _, n := range []int{272000, 272001} {
				p, err := s.getModelPricingAt(tc.model, cutoff)
				require.NoError(t, err)
				cost := s.computeTokenBreakdown(p, UsageTokens{InputTokens: n, OutputTokens: 10}, 1, "fast", true)
				in, out := 2.0, 2.0
				if n > 272000 {
					in *= 2
					out *= 1.5
				}
				require.InDelta(t, float64(n)*tc.input*in, cost.InputCost, 1e-12)
				require.InDelta(t, 10*tc.output*out, cost.OutputCost, 1e-12)
			}
		})
	}
	for _, unknown := range []string{"gpt-6.2", "gpt-6.2-astra-law", "gpt-6.2-solitude"} {
		_, err := s.GetModelPricing(unknown)
		require.ErrorIs(t, err, ErrModelPricingUnavailable, unknown)
	}
}

func TestKitty36CacheUnattributedWritesAreBilledOnce(t *testing.T) {
	s := &BillingService{}
	p := &ModelPricing{SupportsCacheBreakdown: true, CacheCreation5mPrice: 2, CacheCreation1hPrice: 4}
	for _, tc := range []struct {
		name               string
		total, short, long int
		want               float64
	}{
		{"remainder", 100, 20, 30, 260},
		{"no_details", 100, 0, 0, 200},
		{"contradictory_details", 100, 100, 100, 300},
		{"negative_details", 100, -5, 20, 240},
		{"only_details", 0, 20, 30, 160},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cost := s.computeCacheCreationCost(p, UsageTokens{CacheCreationTokens: tc.total, CacheCreation5mTokens: tc.short, CacheCreation1hTokens: tc.long}, 2, 1)
			require.InDelta(t, tc.want, cost, 1e-12)
		})
	}
}
