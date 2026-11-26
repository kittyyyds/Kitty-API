import type { AdminUsageLog } from '@/types'

export type UsageTokenField = 'input_tokens' | 'output_tokens' | 'cache_read_tokens' | 'cache_creation_tokens'
export type UsageReportingRow = Partial<Pick<AdminUsageLog, UsageTokenField | 'usage_audit'>>

const isTokenCount = (value: unknown): value is number =>
  typeof value === 'number' && Number.isFinite(value) && value >= 0

// Audit evidence, not model names or normalized billing zeros, determines unknown usage.
export const getUsageReportingState = (row: UsageReportingRow | null | undefined) => {
  const audit = row?.usage_audit
  const isGemini = audit?.protocol === 'gemini_generate_content'
  const reasons = isGemini ? audit.reasons ?? [] : []
  return {
    missing: reasons.includes('gemini_usage_missing'),
    partial: reasons.includes('gemini_usage_partial'),
    explicitZero: reasons.includes('gemini_usage_explicit_zero'),
    zeroResetIgnored: reasons.includes('gemini_usage_zero_reset_ignored'),
    cacheRegressed: reasons.includes('gemini_cache_counter_regressed'),
    cacheReadMissing: isGemini && !isTokenCount(audit.upstream?.cache_read_tokens),
  }
}

export const getUsageTokenCount = (
  row: UsageReportingRow | null | undefined,
  field: UsageTokenField,
): number | null => {
  if (!row) return null
  if (row.usage_audit?.protocol === 'gemini_generate_content') {
    // This Gemini path has no separately billed cache-creation counter.
    if (field === 'cache_creation_tokens') return 0
    if (getUsageReportingState(row).missing) return null
    // Cache omission is independent of the primary-counter partial/reported reason.
    if (!isTokenCount(row.usage_audit.upstream?.[field])) return null
    return isTokenCount(row[field]) ? row[field] : null
  }
  // Preserve historical and non-Gemini display behavior.
  return isTokenCount(row[field]) ? row[field] : 0
}

const tokenFields: UsageTokenField[] = ['input_tokens', 'output_tokens', 'cache_read_tokens', 'cache_creation_tokens']

export const getUsageTotalTokens = (row: UsageReportingRow | null | undefined): number | null => {
  if (row?.usage_audit?.protocol === 'gemini_generate_content') {
    if (getUsageReportingState(row).missing) return null
    // The reported prompt total already includes cache reads. A missing cache split
    // or the inapplicable cache-creation counter must not erase a known total.
    const prompt = row.usage_audit.upstream?.total_input_tokens
    const output = getUsageTokenCount(row, 'output_tokens')
    if (isTokenCount(prompt) && output != null) return prompt + output
  }
  const counts = tokenFields.map(field => getUsageTokenCount(row, field))
  return counts.some(count => count == null) ? null : counts.reduce<number>((sum, count) => sum + (count ?? 0), 0)
}

export const isUsageCacheRateUnverifiable = (row: UsageReportingRow): boolean => {
  const state = getUsageReportingState(row)
  return state.missing || state.cacheRegressed ||
    (row.usage_audit?.protocol === 'gemini_generate_content' && (['input_tokens', 'cache_read_tokens'] as const)
      .some(field => getUsageTokenCount(row, field) == null))
}
