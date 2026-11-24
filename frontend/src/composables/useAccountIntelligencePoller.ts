import { onMounted, onUnmounted, ref, watch, type Ref } from 'vue'
import { accountIntelligence, type AccountIntelligenceTest } from '@/api/admin/accountIntelligence'
import type { Account } from '@/types'

/**
 * Lifecycle-safe 15s poller for recent account intelligence test summaries.
 * Baked natively from the 2026-11-24 account-model-iq-fix production hotfix.
 */
export function useAccountIntelligencePoller(getAccounts: () => Account[]) {
  const byAccount: Ref<Record<number, AccountIntelligenceTest[]>> = ref({})
  let disposed = false
  let generation = 0
  let flight: Promise<void> | null = null
  let queued = false
  let timer: ReturnType<typeof setInterval> | null = null

  const visible = () => typeof document === 'undefined' || document.visibilityState !== 'hidden'
  const ids = () => [
    ...new Set(
      getAccounts()
        .filter((a) => a.platform === 'openai' || a.platform === 'anthropic')
        .map((a) => a.id)
    )
  ]

  function request(force = false): Promise<void> {
    if (disposed || !visible()) return Promise.resolve()
    const accounts = ids()
    if (!accounts.length) {
      byAccount.value = {}
      return Promise.resolve()
    }
    if (flight) {
      if (force) queued = true
      return flight
    }
    const token = generation
    flight = (async () => {
      try {
        const batches: number[][] = []
        for (let i = 0; i < accounts.length; i += 100) batches.push(accounts.slice(i, i + 100))
        const rows = (await Promise.all(batches.map((b) => accountIntelligence.recent(b)))).flat()
        if (disposed || token !== generation || !visible()) return
        const next: Record<number, AccountIntelligenceTest[]> = {}
        const allowed = new Set(accounts)
        for (const row of rows) {
          if (allowed.has(row.account_id)) {
            ;(next[row.account_id] ||= []).push(row)
          }
        }
        byAccount.value = next
      } catch {
        // Retain last successful summary; retry at the next visible tick.
      } finally {
        flight = null
        if (queued && !disposed && visible()) {
          queued = false
          void request(false)
        }
      }
    })()
    return flight
  }

  const refresh = () => request(true)

  watch(
    () => ids().join(','),
    () => {
      generation++
      queued = false
      byAccount.value = {}
      void refresh()
    },
    { immediate: true, flush: 'sync' }
  )

  function visibilityChanged() {
    generation++
    if (visible()) void refresh()
    else queued = false
  }

  onMounted(() => {
    timer = setInterval(() => {
      void request(false)
    }, 15_000)
    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', visibilityChanged)
    }
  })

  onUnmounted(() => {
    disposed = true
    generation++
    queued = false
    if (timer !== null) clearInterval(timer)
    if (typeof document !== 'undefined') {
      document.removeEventListener('visibilitychange', visibilityChanged)
    }
  })

  return { byAccount, refresh }
}
