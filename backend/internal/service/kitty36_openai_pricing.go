package service

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

// Keep these fallback cards independent from older models and from each other.
// Explicit operator/catalog prices continue to take precedence.
func (s *BillingService) initGPT62FallbackPricing() {
	for model, input := range map[string]float64{"gpt-6.2-sol": 2e-6, "gpt-6.2-astra": 8e-6} {
		longWrite := input * 2.5
		s.fallbackPrices[model] = pricingWithPriorityMultiplier(&ModelPricing{
			InputPricePerToken: input, OutputPricePerToken: input * 5,
			CacheReadPricePerToken: input * .05, CacheCreationPricePerToken: input * 1.25,
			CacheCreationPriceExplicit: true, CacheCreation12hPricePerToken: &longWrite,
			LongContextInputThreshold: 272_000, LongContextInputMultiplier: 2,
			LongContextOutputMultiplier: 1.5,
		}, 2)
	}
}

func openAIUltrafastMultiplierAt(model string, at time.Time) float64 {
	if at.IsZero() {
		at = timezone.Now()
	}
	// Pacific midnight is still UTC-8 on March 1, 2027. Compare instants,
	// not server-local calendar dates, including during delayed settlement.
	if openai.IsGPT62ModelSpelling(model) && at.Before(time.Date(2027, time.March, 1, 8, 0, 0, 0, time.UTC)) {
		return 3
	}
	return 6
}
