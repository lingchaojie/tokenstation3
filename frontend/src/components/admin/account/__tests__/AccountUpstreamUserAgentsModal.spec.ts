import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AccountUpstreamUserAgentsModal from '../AccountUpstreamUserAgentsModal.vue'

const { getUpstreamUserAgents } = vi.hoisted(() => ({
  getUpstreamUserAgents: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getUpstreamUserAgents,
    },
  },
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        if (key === 'admin.accounts.upstreamUserAgents.count') return `${params?.count} requests`
        return key
      },
    }),
  }
})

const BaseDialogStub = {
  props: ['show', 'title'],
  template: '<div v-if="show" data-test="dialog"><h2>{{ title }}</h2><slot /><slot name="footer" /></div>',
}

const account = {
  id: 42,
  name: 'claude-3-max',
  platform: 'anthropic',
  type: 'oauth',
  status: 'active',
  created_at: '2026-06-18T00:00:00Z',
  updated_at: '2026-06-18T00:00:00Z',
}

function mountModal() {
  return mount(AccountUpstreamUserAgentsModal, {
    props: {
      show: true,
      account,
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
      },
    },
  })
}

describe('AccountUpstreamUserAgentsModal', () => {
  beforeEach(() => {
    getUpstreamUserAgents.mockReset()
  })

  it('loads and displays deduped upstream user agents for the selected account', async () => {
    getUpstreamUserAgents.mockResolvedValue({
      items: [
        {
          account_id: 42,
          user_agent: 'claude-cli/2.1.162 (external, cli)',
          first_seen_at: '2026-06-18T10:00:00Z',
          last_seen_at: '2026-06-18T12:00:00Z',
          seen_count: 3,
        },
        {
          account_id: 42,
          user_agent: 'opencode/0.1.0',
          first_seen_at: '2026-06-18T09:00:00Z',
          last_seen_at: '2026-06-18T09:00:00Z',
          seen_count: 1,
        },
      ],
    })

    const wrapper = mountModal()
    await flushPromises()

    expect(getUpstreamUserAgents).toHaveBeenCalledWith(42)
    expect(wrapper.text()).toContain('claude-cli/2.1.162 (external, cli)')
    expect(wrapper.text()).toContain('opencode/0.1.0')
    expect(wrapper.text()).toContain('3 requests')
  })

  it('shows an empty state when no upstream user agent has been recorded yet', async () => {
    getUpstreamUserAgents.mockResolvedValue({ items: [] })

    const wrapper = mountModal()
    await flushPromises()

    expect(wrapper.text()).toContain('admin.accounts.upstreamUserAgents.empty')
  })
})
