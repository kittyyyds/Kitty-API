import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import zh from '@/i18n/locales/zh'
import en from '@/i18n/locales/en'
import Dialog from '../OpenAIUserCacheDiagnosticsDialog.vue'
import { getCacheDiagnostics, startCacheDiagnostics, stopCacheDiagnostics, type CacheDiagnosticStatus } from '@/api/admin/openaiUserCacheDiagnostics'

vi.mock('@/api/admin/openaiUserCacheDiagnostics', () => ({
  getCacheDiagnostics: vi.fn(), startCacheDiagnostics: vi.fn(), stopCacheDiagnostics: vi.fn()
}))

function open() {
  return mount(Dialog, { global: {
    plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh, en } })],
    stubs: { BaseDialog: { template: '<section><slot/><slot name="footer"/></section>' }, TotpStepUpDialog: true }
  } })
}

const fullStatus = (): CacheDiagnosticStatus => ({
  active: false, admitted: 1, config: { user_id: 7, model: 'gpt-6.2', minutes: 1, max_samples: 1 },
  expires_at: new Date(Date.now() + 60_000).toISOString(),
  samples: [{ started_at: new Date().toISOString(), protocol: 'responses', ingress: { bytes: 12, digest: 'hashed-only' },
    forward_attempts: 1, outcome: 'forward_completed', cache_read_known: false, usage_unavailable: false,
    forward_usage: { input_tokens: 3, output_tokens: 4 } }]
})

describe('OpenAI user cache diagnostics', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(getCacheDiagnostics).mockResolvedValue({ active: false, admitted: 0 })
    vi.mocked(startCacheDiagnostics).mockResolvedValue(fullStatus())
    vi.mocked(stopCacheDiagnostics).mockResolvedValue({ active: false, admitted: 0 })
  })
  afterEach(() => vi.useRealTimers())

  it('requires one exact model and bounded scope before starting', async () => {
    const wrapper = open(); await flushPromises()
    expect(wrapper.get('[data-testid="diagnostic-start"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="diagnostic-user"]').setValue(7)
    await wrapper.get('[data-testid="diagnostic-model"]').setValue('gpt-*')
    await wrapper.get('#cache-diagnostic-form').trigger('submit.prevent')
    expect(startCacheDiagnostics).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="diagnostic-model"]').setValue('gpt-6.2')
    await wrapper.get('[data-testid="diagnostic-minutes"]').setValue(31)
    await wrapper.get('#cache-diagnostic-form').trigger('submit.prevent')
    expect(startCacheDiagnostics).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="diagnostic-minutes"]').setValue(1)
    await wrapper.get('[data-testid="diagnostic-samples"]').setValue(1)
    await wrapper.get('#cache-diagnostic-form').trigger('submit.prevent'); await flushPromises()
    expect(startCacheDiagnostics).toHaveBeenCalledTimes(1)
    expect(startCacheDiagnostics).toHaveBeenCalledWith({ user_id: 7, model: 'gpt-6.2', minutes: 1, max_samples: 1 })
    expect(wrapper.text()).toContain('未知')
    expect(wrapper.text()).toContain('不单独证明客户端完整收到回答')
    await wrapper.get('[data-testid="diagnostic-stop"]').trigger('click'); await flushPromises()
    expect(stopCacheDiagnostics).toHaveBeenCalledOnce()
    expect(wrapper.text()).not.toContain('hashed-only')
    wrapper.unmount()
  })

  it('clears visible results at expiry and keeps known zero cache distinct', async () => {
    vi.useFakeTimers()
    const state = fullStatus(); state.samples![0].cache_read_known = true
    vi.mocked(getCacheDiagnostics).mockResolvedValue(state)
    const wrapper = open(); await flushPromises()
    expect(wrapper.text()).toContain('hashed-only')
    expect(wrapper.text()).toContain('缓存读取 0')
    await vi.advanceTimersByTimeAsync(60_001)
    expect(wrapper.text()).not.toContain('hashed-only')
    expect(wrapper.get('[data-testid="diagnostic-stop"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('explains step-up blocking without displaying raw upstream errors', async () => {
    vi.mocked(getCacheDiagnostics).mockRejectedValueOnce({ reason: 'STEP_UP_TOTP_NOT_ENABLED', message: 'private-server-text' })
    const wrapper = open(); await flushPromises()
    expect(wrapper.text()).not.toContain('private-server-text')
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(startCacheDiagnostics).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
