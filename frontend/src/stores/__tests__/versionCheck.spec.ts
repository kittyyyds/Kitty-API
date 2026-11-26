import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { checkUpdates } from '@/api/admin/system'
import { useAppStore } from '@/stores/app'

vi.mock('@/api/admin/system', () => ({ checkUpdates: vi.fn() }))
vi.mock('@/api/auth', () => ({ getPublicSettings: vi.fn() }))

describe('version check evidence', () => {
  beforeEach(() => { setActivePinia(createPinia()); vi.mocked(checkUpdates).mockReset() })
  const result = { current_version: '1', latest_version: '2', has_update: true, cached: false, build_type: 'source', release_info: { name: '2', body: '', published_at: '', html_url: '' } }
  const kittyReleaseURL = 'https://github.com/kittyyyds/Kitty-API/releases/tag/cs-v0.2.24.3-kitty.7'
  const upstreamRelease = { ...result.release_info, html_url: 'https://github.com/Wei-Shaw/sub2api/releases/tag/v0.3.0' }

  it('keeps the current release URL and upstream upgrade evidence separate in the cache', async () => {
    vi.mocked(checkUpdates).mockResolvedValue({ ...result, current_release_url: kittyReleaseURL, release_info: upstreamRelease })
    const store = useAppStore()
    const fresh = await store.fetchVersion()
    const cached = await store.fetchVersion()
    expect(fresh?.current_release_url).toBe(kittyReleaseURL)
    expect(store.currentReleaseURL).toBe(kittyReleaseURL)
    expect(cached?.current_release_url).toBe(kittyReleaseURL)
    expect(cached?.release_info).toEqual(upstreamRelease)
    expect(store.hasUpdate).toBe(true)
    expect(checkUpdates).toHaveBeenCalledOnce()
    store.clearVersionCache()
    expect(store.currentReleaseURL).toBe('')
  })

  it('retains the current release URL after a failed refresh', async () => {
    const log = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      vi.mocked(checkUpdates)
        .mockResolvedValueOnce({ ...result, current_release_url: kittyReleaseURL, release_info: upstreamRelease })
        .mockRejectedValueOnce(new Error('Network unavailable'))
      const store = useAppStore()
      await store.fetchVersion()
      await store.fetchVersion(true)
      expect(store.currentReleaseURL).toBe(kittyReleaseURL)
      expect((await store.fetchVersion())?.current_release_url).toBe(kittyReleaseURL)
      expect(store.releaseInfo).toEqual(upstreamRelease)
      expect(store.versionCheckStatus).toBe('stale')
    } finally { log.mockRestore() }
  })

  it.each([undefined, '', '   '])('clears the old current release URL when a fresh response omits it (%s)', async (url) => {
    vi.mocked(checkUpdates)
      .mockResolvedValueOnce({ ...result, current_release_url: kittyReleaseURL })
      .mockResolvedValueOnce({ ...result, current_release_url: url })
    const store = useAppStore()
    await store.fetchVersion()
    await store.fetchVersion(true)
    expect(store.currentReleaseURL).toBe('')
    expect((await store.fetchVersion())?.current_release_url).toBeUndefined()
  })

  it('does not claim latest before checking', () => {
    expect(useAppStore().versionCheckStatus).toBe('unknown')
  })
  it('marks authoritative successful checks verified', async () => {
    vi.mocked(checkUpdates).mockResolvedValue({ ...result, has_update: false, latest_version: '1' })
    const store = useAppStore(); await store.fetchVersion()
    expect(store.versionCheckStatus).toBe('verified')
    expect(store.versionWarning).toBe('')
  })
  it('retains known updates and marks them stale after network failure', async () => {
    vi.mocked(checkUpdates).mockResolvedValueOnce(result).mockRejectedValueOnce(new Error('Network unavailable'))
    const store = useAppStore(); await store.fetchVersion(); await store.fetchVersion(true)
    expect(store.hasUpdate).toBe(true)
    expect(store.versionCheckStatus).toBe('stale')
    expect(store.versionWarning).not.toBe('')
  })
  it('preserves warnings in the local cache and does not report latest on fallback', async () => {
    vi.mocked(checkUpdates).mockResolvedValue({ ...result, has_update: false, release_info: undefined, warning: 'Cannot contact update source' })
    const store = useAppStore(); await store.fetchVersion()
    expect(store.versionCheckStatus).toBe('failed')
    expect((await store.fetchVersion())?.warning).toBe('Cannot contact update source')
    expect(checkUpdates).toHaveBeenCalledTimes(1)
    store.clearVersionCache(); expect(store.versionCheckStatus).toBe('unknown')
  })
})
