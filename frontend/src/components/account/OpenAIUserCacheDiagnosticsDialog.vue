<template>
  <BaseDialog :show="true" :title="t(prefix + 'title')" width="wide" @close="emit('close')">
    <div class="space-y-4">
      <p class="text-sm text-gray-600 dark:text-gray-300">{{ t(prefix + 'description') }}</p>
      <p class="rounded-lg bg-gray-50 p-3 text-xs text-gray-600 dark:bg-dark-700 dark:text-gray-300">{{ t(prefix + 'limits') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      <form id="cache-diagnostic-form" class="grid grid-cols-1 gap-3 sm:grid-cols-2" @submit.prevent="start">
        <label class="text-sm">{{ t(prefix + 'user') }}
          <input v-model.number="form.user_id" class="input mt-1 w-full" data-testid="diagnostic-user" type="number" min="1" step="1" required :disabled="busy" />
        </label>
        <label class="text-sm">{{ t(prefix + 'model') }}
          <input v-model="form.model" class="input mt-1 w-full" data-testid="diagnostic-model" maxlength="100" required :disabled="busy" />
        </label>
        <label class="text-sm">{{ t(prefix + 'minutes') }}
          <input v-model.number="form.minutes" class="input mt-1 w-full" data-testid="diagnostic-minutes" type="number" min="1" max="30" step="1" required :disabled="busy" />
        </label>
        <label class="text-sm">{{ t(prefix + 'samples') }}
          <input v-model.number="form.max_samples" class="input mt-1 w-full" data-testid="diagnostic-samples" type="number" min="1" max="64" step="1" required :disabled="busy" />
        </label>
      </form>
      <template v-if="status">
        <div class="rounded-lg border border-gray-200 p-3 text-sm dark:border-dark-600" role="status">
          <p>{{ t(prefix + (expired ? 'off' : status.active ? 'active' : status.config ? 'full' : 'off')) }}</p>
          <p v-if="status.config && !expired" class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t(prefix + 'scope', { user: status.config.user_id, model: status.config.model, count: status.admitted, limit: status.config.max_samples }) }}
            · {{ t(prefix + 'expires') }} {{ status.expires_at ? new Date(status.expires_at).toLocaleString() : '—' }}
          </p>
        </div>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t(prefix + 'outcomeNote') }}</p>
        <div v-if="!expired && status.samples?.length" class="max-h-96 space-y-2 overflow-auto">
          <details v-for="(sample, index) in status.samples" :key="index" class="rounded-lg border border-gray-200 p-3 text-xs dark:border-dark-600">
            <summary class="cursor-pointer leading-6">
              {{ new Date(sample.started_at).toLocaleTimeString() }} · {{ sample.protocol }} ·
              {{ t(prefix + 'outcomes.' + sample.outcome) }} · {{ t(prefix + 'attempts', { count: sample.forward_attempts }) }}
              · {{ t(prefix + 'account') }} {{ sample.last_account_id ?? '—' }}
            </summary>
            <p class="my-2 tabular-nums">
              {{ t(prefix + 'tokens') }} {{ sample.forward_usage?.input_tokens ?? '—' }} / {{ sample.forward_usage?.output_tokens ?? '—' }}
              · {{ t(prefix + 'cached') }} {{ sample.cache_read_known && !sample.usage_unavailable ? (sample.forward_usage?.cache_read_input_tokens ?? 0) : t(prefix + 'unknown') }}
              · TTFT {{ sample.first_token_ms ?? '—' }} ms
            </p>
            <p class="break-all text-gray-500 dark:text-gray-400">{{ t(prefix + 'ingress') }} · {{ bodySummary(sample.ingress) }}</p>
            <p v-for="(egress, egressIndex) in sample.http_egress" :key="egressIndex" class="mt-1 break-all text-gray-500 dark:text-gray-400">
              {{ t(prefix + 'egress') }} · {{ t(prefix + 'account') }} {{ egress.account_id }} · {{ egress.body ? bodySummary(egress.body) : '—' }}
            </p>
          </details>
        </div>
        <p v-else class="text-sm text-gray-500 dark:text-gray-400">{{ t(prefix + 'empty') }}</p>
      </template>
    </div>
    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="busy" data-testid="diagnostic-refresh" @click="reload">{{ t(prefix + 'refresh') }}</button>
      <button type="button" class="btn btn-secondary" :disabled="busy || !status?.config || expired" data-testid="diagnostic-stop" @click="stop">{{ t(prefix + 'stop') }}</button>
      <button type="submit" form="cache-diagnostic-form" class="btn btn-primary" :disabled="busy || !valid" data-testid="diagnostic-start">{{ t(prefix + 'start') }}</button>
    </template>
  </BaseDialog>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getCacheDiagnostics, startCacheDiagnostics, stopCacheDiagnostics, type CacheDiagnosticBody, type CacheDiagnosticConfig, type CacheDiagnosticStatus } from '@/api/admin/openaiUserCacheDiagnostics'
import { useStepUp, isStepUpBlocked, isStepUpCancelled, stepUpBlockReason } from '@/composables/useStepUp'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'

const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const prefix = 'admin.accounts.cacheDiagnostics.'
const stepUp = useStepUp()
const form = reactive<CacheDiagnosticConfig>({ user_id: 0, model: '', minutes: 10, max_samples: 32 })
const status = ref<CacheDiagnosticStatus | null>(null)
const busy = ref(false), error = ref(''), now = ref(Date.now())
let expiryTimer: ReturnType<typeof setTimeout> | undefined
let alive = true
const expired = computed(() => !!status.value?.expires_at && new Date(status.value.expires_at).getTime() <= now.value)
const valid = computed(() => Number.isSafeInteger(form.user_id) && form.user_id > 0 &&
  /^[a-zA-Z0-9][a-zA-Z0-9_.:/+-]{0,99}$/.test(form.model) &&
  Number.isInteger(form.minutes) && form.minutes >= 1 && form.minutes <= 30 &&
  Number.isInteger(form.max_samples) && form.max_samples >= 1 && form.max_samples <= 64)

function bodySummary(body: CacheDiagnosticBody) {
  return `${body.bytes} B · HMAC ${body.digest ?? '—'} · cache-key HMAC ${body.cache_key_digest ?? '—'}`
}
function setStatus(next: CacheDiagnosticStatus) {
  clearTimeout(expiryTimer)
  status.value = next
  now.value = Date.now()
  if (next.expires_at) {
    expiryTimer = setTimeout(() => { status.value = { active: false, admitted: 0 }; now.value = Date.now() }, Math.max(0, new Date(next.expires_at).getTime() - now.value))
  }
}
async function run(action: () => Promise<CacheDiagnosticStatus>) {
  if (busy.value) return
  busy.value = true; error.value = ''
  try {
    const next = await stepUp.run(action)
    if (alive) setStatus(next)
  } catch (err) {
    if (alive && !isStepUpCancelled(err)) error.value = isStepUpBlocked(err)
      ? t(stepUpBlockReason(err) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' ? 'stepUp.adminApiKeyForbidden' : 'stepUp.notEnabled')
      : t(prefix + 'failed')
  } finally { busy.value = false }
}
function reload() { return run(getCacheDiagnostics) }
function start() { if (valid.value) return run(() => startCacheDiagnostics({ ...form })) }
function stop() { return run(stopCacheDiagnostics) }
onMounted(reload)
onUnmounted(() => { alive = false; clearTimeout(expiryTimer); stepUp.onCancel() })
</script>
