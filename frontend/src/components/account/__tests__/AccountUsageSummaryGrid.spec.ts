import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AccountUsageSummaryGrid from '../AccountUsageSummaryGrid.vue'
import enAccounts from '@/i18n/locales/en/admin/accounts'
import zhAccounts from '@/i18n/locales/zh/admin/accounts'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  const { default: zh } = await import('@/i18n/locales/zh/admin/accounts')
  return {
    ...actual,
    useI18n: () => ({
    t: (key: string) => ({
      'admin.accounts.stats.todayGroup': '今日',
      'admin.accounts.stats.totalGroup': '总计',
      'admin.accounts.stats.lifetimeStatsHelp': zh.accounts.stats.lifetimeStatsHelp,
      'admin.accounts.stats.summaryRequests': '请求',
      'admin.accounts.stats.summaryTokens': 'Token',
      'admin.accounts.stats.accountBilled': '账号计费',
      'admin.accounts.stats.userBilled': '用户扣费',
      'usage.accountBilled': '账号计费',
      'usage.userBilled': '用户扣费'
    })[key] ?? key
    })
  }
})

describe('AccountUsageSummaryGrid', () => {
  it('shows four today metrics beside four lifetime totals', () => {
    const wrapper = mount(AccountUsageSummaryGrid, {
      props: {
        stats: {
          requests: 12,
          tokens: 345,
          cost: 14.61,
          user_cost: 2.84,
          lifetime_requests: 900,
          lifetime_tokens: 12_345,
          lifetime_cost: 543.22,
          lifetime_user_cost: 123.45
        }
      }
    })

    const today = wrapper.get('[data-testid="today-stats"]')
    const total = wrapper.get('[data-testid="lifetime-stats"]')
    expect(today.text()).toContain('今日')
    expect(today.text()).toContain('12 req')
    expect(today.text()).toContain('345')
    expect(today.text()).toContain('A $14.61')
    expect(today.text()).toContain('U $2.84')
    expect(total.text()).toContain('总计')
    expect(total.text()).toContain('900 req')
    expect(total.text()).toContain('12.3K')
    expect(total.text()).toContain('A $543.22')
    expect(total.text()).toContain('U $123.45')
    expect(today.findAll('.flex')).toHaveLength(4)
    expect(total.findAll('.flex')).toHaveLength(4)
  })

  it('shows a dash when an all-time metric is unavailable', () => {
    const wrapper = mount(AccountUsageSummaryGrid, {
      props: { stats: { requests: 0, tokens: 0, cost: 0 } }
    })

    expect(wrapper.get('[data-testid="lifetime-stats"]').text()).toContain('—')
  })
  it('keeps unavailable lifetime counts separate from permanent costs and a zero-usage day', () => {
    const wrapper = mount(AccountUsageSummaryGrid, { props: { stats: {
      requests: 0, tokens: 0, cost: 0, user_cost: 0,
      lifetime_requests: null, lifetime_tokens: null, lifetime_cost: 1023, lifetime_user_cost: 172.8
    } } })
    const today = wrapper.get('[data-testid="today-stats"]')
    const total = wrapper.get('[data-testid="lifetime-stats"]')
    expect(today.text()).toContain('0 req')
    expect(today.text()).toContain('A $0.00')
    expect(total.text().match(/—/g)).toHaveLength(2)
    expect(total.text()).toContain('A $1023.00')
    expect(total.text()).toContain('U $172.80')
  })

  it('explains permanent ledger coverage and incomplete evidence with an accessible help marker', () => {
    const wrapper = mount(AccountUsageSummaryGrid, { props: { stats: {
      requests: 0, tokens: 0, cost: 0,
      lifetime_requests: null, lifetime_tokens: null, lifetime_cost: 1023, lifetime_user_cost: 172.8
    } } })
    const message = zhAccounts.accounts.stats.lifetimeStatsHelp
    const total = wrapper.get('[data-testid="lifetime-stats"]')
    const help = total.get('[data-testid="lifetime-stats-help"]')
    expect(total.attributes('title')).toBe(message)
    expect(help.attributes('title')).toBe(message)
    expect(help.attributes('aria-label')).toBe(message)
    expect(help.attributes('tabindex')).toBe('0')
    expect(total.text().match(/—/g)).toHaveLength(2)
    expect(enAccounts.accounts.stats.lifetimeStatsHelp).toContain('permanent ledger')
    expect(enAccounts.accounts.stats.lifetimeStatsHelp).toContain('recorded in the permanent ledger')
    expect(enAccounts.accounts.stats.lifetimeStatsHelp).toContain('Logs lost before permanent capture cannot be recovered')
    expect(enAccounts.accounts.stats.lifetimeStatsHelp).toContain('incomplete evidence or unavailable statistics, not zero')
    expect(message).toContain('永久台账已记录的请求')
    expect(message).toContain('首次永久捕获前已丢失的日志无法补齐')
  })

  it('shows recorded request counts even when one token snapshot is unknown', () => {
    const wrapper = mount(AccountUsageSummaryGrid, { props: { stats: {
      lifetime_requests: 2, lifetime_tokens: null, lifetime_cost: 10, lifetime_user_cost: 3
    } } })
    const total = wrapper.get('[data-testid="lifetime-stats"]')
    expect(total.text()).toContain('2 req')
    expect(total.text().match(/—/g)).toHaveLength(1)
    expect(total.text()).toContain('A $10.00')
    expect(total.text()).toContain('U $3.00')
  })

  it('renders confirmed empty permanent coverage as zero, not unknown', () => {
    const wrapper = mount(AccountUsageSummaryGrid, { props: { stats: {
      lifetime_requests: 0, lifetime_tokens: 0, lifetime_cost: null, lifetime_user_cost: null
    } } })
    const rows = wrapper.get('[data-testid="lifetime-stats"]').findAll('.flex')
    expect(rows[0].text()).toContain('0 req')
    expect(rows[1].text()).toContain('0')
    expect(rows[1].text()).not.toContain('—')
    expect(rows[2].text()).toContain('—')
    expect(rows[3].text()).toContain('—')
  })
  it('shows partial/failed today data as dashes, not zero or lifetime values', () => {
    const wrapper = mount(AccountUsageSummaryGrid, { props: { stats: {
      requests: null, tokens: null, cost: null, user_cost: null,
      lifetime_cost: 1023, lifetime_user_cost: 172.8
    } } })
    expect(wrapper.get('[data-testid="today-stats"]').text().match(/—/g)).toHaveLength(4)
    expect(wrapper.get('[data-testid="today-stats"]').text()).not.toContain('$0.00')
    expect(wrapper.get('[data-testid="lifetime-stats"]').text()).toContain('A $1023.00')
  })

  it('renders a loading skeleton or unavailable placeholder without fabricated values', () => {
    const loading = mount(AccountUsageSummaryGrid, { props: { loading: true } })
    expect(loading.find('[data-testid="account-usage-summary-loading"]').exists()).toBe(true)
    const missing = mount(AccountUsageSummaryGrid)
    expect(missing.get('[data-testid="account-usage-summary-unavailable"]').text()).toBe('—')
  })

  it('does not display non-finite numbers as valid statistics', () => {
    const wrapper = mount(AccountUsageSummaryGrid, { props: { stats: {
      requests: NaN, tokens: Infinity, cost: NaN, user_cost: -Infinity,
      lifetime_cost: Infinity
    } } })
    expect(wrapper.text()).not.toMatch(/NaN|Infinity/)
    expect(wrapper.get('[data-testid="today-stats"]').text().match(/—/g)).toHaveLength(4)
  })
})
