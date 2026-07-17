import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { claudeModels } from '@/composables/useModelWhitelist'

const {
  createAccountMock,
  exchangeCodeMock,
  checkMixedChannelRiskMock,
  appShowErrorMock,
  appShowSuccessMock,
  anthropicOAuthMock,
  genericOAuthMock
} = vi.hoisted(() => {
  const makeOAuthMock = () => ({
    authUrl: { value: '' },
    externalIDPAuthUrl: { value: '' },
    sessionId: { value: 'session-1' },
    oauthState: { value: 'state-1' },
    state: { value: 'state-1' },
    loading: { value: false },
    error: { value: '' },
    generateAuthUrl: vi.fn(),
    generateIDCAuthUrl: vi.fn(),
    resetState: vi.fn(),
    exchangeAuthCode: vi.fn(),
    validateRefreshToken: vi.fn(),
    buildCredentials: vi.fn((tokenInfo: Record<string, unknown>) => ({ ...tokenInfo })),
    buildExtraInfo: vi.fn(() => ({})),
    parseSessionKeys: vi.fn((value: string) => value.split('\n').map((item) => item.trim()).filter(Boolean)),
    importToken: vi.fn(),
    isExternalIDPCallback: vi.fn(() => false),
    startExternalIDPAuth: vi.fn()
  })

  return {
    createAccountMock: vi.fn(),
    exchangeCodeMock: vi.fn(),
    checkMixedChannelRiskMock: vi.fn(),
    appShowErrorMock: vi.fn(),
    appShowSuccessMock: vi.fn(),
    anthropicOAuthMock: makeOAuthMock(),
    genericOAuthMock: makeOAuthMock()
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: appShowErrorMock,
    showSuccess: appShowSuccessMock,
    showInfo: vi.fn(),
    showWarning: vi.fn()
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    isSimpleMode: true
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      create: createAccountMock,
      exchangeCode: exchangeCodeMock,
      checkMixedChannelRisk: checkMixedChannelRiskMock,
      importCodexSession: vi.fn(),
      createOpenAICodexPAT: vi.fn()
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

vi.mock('@/composables/useQuotaNotifyState', () => ({
  useQuotaNotifyState: () => ({
    globalEnabled: { value: false },
    state: {
      daily: { enabled: false, threshold: null, thresholdType: 'percentage' },
      weekly: { enabled: false, threshold: null, thresholdType: 'percentage' },
      total: { enabled: false, threshold: null, thresholdType: 'percentage' }
    },
    loadGlobalState: vi.fn(),
    writeToExtra: vi.fn()
  })
}))

vi.mock('@/composables/useAccountOAuth', () => ({
  useAccountOAuth: () => anthropicOAuthMock
}))

vi.mock('@/composables/useOpenAIOAuth', () => ({
  useOpenAIOAuth: () => genericOAuthMock
}))

vi.mock('@/composables/useGeminiOAuth', () => ({
  useGeminiOAuth: () => ({
    ...genericOAuthMock,
    getCapabilities: vi.fn().mockResolvedValue({ ai_studio_oauth_enabled: false })
  })
}))

vi.mock('@/composables/useAntigravityOAuth', () => ({
  useAntigravityOAuth: () => genericOAuthMock
}))

vi.mock('@/composables/useKiroOAuth', () => ({
  useKiroOAuth: () => ({
    ...genericOAuthMock,
    externalIdpStage: { value: 'portal' }
  })
}))

vi.mock('@/composables/useGrokOAuth', () => ({
  useGrokOAuth: () => genericOAuthMock
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn().mockResolvedValue({}),
  getKiroDefaultModelMapping: vi.fn().mockResolvedValue({})
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

import CreateAccountModal from '../CreateAccountModal.vue'

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
    },
    platform: {
      type: String,
      default: ''
    },
    syncCredentials: {
      type: Object,
      default: undefined
    }
  },
  emits: ['update:modelValue'],
  setup(props) {
    return {
      displayValue: () => (props.modelValue as string[]).join(',')
    }
  },
  template: '<span data-testid="model-whitelist-value">{{ displayValue() }}</span>'
})

const OAuthAuthorizationFlowStub = defineComponent({
  name: 'OAuthAuthorizationFlow',
  setup(_, { expose }) {
    expose({
      authCode: 'oauth-code',
      oauthState: 'state-1',
      projectId: '',
      sessionKey: '',
      refreshToken: '',
      sessionToken: '',
      codexSession: '',
      codexPAT: '',
      oauthCallbackPath: '',
      oauthLoginOption: '',
      inputMethod: 'manual',
      reset: vi.fn()
    })
    return {}
  },
  template: '<div data-testid="oauth-flow" />'
})

function mountModal() {
  return mount(CreateAccountModal, {
    props: {
      show: false,
      proxies: [],
      groups: []
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        Select: true,
        Icon: true,
        PlatformIcon: true,
        ProxySelector: true,
        ProxyAdBanner: true,
        GroupSelector: true,
        QuotaLimitCard: true,
        OAuthAuthorizationFlow: OAuthAuthorizationFlowStub,
        ModelWhitelistSelector: ModelWhitelistSelectorStub
      }
    }
  })
}

async function openModal(wrapper: ReturnType<typeof mountModal>) {
  await wrapper.setProps({ show: true })
  await flushPromises()
}

async function advanceToOAuthStep(wrapper: ReturnType<typeof mountModal>) {
  await wrapper.get<HTMLInputElement>('input[data-tour="account-form-name"]').setValue('Claude OAuth')
  await wrapper.get('form#create-account-form').trigger('submit.prevent')
  await flushPromises()
}

describe('CreateAccountModal Anthropic OAuth model mapping', () => {
  beforeEach(() => {
    createAccountMock.mockReset().mockResolvedValue({})
    exchangeCodeMock.mockReset().mockResolvedValue({
      access_token: 'access-token',
      refresh_token: 'refresh-token'
    })
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })
    appShowErrorMock.mockReset()
    appShowSuccessMock.mockReset()
    anthropicOAuthMock.sessionId.value = 'session-1'
    anthropicOAuthMock.loading.value = false
    anthropicOAuthMock.error.value = ''
  })

  it('shows the default Claude model whitelist for Anthropic OAuth creation', async () => {
    const wrapper = mountModal()
    await openModal(wrapper)

    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe(claudeModels.join(','))
  })

  it('persists the selected Claude model whitelist when creating an Anthropic OAuth account', async () => {
    const wrapper = mountModal()
    await openModal(wrapper)

    await advanceToOAuthStep(wrapper)

    const completeButton = wrapper.findAll('button').find((button) =>
      button.text().includes('admin.accounts.oauth.completeAuth')
    )
    expect(completeButton).toBeTruthy()

    await completeButton!.trigger('click')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.credentials?.model_mapping).toEqual(
      Object.fromEntries(claudeModels.map((model) => [model, model]))
    )
  })
})
