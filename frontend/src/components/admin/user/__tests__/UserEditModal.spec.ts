import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import UserEditModal from '../UserEditModal.vue'
import UserAllowedGroupsModal from '../UserAllowedGroupsModal.vue'

const { update, listGroups, updateUserAttributeValues, showSuccess, showError } = vi.hoisted(() => ({
  update: vi.fn(),
  listGroups: vi.fn(),
  updateUserAttributeValues: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    users: { update },
    groups: { list: listGroups },
    userAttributes: { updateUserAttributeValues }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: vi.fn() })
}))

// useStepUp pulls in the API client, which needs the real i18n instance.
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      params ? `${key}:${JSON.stringify(params)}` : key
  })
}))

const mountModal = (concurrency: number) => mount(UserEditModal, {
  props: {
    show: true,
    user: { id: 7, email: 'user@example.test', username: 'user', notes: '', role: 'user', concurrency, rpm_limit: 0 } as never
  },
  global: {
    stubs: {
      BaseDialog: {
        props: ['show', 'title'],
        template: '<div v-if="show"><slot /><slot name="footer" /></div>'
      },
      Select: true,
      Icon: true,
      UserAttributeForm: true,
      TotpStepUpDialog: true
    }
  }
})

const mountAllowedGroupsModal = () => mount(UserAllowedGroupsModal, {
  props: {
    show: false,
    user: {
      id: 7,
      email: 'user@example.test',
      role: 'user',
      status: 'active',
      concurrency: 3,
      allowed_groups: [20],
      restrict_public_groups: false,
      group_rates: {}
    } as never
  },
  global: {
    stubs: {
      BaseDialog: {
        props: ['show', 'title'],
        template: '<div v-if="show"><slot /><slot name="footer" /></div>'
      },
      PlatformIcon: true
    }
  }
})

describe('UserEditModal concurrency', () => {
  beforeEach(() => {
    update.mockReset()
    updateUserAttributeValues.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
    update.mockResolvedValue({})
    listGroups.mockReset()
    listGroups.mockResolvedValue({
      items: [
        { id: 10, name: 'Public', platform: 'anthropic', status: 'active', subscription_type: 'standard', is_exclusive: false, rate_multiplier: 1 },
        { id: 20, name: 'Exclusive', platform: 'openai', status: 'active', subscription_type: 'standard', is_exclusive: true, rate_multiplier: 1 }
      ]
    })
  })

  // Regression coverage for issue #5977: the gateway treats concurrency <= 0 as
  // unlimited (AcquireUserSlot) and both the batch limits endpoint and the bulk
  // edit modal accept 0, so this dialog must not be the only place that rejects
  // it — doing so blocked every other edit on such a user.
  it('saves an unlimited (0) concurrency instead of blocking the whole form', async () => {
    const wrapper = mountModal(0)

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(showError).not.toHaveBeenCalled()
    expect(update).toHaveBeenCalledWith(7, expect.objectContaining({ concurrency: 0 }))
    expect(wrapper.emitted('success')).toBeTruthy()
  })

  it('still rejects a negative concurrency', async () => {
    const wrapper = mountModal(3)

    await wrapper.get('[data-test="concurrency-input"]').setValue('-1')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('admin.users.concurrencyNonNegative')
    expect(update).not.toHaveBeenCalled()
  })

  it('omits group restriction fields when saving unrelated edits', async () => {
    const wrapper = mountModal(3)

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    const payload = update.mock.calls[0]?.[1]
    expect(payload).not.toHaveProperty('restrict_public_groups')
    expect(payload).not.toHaveProperty('allowed_groups')
  })

  it('sends explicit false when the group editor saves an unrestricted user', async () => {
    const wrapper = mountAllowedGroupsModal()
    await wrapper.setProps({ show: true })
    await flushPromises()

    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()

    expect(update).toHaveBeenCalledWith(7, expect.objectContaining({
      allowed_groups: [20],
      restrict_public_groups: false
    }))
  })
})
