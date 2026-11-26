import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import KiroModelSyncButton from '../KiroModelSyncButton.vue'

const { syncUpstreamModels, syncUpstreamModelsPreview } = vi.hoisted(() => ({
  syncUpstreamModels: vi.fn(),
  syncUpstreamModelsPreview: vi.fn(),
}))

vi.mock('@/api/admin/accounts', () => ({
  accountsAPI: { syncUpstreamModels, syncUpstreamModelsPreview },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showInfo: vi.fn(), showSuccess: vi.fn(), showError: vi.fn() }),
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

describe('KiroModelSyncButton', () => {
  it('uses configured preview credentials for a new account', async () => {
    syncUpstreamModelsPreview.mockResolvedValueOnce({
      models: ['claude-opus-6-1-2-thinking'],
      metadata: { 'claude-opus-6-1-2-thinking': { id: 'claude-opus-6.1.2' } },
    })
    const credentials = {
      platform: 'kiro',
      type: 'apikey',
      api_key: 'test-key',
      api_region: 'eu-central-1',
      proxy_id: 12,
    }
    const wrapper = mount(KiroModelSyncButton, { props: { credentials, modelValue: [] } })
    await wrapper.get('button').trigger('click')
    await flushPromises()

    expect(syncUpstreamModelsPreview).toHaveBeenCalledWith(credentials)
    expect(wrapper.emitted('update:modelValue')).toEqual([
      [[{ from: 'claude-opus-6-1-2-thinking', to: 'claude-opus-6.1.2' }]],
    ])
  })

  it('uses the saved account for edit and preserves the configured mapping', async () => {
    syncUpstreamModels.mockResolvedValueOnce({ models: ['kimi-k3', 'relay.model'] })
    const priorMapping = { from: 'kimi-k3', to: 'custom-kimi' }
    const wrapper = mount(KiroModelSyncButton, { props: { accountId: 52, modelValue: [priorMapping] } })
    await wrapper.get('button').trigger('click')
    await flushPromises()

    expect(syncUpstreamModels).toHaveBeenCalledWith(52)
    expect(wrapper.emitted('update:modelValue')).toEqual([
      [[priorMapping, { from: 'relay.model', to: 'relay.model' }]],
    ])
  })
})
