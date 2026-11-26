import { describe, expect, it } from 'vitest'
import type { UsageAudit } from '@/types'
import {
  getUsageReportingState,
  getUsageTokenCount,
  getUsageTotalTokens,
  isUsageCacheRateUnverifiable,
  type UsageReportingRow,
} from '../usageReporting'

const audit = (reasons: string[], upstream: UsageAudit['upstream'] = {}): UsageAudit => ({
  version: 1, protocol: 'gemini_generate_content', status: 'pass', confidence: 'high',
  evidence_source: 'upstream_raw', upstream, local: {}, delta: {}, deviation_pct: {},
  reasons, capture_truncated: false,
})
const counts = { input_tokens: 200, output_tokens: 10, cache_read_tokens: 800, cache_creation_tokens: 0 }

describe('usage reporting evidence', () => {
  it('treats missing usage as unknown even when normalized counters contain zeros', () => {
    const row = { ...counts, usage_audit: audit(['gemini_usage_missing']) }
    for (const field of ['input_tokens', 'output_tokens', 'cache_read_tokens'] as const) {
      expect(getUsageTokenCount(row, field)).toBeNull()
    }
    expect(getUsageTotalTokens(row)).toBeNull()
    expect(isUsageCacheRateUnverifiable(row)).toBe(true)
  })

  it('preserves explicit zero as known zero without labeling it missing', () => {
    const row = { input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_creation_tokens: 0,
      usage_audit: audit(['gemini_usage_explicit_zero'], { input_tokens: 0, total_input_tokens: 0, output_tokens: 0 }) }
    expect(getUsageReportingState(row)).toMatchObject({ missing: false, partial: false, explicitZero: true })
    expect(getUsageTokenCount(row, 'input_tokens')).toBe(0)
    expect(getUsageTokenCount(row, 'output_tokens')).toBe(0)
    expect(getUsageTotalTokens(row)).toBe(0)
  })

  it('does not fill omitted partial fields from normalized defaults or local estimates', () => {
    const row = { ...counts, input_tokens: 100, output_tokens: 0, cache_read_tokens: 0,
      usage_audit: { ...audit(['gemini_usage_partial'], { input_tokens: 100 }), local: counts } }
    expect(getUsageTokenCount(row, 'input_tokens')).toBe(100)
    expect(getUsageTokenCount(row, 'output_tokens')).toBeNull()
    expect(getUsageTokenCount(row, 'cache_read_tokens')).toBeNull()
    expect(getUsageTotalTokens(row)).toBeNull()
    expect(isUsageCacheRateUnverifiable(row)).toBe(true)
  })

  it('keeps a reported zero in a partial snapshot', () => {
    const row = { ...counts, output_tokens: 0,
      usage_audit: audit(['gemini_usage_partial'], { output_tokens: 0 }) }
    expect(getUsageTokenCount(row, 'output_tokens')).toBe(0)
  })

  it('allows a cache rate when only output is missing', () => {
    const prompt = { input_tokens: 200, cache_read_tokens: 800 }
    const row = { ...counts, usage_audit: audit(['gemini_usage_partial'], prompt) }
    expect(isUsageCacheRateUnverifiable(row)).toBe(false)
    expect(getUsageTokenCount(row, 'output_tokens')).toBeNull()
  })

  it('does not infer normalized input solely from a total-input snapshot', () => {
    const row = { ...counts, usage_audit: audit(['gemini_usage_partial'], { total_input_tokens: 1000 }) }
    expect(getUsageTokenCount(row, 'input_tokens')).toBeNull()
  })

  it('preserves normal cache counts and an ignored zero-reset tail without taking maxima', () => {
    const row = { ...counts, usage_audit: audit(['gemini_usage_zero_reset_ignored'], counts) }
    expect(getUsageTokenCount(row, 'cache_read_tokens')).toBe(800)
    expect(getUsageTotalTokens(row)).toBe(1010)
    expect(isUsageCacheRateUnverifiable(row)).toBe(false)
  })

  it('marks a regressed cache rate unverifiable but keeps the persisted zero', () => {
    const row = { ...counts, cache_read_tokens: 0,
      usage_audit: { ...audit(['gemini_cache_counter_regressed'], { cache_read_tokens: 0 }), cache_hit_rate: 99 } }
    expect(getUsageReportingState(row).cacheRegressed).toBe(true)
    expect(getUsageTokenCount(row, 'cache_read_tokens')).toBe(0)
    expect(isUsageCacheRateUnverifiable(row)).toBe(true)
  })

  it.each([null, undefined])('keeps historical rows compatible when audit is %s', (usage_audit) => {
    const row: UsageReportingRow = { ...counts, usage_audit }
    expect(getUsageReportingState(row).missing).toBe(false)
    expect(getUsageTotalTokens(row)).toBe(1010)
    expect(isUsageCacheRateUnverifiable(row)).toBe(false)
    expect(getUsageTokenCount({ usage_audit }, 'input_tokens')).toBe(0)
  })

  it('ignores Gemini reason names on another protocol', () => {
    const row = { ...counts, usage_audit: { ...audit(['gemini_usage_missing']), protocol: 'openai_responses' } }
    expect(getUsageTokenCount(row, 'input_tokens')).toBe(200)
    expect(isUsageCacheRateUnverifiable(row)).toBe(false)
  })

  it.each([undefined, NaN, -1])('does not manufacture a zero from invalid audited counters (%s)', (input_tokens) => {
    const row = { ...counts, input_tokens, usage_audit: audit([]) }
    expect(getUsageTokenCount(row, 'input_tokens')).toBeNull()
    expect(getUsageTotalTokens(row)).toBeNull()
  })
  it('does not turn an omitted cache counter into zero on a fully reported primary snapshot', () => {
    const row = { input_tokens: 100, output_tokens: 2, cache_read_tokens: 0, cache_creation_tokens: 0,
      usage_audit: audit(['gemini_usage_reported'], { input_tokens: 100, total_input_tokens: 100, output_tokens: 2 }) }
    expect(getUsageTokenCount(row, 'cache_read_tokens')).toBeNull()
    expect(isUsageCacheRateUnverifiable(row)).toBe(true)
    expect(getUsageReportingState(row).cacheReadMissing).toBe(true)
    expect(getUsageTokenCount(row, 'cache_creation_tokens')).toBe(0)
    expect(getUsageTotalTokens(row)).toBe(102)
  })

  it('distinguishes an explicit cache zero from omission without requiring a cache-creation pointer', () => {
    const row = { input_tokens: 100, output_tokens: 2, cache_read_tokens: 0,
      usage_audit: audit(['gemini_usage_reported'], { input_tokens: 100, total_input_tokens: 100, output_tokens: 2, cache_read_tokens: 0 }) }
    expect(getUsageTokenCount(row, 'cache_read_tokens')).toBe(0)
    expect(isUsageCacheRateUnverifiable(row)).toBe(false)
    expect(getUsageTotalTokens(row)).toBe(102)
  })

  it('sums known split counters without requiring the inapplicable Gemini cache-creation field', () => {
    const row = { input_tokens: 200, output_tokens: 10, cache_read_tokens: 800,
      usage_audit: audit(['gemini_usage_reported'], { input_tokens: 200, output_tokens: 10, cache_read_tokens: 800 }) }
    expect(getUsageTotalTokens(row)).toBe(1010)
    expect(isUsageCacheRateUnverifiable(row)).toBe(false)
  })

})
