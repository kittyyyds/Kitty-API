import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount, type DOMWrapper, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import type { AdminUsageLog, UsageAudit } from '@/types'
import en from '@/i18n/locales/en/dashboard'
import zh from '@/i18n/locales/zh/dashboard'
import enAdmin from '@/i18n/locales/en/admin/resources'
import zhAdmin from '@/i18n/locales/zh/admin/resources'
import UsageTable from '../UsageTable.vue'

vi.mock('@/stores/app', () => ({ useAppStore: () => ({}) }))

const counters = { input_tokens: 200, output_tokens: 10, cache_read_tokens: 800, cache_creation_tokens: 0 }
const audit = (reasons: string[], upstream: UsageAudit['upstream'] = { input_tokens: 200, total_input_tokens: 1000, output_tokens: 10, cache_read_tokens: 800 }): UsageAudit => ({
  version: 1, protocol: 'gemini_generate_content', status: 'pass', confidence: 'high',
  evidence_source: 'upstream_raw', upstream, local: {}, delta: {}, deviation_pct: {},
  reasons, capture_truncated: false,
})
const baseRow = {
  id: 1, request_id: 'usage-reporting-test', model: 'custom-alias',
  ...counters, image_input_tokens: 0, image_output_tokens: 0, image_count: 0,
  cache_creation_5m_tokens: 0, cache_creation_1h_tokens: 0,
  billing_mode: 'token', request_type: 'stream', stream: true,
  input_cost: 0, output_cost: 0, cache_creation_cost: 0, cache_read_cost: 0,
  actual_cost: 0, total_cost: 0, rate_multiplier: 1, account_rate_multiplier: 1,
  duration_ms: 1000,
} as AdminUsageLog

const DataTableStub = {
  props: ['data'],
  template: `<div>
    <div data-testid="tokens-slot"><slot name="cell-tokens" :row="data[0]" /></div>
    <div data-testid="latency-slot"><slot name="cell-latency" :row="data[0]" /></div>
    <div data-testid="cost-slot"><slot name="cell-cost" :row="data[0]" /></div>
    <div><slot name="cell-stream" :row="data[0]" /></div>
  </div>`,
}
let wrapper: VueWrapper | undefined

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
})

function detailValue(panel: DOMWrapper<Element>, label: string): string {
  const labelSpan = panel.findAll('span').find(span => span.text() === label)
  expect(labelSpan, `Missing detail label: ${label}`).toBeDefined()
  return labelSpan!.element.parentElement!.lastElementChild!.textContent!.trim()
}

describe.each([
  { locale: 'zh', messages: zh, admin: zhAdmin },
  { locale: 'en', messages: en, admin: enAdmin },
])('Gemini usage reporting ($locale)', ({ locale, messages, admin }) => {
  function render(overrides: Partial<AdminUsageLog>) {
    wrapper = mount(UsageTable, {
      props: { data: [{ ...baseRow, ...overrides }], columns: [] },
      global: {
        plugins: [createI18n({ legacy: false, locale, messages: {
          [locale]: { ...messages, admin: { usage: admin.usage } },
        } })],
        stubs: { DataTable: DataTableStub, EmptyState: true, Icon: true, Teleport: true },
      },
    })
    return wrapper
  }
  async function openDetails(kind: 'token' | 'cost') {
    await wrapper!.get(`button[aria-label="${kind === 'token' ? messages.usage.tokenDetails : messages.usage.costDetails}"]`).trigger('click')
    return wrapper!.get(`[data-testid="${kind}-details-tooltip"]`)
  }

  it('labels unknown tokens, cache rate and recorded costs as unreported, not known zero or a cache miss', async () => {
    const view = render({ input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_creation_tokens: 0,
      usage_audit: audit(['gemini_usage_missing'], {}) })
    expect(view.get('[data-testid="input-tokens-cell"]').text()).toBe('—')
    expect(view.get('[data-testid="output-tokens-cell"]').text()).toBe('—')
    for (const id of ['cache-hit-rate-cell', 'cache-hit-rate']) {
      expect(view.get(`[data-testid="${id}"]`).text()).toBe('—')
      expect(view.get(`[data-testid="${id}"]`).attributes('title')).toContain(messages.usage.usageNotReportedHint)
    }
    expect(view.get('[data-testid="token-usage-reporting-label"]').text()).toBe(messages.usage.usageNotReported)
    expect(view.get('[data-testid="cost-usage-reporting-label"]').text()).toBe(messages.usage.usageNotReported)
    expect(view.get('[data-testid="usage-cost-cell"]').text()).toContain('$0.000000')
    expect(view.get('[data-testid="usage-cost-cell"]').attributes('title')).toContain(messages.usage.usageUnreportedCostHint)
    const tokens = await openDetails('token')
    expect(tokens.text()).toContain(messages.usage.usageNotReportedHint)
    for (const label of [admin.usage.inputTokens, admin.usage.outputTokens, admin.usage.cacheReadTokens,
      messages.usage.totalTokens]) {
      expect(detailValue(tokens, label)).toBe('—')
    }
    const cost = await openDetails('cost')
    expect(cost.text()).toContain(messages.usage.usageUnreportedCostHint)
    for (const label of [messages.usage.inputTokenPrice, messages.usage.outputTokenPrice]) {
      expect(detailValue(cost, label)).toBe(`— ${messages.usage.perMillionTokens}`)
    }
    expect(cost.text()).not.toContain('gemini_usage_missing')
  })

  it('shows explicit zero as zero and keeps a normal tool-capable request, with a neutral tooltip', async () => {
    const view = render({ input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_creation_tokens: 0,
      usage_audit: audit(['gemini_usage_explicit_zero'], { input_tokens: 0, total_input_tokens: 0, output_tokens: 0 }) })
    expect(view.get('[data-testid="input-tokens-cell"]').text()).toBe('0')
    expect(view.get('[data-testid="output-tokens-cell"]').text()).toBe('0')
    expect(view.find('[data-testid="token-usage-reporting-label"]').exists()).toBe(false)
    expect(view.get('[data-testid="request-type-badge"]').text()).toBe(messages.usage.stream)
    expect(view.get('[data-testid="cache-hit-rate"]').text()).toBe('—')
    const tokens = await openDetails('token')
    expect(tokens.text()).toContain(messages.usage.usageExplicitZeroHint)
    expect(detailValue(tokens, admin.usage.inputTokens)).toBe('0')
    expect(detailValue(tokens, admin.usage.outputTokens)).toBe('0')
    expect(detailValue(tokens, messages.usage.totalTokens)).toBe('0')
    expect(tokens.get('[data-testid="token-usage-reporting-hint"]').classes().join(' ')).not.toMatch(/(?:red|rose)-/)
  })

  it('preserves normal caches while explaining that a recorded zero cost is not proof of free usage', async () => {
    const view = render({ usage_audit: audit([]) })
    expect(view.get('[data-testid="cache-read-tokens-cell"]').text()).toBe('800')
    expect(view.find('[data-testid="cache-creation-tokens-cell"]').exists()).toBe(false)
    expect(view.get('[data-testid="cache-hit-rate-cell"]').text()).toBe('80.0%')
    expect(view.get('[data-testid="cache-hit-rate"]').text()).toBe('80.0%')
    expect(view.find('[data-testid="token-usage-reporting-label"]').exists()).toBe(false)
    expect(view.get('[data-testid="usage-cost-cell"]').attributes('title')).toContain(messages.usage.zeroRecordedCostHint)
    const tokens = await openDetails('token')
    expect(tokens.get('[data-testid="token-cache-hit-rate-tooltip"]').text()).toContain('80.00%')
    expect(detailValue(tokens, messages.usage.totalTokens)).toBe((1010).toLocaleString())
    const cost = await openDetails('cost')
    expect(cost.text()).toContain(messages.usage.zeroRecordedCostHint)
    expect(detailValue(cost, messages.usage.userBilled)).toBe('$0.00000000')
  })

  it.each([null, undefined])('keeps historical Gemini-model rows compatible without audit (%s)', async (usage_audit) => {
    const view = render({ model: 'gemini-legacy', usage_audit })
    expect(view.get('[data-testid="input-tokens-cell"]').text()).toBe('200')
    expect(view.get('[data-testid="cache-hit-rate-cell"]').text()).toBe('80.0%')
    expect(view.get('[data-testid="cache-hit-rate"]').text()).toBe('80.0%')
    expect(view.find('[data-testid="token-usage-reporting-label"]').exists()).toBe(false)
    const tokens = await openDetails('token')
    expect(tokens.find('[data-testid="token-usage-reporting-hint"]').exists()).toBe(false)
    await view.setProps({ data: [{ ...baseRow, model: 'gemini-legacy', usage_audit,
      input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_creation_tokens: 0 }] })
    expect(view.get('[data-testid="input-tokens-cell"]').text()).toBe('0')
    expect(view.get('[data-testid="cache-hit-rate"]').text()).toBe('—')
    expect(view.find('[data-testid="token-usage-reporting-label"]').exists()).toBe(false)
  })

  it('keeps known partial fields but leaves omitted counters, totals, rates and prices unavailable', async () => {
    const view = render({ input_tokens: 100, output_tokens: 0, cache_read_tokens: 0, cache_creation_tokens: 0,
      usage_audit: audit(['gemini_usage_partial'], { input_tokens: 100 }) })
    expect(view.get('[data-testid="input-tokens-cell"]').text()).toBe('100')
    expect(view.get('[data-testid="output-tokens-cell"]').text()).toBe('—')
    expect(view.get('[data-testid="cache-hit-rate-cell"]').text()).toBe('—')
    expect(view.get('[data-testid="cache-hit-rate"]').text()).toBe('—')
    expect(view.get('[data-testid="token-usage-reporting-label"]').text()).toBe(messages.usage.usagePartiallyReported)
    const tokens = await openDetails('token')
    expect(detailValue(tokens, admin.usage.inputTokens)).toBe('100')
    expect(detailValue(tokens, admin.usage.outputTokens)).toBe('—')
    expect(detailValue(tokens, admin.usage.cacheReadTokens)).toBe('—')
    expect(detailValue(tokens, messages.usage.totalTokens)).toBe('—')
    const cost = await openDetails('cost')
    expect(cost.text()).toContain(messages.usage.usageUnreportedCostHint)
    expect(detailValue(cost, messages.usage.outputTokenPrice)).toBe(`— ${messages.usage.perMillionTokens}`)
  })

  it('does not hide a cache rate when only output is missing', () => {
    const prompt = { input_tokens: 200, cache_read_tokens: 800 }
    const view = render({ output_tokens: 0, usage_audit: audit(['gemini_usage_partial'], prompt) })
    expect(view.get('[data-testid="output-tokens-cell"]').text()).toBe('—')
    expect(view.get('[data-testid="cache-hit-rate-cell"]').text()).toBe('80.0%')
    expect(view.get('[data-testid="cache-hit-rate"]').text()).toBe('80.0%')
  })

  it('preserves real usage when a zero-reset tail was ignored', async () => {
    const view = render({ usage_audit: audit(['gemini_usage_zero_reset_ignored']) })
    expect(view.get('[data-testid="input-tokens-cell"]').text()).toBe('200')
    expect(view.get('[data-testid="cache-read-tokens-cell"]').text()).toBe('800')
    expect(view.get('[data-testid="cache-hit-rate-cell"]').text()).toBe('80.0%')
    const tokens = await openDetails('token')
    expect(tokens.text()).toContain(messages.usage.usageZeroResetIgnoredHint)
    expect(detailValue(tokens, messages.usage.totalTokens)).toBe((1010).toLocaleString())
  })

  it('marks regressed cache counts for verification without inventing a maximum or confirming a miss', async () => {
    const view = render({ cache_read_tokens: 0,
      usage_audit: { ...audit(['gemini_cache_counter_regressed'], { ...counters, cache_read_tokens: 0 }), cache_hit_rate: 99.9 } })
    expect(view.get('[data-testid="token-usage-reporting-label"]').text()).toBe(messages.usage.usagePendingVerification)
    expect(view.get('[data-testid="cache-hit-rate-cell"]').text()).toBe('—')
    expect(view.get('[data-testid="cache-hit-rate"]').text()).toBe('—')
    expect(view.get('[data-testid="cache-hit-rate-cell"]').attributes('title')).toContain(messages.usage.usageCacheRegressedHint)
    const tokens = await openDetails('token')
    expect(detailValue(tokens, admin.usage.cacheReadTokens)).toBe('0')
    expect(tokens.text()).toContain(messages.usage.usageCacheRegressedHint)
    expect(tokens.text()).not.toContain('99.9%')
  })

  it('keeps genuinely missing cost fields as dashes for audited requests', async () => {
    const view = render({ actual_cost: undefined, total_cost: undefined,
      usage_audit: audit(['gemini_usage_missing'], {}) })
    expect(view.get('[data-testid="usage-cost-cell"]').find('span').text()).toBe('—')
    const cost = await openDetails('cost')
    expect(detailValue(cost, messages.usage.original)).toBe('—')
    expect(detailValue(cost, messages.usage.userBilled)).toBe('—')
  })
  it('keeps omitted cache evidence unknown even with gemini_usage_reported, but retains the reported total', async () => {
    const view = render({ input_tokens: 100, output_tokens: 2, cache_read_tokens: 0,
      usage_audit: audit(['gemini_usage_reported'], { input_tokens: 100, total_input_tokens: 100, output_tokens: 2 }) })
    expect(view.get('[data-testid="input-tokens-cell"]').text()).toBe('100')
    expect(view.get('[data-testid="output-tokens-cell"]').text()).toBe('2')
    expect(view.get('[data-testid="cache-hit-rate-cell"]').text()).toBe('—')
    expect(view.get('[data-testid="cache-hit-rate"]').text()).toBe('—')
    expect(view.get('[data-testid="cache-hit-rate-cell"]').attributes('title')).toContain(messages.usage.cacheUsageNotReportedHint)
    expect(view.get('[data-testid="token-usage-reporting-label"]').text()).toBe(messages.usage.cacheUsageNotReported)
    const tokens = await openDetails('token')
    expect(detailValue(tokens, admin.usage.cacheReadTokens)).toBe('—')
    expect(detailValue(tokens, admin.usage.cacheCreationTokens)).toBe('0')
    expect(detailValue(tokens, messages.usage.totalTokens)).toBe('102')
    const cost = await openDetails('cost')
    expect(cost.text()).toContain(messages.usage.cacheUsageNotReportedHint)
    expect(cost.text()).toContain(messages.usage.usageUnreportedCostHint)
  })

  it('allows a confirmed zero-percent cache rate when cache read is explicitly zero', async () => {
    const view = render({ input_tokens: 100, output_tokens: 2, cache_read_tokens: 0,
      usage_audit: audit(['gemini_usage_reported'], { input_tokens: 100, total_input_tokens: 100, output_tokens: 2, cache_read_tokens: 0 }) })
    expect(view.get('[data-testid="cache-hit-rate-cell"]').text()).toBe('0.0%')
    expect(view.get('[data-testid="cache-hit-rate"]').text()).toBe('0.0%')
    expect(view.find('[data-testid="token-usage-reporting-label"]').exists()).toBe(false)
    const tokens = await openDetails('token')
    expect(detailValue(tokens, messages.usage.totalTokens)).toBe('102')
  })

})
