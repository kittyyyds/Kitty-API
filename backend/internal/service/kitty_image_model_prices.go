package service

import (
	"math"
	"strings"
)

// CanonicalImagePriceModel normalizes model IDs for per-model image price overrides.
func CanonicalImagePriceModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return ""
	}
	if idx := strings.LastIndex(m, "/"); idx >= 0 && idx+1 < len(m) {
		m = m[idx+1:]
	}
	return m
}

// NormalizeImageModelPrices validates and canonicalizes per-model/resolution image price overrides.
func NormalizeImageModelPrices(raw map[string]map[string]float64) map[string]map[string]float64 {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]map[string]float64, len(raw))
	for model, tiers := range raw {
		cm := CanonicalImagePriceModel(model)
		if cm == "" || len(tiers) == 0 {
			continue
		}
		cleanTiers := make(map[string]float64, len(tiers))
		for res, price := range tiers {
			r := strings.ToLower(strings.TrimSpace(res))
			if r == "" || math.IsNaN(price) || math.IsInf(price, 0) || price < 0 {
				continue
			}
			cleanTiers[r] = price
		}
		if len(cleanTiers) > 0 {
			out[cm] = cleanTiers
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// LookupImageModelPrice resolves a per-model + resolution override price if configured.
func LookupImageModelPrice(prices map[string]map[string]float64, model, resolution string) *float64 {
	if len(prices) == 0 {
		return nil
	}
	cm := CanonicalImagePriceModel(model)
	res := strings.ToLower(strings.TrimSpace(resolution))
	if res == "" {
		res = "1k"
	}
	for k, tiers := range prices {
		if CanonicalImagePriceModel(k) != cm {
			continue
		}
		for tr, p := range tiers {
			if strings.ToLower(strings.TrimSpace(tr)) == res && !math.IsNaN(p) && !math.IsInf(p, 0) && p >= 0 {
				v := p
				return &v
			}
		}
		if p, ok := tiers["default"]; ok && !math.IsNaN(p) && !math.IsInf(p, 0) && p >= 0 {
			v := p
			return &v
		}
	}
	return nil
}

// GetImagePriceForModel returns group's per-model image price override if present, falling back to tier columns.
func (g *Group) GetImagePriceForModel(model, resolution string) *float64 {
	if g == nil {
		return nil
	}
	if p := LookupImageModelPrice(g.ImageModelPrices, model, resolution); p != nil {
		return p
	}
	return g.GetImagePrice(resolution)
}
