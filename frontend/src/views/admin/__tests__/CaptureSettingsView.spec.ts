import { reactive } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import CaptureSettingsView from '../CaptureSettingsView.vue'

const { updateCaptureSettings, getCaptureHealthHistory, getGroups, showSuccess, showError } = vi.hoisted(() => ({
  updateCaptureSettings: vi.fn(),
  getCaptureHealthHistory: vi.fn(),
  getGroups: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

const captureSettings = reactive({
  policy: {
    version: 1 as const,
    enabled: false,
    platforms: { anthropic: true, kiro: true, openai: false },
    outcomes: { success: true, terminal_error: true },
    content: {
      raw_request: true,
      raw_response: true,
      request_headers: true,
      response_headers: true,
    },
    group_ids: [] as number[],
    user_ids: [] as number[],
  },
  provisioned: false,
  ready: false,
  initialization_error: '',
  addresses: [] as string[],
  database: 'llm_archive',
  table: 'model_call_archive',
  capacity: {
    max_body_bytes: 8_388_608,
    max_queue_bytes: 134_217_728,
    queue_size: 512,
    worker_count: 2,
    writer_queue_size: 1024,
    overflow_policy: 'drop',
    overflow_sample_percent: 0,
    batch_max_size: 100,
    batch_max_interval_ms: 2000,
  },
  health: {
    started_at: '2026-08-11T00:00:00Z',
    submitted_records: 12,
    accepted_records: 11,
    written_records: 10,
    dropped_records: 2,
    dropped_bytes: 2048,
    dropped_by_reason: {},
    worker_queue: { current: 1, peak: 4, capacity: 512 },
    writer_queue: { current: 2, peak: 8, capacity: 1024 },
    in_flight_bytes: { current: 4096, peak: 8192, capacity: 134_217_728 },
    last_drop_reason: 'worker_queue_full',
    last_error: '',
    recent_incidents: [],
  },
})

const captureStore = {
  settings: captureSettings,
  loading: false,
  startPolling: vi.fn(async () => undefined),
  stopPolling: vi.fn(),
  refresh: vi.fn(async () => captureSettings),
  applySettings: vi.fn(),
  acknowledgeLoss: vi.fn(),
}

vi.mock('@/api/admin', () => ({
  adminAPI: {
    capture: { updateCaptureSettings, getCaptureHealthHistory },
    groups: { getAll: getGroups },
  },
}))

vi.mock('@/stores', () => ({
  useCaptureHealthStore: () => captureStore,
  useAppStore: () => ({ showSuccess, showError }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const ToggleStub = {
  props: ['modelValue', 'disabled'],
  emits: ['update:modelValue'],
  template: '<button role="switch" :aria-checked="modelValue" :disabled="disabled" @click="$emit(\'update:modelValue\', !modelValue)" />',
}

describe('CaptureSettingsView', () => {
  beforeEach(() => {
    captureSettings.policy.enabled = false
    captureSettings.policy.platforms.openai = false
    captureSettings.provisioned = false
    captureSettings.ready = false
    updateCaptureSettings.mockReset()
    getCaptureHealthHistory.mockReset()
    getGroups.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
    captureStore.startPolling.mockClear()
    captureStore.stopPolling.mockClear()
    captureStore.applySettings.mockClear()
    captureStore.acknowledgeLoss.mockClear()
    getGroups.mockResolvedValue([])
    getCaptureHealthHistory.mockResolvedValue({
      range: '24h',
      start: '2026-08-10T00:00:00Z',
      end: '2026-08-11T00:00:00Z',
      events: [{
        minute_bucket: '2026-08-10T23:00:00Z',
        instance_id: 'app-1',
        reason: 'worker_queue_full',
        dropped_records: 2,
        dropped_bytes: 2048,
        worker_queue_peak: 512,
        writer_queue_peak: 12,
        in_flight_bytes_peak: 4096,
        last_error: '',
      }],
    })
    updateCaptureSettings.mockResolvedValue(captureSettings)
  })

  it('renders defaults, readiness protection, health, and loss history', async () => {
    const wrapper = mount(CaptureSettingsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Toggle: ToggleStub,
          GroupSelector: true,
          OpenAIFastPolicyUserSelector: true,
          Icon: true,
        },
      },
    })
    await flushPromises()

    expect(wrapper.get('[data-test="capture-openai"] [role="switch"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.get('[data-test="capture-master"] [role="switch"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('admin.captureSettings.content.warning')
    expect(wrapper.text()).toContain('12')
    expect(wrapper.text()).toContain('worker_queue_full')
    expect(wrapper.text()).toContain('2 KB')
    expect(captureStore.acknowledgeLoss).toHaveBeenCalled()
  })

  it('saves a complete version-one policy and reloads selected history ranges', async () => {
    captureSettings.provisioned = true
    captureSettings.ready = true
    const wrapper = mount(CaptureSettingsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Toggle: ToggleStub,
          GroupSelector: true,
          OpenAIFastPolicyUserSelector: true,
          Icon: true,
        },
      },
    })
    await flushPromises()

    await wrapper.get('[data-test="capture-openai"] [role="switch"]').trigger('click')
    await wrapper.get('[data-test="capture-save"]').trigger('click')
    await flushPromises()

    expect(updateCaptureSettings).toHaveBeenCalledWith(expect.objectContaining({
      version: 1,
      enabled: false,
      platforms: { anthropic: true, kiro: true, openai: true },
      outcomes: { success: true, terminal_error: true },
      content: {
        raw_request: true,
        raw_response: true,
        request_headers: true,
        response_headers: true,
      },
      group_ids: [],
      user_ids: [],
    }))

    await wrapper.get('[data-test="history-7d"]').trigger('click')
    await flushPromises()
    expect(getCaptureHealthHistory).toHaveBeenLastCalledWith('7d')
  })
})
