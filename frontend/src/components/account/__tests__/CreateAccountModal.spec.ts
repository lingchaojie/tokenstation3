import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const {
  createAccountMock,
  importCodexSessionMock,
  createOpenAICodexPATMock,
  kiroImportTokenMock,
} = vi.hoisted(() => ({
  createAccountMock: vi.fn(),
  importCodexSessionMock: vi.fn(),
  createOpenAICodexPATMock: vi.fn(),
  kiroImportTokenMock: vi.fn(),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showWarning: vi.fn(),
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ isSimpleMode: true }),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      create: createAccountMock,
      checkMixedChannelRisk: vi.fn().mockResolvedValue({ has_risk: false }),
      importCodexSession: importCodexSessionMock,
      createOpenAICodexPAT: createOpenAICodexPATMock,
    },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({}),
    },
    tlsFingerprintProfiles: {
      list: vi.fn().mockResolvedValue([]),
    },
    kiro: {
      importToken: kiroImportTokenMock,
    },
  },
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn().mockResolvedValue([]),
  getKiroDefaultModelMapping: vi.fn().mockResolvedValue([]),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

import CreateAccountModal from '../CreateAccountModal.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})

const OAuthAuthorizationFlowStub = defineComponent({
  name: 'OAuthAuthorizationFlow',
  props: {
    showManualOption: Boolean,
    showCodexSessionImportOption: Boolean,
    showAgentIdentityOption: Boolean,
    showCodexPatOption: Boolean,
    initialInputMethod: String,
  },
  data: () => ({ inputMethod: 'manual' }),
  emits: ['import-codex-session', 'import-codex-pat'],
  template: `
    <div>
      <button data-testid="import-codex-session" @click="$emit('import-codex-session', 'session-json')">session</button>
      <button data-testid="import-codex-pat" @click="$emit('import-codex-pat', 'pat-token')">pat</button>
    </div>
  `,
})

function mountModal() {
  return mount(CreateAccountModal, {
    props: { show: true, proxies: [], groups: [] },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        OAuthAuthorizationFlow: OAuthAuthorizationFlowStub,
        ConfirmDialog: true,
        Select: true,
        Icon: true,
        PlatformIcon: true,
        ProxySelector: true,
        ProxyAdBanner: true,
        GroupSelector: true,
        ModelWhitelistSelector: true,
        QuotaLimitCard: true,
      },
    },
  })
}

async function selectButtonByText(wrapper: ReturnType<typeof mountModal>, text: string) {
  const button = wrapper.findAll('button').find((candidate) => candidate.text().includes(text))
  expect(button).toBeDefined()
  await button?.trigger('click')
}

async function submitApiKeyAccount(platform: 'openai' | 'anthropic', enableLongContextBilling = false) {
  const wrapper = mountModal()
  await selectButtonByText(wrapper, platform === 'openai' ? 'OpenAI' : 'admin.accounts.claudeConsole')
  if (platform === 'openai') {
    await selectButtonByText(wrapper, 'API Key')
  }
  await wrapper.get('form#create-account-form input[type="text"]').setValue(`${platform} account`)
  await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
  if (enableLongContextBilling) {
    await wrapper.get('[data-testid="openai-long-context-billing-toggle"]').trigger('click')
  }
  await wrapper.get('form#create-account-form').trigger('submit.prevent')
  await flushPromises()
}

async function openCodexImportStep(toggleClicks = 0) {
  const wrapper = mountModal()
  await selectButtonByText(wrapper, 'OpenAI')
  for (let click = 0; click < toggleClicks; click += 1) {
    await wrapper.get('[data-testid="openai-long-context-billing-toggle"]').trigger('click')
  }
  await wrapper.get('form#create-account-form input[type="text"]').setValue('Codex import')
  await wrapper.get('form#create-account-form').trigger('submit.prevent')
  return wrapper
}

async function openKiroImportStep() {
  const wrapper = mountModal()
  await selectButtonByText(wrapper, 'Kiro')
  await selectButtonByText(wrapper, 'admin.accounts.oauth.kiro.importTitle')
  await wrapper.get('form#create-account-form input[type="text"]').setValue('Kiro import')
  await wrapper.get('form#create-account-form').trigger('submit.prevent')
  await flushPromises()
  return wrapper
}

describe('CreateAccountModal OpenAI long-context billing', () => {
  beforeEach(() => {
    createAccountMock.mockReset().mockResolvedValue({})
    importCodexSessionMock.mockReset().mockResolvedValue({
      created: 1,
      updated: 0,
      skipped: 0,
      failed: 0,
      errors: [],
      warnings: [],
    })
    createOpenAICodexPATMock.mockReset().mockResolvedValue({})
    kiroImportTokenMock.mockReset().mockResolvedValue({
      access_token: 'access-token',
      refresh_token: 'refresh-token',
      auth_method: 'external_idp',
      provider: 'ExternalIdp',
      client_id: 'client-id',
      token_endpoint: 'https://login.microsoftonline.com/tenant-id/oauth2/v2.0/token',
      issuer_url: 'https://login.microsoftonline.com/tenant-id/v2.0',
      scopes: 'openid offline_access',
    })
  })

  it('sends false explicitly for normal OpenAI account creation by default', async () => {
    await submitApiKeyAccount('openai')

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBe(false)
  })

  it('exposes Agent Identity in the OpenAI authorization methods', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('OpenAI account')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')

    const flow = wrapper.getComponent(OAuthAuthorizationFlowStub)
    expect(flow.props('showManualOption')).toBe(true)
    expect(flow.props('showCodexSessionImportOption')).toBe(true)
    expect(flow.props('showAgentIdentityOption')).toBe(true)
    expect(flow.props('showCodexPatOption')).toBe(true)
    expect(flow.props('initialInputMethod')).toBe('manual')
  })

  it.each([
    ['camelCase', { authMode: 'agentIdentity', agentIdentity: { agentRuntimeId: 'runtime' } }],
    ['nested identity without auth_mode', { agent_identity: { agent_runtime_id: 'runtime' } }],
  ])('accepts backend-compatible %s Agent Identity imports', async (_name, content) => {
    const wrapper = await openCodexImportStep()
    const flow = wrapper.getComponent(OAuthAuthorizationFlowStub)
    flow.vm.inputMethod = 'agent_identity'

    flow.vm.$emit('import-codex-session', JSON.stringify(content))
    await flushPromises()

    expect(importCodexSessionMock).toHaveBeenCalledTimes(1)
  })

  it('sends true explicitly when OpenAI long-context billing is enabled', async () => {
    await submitApiKeyAccount('openai', true)

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBe(true)
  })

  it('omits the OpenAI setting for non-OpenAI account creation', async () => {
    await submitApiKeyAccount('anthropic')

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBeUndefined()
  })

  it('leaves Codex session import billing ownership to the backend', async () => {
    const wrapper = await openCodexImportStep()
    await wrapper.get('[data-testid="import-codex-session"]').trigger('click')
    await flushPromises()

    expect(importCodexSessionMock).toHaveBeenCalledTimes(1)
    expect(importCodexSessionMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBeUndefined()
  })

  it('leaves Codex PAT import billing ownership to the backend', async () => {
    const wrapper = await openCodexImportStep()
    await wrapper.get('[data-testid="import-codex-pat"]').trigger('click')
    await flushPromises()

    expect(createOpenAICodexPATMock).toHaveBeenCalledTimes(1)
    expect(createOpenAICodexPATMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBeUndefined()
  })

  it('sends explicit true for Codex session import after the toggle is enabled', async () => {
    const wrapper = await openCodexImportStep(1)
    await wrapper.get('[data-testid="import-codex-session"]').trigger('click')
    await flushPromises()

    expect(importCodexSessionMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBe(true)
  })

  it('sends explicit false for Codex session import after the toggle is changed back', async () => {
    const wrapper = await openCodexImportStep(2)
    await wrapper.get('[data-testid="import-codex-session"]').trigger('click')
    await flushPromises()

    expect(importCodexSessionMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBe(false)
  })

  it('sends explicit true for Codex PAT import after the toggle is enabled', async () => {
    const wrapper = await openCodexImportStep(1)
    await wrapper.get('[data-testid="import-codex-pat"]').trigger('click')
    await flushPromises()

    expect(createOpenAICodexPATMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBe(true)
  })

  it('switches Kiro import provider controls and only requires device registration for IDC providers', async () => {
    const wrapper = await openKiroImportStep()

    expect(wrapper.find('input[value="Google"]').exists()).toBe(true)
    expect(wrapper.find('input[value="Github"]').exists()).toBe(true)
    expect(wrapper.find('input[value="BuilderId"]').exists()).toBe(true)
    expect(wrapper.find('input[value="Enterprise"]').exists()).toBe(true)
    expect(wrapper.find('input[value="ExternalIdp"]').exists()).toBe(true)
    expect(wrapper.findAll('textarea')).toHaveLength(1)

    await wrapper.get('input[value="BuilderId"]').setValue()
    expect(wrapper.findAll('textarea')).toHaveLength(2)

    await wrapper.get('input[value="ExternalIdp"]').setValue()
    expect(wrapper.findAll('textarea')).toHaveLength(1)
    expect(wrapper.get('textarea').attributes('placeholder')).toContain('"tokenEndpoint"')
  })

  it.each([
    ['invalid JSON', '{not-json'],
    ['provider mismatch', '{"provider":"Github","accessToken":"access-token"}'],
  ])('rejects Kiro import %s before calling the API', async (_name, tokenJSON) => {
    const wrapper = await openKiroImportStep()
    await wrapper.get('textarea').setValue(tokenJSON)
    await selectButtonByText(wrapper, 'admin.accounts.oauth.kiro.importAndUpdate')
    await flushPromises()

    expect(kiroImportTokenMock).not.toHaveBeenCalled()
    expect(createAccountMock).not.toHaveBeenCalled()
  })

  it('creates ExternalIdp imports with the independently selected API region', async () => {
    const wrapper = await openKiroImportStep()
    await wrapper.get('input[value="ExternalIdp"]').setValue()
    await wrapper.get('textarea').setValue(JSON.stringify({
      accessToken: 'access-token',
      refreshToken: 'refresh-token',
      authMethod: 'external_idp',
      provider: 'ExternalIdp',
      clientId: 'client-id',
      tokenEndpoint: 'https://login.microsoftonline.com/tenant-id/oauth2/v2.0/token',
      issuerUrl: 'https://login.microsoftonline.com/tenant-id/v2.0',
      scopes: 'openid offline_access',
    }))
    await selectButtonByText(wrapper, 'admin.accounts.oauth.kiro.importAndUpdate')
    await flushPromises()

    expect(kiroImportTokenMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.credentials).toMatchObject({
      provider: 'ExternalIdp',
      token_endpoint: 'https://login.microsoftonline.com/tenant-id/oauth2/v2.0/token',
      api_region: 'us-east-1',
    })
  })

  it('sends explicit false for Codex PAT import after the toggle is changed back', async () => {
    const wrapper = await openCodexImportStep(2)
    await wrapper.get('[data-testid="import-codex-pat"]').trigger('click')
    await flushPromises()

    expect(createOpenAICodexPATMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBe(false)
  })
})
