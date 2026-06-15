import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import type { ApiKey, PublicSettings } from '@/types'

const {
  authGetPublicSettings,
  copyToClipboard,
  i18nMessages,
  isCurrentStep,
  keysCreate,
  keysDelete,
  keysList,
  keysToggleStatus,
  keysUpdate,
  nextStep,
  showError,
  showInfo,
  showSuccess,
  showWarning,
  usageGetDashboardApiKeysUsage
} = vi.hoisted(() => ({
  authGetPublicSettings: vi.fn(),
  copyToClipboard: vi.fn().mockResolvedValue(true),
  i18nMessages: {
    'keys.cannotImportUnconfiguredKey': 'Cannot import an unconfigured key',
    'keys.ccsClientSelect.claudeCode': 'Claude Code',
    'keys.ccsClientSelect.claudeCodeDesc': 'Import as Claude Code configuration',
    'keys.ccsClientSelect.description': 'Please select the client type to import to CC-Switch:',
    'keys.ccsClientSelect.title': 'Select Client',
    'keys.importToCcSwitch': 'Import to CCS',
    'keys.useKey': 'Use Key'
  } as Record<string, string>,
  isCurrentStep: vi.fn(() => false),
  keysCreate: vi.fn(),
  keysDelete: vi.fn(),
  keysList: vi.fn(),
  keysToggleStatus: vi.fn(),
  keysUpdate: vi.fn(),
  nextStep: vi.fn(),
  showError: vi.fn(),
  showInfo: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn(),
  usageGetDashboardApiKeysUsage: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: {
    post: vi.fn(async (_url: string, payload: unknown) => ({ data: payload }))
  }
}))

vi.mock('@/api', () => ({
  authAPI: {
    getPublicSettings: authGetPublicSettings
  },
  keysAPI: {
    create: keysCreate,
    delete: keysDelete,
    list: keysList,
    toggleStatus: keysToggleStatus,
    update: keysUpdate
  },
  usageAPI: {
    getDashboardApiKeysUsage: usageGetDashboardApiKeysUsage
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showInfo, showSuccess, showWarning })
}))

vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({ isCurrentStep, nextStep })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard })
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => i18nMessages[key] ?? key
    })
  }
})

import { apiClient } from '@/api/client'
import { create } from '@/api/keys'
import KeysView from '../KeysView.vue'

const keysViewSource = readFileSync(
  resolve(dirname(fileURLToPath(import.meta.url)), '..', 'KeysView.vue'),
  'utf8'
)

const AppLayoutStub = { template: '<div><slot /></div>' }
const BaseDialogStub = {
  props: ['show', 'title'],
  template: '<section v-if="show" data-testid="base-dialog"><h2>{{ title }}</h2><slot /><slot name="footer" /></section>'
}
const TablePageLayoutStub = {
  template: '<div><slot name="filters" /><slot name="actions" /><slot name="table" /><slot name="pagination" /></div>'
}
const DataTableStub = {
  props: ['data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.id">
        <slot name="cell-actions" :row="row" />
      </div>
      <slot v-if="data.length === 0" name="empty" />
    </div>
  `
}
const UseKeyModalStub = {
  props: ['show', 'apiKey', 'baseUrl', 'platform', 'allowMessagesDispatch'],
  template: `
    <div
      data-testid="use-key-modal"
      :data-show="String(show)"
      :data-api-key="apiKey"
      :data-platform="platform || ''"
      :data-allow-messages-dispatch="String(allowMessagesDispatch)"
    />
  `
}

const makePublicSettings = (overrides: Partial<PublicSettings> = {}): PublicSettings => ({
  api_base_url: 'https://api.example.com',
  hide_ccs_import_button: false,
  site_name: 'Sub2API',
  ...overrides
}) as PublicSettings

const makeApiKey = (overrides: Partial<ApiKey> = {}): ApiKey => ({
  id: 1,
  user_id: 1,
  key: 'sk-test',
  name: 'Test key',
  group_id: null,
  key_type: 'anthropic',
  group_binding_mode: 'default_follow',
  status: 'active',
  ip_whitelist: [],
  ip_blacklist: [],
  last_used_at: null,
  quota: 0,
  quota_used: 0,
  expires_at: null,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  rate_limit_5h: 0,
  rate_limit_1d: 0,
  rate_limit_7d: 0,
  usage_5h: 0,
  usage_1d: 0,
  usage_7d: 0,
  window_5h_start: null,
  window_1d_start: null,
  window_7d_start: null,
  reset_5h_at: null,
  reset_1d_at: null,
  reset_7d_at: null,
  ...overrides
})

const listResponse = (items: ApiKey[]) => ({
  items,
  total: items.length,
  page: 1,
  page_size: 20,
  pages: items.length > 0 ? 1 : 0
})

const mountKeysView = () => mount(KeysView, {
  global: {
    stubs: {
      AppLayout: AppLayoutStub,
      TablePageLayout: TablePageLayoutStub,
      DataTable: DataTableStub,
      Pagination: true,
      BaseDialog: BaseDialogStub,
      ConfirmDialog: true,
      EmptyState: true,
      Select: true,
      SearchInput: true,
      Icon: true,
      UseKeyModal: UseKeyModalStub,
      EndpointPopover: true,
      Teleport: true
    }
  }
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.spyOn(window, 'open').mockReturnValue(null)
  vi.spyOn(document, 'hasFocus').mockReturnValue(false)
  authGetPublicSettings.mockResolvedValue(makePublicSettings())
  keysList.mockResolvedValue(listResponse([]))
  usageGetDashboardApiKeysUsage.mockResolvedValue({ stats: {} })
})

describe('keysAPI.create provider routing payload', () => {
  it('sends key_type and omits group_id for normal user provider keys', async () => {
    await create('Provider key', 'openai')

    expect(apiClient.post).toHaveBeenCalledWith('/keys', {
      name: 'Provider key',
      key_type: 'openai'
    })
  })
})

describe('KeysView provider routing actions', () => {
  it('passes the selected key group messages dispatch flag to UseKeyModal', async () => {
    const key = makeApiKey({
      id: 42,
      key: 'sk-openai',
      key_type: 'openai',
      group_id: 7,
      group: {
        id: 7,
        platform: 'openai',
        allow_messages_dispatch: true
      } as ApiKey['group']
    })
    keysList.mockResolvedValue(listResponse([key]))

    const wrapper = mountKeysView()
    await flushPromises()
    await nextTick()

    const useKeyButton = wrapper.findAll('button').find((button) => button.text().includes('Use Key'))
    expect(useKeyButton).toBeDefined()
    await useKeyButton!.trigger('click')
    await nextTick()

    const modal = wrapper.find('[data-testid="use-key-modal"]')
    expect(modal.attributes('data-show')).toBe('true')
    expect(modal.attributes('data-api-key')).toBe('sk-openai')
    expect(modal.attributes('data-platform')).toBe('openai')
    expect(modal.attributes('data-allow-messages-dispatch')).toBe('true')

    wrapper.unmount()
  })

  it('does not expose the unavailable Gemini CC-Switch import route', () => {
    expect(keysViewSource).not.toContain("handleCcsClientSelect('gemini')")
    expect(keysViewSource).not.toContain("t('keys.ccsClientSelect.geminiCli')")
    expect(keysViewSource).not.toContain("t('keys.ccsClientSelect.geminiCliDesc')")
  })

  it('rejects CCS import for unknown key types instead of importing them as Anthropic', async () => {
    const key = makeApiKey({
      id: 43,
      key: 'sk-unknown',
      key_type: 'unknown'
    })
    keysList.mockResolvedValue(listResponse([key]))

    const wrapper = mountKeysView()
    await flushPromises()
    await nextTick()

    const importButton = wrapper.findAll('button').find((button) => button.text().includes('Import to CCS'))
    expect(importButton).toBeDefined()
    await importButton!.trigger('click')

    expect(window.open).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('Cannot import an unconfigured key')

    wrapper.unmount()
  })
})
