import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const {
  createAccountMock,
  probeUpstreamBillingMock,
  importCodexSessionMock,
  createOpenAICodexPATMock,
  kiroImportTokenMock,
  authIsSimpleMode,
} = vi.hoisted(() => ({
  createAccountMock: vi.fn(),
  probeUpstreamBillingMock: vi.fn(),
  importCodexSessionMock: vi.fn(),
  createOpenAICodexPATMock: vi.fn(),
  kiroImportTokenMock: vi.fn(),
  authIsSimpleMode: { value: true },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showWarning: vi.fn(),
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    get isSimpleMode() {
      return authIsSimpleMode.value
    },
  }),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      create: createAccountMock,
      probeUpstreamBilling: probeUpstreamBillingMock,
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

const SelectStub = defineComponent({
  name: 'SelectStub',
  inheritAttrs: false,
  props: {
    modelValue: { type: [String, Number, Boolean], default: null },
    options: { type: Array, default: () => [] },
  },
  emits: ['update:modelValue'],
  template: `
    <label>
      <select
        v-bind="$attrs"
        :value="modelValue"
        @change="$emit('update:modelValue', $event.target.value)"
      >
        <option
          v-for="option in options"
          :key="String(option.value)"
          :value="option.value"
        >
          {{ option.label }}
        </option>
      </select>
    </label>
  `,
})

const GroupSelectorStub = defineComponent({
  name: 'GroupSelector',
  props: {
    modelValue: {
      type: Array,
      default: () => [],
    },
  },
  emits: ['update:modelValue'],
  template: `
    <button
      type="button"
      data-testid="select-pricing-groups"
      @click="$emit('update:modelValue', [1, 2])"
    >
      groups
    </button>
  `,
})

const ModelWhitelistSelectorStub = defineComponent({
  name: 'ModelWhitelistSelector',
  props: {
    modelValue: {
      type: Array,
      default: () => [],
    },
    platform: String,
    syncCredentials: Object,
  },
  emits: ['update:modelValue'],
  template: '<div data-testid="model-whitelist-selector" />',
})

function mountModal(groups: any[] = []) {
  return mount(CreateAccountModal, {
    props: { show: true, proxies: [], groups },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        OAuthAuthorizationFlow: OAuthAuthorizationFlowStub,
        ConfirmDialog: true,
        Select: SelectStub,
        Icon: true,
        PlatformIcon: true,
        ProxySelector: true,
        ProxyAdBanner: true,
        GroupSelector: GroupSelectorStub,
        ModelWhitelistSelector: ModelWhitelistSelectorStub,
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

function checkboxByLabel(wrapper: ReturnType<typeof mountModal>, text: string) {
  const label = wrapper.findAll('label').find(candidate => candidate.text().includes(text))
  expect(label, `missing checkbox label: ${text}`).toBeDefined()
  return label!.get('input[type="checkbox"]')
}

function kiroEndpointModeSelect(wrapper: ReturnType<typeof mountModal>) {
  const select = wrapper.findAllComponents(SelectStub).find(candidate => {
    const options = candidate.props('options') as Array<{ value?: unknown }>
    return options.some(option => option.value === 'auto')
  })
  expect(select, 'missing Kiro endpoint mode select').toBeDefined()
  return select!.get('select')
}

async function submitApiKeyAccount(
  platform: 'openai' | 'anthropic',
  enableLongContextBilling = false,
  disableUpstreamBillingProbe = false
) {
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
  if (disableUpstreamBillingProbe) {
    await wrapper.get('[data-testid="upstream-billing-auto-probe"]').trigger('click')
  }
  await wrapper.get('form#create-account-form').trigger('submit.prevent')
  await flushPromises()
  return wrapper
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

async function openKiroImportStep(apiRegion?: string) {
  const wrapper = mountModal()
  await selectButtonByText(wrapper, 'Kiro')
  await selectButtonByText(wrapper, 'admin.accounts.oauth.kiro.importTitle')
  if (apiRegion) {
    await wrapper
      .get('[data-testid="kiro-api-region-select-create"]')
      .get<HTMLSelectElement>('select')
      .setValue(apiRegion)
  }
  await wrapper.get('form#create-account-form input[type="text"]').setValue('Kiro import')
  await wrapper.get('form#create-account-form').trigger('submit.prevent')
  await flushPromises()
  return wrapper
}

describe('CreateAccountModal OpenAI long-context billing', () => {
  beforeEach(() => {
    authIsSimpleMode.value = true
    createAccountMock.mockReset().mockResolvedValue({ id: 42, platform: 'openai', type: 'apikey' })
    probeUpstreamBillingMock.mockReset().mockResolvedValue({})
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

  it('hides only the redundant account toggle when every selected group enables tier pricing', async () => {
    authIsSimpleMode.value = false
    const wrapper = mountModal([
      { id: 1, long_context_pricing_enabled: true },
      { id: 2, long_context_pricing_enabled: true },
    ])

    await selectButtonByText(wrapper, 'OpenAI')
    await wrapper.get('[data-testid="select-pricing-groups"]').trigger('click')

    expect(wrapper.find('[data-testid="openai-long-context-billing-toggle"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="create-openai-ws-mode"]').exists()).toBe(true)
  })

  it('keeps the account toggle when any selected group disables tier pricing', async () => {
    authIsSimpleMode.value = false
    const wrapper = mountModal([
      { id: 1, long_context_pricing_enabled: true },
      { id: 2, long_context_pricing_enabled: false },
    ])

    await selectButtonByText(wrapper, 'OpenAI')
    await wrapper.get('[data-testid="select-pricing-groups"]').trigger('click')

    expect(wrapper.find('[data-testid="openai-long-context-billing-toggle"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="create-openai-ws-mode"]').exists()).toBe(true)
  })

  it('sends false explicitly for normal OpenAI account creation by default', async () => {
    await submitApiKeyAccount('openai')

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBe(false)
  })

  // namespace 摊平是仅 OAuth 的兼容开关：API Key 走 chat completions 回退桥时由桥自行摊平
  it('shows the Codex namespace flatten toggle only for OpenAI OAuth accounts', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')

    expect(wrapper.find('[data-testid="create-openai-flatten-namespaces-toggle"]').exists()).toBe(
      true
    )

    await selectButtonByText(wrapper, 'API Key')
    expect(wrapper.find('[data-testid="create-openai-flatten-namespaces-toggle"]').exists()).toBe(
      false
    )
  })

  it('enables upstream billing probes by default for new OpenAI API key accounts', async () => {
    await submitApiKeyAccount('openai')

    expect(createAccountMock.mock.calls[0]?.[0]?.upstream_billing_probe_enabled).toBe(true)
  })

  it('waits for the initial upstream billing probe before refreshing the account list', async () => {
    let resolveProbe: (() => void) | undefined
    probeUpstreamBillingMock.mockImplementationOnce(
      () => new Promise<void>((resolve) => {
        resolveProbe = resolve
      })
    )

    const wrapper = await submitApiKeyAccount('openai')

    expect(probeUpstreamBillingMock).toHaveBeenCalledWith(42)
    expect(wrapper.emitted('created')).toBeUndefined()

    resolveProbe?.()
    await flushPromises()

    expect(wrapper.emitted('created')).toHaveLength(1)
  })

  it('sends an explicit disabled state when the create toggle is turned off', async () => {
    await submitApiKeyAccount('openai', false, true)

    expect(createAccountMock.mock.calls[0]?.[0]?.upstream_billing_probe_enabled).toBe(false)
    expect(probeUpstreamBillingMock).not.toHaveBeenCalled()
  })

  it('submits adaptive Kimi protocol endpoints', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'Kimi')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Kimi adaptive')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-kimi')

    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.credentials).toMatchObject({
      account_mode: 'payg',
      api_protocol: 'adaptive',
      base_url: 'https://api.moonshot.cn/v1',
      api_base_urls: {
        chat_completions: 'https://api.moonshot.cn/v1',
        anthropic: 'https://api.moonshot.cn/anthropic'
      }
    })
  })

  it('uses the edited adaptive Chat endpoint when previewing upstream models', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'Kimi')
    await wrapper
      .get('[data-testid="cn-adaptive-base-url-chat_completions"]')
      .setValue('https://relay.example.com/v1')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-relay')

    expect(wrapper.getComponent(ModelWhitelistSelectorStub).props('syncCredentials')).toMatchObject({
      platform: 'kimi',
      type: 'apikey',
      base_url: 'https://relay.example.com/v1',
      api_key: 'sk-relay'
    })
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
    // 上游倍率探测已放宽到全部 API-key 平台：非 OpenAI 平台与 OpenAI 一致，默认开启。
    expect(createAccountMock.mock.calls[0]?.[0]?.upstream_billing_probe_enabled).toBe(true)
  })

  it('sends an explicit disabled state when the non-OpenAI create toggle is turned off', async () => {
    await submitApiKeyAccount('anthropic', false, true)

    expect(createAccountMock.mock.calls[0]?.[0]?.upstream_billing_probe_enabled).toBe(false)
  })

  it('antigravity upstream 创建默认携带上游倍率探测开关', async () => {
    // antigravity upstream 走独立创建 helper，
    // 也必须与其余 API-key 平台一样默认开启探测并传递开关。
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'Antigravity')
    await selectButtonByText(wrapper, 'admin.accounts.types.antigravityApikey')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('antigravity relay')
    const baseInput = wrapper
      .findAll('input')
      .find((candidate) => candidate.attributes('placeholder') === 'https://cloudcode-pa.googleapis.com')
    expect(baseInput).toBeDefined()
    await baseInput?.setValue('https://relay.example')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-upstream')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    const payload = createAccountMock.mock.calls[0]?.[0]
    expect(payload?.platform).toBe('antigravity')
    expect(payload?.type).toBe('apikey')
    expect(payload?.upstream_billing_probe_enabled).toBe(true)
    // 创建成功后前端立即发起一次首探（与其他 apikey 平台一致）。
    expect(probeUpstreamBillingMock).toHaveBeenCalledWith(42)
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

  it('offers all 34 AWS regions for Kiro IDC and preserves a custom region value', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'Kiro')
    await selectButtonByText(wrapper, 'admin.accounts.oauth.kiro.idcTitle')

    const selector = wrapper.get('[data-testid="kiro-idc-region-select-create"]')
    expect(selector.findAll('option')).toHaveLength(34)
    expect(selector.text()).toContain('ap-southeast-7')
    expect(selector.text()).toContain('mx-central-1')

    const component = wrapper.findAllComponents(SelectStub).find(candidate =>
      candidate.find('[data-testid="kiro-idc-region-select-create"]').exists()
    )
    expect(component).toBeDefined()
    component?.vm.$emit('update:modelValue', 'legacy-idc-1')
    await flushPromises()
    expect((component?.props('options') as Array<{ value: string }>).at(-1)?.value).toBe('legacy-idc-1')
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
    const wrapper = await openKiroImportStep('eu-west-1')
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
      api_region: 'eu-west-1',
    })
  })

  it('resets Kiro mixed endpoint settings across close and reopen before serializing a new account', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'Kiro')
    await checkboxByLabel(wrapper, 'admin.accounts.kiroMixedScheduling').setValue(true)
    await kiroEndpointModeSelect(wrapper).setValue('auto')
    await checkboxByLabel(wrapper, 'admin.groups.kiroCache.stickyRouting').setValue(false)
    await checkboxByLabel(wrapper, 'admin.groups.kiroCache.enabled').setValue(true)
    await wrapper.get('input[placeholder="1"]').setValue('0.25')

    await wrapper.setProps({ show: false })
    await flushPromises()
    await wrapper.setProps({ show: true })
    await flushPromises()
    await selectButtonByText(wrapper, 'Kiro')

    expect((checkboxByLabel(wrapper, 'admin.accounts.kiroMixedScheduling').element as HTMLInputElement).checked).toBe(false)
    await checkboxByLabel(wrapper, 'admin.accounts.kiroMixedScheduling').setValue(true)
    expect((kiroEndpointModeSelect(wrapper).element as HTMLSelectElement).value).toBe('q')
    expect((checkboxByLabel(wrapper, 'admin.groups.kiroCache.stickyRouting').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.get('input[placeholder="3600"]').element as HTMLInputElement).value).toBe('3600')
    expect((checkboxByLabel(wrapper, 'admin.groups.kiroCache.enabled').element as HTMLInputElement).checked).toBe(false)
    await checkboxByLabel(wrapper, 'admin.groups.kiroCache.enabled').setValue(true)
    expect((wrapper.get('input[placeholder="1"]').element as HTMLInputElement).value).toBe('1')
    await checkboxByLabel(wrapper, 'admin.accounts.kiroMixedScheduling').setValue(false)

    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('fresh Kiro account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('ksk_fresh')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.extra?.mixed_scheduling).toBeUndefined()
    expect(createAccountMock.mock.calls[0]?.[0]?.extra?.kiro_endpoint_mode).toBeUndefined()
    expect(createAccountMock.mock.calls[0]?.[0]).not.toHaveProperty('upstream_billing_probe_enabled')
    expect(probeUpstreamBillingMock).not.toHaveBeenCalled()
  })

  it('sends explicit false for Codex PAT import after the toggle is changed back', async () => {
    const wrapper = await openCodexImportStep(2)
    await wrapper.get('[data-testid="import-codex-pat"]').trigger('click')
    await flushPromises()

    expect(createOpenAICodexPATMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBe(false)
  })
})
