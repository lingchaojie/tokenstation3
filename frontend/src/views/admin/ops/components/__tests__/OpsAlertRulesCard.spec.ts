import { describe, expect, it, vi } from 'vitest'
import { defineComponent, nextTick } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import en from '@/i18n/locales/en'
import zh from '@/i18n/locales/zh'
import OpsAlertRulesCard from '../OpsAlertRulesCard.vue'

const mockListAlertRules = vi.fn().mockResolvedValue([])
const mockGetGroups = vi.fn().mockResolvedValue([])

vi.mock('@/api/admin/ops', () => ({
  opsAPI: {
    listAlertRules: (...args: unknown[]) => mockListAlertRules(...args)
  }
}))

vi.mock('@/api', () => ({
  adminAPI: {
    groups: {
      getAll: (...args: unknown[]) => mockGetGroups(...args)
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn()
  })
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        if (key === 'admin.ops.alertRules.hints.recommended' && params) {
          return `recommended:${params.operator}:${params.threshold}:${params.unit ?? ''}`
        }
        return key
      }
    })
  }
})

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show" class="dialog-stub"><slot /><slot name="footer" /></div>'
})

const SelectStub = defineComponent({
  name: 'SelectControlStub',
  props: {
    modelValue: { type: [String, Number, Boolean], default: null },
    options: { type: Array, default: () => [] }
  },
  emits: ['update:modelValue'],
  template: '<div class="select-stub" />'
})

const ConfirmDialogStub = defineComponent({
  name: 'ConfirmDialog',
  template: '<div />'
})

function localeValue(locale: Record<string, any>, path: string): unknown {
  return path.split('.').reduce<unknown>((value, key) => {
    if (!value || typeof value !== 'object') return undefined
    return (value as Record<string, unknown>)[key]
  }, locale)
}

describe('OpsAlertRulesCard capture metrics', () => {
  it('lists capture metrics in their own group and shows the recommended readiness rule', async () => {
    const wrapper = mount(OpsAlertRulesCard, {
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          ConfirmDialog: ConfirmDialogStub,
          Select: SelectStub
        }
      }
    })
    await flushPromises()

    const createButton = wrapper
      .findAll('button')
      .find((button) => button.text() === 'admin.ops.alertRules.create')
    expect(createButton).toBeDefined()
    await createButton!.trigger('click')

    const metricSelect = wrapper.findAllComponents(SelectStub).find((select) =>
      (select.props('options') as Array<{ value: unknown }>).some(
        (option) => option.value === '__group__capture'
      )
    )
    expect(metricSelect).toBeDefined()

    const options = metricSelect!.props('options') as Array<{ value: unknown; disabled?: boolean }>
    expect(options.filter((option) => String(option.value).startsWith('__group__')).map((option) => option.value)).toEqual([
      '__group__system',
      '__group__capture',
      '__group__group',
      '__group__account'
    ])
    expect(options.filter((option) => String(option.value).startsWith('capture_')).map((option) => option.value)).toEqual([
      'capture_ready',
      'capture_dropped_records',
      'capture_writer_failures'
    ])

    await metricSelect!.vm.$emit('update:modelValue', 'capture_ready')
    await nextTick()

    expect(wrapper.text()).toContain('admin.ops.alertRules.metricDescriptions.captureReady')
    expect(wrapper.text()).toContain('recommended:<:1:')
  })

  it.each([
    'admin.ops.alertRules.metricGroups.capture',
    'admin.ops.alertRules.metrics.captureReady',
    'admin.ops.alertRules.metrics.captureDroppedRecords',
    'admin.ops.alertRules.metrics.captureWriterFailures',
    'admin.ops.alertRules.metricDescriptions.captureReady',
    'admin.ops.alertRules.metricDescriptions.captureDroppedRecords',
    'admin.ops.alertRules.metricDescriptions.captureWriterFailures'
  ])('defines %s in both locales', (key) => {
    expect(localeValue(en, key)).toEqual(expect.any(String))
    expect(localeValue(zh, key)).toEqual(expect.any(String))
  })
})
