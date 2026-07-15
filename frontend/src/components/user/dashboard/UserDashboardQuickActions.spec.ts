import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { describe, expect, it, vi } from 'vitest'

import UserDashboardQuickActions from './UserDashboardQuickActions.vue'

const routerPush = vi.hoisted(() => vi.fn())
const refreshBatchImageAccess = vi.hoisted(() => vi.fn())

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: routerPush })
}))

vi.mock('@/composables/useBatchImageAccess', () => ({
  useBatchImageAccess: () => ({
    canUseBatchImage: false,
    refreshBatchImageAccess
  })
}))

function mountActions() {
  const i18n = createI18n({
    legacy: false,
    locale: 'en',
    messages: {
      en: {
        dashboard: {
          quickActions: () => 'Quick actions',
          createApiKey: () => 'Create API key',
          generateNewKey: () => 'Generate a new key',
          viewUsage: () => 'View usage',
          checkDetailedLogs: () => 'Check detailed logs',
          redeemCode: () => 'Redeem code',
          addBalanceWithCode: () => 'Add balance with a code'
        },
        gettingStarted: {
          dashboard: {
            quickActionTitle: () => 'Beginner Guide',
            quickActionDescription: () =>
              'Set up Claude Code or Codex and complete your first task.'
          }
        }
      }
    }
  })

  return mount(UserDashboardQuickActions, {
    global: { plugins: [i18n] }
  })
}

describe('UserDashboardQuickActions beginner guide entry', () => {
  it('always renders the canonical guide action near Create API key', () => {
    const wrapper = mountActions()
    const actions = wrapper.findAll('button')
    const guideIndex = actions.findIndex((action) => action.text().includes('Beginner Guide'))
    const keysIndex = actions.findIndex((action) => action.text().includes('Create API key'))

    expect(guideIndex).toBeGreaterThanOrEqual(0)
    expect(Math.abs(guideIndex - keysIndex)).toBe(1)
    expect(actions[guideIndex].text()).toContain('Claude Code or Codex')
  })

  it('navigates to the public canonical route without depending on prompt state', async () => {
    const wrapper = mountActions()
    const guide = wrapper
      .findAll('button')
      .find((action) => action.text().includes('Beginner Guide'))

    expect(guide).toBeDefined()
    await guide!.trigger('click')

    expect(routerPush).toHaveBeenCalledWith('/getting-started')
  })
})
