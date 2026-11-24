package service

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestKitty36ChannelUltrafastIndependentAndExplicitZero(t *testing.T) {
	s := NewBillingService(nil, nil)
	zero, ultra, fast := 0.0, 4.25, 1.75
	channel := &ChannelModelPricing{InputPrice: &zero, OutputPrice: &zero, CacheWritePrice: &zero, CacheReadPrice: &zero, UltrafastMultiplier: &ultra, FastMultiplier: &fast}
	p, err := s.GetModelPricingWithChannel("gpt-6.2-sol", channel)
	require.NoError(t, err)
	require.Equal(t, ultra, configuredServiceTierMultiplier("ultrafast", p))
	require.Equal(t, fast, configuredServiceTierMultiplier("fast", p))
	cost := s.computeTokenBreakdown(p, UsageTokens{InputTokens: 20, OutputTokens: 30, CacheReadTokens: 40, CacheCreationTokens: 10, CacheCreation12hTokens: 10}, 1, "ultrafast", false)
	require.Zero(t, cost.TotalCost)
	base, err := s.GetModelPricing("gpt-6.2-sol")
	require.NoError(t, err)
	require.InDelta(t, 2e-6, base.InputPricePerToken, 1e-15)
	// A delayed settlement must still honor an explicit operator tier override.
	p = s.applyModelSpecificPricingPolicyEx("gpt-6.2-sol", p, false, time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC))
	require.Equal(t, ultra, configuredServiceTierMultiplier("ultrafast", p))
	resolved := &ResolvedPricing{BasePricing: p, Source: PricingSourceChannel}
	b, err := s.calculateTokenCost(resolved, CostInput{Model: "gpt-6.2-sol", Tokens: UsageTokens{InputTokens: 100}, ServiceTier: "ultrafast", RateMultiplier: 1, Resolver: &ModelPricingResolver{}})
	require.NoError(t, err)
	require.Zero(t, b.TotalCost)
	for _, invalid := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		require.Error(t, checkPricesNotNegative(ChannelModelPricing{UltrafastMultiplier: &invalid}))
	}
}
