import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount, type VueWrapper } from '@vue/test-utils'
import type { AccountUsageSummaryStats } from '@/types'
import AccountsView from '../AccountsView.vue'

const { listAccounts, getBatchTodayStats } = vi.hoisted(() => ({ listAccounts: vi.fn(), getBatchTodayStats: vi.fn() }))
vi.mock('@/api/admin/accountIntelligence', () => ({ accountIntelligence: { recent: async () => [] } }))
vi.mock('@/api/admin/channelQuality', () => ({ channelQualityAPI: { scores: async () => ({}) } }))
vi.mock('@/api/admin', () => ({ adminAPI: {
  accounts: { list: listAccounts, getBatchTodayStats,
    getPlatformCounts: async () => [], getUpstreamBillingProbeSettings: async () => ({ enabled: false }) },
  ops: { listRequestDetails: async () => ({ items: [] }), getAccountRequestStatsBatch: async () => ({ stats: {} }) },
  proxies: { getAll: async () => [] }, groups: { getAll: async () => [] }
} }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showWarning: vi.fn(), showSuccess: vi.fn(), showInfo: vi.fn() }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ token: 'test-token', isSimpleMode: false }) }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))

const account = (id: number) => ({ id, name: 'account-' + id, platform: 'openai', type: 'apikey',
  status: 'active', schedulable: true, concurrency: 2, priority: 1, group_ids: [], extra: {}, credentials: {} })
const page = (ids: number[]) => ({ items: ids.map(account), total: 50, page: 1, page_size: 20, pages: 3 })
const today: AccountUsageSummaryStats = { requests: 2300, tokens: 285100000, cost: 102.30, user_cost: 17.28,
  lifetime_requests: 23000, lifetime_tokens: 2851000000, lifetime_cost: 1023, lifetime_user_cost: 172.8 }
let wrapper: VueWrapper | undefined
const mountView = () => {
  wrapper = shallowMount(AccountsView, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    TablePageLayout: { template: '<div><slot name="table" /><slot name="pagination" /></div>' },
    DataTable: { props: ['data'], template: '<div><div v-for="row in data" :key="row.id" :data-account-id="row.id"><slot name="cell-today_stats" :row="row" /></div></div>' },
    AccountUsageSummaryGrid: false
  } } })
  return wrapper
}

describe('AccountsView paginated usage summary contract', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
    localStorage.clear()
    listAccounts.mockReset().mockResolvedValue(page([42, 7]))
    getBatchTodayStats.mockReset().mockResolvedValue({ stats: {
      '7': { requests: 0, tokens: 0, cost: 0, user_cost: 0, lifetime_requests: 12, lifetime_tokens: 345, lifetime_cost: 600, lifetime_user_cost: 125 },
      '42': today,
      '999': { requests: 999, tokens: 999, cost: 999, lifetime_cost: 999 }
    } })
  })
  afterEach(() => { wrapper?.unmount(); wrapper = undefined; vi.restoreAllMocks(); vi.useRealTimers() })

  it('fetches one current-page batch and renders by account ID, not response order', async () => {
    const view = mountView(); await flushPromises()
    expect(getBatchTodayStats).toHaveBeenCalledTimes(1)
    expect(getBatchTodayStats).toHaveBeenCalledWith([42, 7])
    const first = view.get('[data-account-id="42"]')
    expect(first.get('[data-testid="today-stats"]').text()).toContain('2.3K req')
    expect(first.get('[data-testid="today-stats"]').text()).toContain('285.1M')
    expect(first.get('[data-testid="today-stats"]').text()).toContain('A $102.30')
    expect(first.get('[data-testid="today-stats"]').text()).toContain('U $17.28')
    expect(first.get('[data-testid="lifetime-stats"]').text()).toContain('A $1023.00')
    expect(first.get('[data-testid="lifetime-stats"]').text()).toContain('U $172.80')
    expect(first.get('[data-testid="lifetime-stats"]').text()).toContain('23.0K req')
    expect(first.get('[data-testid="lifetime-stats"]').text()).not.toContain('—')
    const zero = view.get('[data-account-id="7"]')
    expect(zero.get('[data-testid="today-stats"]').text()).toContain('0 req')
    expect(zero.get('[data-testid="lifetime-stats"]').text()).toContain('A $600.00')
    expect(view.text()).not.toContain('$999.00')
  })

  it('keeps today and lifetime money when only permanent counters are unavailable', async () => {
    getBatchTodayStats.mockResolvedValue({ stats: { '42': {
      ...today, lifetime_requests: null, lifetime_tokens: null
    } } })
    const view = mountView(); await flushPromises()
    const row = view.get('[data-account-id="42"]')
    expect(row.get('[data-testid="today-stats"]').text()).toContain('A $102.30')
    const total = row.get('[data-testid="lifetime-stats"]')
    expect(total.text().match(/—/g)).toHaveLength(2)
    expect(total.text()).toContain('A $1023.00')
    expect(total.text()).toContain('U $172.80')
  })
  it('preserves missing and null account results as unknown instead of fake zero', async () => {
    getBatchTodayStats.mockResolvedValue({ stats: { '42': null } })
    const view = mountView(); await flushPromises()
    for (const id of [42,7]) {
      const row = view.get('[data-account-id="' + id + '"]')
      expect(row.get('[data-testid="today-stats"]').text().match(/—/g)).toHaveLength(4)
      expect(row.text()).not.toContain('$0.00')
      expect(row.text()).not.toContain('0 req')
    }
  })

  it('does not manufacture today statistics when the batch endpoint fails', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    getBatchTodayStats.mockRejectedValue(new Error('unavailable'))
    const view = mountView(); await flushPromises()
    expect(view.findAll('[data-testid="account-usage-summary-unavailable"]')).toHaveLength(2)
    expect(view.text()).not.toContain('$0.00')
    expect(getBatchTodayStats).toHaveBeenCalledTimes(1)
  })

  it('only fetches IDs from the newly selected page', async () => {
    const view = mountView(); await flushPromises()
    listAccounts.mockResolvedValue({ ...page([9]), page: 2 })
    getBatchTodayStats.mockResolvedValue({ stats: { '9': { ...today, lifetime_cost: 9000 }, '42': today } })
    const vm = view.vm as unknown as { handlePageChange: (page: number) => void }
    vm.handlePageChange(2); await flushPromises()
    expect(getBatchTodayStats).toHaveBeenLastCalledWith([9])
    expect(view.find('[data-account-id="42"]').exists()).toBe(false)
    expect(view.get('[data-account-id="9"] [data-testid="lifetime-stats"]').text()).toContain('A $9000.00')
  })

  it('discards an older in-flight summary after a newer page refresh', async () => {
    let completeOld!: (value: { stats: Record<string, AccountUsageSummaryStats> }) => void
    getBatchTodayStats.mockImplementationOnce(() => new Promise(resolve => { completeOld = resolve }))
    const view = mountView(); await flushPromises()
    const vm = view.vm as unknown as { refreshTodayStatsBatch: () => Promise<void> }
    getBatchTodayStats.mockResolvedValue({ stats: { '42': { ...today, lifetime_cost: 2046 } } })
    await vm.refreshTodayStatsBatch(); await flushPromises()
    completeOld({ stats: { '42': today } }); await flushPromises()
    expect(view.get('[data-account-id="42"] [data-testid="lifetime-stats"]').text()).toContain('A $2046.00')
    expect(view.get('[data-account-id="42"] [data-testid="lifetime-stats"]').text()).not.toContain('A $1023.00')
  })
})
