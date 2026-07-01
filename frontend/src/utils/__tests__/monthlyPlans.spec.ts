import { describe, it, expect } from 'vitest'
import type { SubscriptionPlan } from '@/types/payment'
import { getMonthlyPlanDisplay, getMonthlyPlanDisplayFromPlan } from '@/utils/monthlyPlans'

function makePlan(overrides: Partial<SubscriptionPlan>): SubscriptionPlan {
  return {
    id: 1,
    name: 'Basic monthly',
    description: '',
    price: 179,
    validity_days: 30,
    validity_unit: 'day',
    features: [],
    ...overrides,
  } as SubscriptionPlan
}

describe('monthlyPlans dynamic features', () => {
  it('renders default amount lines identical to the previous hardcoded copy (zh)', () => {
    expect(getMonthlyPlanDisplay('basic', 'zh').features).toEqual(['总共可获取：$200', '7 日额度：$50', '可使用充值余额兜底'])
    expect(getMonthlyPlanDisplay('pro', 'zh').features).toEqual(['总共可获取：$1,040', '7 日额度：$260', '适合主力项目'])
    expect(getMonthlyPlanDisplay('max', 'zh').features).toEqual(['总共可获取：$2,200', '7 日额度：$550', '最高月卡额度'])
  })

  it('renders default amount lines identical to the previous hardcoded copy (en)', () => {
    expect(getMonthlyPlanDisplay('basic', 'en').features).toEqual(['Total obtainable: $200', 'Seven-day quota: $50', 'Recharge balance fallback'])
    expect(getMonthlyPlanDisplay('plus', 'en').features).toEqual(['Total obtainable: $440', 'Seven-day quota: $110', 'Quota used before recharge balance'])
  })

  it('follows the DB seven_day_quota_usd override instead of hardcoded amounts', () => {
    const plan = makePlan({ name: 'Basic monthly', seven_day_quota_usd: 100 })
    const display = getMonthlyPlanDisplayFromPlan(plan, 'zh')
    expect(display?.features).toEqual(['总共可获取：$400', '7 日额度：$100', '可使用充值余额兜底'])
    expect(display?.quotaLabel).toBe('$100 / 7 天')
    expect(display?.monthlyTotalLabel).toBe('$400')
  })

  it('falls back to the plan default quota when DB value is null', () => {
    const plan = makePlan({ name: 'Basic monthly', seven_day_quota_usd: null })
    const display = getMonthlyPlanDisplayFromPlan(plan, 'en')
    expect(display?.features).toEqual(['Total obtainable: $200', 'Seven-day quota: $50', 'Recharge balance fallback'])
  })
})
