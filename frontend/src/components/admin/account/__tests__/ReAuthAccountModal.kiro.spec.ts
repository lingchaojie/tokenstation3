import { defineComponent, nextTick } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Account } from '@/types'

const { applyOAuthCredentialsMock, kiroImportTokenMock } = vi.hoisted(() => ({
  applyOAuthCredentialsMock: vi.fn(),
  kiroImportTokenMock: vi.fn()
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn()
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      applyOAuthCredentials: applyOAuthCredentialsMock
    },
    kiro: {
      importToken: kiroImportTokenMock
    }
  }
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

import ReAuthAccountModal from '../ReAuthAccountModal.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

const SelectStub = defineComponent({
  name: 'TestSelect',
  props: {
    modelValue: String,
    options: { type: Array, default: () => [] }
  },
  emits: ['update:modelValue'],
  template: `
    <select :value="modelValue" @change="$emit('update:modelValue', $event.target.value)">
      <option v-for="option in options" :key="option.value" :value="option.value">
        {{ option.label }}
      </option>
    </select>
  `
})

const account = {
  id: 42,
  name: 'External IdP account',
  platform: 'kiro',
  type: 'oauth',
  credentials: {
    access_token: 'old-access-token',
    machine_id: 'preserve-machine-id',
    auth_method: 'external_idp',
    provider: 'ExternalIdp',
    client_id: 'old-client-id',
    token_endpoint: 'https://login.microsoftonline.com/tenant-id/oauth2/v2.0/token',
    issuer_url: 'https://login.microsoftonline.com/tenant-id/v2.0',
    api_region: 'eu-central-1'
  },
  proxy_id: null
} as Account

function mountModal() {
  return mount(ReAuthAccountModal, {
    props: { show: false, account },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Select: SelectStub,
        OAuthAuthorizationFlow: true,
        Icon: true
      }
    }
  })
}

async function openImportMode() {
  const wrapper = mountModal()
  await wrapper.setProps({ show: true })
  await flushPromises()
  const button = wrapper.findAll('button').find(candidate =>
    candidate.text().includes('admin.accounts.oauth.kiro.importTitle')
  )
  expect(button).toBeDefined()
  await button?.trigger('click')
  await nextTick()
  return wrapper
}

async function submitImport(wrapper: Awaited<ReturnType<typeof openImportMode>>) {
  const button = wrapper.findAll('button').find(candidate =>
    candidate.text().includes('admin.accounts.oauth.kiro.importAndUpdate')
  )
  expect(button).toBeDefined()
  await button?.trigger('click')
  await flushPromises()
}

describe('ReAuthAccountModal Kiro import', () => {
  beforeEach(() => {
    applyOAuthCredentialsMock.mockReset().mockResolvedValue(account)
    kiroImportTokenMock.mockReset().mockResolvedValue({
      access_token: 'new-access-token',
      refresh_token: 'new-refresh-token',
      auth_method: 'external_idp',
      provider: 'ExternalIdp',
      client_id: 'new-client-id',
      token_endpoint: 'https://login.microsoftonline.com/tenant-id/oauth2/v2.0/token',
      issuer_url: 'https://login.microsoftonline.com/tenant-id/v2.0',
      scopes: 'openid offline_access'
    })
  })

  it('exposes all import providers and only requires device registration for IDC providers', async () => {
    const wrapper = await openImportMode()

    expect(wrapper.find('input[value="Google"]').exists()).toBe(true)
    expect(wrapper.find('input[value="Github"]').exists()).toBe(true)
    expect(wrapper.find('input[value="BuilderId"]').exists()).toBe(true)
    expect(wrapper.find('input[value="Enterprise"]').exists()).toBe(true)
    expect(wrapper.find('input[value="ExternalIdp"]').exists()).toBe(true)
    expect(wrapper.findAll('textarea')).toHaveLength(1)

    await wrapper.get('input[value="Enterprise"]').setValue()
    expect(wrapper.findAll('textarea')).toHaveLength(2)

    await wrapper.get('input[value="ExternalIdp"]').setValue()
    expect(wrapper.findAll('textarea')).toHaveLength(1)
  })

  it.each([
    ['invalid JSON', '{not-json'],
    ['provider mismatch', '{"provider":"Github","accessToken":"access-token"}']
  ])('rejects %s before calling the import API', async (_name, tokenJSON) => {
    const wrapper = await openImportMode()
    await wrapper.get('textarea').setValue(tokenJSON)
    await submitImport(wrapper)

    expect(kiroImportTokenMock).not.toHaveBeenCalled()
    expect(applyOAuthCredentialsMock).not.toHaveBeenCalled()
  })

  it('preserves existing credentials and the independently configured API region', async () => {
    const wrapper = await openImportMode()
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('eu-central-1')
    await wrapper.get('textarea').setValue(JSON.stringify({
      accessToken: 'new-access-token',
      refreshToken: 'new-refresh-token',
      authMethod: 'external_idp',
      provider: 'ExternalIdp',
      clientId: 'new-client-id',
      tokenEndpoint: 'https://login.microsoftonline.com/tenant-id/oauth2/v2.0/token',
      issuerUrl: 'https://login.microsoftonline.com/tenant-id/v2.0',
      scopes: 'openid offline_access'
    }))
    await submitImport(wrapper)

    expect(kiroImportTokenMock).toHaveBeenCalledTimes(1)
    expect(applyOAuthCredentialsMock).toHaveBeenCalledWith(42, {
      type: 'oauth',
      credentials: expect.objectContaining({
        machine_id: 'preserve-machine-id',
        provider: 'ExternalIdp',
        client_id: 'new-client-id',
        token_endpoint: 'https://login.microsoftonline.com/tenant-id/oauth2/v2.0/token',
        api_region: 'eu-central-1'
      })
    })
  })
})
