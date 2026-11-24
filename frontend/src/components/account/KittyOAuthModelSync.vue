<template>
  <div
    v-if="eligible"
    class="rounded-lg border border-sky-200 bg-sky-50/70 p-3 dark:border-sky-800/70 dark:bg-sky-900/20"
  >
    <div class="flex flex-wrap items-center justify-between gap-2">
      <div>
        <div class="text-sm font-medium text-gray-900 dark:text-white">
          {{ copy('OAuth 模型目录同步', 'OAuth model catalog sync') }}
        </div>
        <div class="mt-0.5 text-xs text-gray-600 dark:text-gray-300">
          {{
            copy(
              '使用当前 OAuth 账号同步上游官方可用模型目录，不修改凭证、白名单或调度配置。',
              'Sync the upstream OAuth model catalog without changing credentials, allowlists, or scheduling.'
            )
          }}
        </div>
      </div>
      <button
        type="button"
        class="btn btn-secondary btn-sm whitespace-nowrap"
        :disabled="busy || disabled"
        @click="synchronize"
      >
        {{ busy ? copy('同步中…', 'Syncing...') : copy('同步上游模型', 'Sync Upstream Models') }}
      </button>
    </div>
    <p
      v-if="message"
      class="mt-2 text-xs"
      :class="
        severity === 'error'
          ? 'text-red-600 dark:text-red-400'
          : severity === 'warning'
            ? 'text-amber-700 dark:text-amber-300'
            : 'text-emerald-700 dark:text-emerald-300'
      "
    >
      {{ message }}
    </p>
    <div v-if="models.length > 0" class="mt-2 flex flex-wrap gap-1.5">
      <span
        v-for="m in models.slice(0, 12)"
        :key="m"
        class="rounded bg-white/90 px-1.5 py-0.5 font-mono text-[11px] text-gray-700 ring-1 ring-gray-200 dark:bg-dark-800 dark:text-gray-200 dark:ring-dark-600"
      >
        {{ m }}
      </span>
      <span v-if="models.length > 12" class="text-[11px] text-gray-500 dark:text-gray-400">
        +{{ models.length - 12 }}
      </span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { Account } from '@/types'

const props = defineProps<{
  account: Account | null
  active: boolean
  disabled?: boolean
}>()

const emit = defineEmits<{
  (e: 'busy', value: boolean): void
  (e: 'finished', accountId: number): void
}>()

const { locale } = useI18n()
const busy = ref(false)
const models = ref<string[]>([])
const message = ref('')
const severity = ref<'info' | 'warning' | 'error'>('info')

let generation = 0
let disposed = false

const copy = (zh: string, en: string) => (String(locale.value).startsWith('zh') ? zh : en)
const eligible = computed(
  () => props.active && props.account?.platform === 'openai' && props.account?.type === 'oauth'
)

function reset() {
  generation++
  busy.value = false
  models.value = []
  message.value = ''
  severity.value = 'info'
  emit('busy', false)
}

watch(
  () => [props.active, props.account?.id, props.account?.platform, props.account?.type],
  reset,
  { flush: 'sync' }
)

onUnmounted(() => {
  disposed = true
  generation++
  emit('busy', false)
})

async function synchronize() {
  const id = props.account?.id
  if (busy.value || props.disabled || !eligible.value || !id) return
  const token = ++generation
  const current = () => !disposed && token === generation && eligible.value && props.account?.id === id
  busy.value = true
  message.value = ''
  models.value = []
  emit('busy', true)
  try {
    const result = await adminAPI.accounts.syncUpstreamModels(id)
    if (!current()) return
    const list = Array.isArray(result?.models) ? result.models : []
    models.value = [...new Set(list.filter((v): v is string => typeof v === 'string').map((v) => v.trim()).filter(Boolean))]
    const warnings = Array.isArray(result?.warnings) ? result.warnings : []
    const incomplete = warnings.some((w) => w.code === 'upstream_model_metadata_incomplete')
    const partial = warnings.some((w) => w.code === 'upstream_model_metadata_partial')
    const fallback = result?.discovery_source === 'configured_fallback'
    severity.value = incomplete || partial || fallback ? 'warning' : 'info'
    message.value =
      models.value.length > 0
        ? copy(`已同步 ${models.value.length} 个模型目录`, `Synced ${models.value.length} models`)
        : copy('同步完成，未返回新模型', 'Sync completed with no models returned')
    emit('finished', id)
  } catch (err) {
    if (!current()) return
    severity.value = 'error'
    message.value = extractApiErrorMessage(err, copy('模型同步失败', 'Failed to sync upstream models'))
  } finally {
    if (current()) {
      busy.value = false
      emit('busy', false)
    }
  }
}
</script>
