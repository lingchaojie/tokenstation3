import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { claudeModels } from '@/composables/useModelWhitelist'

const { updateAccountMock, checkMixedChannelRiskMock, authIsSimpleMode } = vi.hoisted(() => ({
  updateAccountMock: vi.fn(),
  checkMixedChannelRiskMock: vi.fn(),
  authIsSimpleMode: { value: true }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn()
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    get isSimpleMode() {
      return authIsSimpleMode.value
    }
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      update: updateAccountMock,
      checkMixedChannelRisk: checkMixedChannelRiskMock
    },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({})
    },
    tlsFingerprintProfiles: {
      list: vi.fn().mockResolvedValue([])
    }
  }
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn()
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

import EditAccountModal from '../EditAccountModal.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: {
    show: {
      type: Boolean,
      default: false
    }
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

const ModelWhitelistSelectorStub = defineComponent({
  name: 'ModelWhitelistSelector',
  props: {
    modelValue: {
      type: Array,
      default: () => []
    }
  },
  emits: ['update:modelValue'],
  template: `
    <div>
      <button
        type="button"
        data-testid="rewrite-to-snapshot"
        @click="$emit('update:modelValue', ['gpt-5.2-2025-12-11'])"
      >
        rewrite
      </button>
      <span data-testid="model-whitelist-value">
        {{ Array.isArray(modelValue) ? modelValue.join(',') : '' }}
      </span>
    </div>
  `
})

const SelectStub = defineComponent({
  name: 'SelectStub',
  props: {
    modelValue: {
      type: [String, Number, Boolean, null],
      default: ''
    },
    options: {
      type: Array,
      default: () => []
    }
  },
  emits: ['update:modelValue'],
  template: `
    <select
      v-bind="$attrs"
      :value="modelValue"
      @change="$emit('update:modelValue', $event.target.value)"
    >
      <option
        v-for="option in options"
        :key="option.value"
        :value="option.value"
        :disabled="option.disabled"
      >
        {{ option.label }}
      </option>
    </select>
  `
})

const GroupSelectorStub = defineComponent({
  name: 'GroupSelector',
  props: {
    modelValue: {
      type: Array,
      default: () => []
    }
  },
  emits: ['update:modelValue'],
  template: `
    <div data-testid="group-selector">
      <button
        type="button"
        data-testid="set-shadow-group"
        @click="$emit('update:modelValue', [7])"
      >
        group
      </button>
    </div>
  `
})

function buildAccount() {
  return {
    id: 1,
    name: 'OpenAI Key',
    notes: '',
    platform: 'openai',
    type: 'apikey',
    credentials: {
      api_key: 'sk-test',
      base_url: 'https://api.openai.com',
      model_mapping: {
        'gpt-5.2': 'gpt-5.2'
      }
    },
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false
  } as any
}

function buildOpenAISparkShadowAccount() {
  const account = buildAccount()
  return {
    ...account,
    id: 4,
    name: 'OpenAI Spark Shadow',
    type: 'oauth',
    parent_account_id: 1,
    credentials: {
      access_token: 'parent-access-token',
      refresh_token: 'parent-refresh-token',
      api_key: 'sk-parent',
      base_url: 'https://api.openai.com',
      model_mapping: {
        'gpt-5.3-codex-spark': 'gpt-5.3-codex-spark'
      },
      compact_model_mapping: {
        'gpt-5.3-codex-spark': 'gpt-5.3-codex-spark-compact'
      }
    },
    group_ids: []
  } as any
}

function buildVertexAccount() {
  return {
    id: 2,
    name: 'Vertex SA',
    notes: '',
    platform: 'gemini',
    type: 'service_account',
    credentials: {
      service_account_json: '{"type":"service_account","client_email":"sa@example.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----\\nMIIE\\n-----END PRIVATE KEY-----\\n"}',
      project_id: 'demo-project',
      client_email: 'sa@example.iam.gserviceaccount.com',
      location: 'us-central1',
      tier_id: 'vertex'
    },
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false
  } as any
}

function buildAntigravityAccount(projectId = 'configured-project') {
  return {
    id: 3,
    name: 'Antigravity OAuth',
    notes: '',
    platform: 'antigravity',
    type: 'oauth',
    credentials: {
      antigravity_project_id: projectId,
      model_mapping: {
        'gemini-2.5-flash': 'gemini-2.5-flash'
      }
    },
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false
  } as any
}

function buildKiroOrganizationAccount() {
  return {
    id: 4,
    name: 'Kiro Organization',
    notes: '',
    platform: 'kiro',
    type: 'oauth',
    credentials: {
      access_token: 'at',
      refresh_token: 'rt',
      auth_method: 'idc',
      provider: 'AWS',
      client_id: 'client-id',
      client_secret: 'client-secret',
      client_id_hash: 'client-hash',
      start_url: 'https://d-99674ac649.awsapps.com/start',
      region: 'eu-north-1',
      api_region: 'us-east-1',
      model_mapping: {
        'claude-sonnet-4-5': 'claude-sonnet-4-5'
      }
    },
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false
  } as any
}

function buildKiroExternalIdpAccount() {
  const account = buildKiroOrganizationAccount()
  return {
    ...account,
    id: 8,
    name: 'Kiro External IdP',
    credentials: {
      ...account.credentials,
      auth_method: 'external_idp',
      provider: 'ExternalIdp',
      token_endpoint: 'https://login.microsoftonline.com/tenant/oauth2/v2.0/token',
      issuer_url: 'https://login.microsoftonline.com/tenant/v2.0',
      scopes: 'openid offline_access',
      api_region: 'eu-central-1'
    },
    extra: {
      mixed_scheduling: true,
      kiro_endpoint_mode: 'krs'
    }
  } as any
}

function buildKiroAPIKeyAccount(baseUrl = '', apiRegion?: string) {
  return {
    ...buildAccount(),
    id: 7,
    name: baseUrl ? 'Kiro Relay API Key' : 'Kiro Native API Key',
    platform: 'kiro',
    credentials: {
      api_key: 'sk-kiro-test',
      ...(baseUrl ? { base_url: baseUrl } : {}),
      ...(apiRegion ? { api_region: apiRegion } : {}),
      model_mapping: {
        'claude-sonnet-4-5': 'claude-sonnet-4-5'
      }
    }
  } as any
}

function buildGrokOAuthAccount() {
  return {
    id: 5,
    name: 'Grok OAuth',
    notes: '',
    platform: 'grok',
    type: 'oauth',
    credentials: {
      refresh_token: 'grok-rt',
      base_url: 'https://api.x.ai/v1',
      model_mapping: {
        'grok-latest': 'grok-4.3'
      }
    },
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false
  } as any
}

function buildGrokAPIKeyAccount() {
  return {
    ...buildAccount(),
    id: 6,
    name: 'Grok API Key',
    platform: 'grok',
    credentials: {},
    credentials_status: { has_api_key: true },
    concurrency: 2
  } as any
}

function buildOpenAISetupTokenAccount() {
  return {
    ...buildAccount(),
    type: 'setup-token',
    extra: {
      openai_oauth_responses_websockets_v2_mode: 'ctx_pool',
      openai_oauth_responses_websockets_v2_enabled: true
    }
  } as any
}

function buildAnthropicOAuthAccount() {
  return {
    id: 5,
    name: 'Claude OAuth',
    notes: '',
    platform: 'anthropic',
    type: 'oauth',
    credentials: {
      access_token: 'anthropic-access-token',
      refresh_token: 'anthropic-refresh-token'
    },
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false
  } as any
}

function mountModal(account = buildAccount()) {
  return mount(EditAccountModal, {
    props: {
      show: true,
      account,
      proxies: [],
      groups: []
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Select: SelectStub,
        Icon: true,
        ProxySelector: true,
        GroupSelector: GroupSelectorStub,
        ModelWhitelistSelector: ModelWhitelistSelectorStub
      }
    }
  })
}

describe('EditAccountModal', () => {
  beforeEach(() => {
    authIsSimpleMode.value = true
  })

  it('reopening the same account rehydrates the OpenAI whitelist from props', async () => {
    const account = buildAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.2')

    await wrapper.get('[data-testid="rewrite-to-snapshot"]').trigger('click')
    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.2-2025-12-11')

    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })

    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.2')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      'gpt-5.2': 'gpt-5.2'
    })
  })

  it('preserves model mappings when editing the whitelist', async () => {
    const account = buildAccount()
    account.credentials.model_mapping = {
      'gpt-5.2': 'gpt-5.2',
      'gpt-latest': 'gpt-5.2'
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.2')

    await wrapper.get('[data-testid="rewrite-to-snapshot"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      'gpt-5.2-2025-12-11': 'gpt-5.2-2025-12-11',
      'gpt-latest': 'gpt-5.2'
    })
  })

  it('submits OpenAI compact mode and compact-only model mapping', async () => {
    const account = buildAccount()
    account.extra = {
      openai_compact_mode: 'force_on'
    }
    account.credentials = {
      ...account.credentials,
      compact_model_mapping: {
        'gpt-5.4': 'gpt-5.4-openai-compact'
      }
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_compact_mode).toBe('force_on')
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.compact_model_mapping).toEqual({
      'gpt-5.4': 'gpt-5.4-openai-compact'
    })
  })

  it('loads and submits the per-account OpenAI long-context billing toggle', async () => {
    const account = buildAccount()
    account.extra = {
      openai_long_context_billing_enabled: true
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const toggle = wrapper.get('[data-testid="openai-long-context-billing-toggle"]')
    expect(toggle.attributes('aria-checked')).toBe('true')

    await toggle.trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_long_context_billing_enabled).toBe(false)
  })

  it('defaults legacy OpenAI accounts to long-context billing disabled', async () => {
    const account = buildAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const toggle = wrapper.get('[data-testid="openai-long-context-billing-toggle"]')
    expect(toggle.attributes('aria-checked')).toBe('false')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_long_context_billing_enabled).toBe(false)
  })

  it('does not render or submit the long-context billing toggle for Spark shadow accounts', async () => {
    const account = buildOpenAISparkShadowAccount()
    account.extra = {
      openai_long_context_billing_enabled: false
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)
    const wrapper = mountModal(account)

    expect(wrapper.find('[data-testid="openai-long-context-billing-toggle"]').exists()).toBe(false)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty(
      'openai_long_context_billing_enabled'
    )
  })

  it('preserves an explicit OpenAI long-context billing opt-out', async () => {
    const account = buildAccount()
    account.extra = {
      openai_long_context_billing_enabled: false
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const toggle = wrapper.get('[data-testid="openai-long-context-billing-toggle"]')
    expect(toggle.attributes('aria-checked')).toBe('false')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_long_context_billing_enabled).toBe(false)
  })

  it('fails closed for malformed OpenAI long-context billing values', async () => {
    const account = buildAccount()
    account.extra = {
      openai_long_context_billing_enabled: 'false'
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect(wrapper.get('[data-testid="openai-long-context-billing-toggle"]').attributes('aria-checked')).toBe('false')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_long_context_billing_enabled).toBe(false)
  })

  it('loads and submits Grok OAuth model mapping edits', async () => {
    const account = buildGrokOAuthAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    expect(wrapper.text()).toContain('Imagine Image')
    expect(wrapper.text()).toContain('Imagine Video')

    const inputWithValue = (value: string) => {
      const input = wrapper
        .findAll('input')
        .find((input) => (input.element as HTMLInputElement).value === value)
      expect(input).toBeTruthy()
      return input!
    }

    await inputWithValue('grok-latest').setValue('grok')
    await inputWithValue('grok-4.3').setValue('grok-build-0.1')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      grok: 'grok-build-0.1'
    })
  })

  it('uses the official xAI base URL when a Grok API-key account omits base_url', async () => {
    const account = buildGrokAPIKeyAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect((wrapper.get('input[placeholder="https://api.x.ai/v1"]').element as HTMLInputElement).value)
      .toBe('https://api.x.ai/v1')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.base_url).toBe('https://api.x.ai/v1')
  })

  it('only submits model mapping credentials when saving an OpenAI spark shadow account', async () => {
    authIsSimpleMode.value = false
    const account = buildOpenAISparkShadowAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('[data-testid="set-shadow-group"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    const payload = updateAccountMock.mock.calls[0]?.[1]
    expect(payload?.group_ids).toEqual([7])
    expect(payload?.credentials).toEqual({
      model_mapping: {
        'gpt-5.3-codex-spark': 'gpt-5.3-codex-spark'
      },
      compact_model_mapping: {
        'gpt-5.3-codex-spark': 'gpt-5.3-codex-spark-compact'
      }
    })
  })

  it('submits OpenAI APIKey Responses support override mode', async () => {
    const account = buildAccount()
    account.extra = {
      openai_responses_mode: 'force_chat_completions',
      openai_responses_supported: false
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('[data-testid="openai-responses-mode-select"]').setValue('force_responses')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_responses_mode).toBe('force_responses')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_responses_supported).toBe(false)
  })

  it('clears OpenAI APIKey Responses override when set back to auto', async () => {
    const account = buildAccount()
    account.extra = {
      openai_responses_mode: 'force_chat_completions',
      openai_responses_supported: true
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('[data-testid="openai-responses-mode-select"]').setValue('auto')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_responses_mode')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_responses_supported).toBe(true)
  })

  it('submits OpenAI APIKey endpoint capabilities from credentials', async () => {
    const account = buildAccount()
    account.credentials.openai_capabilities = ['chat_completions']
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect(wrapper.findAll('input[type="checkbox"]').some((input) => (input.element as HTMLInputElement).checked)).toBe(true)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.openai_capabilities).toEqual([
      'chat_completions'
    ])
  })

	it('submits OpenAI quota auto-pause thresholds in extra', async () => {
	  const account = buildAccount()
	  account.extra = {
		auto_pause_5h_threshold: 0.9,
		auto_pause_7d_threshold: 0.8
	  }
	  updateAccountMock.mockReset()
	  checkMixedChannelRiskMock.mockReset()
	  checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
	  updateAccountMock.mockResolvedValue(account)

	  const wrapper = mountModal(account)

	  await wrapper.get('[data-testid="auto-pause-5h-threshold"]').setValue('95')
	  await wrapper.get('[data-testid="auto-pause-7d-threshold"]').setValue('96')
	  await wrapper.get('form#edit-account-form').trigger('submit.prevent')

	  expect(updateAccountMock).toHaveBeenCalledTimes(1)
	  expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.auto_pause_5h_threshold).toBe(0.95)
	  expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.auto_pause_7d_threshold).toBe(0.96)
	})

	it('submits OpenAI quota auto-pause disable flag in extra', async () => {
	  // Toggling the per-account disable flag must persist as auto_pause_5h_disabled
	  // so an admin can exempt one account from auto-pause even when a global default
	  // threshold is configured (otherwise leaving the threshold blank would silently
	  // fall back to the global default).
	  const account = buildAccount()
	  updateAccountMock.mockReset()
	  checkMixedChannelRiskMock.mockReset()
	  checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
	  updateAccountMock.mockResolvedValue(account)

	  const wrapper = mountModal(account)

	  await wrapper.get('[data-testid="auto-pause-5h-disabled"]').trigger('click')
	  await wrapper.get('form#edit-account-form').trigger('submit.prevent')

	  expect(updateAccountMock).toHaveBeenCalledTimes(1)
	  expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.auto_pause_5h_disabled).toBe(true)
	  expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.auto_pause_7d_disabled).toBeUndefined()
	})

  it('keeps at least one OpenAI APIKey endpoint capability selected', async () => {
    const account = buildAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    const chatCheckbox = wrapper.get<HTMLInputElement>(
      '[data-testid="openai-endpoint-capability-chat_completions"]'
    )
    const embeddingsCheckbox = wrapper.get<HTMLInputElement>(
      '[data-testid="openai-endpoint-capability-embeddings"]'
    )

    expect(chatCheckbox.element.checked).toBe(true)
    expect(embeddingsCheckbox.element.checked).toBe(true)

    await embeddingsCheckbox.setValue(false)

    expect(chatCheckbox.element.checked).toBe(true)
    expect(embeddingsCheckbox.element.checked).toBe(false)

    await chatCheckbox.setValue(false)

    expect(chatCheckbox.element.checked).toBe(true)
    expect(embeddingsCheckbox.element.checked).toBe(false)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.openai_capabilities).toEqual([
      'chat_completions'
    ])
  })

  it('disables text generation protocol when only embeddings requests are accepted', async () => {
    const account = buildAccount()
    account.credentials.openai_capabilities = ['embeddings']
    account.extra = {
      openai_responses_mode: 'force_responses',
      openai_responses_supported: true
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    const responsesModeSelect = wrapper.get<HTMLSelectElement>(
      '[data-testid="openai-responses-mode-select"]'
    )

    expect(responsesModeSelect.element.disabled).toBe(true)
    expect(wrapper.find('[data-testid="openai-responses-mode-not-applicable"]').exists()).toBe(true)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.openai_capabilities).toEqual([
      'embeddings'
    ])
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_responses_mode')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_responses_supported).toBe(true)
  })

  it('submits Codex image tool force-inject mode as bridge override', async () => {
    const account = buildAccount()
    account.extra = {
      codex_image_generation_bridge: false,
      codex_image_generation_bridge_enabled: true
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('button[data-testid="codex-image-tool-enabled"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.codex_image_generation_bridge).toBe(true)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_bridge_enabled')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_explicit_tool_policy')
  })

  it('submits Codex image tool no-injection mode without strip policy', async () => {
    const account = buildAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('button[data-testid="codex-image-tool-disabled"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.codex_image_generation_bridge).toBe(false)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_explicit_tool_policy')
  })

  it('submits Codex image tool block mode as strip policy and clears bridge override', async () => {
    const account = buildAccount()
    account.extra = {
      codex_image_generation_bridge: true
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('button[data-testid="codex-image-tool-block"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.codex_image_generation_explicit_tool_policy).toBe('strip')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_bridge')
  })

  it('loads strip policy as block mode and clears both keys when reset to inherit', async () => {
    const account = buildAccount()
    account.extra = {
      codex_image_generation_explicit_tool_policy: 'strip'
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('button[data-testid="codex-image-tool-inherit"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_explicit_tool_policy')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_bridge')
  })

  it('setup-token account can select and submit OAuth WS mode', async () => {
    const account = buildOpenAISetupTokenAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('[data-testid="edit-openai-ws-mode-select"]').setValue('http_bridge')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_oauth_responses_websockets_v2_mode).toBe('http_bridge')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_oauth_responses_websockets_v2_enabled).toBe(true)
  })

  it('allows saving apikey account when backend redacted api_key but credentials_status reports it exists', async () => {
    // 新前端 + 新后端：响应已脱敏，credentials 里没有 api_key，credentials_status.has_api_key=true
    const account = buildAccount()
    account.credentials = {
      base_url: 'https://api.openai.com',
      model_mapping: { 'gpt-5.2': 'gpt-5.2' }
    }
    account.credentials_status = { has_api_key: true }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    // 用户未输入新 key 时，payload 不应带 api_key，由后端合并保留旧值
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).not.toHaveProperty('api_key')
  })

  it('allows saving apikey account against legacy backend without credentials_status', async () => {
    // 新前端 + 旧后端：credentials_status 缺失，但 credentials.api_key 仍是明文，应允许保存
    const account = buildAccount()
    // 显式确保没有 credentials_status
    expect(account.credentials_status).toBeUndefined()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    // 旧后端响应未脱敏，原 api_key 会随 currentCredentials 一起传回去（旧行为，等价于无操作）
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.api_key).toBe('sk-test')
  })

  it('blocks apikey save when neither credentials_status nor legacy api_key indicates existence', async () => {
    const account = buildAccount()
    account.credentials = {
      base_url: 'https://api.openai.com'
    }
    // 既没有 credentials_status 也没有旧的 api_key
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).not.toHaveBeenCalled()
  })

  it('allows saving Vertex SA account when backend redacted service_account_json but credentials_status reports it exists', async () => {
    // 新前端 + 新后端：响应已脱敏，credentials 里没有 service_account_json，credentials_status.has_service_account_json=true
    const account = buildVertexAccount()
    account.credentials = {
      project_id: 'demo-project',
      client_email: 'sa@example.iam.gserviceaccount.com',
      location: 'us-central1',
      tier_id: 'vertex'
    }
    account.credentials_status = { has_service_account_json: true }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.project_id).toBe('demo-project')
  })

  it('allows saving Vertex SA account against legacy backend without credentials_status', async () => {
    // 新前端 + 旧后端：credentials_status 缺失，但 credentials.service_account_json 仍是明文，应允许保存
    const account = buildVertexAccount()
    expect(account.credentials_status).toBeUndefined()
    expect(account.credentials.service_account_json).toBeTruthy()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
  })

  it('blocks Vertex SA save when neither credentials_status nor legacy json indicates existence', async () => {
    const account = buildVertexAccount()
    account.credentials = {
      project_id: 'demo-project',
      client_email: 'sa@example.iam.gserviceaccount.com',
      location: 'us-central1',
      tier_id: 'vertex'
    }
    // 既没有 credentials_status 也没有旧的 service_account_json
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).not.toHaveBeenCalled()
  })

  it('loads and submits Antigravity configured project fallback', async () => {
    const account = buildAntigravityAccount('configured-project')
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const input = wrapper.get<HTMLInputElement>('[data-testid="antigravity-project-id-input"]')
    expect(input.element.value).toBe('configured-project')

    await input.setValue('  updated-project  ')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.antigravity_project_id).toBe(
      'updated-project'
    )
  })

  it('clears Antigravity configured project fallback when input is empty', async () => {
    const account = buildAntigravityAccount('configured-project')
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const input = wrapper.get<HTMLInputElement>('[data-testid="antigravity-project-id-input"]')

    await input.setValue('')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).not.toHaveProperty(
      'antigravity_project_id'
    )
  })

  it('loads and submits Kiro Organization start URL and region credentials', async () => {
    const account = buildKiroOrganizationAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    const startUrlInput = wrapper.get<HTMLInputElement>('[data-testid="kiro-idc-start-url-input"]')
    const regionInput = wrapper.get<HTMLInputElement>('[data-testid="kiro-idc-region-input"]')
    const apiRegionSelect = wrapper
      .get('[data-testid="kiro-api-region-select-edit"]')
      .get<HTMLSelectElement>('select')

    expect(startUrlInput.element.value).toBe('https://d-99674ac649.awsapps.com/start')
    expect(regionInput.element.value).toBe('eu-north-1')
    expect(apiRegionSelect.element.value).toBe('us-east-1')

    await startUrlInput.setValue('  https://d-1111111111.awsapps.com/start  ')
    await regionInput.setValue('  eu-west-1  ')
    await apiRegionSelect.setValue('eu-central-1')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      auth_method: 'idc',
      provider: 'AWS',
      client_id: 'client-id',
      client_secret: 'client-secret',
      client_id_hash: 'client-hash',
      start_url: 'https://d-1111111111.awsapps.com/start',
      region: 'eu-west-1',
      api_region: 'eu-central-1'
    })
  })

  it('defaults a missing Kiro API region independently from the IDC region', () => {
    const account = buildKiroOrganizationAccount()
    delete account.credentials.api_region

    const wrapper = mountModal(account)
    const apiRegionSelect = wrapper
      .get('[data-testid="kiro-api-region-select-edit"]')
      .get<HTMLSelectElement>('select')

    expect(account.credentials.region).toBe('eu-north-1')
    expect(apiRegionSelect.element.value).toBe('us-east-1')
  })

  it('submits the selected API region for a direct Kiro API-key account', async () => {
    const account = buildKiroAPIKeyAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const apiRegionSelect = wrapper
      .get('[data-testid="kiro-api-region-select-edit"]')
      .get<HTMLSelectElement>('select')

    await apiRegionSelect.setValue('eu-central-1')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.api_region).toBe('eu-central-1')
  })

  it('uses the relay hint only for a Kiro API-key account with a base URL', () => {
    const relayWrapper = mountModal(buildKiroAPIKeyAccount('https://relay.example.com'))
    const relayBaseUrlInput = relayWrapper.get<HTMLInputElement>(
      'input[placeholder="https://your-relay.example.com"]'
    )

    expect(relayBaseUrlInput.element.value).toBe('https://relay.example.com')
    expect(relayBaseUrlInput.element.parentElement?.textContent).toContain(
      'admin.accounts.kiro.relayBaseUrlHint'
    )

    const directWrapper = mountModal(buildKiroAPIKeyAccount())
    expect(
      directWrapper.find('input[placeholder="https://your-relay.example.com"]').exists()
    ).toBe(false)
  })

  it('does not expose or introduce API region for a relay Kiro API-key account', async () => {
    const account = buildKiroAPIKeyAccount('https://relay.example.com')
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect(wrapper.find('[data-testid="kiro-api-region-select-edit"]').exists()).toBe(false)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).not.toHaveProperty('api_region')
  })

  it('keeps a historical unsupported Kiro API region selected and preserves it on save', async () => {
    const account = buildKiroAPIKeyAccount('', 'eu-north-1')
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const apiRegionSelect = wrapper
      .get('[data-testid="kiro-api-region-select-edit"]')
      .get<HTMLSelectElement>('select')
    const legacyOption = apiRegionSelect
      .findAll('option')
      .find(option => option.attributes('value') === 'eu-north-1')

    expect(apiRegionSelect.element.value).toBe('eu-north-1')
    expect(legacyOption?.attributes('disabled')).toBeDefined()

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.api_region).toBe('eu-north-1')
  })

  it('preserves canonical ExternalIdp metadata, API region, and endpoint mode on edit', async () => {
    const account = buildKiroExternalIdpAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      auth_method: 'external_idp',
      provider: 'ExternalIdp',
      token_endpoint: 'https://login.microsoftonline.com/tenant/oauth2/v2.0/token',
      issuer_url: 'https://login.microsoftonline.com/tenant/v2.0',
      scopes: 'openid offline_access',
      api_region: 'eu-central-1'
    })
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).not.toHaveProperty('preferred_endpoint')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.kiro_endpoint_mode).toBe('krs')
  })

  it('preserves auto Kiro endpoint mode on edit', async () => {
    const account = buildKiroExternalIdpAccount()
    account.extra.kiro_endpoint_mode = 'auto'
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.kiro_endpoint_mode).toBe('auto')
  })

  it('defaults Anthropic OAuth model mapping to the supported Claude model list', async () => {
    const account = buildAnthropicOAuthAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe(claudeModels.join(','))

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual(
      Object.fromEntries(claudeModels.map((model) => [model, model]))
    )
  })
})
