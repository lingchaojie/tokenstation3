import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const getAffiliateDetail = vi.hoisted(() => vi.fn())
const refreshUser = vi.hoisted(() => vi.fn())
const copyToClipboard = vi.hoisted(() => vi.fn())
const authState = vi.hoisted(() => ({
  user: {
    reward_balances: {
      daily_check_in: { amount: 0, expires_at: null },
      affiliate: { amount: 10, earliest_expires_at: '2030-01-08T00:00:00Z', credit_count: 1 },
    },
  },
}))

vi.mock('@/api/user', () => ({
  default: { getAffiliateDetail },
  userAPI: { getAffiliateDetail },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ ...authState, refreshUser }),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard }),
}))

vi.mock('@/utils/apiError', () => ({
  extractApiErrorMessage: () => 'error',
}))

vi.mock('@/utils/format', () => ({
  formatCurrency: (amount: number) => `¥${amount}`,
  formatDateTime: (value: string) => value,
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => {
      const messages: Record<string, string> = {
        'affiliate.title': '邀请返利',
        'affiliate.description': '分享邀请码给新用户',
        'affiliate.immediateTitle': '邀请立刻获得返现',
        'affiliate.immediateRewardIntro': '你获得 {inviter}，好友获得 {invitee}',
        'affiliate.firstRechargeTitle': '邀请好友首充得返现',
        'affiliate.firstRechargeRewardIntro': '好友首充满 {threshold}，你获得 {inviter}，好友获得 {invitee}',
        'affiliate.validityHint': '奖励到账后 {days} 天有效',
        'affiliate.limit.reached': '邀请方奖励已达 {count}/{limit} 上限',
        'affiliate.limit.inviteeStillRewarded': '继续邀请时，好友仍可获得受邀方奖励',
        'affiliate.stats.invitedUsers': '已邀请用户',
        'affiliate.yourCode': '你的邀请码',
        'affiliate.copyCode': '复制邀请码',
        'affiliate.inviteLink': '邀请链接',
        'affiliate.copyLink': '复制链接',
        'affiliate.tips.title': '活动规则',
        'affiliate.tips.immediate': '好友注册后双方奖励立即到账',
        'affiliate.tips.firstRecharge': '好友首次充值达到 {threshold} 后双方到账',
        'affiliate.tips.validity': '每笔奖励自到账起 {days} 天有效',
        'affiliate.invitees.title': '邀请记录',
        'affiliate.invitees.empty': '暂无邀请记录',
        'affiliate.invitees.columns.email': '邮箱',
        'affiliate.invitees.columns.username': '用户名',
        'affiliate.invitees.columns.rebate': '奖励',
        'affiliate.invitees.columns.joinedAt': '注册时间',
      }
      let value = messages[key] ?? key
      for (const [name, replacement] of Object.entries(params ?? {})) {
        value = value.replace(`{${name}}`, String(replacement))
      }
      return value
    },
    }),
  }
})

vi.mock('@/components/user/RewardBalanceBreakdown.vue', () => ({
  default: {
    props: ['summary'],
    template: '<div v-if="summary" data-testid="affiliate-reward-balance">{{ summary.affiliate.amount }}</div>',
  },
}))

import AffiliateView from '../AffiliateView.vue'

function detail(overrides: Record<string, unknown> = {}) {
  return {
    user_id: 1,
    aff_code: 'HELLO10',
    inviter_id: null,
    aff_count: 3,
    aff_quota: 99,
    aff_frozen_quota: 12,
    aff_history_quota: 111,
    first_recharge_threshold: 0,
    inviter_reward: 10,
    invitee_reward: 5,
    reward_mode: 'immediate',
    reward_validity_days: 7,
    inviter_reward_limit: 3,
    inviter_reward_count: 3,
    inviter_reward_limit_reached: true,
    invitees: [],
    ...overrides,
  }
}

function mountView() {
  return mount(AffiliateView, {
    global: {
      stubs: {
        AppLayout: { template: '<main><slot /></main>' },
        Icon: true,
      },
    },
  })
}

describe('AffiliateView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    refreshUser.mockResolvedValue(undefined)
  })

  it('shows immediate rewards and only shows limit progress after the limit is reached', async () => {
    getAffiliateDetail.mockResolvedValue(detail())
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('邀请立刻获得返现')
    expect(wrapper.text()).toContain('你获得 ¥10，好友获得 ¥5')
    expect(wrapper.text()).not.toContain('首充')
    expect(wrapper.text()).toContain('邀请方奖励已达 3/3 上限')
    expect(wrapper.text()).toContain('好友仍可获得受邀方奖励')
    expect(wrapper.text()).toContain('HELLO10')
    expect(wrapper.text()).toContain('复制链接')
    expect(wrapper.get('[data-testid="affiliate-reward-balance"]').text()).toBe('10')
    expect(wrapper.text()).not.toContain('¥99')
    expect(wrapper.text()).not.toContain('¥12')
    expect(wrapper.text()).not.toContain('手动转入')
  })

  it('switches to first-recharge copy and hides progress before the cap is reached', async () => {
    getAffiliateDetail.mockResolvedValue(detail({
      first_recharge_threshold: 20,
      reward_mode: 'first_recharge',
      inviter_reward_count: 2,
      inviter_reward_limit_reached: false,
    }))
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('邀请好友首充得返现')
    expect(wrapper.text()).toContain('好友首充满 ¥20，你获得 ¥10，好友获得 ¥5')
    expect(wrapper.text()).not.toContain('2/3')
  })
})
