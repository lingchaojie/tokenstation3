import { reactive } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import CaptureSettingsView from '../CaptureSettingsView.vue'
import enCaptureSettings from '@/i18n/locales/en/admin/captureSettings'
import zhCaptureSettings from '@/i18n/locales/zh/admin/captureSettings'

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
    platforms: { anthropic: true, kiro: true, openai: false, gemini: true, antigravity: true, grok: true, cursor: true },
    outcomes: { success: true, terminal_error: true },
    content: {
      raw_request: true,
      raw_response: true,
      request_headers: true,
      response_headers: true,
    },
    model_allowlists: { anthropic: ['claude-fable-5', 'claude-opus-5'], kiro: ['claude-fable-5', 'claude-opus-5'] },
    group_ids: [] as number[],
    user_ids: [] as number[],
  },
  provisioned: true,
  ready: true,
  sidecar_running: true,
  spool_ready: true,
  delivery_ready: false,
  initialization_error: '',
  database: 'llm_archive',
  table: 'model_call_archive',
  spool_used_bytes: 9 * 2 ** 30,
  spool_max_bytes: 12 * 2 ** 30,
  spool_min_free_bytes: 8 * 2 ** 30,
  filesystem_free_bytes: 10 * 2 ** 30,
  ready_records: 42,
  oldest_ready_age_seconds: 90,
  current_batch_id: '19b5c4f7-b8f7-45f1-b45f-dc17fdbb2d9d',
  sidecar_restart_count: 2,
  upload_retries: 7,
  last_upload_at: '2026-08-11T00:02:00Z',
  health_source_id: 'ebd8f2e7-aac1-479d-a1de-2b4642f12156',
  dropped_records: 2,
  dropped_by_reason: { spool_cap: 2 },
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
  const translations: Record<string, string> = {
    'admin.captureSettings.infrastructure.staticOff': 'Sidecar 未启动',
    'admin.captureSettings.infrastructure.sidecarDown': 'Sidecar 进程异常',
    'admin.captureSettings.infrastructure.ready': '本地 Spool 正常',
    'admin.captureSettings.runtime.draining': '不再接收新转存，现有积压仍在续传',
    'admin.captureSettings.health.deliveryDown': 'ClickHouse 传输异常',
    'admin.captureSettings.health.deliveryDownHint': '本地 Spool 正常，数据将自动续传',
    'admin.captureSettings.health.spoolCap': 'Spool 已达到硬容量上限，只丢弃新转存，正常转发不受影响',
    'admin.captureSettings.health.freeReserve': '文件系统已触发保留空间保护，只丢弃新转存，正常转发不受影响',
    'admin.captureSettings.health.spoolUsage': 'Spool 使用量',
    'admin.captureSettings.health.readyRecords': '待传记录',
    'admin.captureSettings.health.backlogAge': '最旧积压时间',
    'admin.captureSettings.health.sidecarRestarts': 'Sidecar 重启次数',
    'admin.captureSettings.health.uploadRetries': '上传重试次数',
  }
  return { ...actual, useI18n: () => ({ t: (key: string) => translations[key] ?? key }) }
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
    captureSettings.policy.platforms.cursor = true
    captureSettings.policy.model_allowlists.anthropic = ['claude-fable-5', 'claude-opus-5']
    captureSettings.policy.model_allowlists.kiro = ['claude-fable-5', 'claude-opus-5']
    captureSettings.provisioned = true
    captureSettings.ready = true
    captureSettings.sidecar_running = true
    captureSettings.spool_ready = true
    captureSettings.delivery_ready = false
    captureSettings.spool_used_bytes = 9 * 2 ** 30
    captureSettings.spool_max_bytes = 12 * 2 ** 30
    captureSettings.spool_min_free_bytes = 8 * 2 ** 30
    captureSettings.filesystem_free_bytes = 10 * 2 ** 30
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
        instance_id: 'sidecar-source',
        reason: 'spool_cap',
        dropped_records: 2,
        dropped_bytes: 2048,
        spool_used_bytes_peak: 12 * 2 ** 30,
        ready_records_peak: 42,
        oldest_ready_age_seconds_peak: 90,
        upload_retries: 7,
        sidecar_restarts: 2,
        last_error: 'capture spool reached physical cap',
      }],
    })
    updateCaptureSettings.mockResolvedValue(captureSettings)
  })

  it('distinguishes local acceptance from remote delivery and renders spool operations', async () => {
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
    expect(wrapper.get('[data-test="capture-cursor"] [role="switch"]').attributes('aria-checked')).toBe('true')
    expect(wrapper.get('[data-test="capture-master"] [role="switch"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.text()).toContain('admin.captureSettings.content.warning')
    expect(wrapper.text()).toContain('ClickHouse 传输异常')
    expect(wrapper.text()).toContain('本地 Spool 正常，数据将自动续传')
    expect(wrapper.text()).toContain('9 GiB / 12 GiB')
    expect(wrapper.text()).toContain('42')
    expect(wrapper.text()).toContain('spool_cap')
    expect(wrapper.text()).toContain('2 KB')
    expect(wrapper.text()).toContain('上传重试次数')
    expect(wrapper.text()).toContain('Sidecar 重启次数')
    expect(wrapper.text()).not.toContain('Writer queue')
    expect(wrapper.text()).not.toContain('worker_queue')
    expect(wrapper.get('[data-test="capture-history-spool"]').text()).toContain('12 GiB')
    expect(wrapper.get('[data-test="capture-history-error"]').text()).toContain('capture spool reached physical cap')
    expect(captureStore.acknowledgeLoss).toHaveBeenCalled()
  })

  it('renders static-off, runtime drain, sidecar-down, cap, and free-reserve states', async () => {
    const wrapper = mount(CaptureSettingsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' }, Toggle: ToggleStub,
          GroupSelector: true, OpenAIFastPolicyUserSelector: true, Icon: true,
        },
      },
    })
    await flushPromises()
    expect(wrapper.text()).toContain('不再接收新转存，现有积压仍在续传')

    captureSettings.provisioned = false
    captureSettings.spool_used_bytes = 0
    captureSettings.filesystem_free_bytes = 0
    await flushPromises()
    expect(wrapper.text()).toContain('Sidecar 未启动')
    expect(wrapper.text()).not.toContain('ClickHouse 传输异常')
    expect(wrapper.text()).not.toContain('Spool 已达到硬容量上限')
    expect(wrapper.text()).not.toContain('文件系统已触发保留空间保护')

    captureSettings.provisioned = true
    captureSettings.sidecar_running = false
    captureSettings.ready = false
    await flushPromises()
    expect(wrapper.text()).toContain('Sidecar 进程异常')

    captureSettings.sidecar_running = true
    captureSettings.ready = true
    captureSettings.spool_used_bytes = captureSettings.spool_max_bytes
    await flushPromises()
    expect(wrapper.text()).toContain('Spool 已达到硬容量上限')

    captureSettings.spool_used_bytes = 1
    captureSettings.filesystem_free_bytes = captureSettings.spool_min_free_bytes
    await flushPromises()
    expect(wrapper.text()).toContain('文件系统已触发保留空间保护')
  })

  it('keeps complete spool-operation copy in both locale dictionaries', () => {
    for (const locale of [enCaptureSettings.captureSettings, zhCaptureSettings.captureSettings]) {
      expect(locale.platforms.cursor).toBeTruthy()
      expect(locale.platforms.cursorDescription).toBeTruthy()
      expect(locale.infrastructure.staticOff).toBeTruthy()
      expect(locale.infrastructure.sidecarDown).toBeTruthy()
      expect(locale.runtime.draining).toBeTruthy()
      expect(locale.health.deliveryDown).toBeTruthy()
      expect(locale.health.deliveryDownHint).toBeTruthy()
      expect(locale.health.spoolCap).toBeTruthy()
      expect(locale.health.freeReserve).toBeTruthy()
      expect(locale.health.backlogAge).toBeTruthy()
      expect(locale.health.sidecarRestarts).toBeTruthy()
      expect(locale.health.uploadRetries).toBeTruthy()
      for (const reason of ['ipcUnavailable', 'ipcBackpressure', 'sidecarDown', 'spoolCap', 'spoolFreeReserve', 'spoolCorrupt', 'preCommitDisconnect']) {
        expect(locale.lossReasons[reason as keyof typeof locale.lossReasons]).toBeTruthy()
      }
    }
  })

  it('saves a complete version-one policy and reloads selected history ranges', async () => {
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
    await wrapper.get('[data-test="capture-cursor"] [role="switch"]').trigger('click')
    await wrapper.get('[data-test="capture-save"]').trigger('click')
    await flushPromises()

    expect(updateCaptureSettings).toHaveBeenCalledWith(expect.objectContaining({
      version: 1,
      enabled: false,
      platforms: { anthropic: true, kiro: true, openai: true, gemini: true, antigravity: true, grok: true, cursor: false },
      outcomes: { success: true, terminal_error: true },
      content: {
        raw_request: true,
        raw_response: true,
        request_headers: true,
        response_headers: true,
      },
      model_allowlists: { anthropic: ['claude-fable-5', 'claude-opus-5'], kiro: ['claude-fable-5', 'claude-opus-5'] },
      group_ids: [],
      user_ids: [],
    }))

    await wrapper.get('[data-test="history-7d"]').trigger('click')
    await flushPromises()
    expect(getCaptureHealthHistory).toHaveBeenLastCalledWith('7d')
  })

  it('normalizes the Anthropic and Kiro model allowlists before saving', async () => {
    const wrapper = mount(CaptureSettingsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' }, Toggle: ToggleStub,
          GroupSelector: true, OpenAIFastPolicyUserSelector: true, Icon: true,
        },
      },
    })
    await flushPromises()

    await wrapper.get('[data-test="capture-models-anthropic"]').setValue(' Claude-Opus-5, claude-fable-5\nclaude-opus-5 ')
    await wrapper.get('[data-test="capture-models-kiro"]').setValue('claude-fable-5')
    await wrapper.get('[data-test="capture-save"]').trigger('click')
    await flushPromises()

    expect(updateCaptureSettings).toHaveBeenCalledWith(expect.objectContaining({
      model_allowlists: {
        anthropic: ['claude-fable-5', 'claude-opus-5'],
        kiro: ['claude-fable-5'],
      },
    }))
  })
})
