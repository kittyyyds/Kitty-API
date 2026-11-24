<template>
  <div v-if="isAdmin" class="relative inline-block">
    <a
      :href="RELEASES_URL"
      target="_blank"
      rel="noopener noreferrer"
      :title="badgeTitle"
      :aria-label="badgeTitle"
      data-upstream-release-reminder="zuchengchen/sub2api"
      class="flex items-center gap-1.5 rounded-lg px-2 py-1 text-xs transition-colors"
      :class="
        hasUpdate
          ? 'bg-amber-100 text-amber-700 hover:bg-amber-200 dark:bg-amber-900/30 dark:text-amber-400 dark:hover:bg-amber-900/50'
          : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-800 dark:text-dark-400 dark:hover:bg-dark-700'
      "
    >
      <span class="font-medium">{{ displayVersion ? `v${displayVersion}` : '--' }}</span>
      <span v-if="hasUpdate" class="relative flex h-2 w-2" aria-hidden="true">
        <span
          class="absolute inline-flex h-full w-full animate-ping rounded-full bg-amber-400 opacity-75"
        ></span>
        <span class="relative inline-flex h-2 w-2 rounded-full bg-amber-500"></span>
      </span>
    </a>
  </div>
  <span v-else-if="version" class="text-xs text-gray-400 dark:text-dark-500">v{{ version }}</span>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'

interface UpstreamRelease {
  id: number
  tag_name: string
  name?: string
  published_at: string
  draft?: boolean
  prerelease?: boolean
}

const props = defineProps<{
  version?: string
}>()

const RELEASES_URL = 'https://github.com/zuchengchen/sub2api/releases'
const API_URL = 'https://api.github.com/repos/zuchengchen/sub2api/releases/latest'
const POLL_INTERVAL_MS = 15 * 60 * 1000

// Upstream baseline corresponding to this reconstructed Kitty build (czc-v2026.11.24.4 / 0.2.24.3)
const UPSTREAM_BASELINE: UpstreamRelease = {
  id: 455986262,
  tag_name: 'czc-v2026.11.24.4',
  name: 'czc-v2026.11.24.4 (0.2.24.3)',
  published_at: '2026-11-24T11:28:34Z'
}

let cachedResult: { release: UpstreamRelease | null; error: boolean } | null = null
let inFlight: Promise<{ release: UpstreamRelease | null; error: boolean }> | null = null
let checkedAt = 0

function isNewRelease(release: UpstreamRelease | null, baseline: UpstreamRelease): boolean {
  if (!release || release.draft || release.prerelease || !release.tag_name || release.tag_name === baseline.tag_name) {
    return false
  }
  const published = Date.parse(release.published_at)
  const followed = Date.parse(baseline.published_at)
  if (!Number.isFinite(published) || !Number.isFinite(followed)) return false
  return published > followed || (published === followed && Number(release.id) > Number(baseline.id))
}

async function readLatestRelease(): Promise<{ release: UpstreamRelease | null; error: boolean }> {
  if (inFlight) return inFlight
  const ttl = cachedResult?.error ? 60_000 : POLL_INTERVAL_MS
  if (cachedResult && Date.now() - checkedAt < ttl) return cachedResult
  inFlight = (async () => {
    const abort = new AbortController()
    const timeout = setTimeout(() => abort.abort(), 12_000)
    try {
      const response = await fetch(API_URL, {
        method: 'GET',
        credentials: 'omit',
        referrerPolicy: 'no-referrer',
        cache: 'no-cache',
        headers: { Accept: 'application/vnd.github+json' },
        signal: abort.signal
      })
      if (!response.ok) throw new Error('Upstream release unavailable')
      const release = (await response.json()) as UpstreamRelease
      if (!release?.tag_name || !Number.isFinite(Date.parse(release.published_at)) || release.draft || release.prerelease) {
        throw new Error('Invalid upstream release')
      }
      cachedResult = { release, error: false }
    } catch {
      cachedResult = { release: cachedResult?.release ?? null, error: true }
    } finally {
      clearTimeout(timeout)
      checkedAt = Date.now()
      inFlight = null
    }
    return cachedResult
  })()
  return inFlight
}

const authStore = useAuthStore()
const appStore = useAppStore()

const latestRelease = ref<UpstreamRelease | null>(null)
const checking = ref(false)
const failed = ref(false)

const isAdmin = computed(() => authStore.isAdmin)
const displayVersion = computed(() => appStore.currentVersion || props.version || '')
const hasUpdate = computed(() => isNewRelease(latestRelease.value, UPSTREAM_BASELINE))

const badgeTitle = computed(() => {
  const current = displayVersion.value ? `当前站点：v${displayVersion.value}\n` : ''
  const latest = latestRelease.value ? `上游发布：${latestRelease.value.name || latestRelease.value.tag_name}\n` : ''
  const state = failed.value
    ? '上游发布检查失败，可点击查看 Releases'
    : checking.value && !latestRelease.value
      ? '正在检查开源底座发布…'
      : hasUpdate.value
        ? '开源底座有新发布，点击查看 Releases'
        : '开源底座暂无新发布，点击查看 Releases'
  return current + latest + state
})

let timer: ReturnType<typeof setInterval> | null = null
let disposed = false

async function refresh() {
  if (!isAdmin.value || disposed) return
  checking.value = true
  try {
    const result = await readLatestRelease()
    if (!disposed) {
      latestRelease.value = result.release
      failed.value = result.error
    }
  } finally {
    if (!disposed) checking.value = false
  }
}

function whenVisible() {
  if (typeof document === 'undefined' || document.visibilityState !== 'hidden') {
    void refresh()
  }
}

onMounted(() => {
  if (!isAdmin.value) return
  void refresh()
  timer = setInterval(whenVisible, POLL_INTERVAL_MS)
  window.addEventListener('focus', whenVisible)
  document.addEventListener('visibilitychange', whenVisible)
})

onUnmounted(() => {
  disposed = true
  if (timer !== null) clearInterval(timer)
  window.removeEventListener('focus', whenVisible)
  document.removeEventListener('visibilitychange', whenVisible)
})
</script>
