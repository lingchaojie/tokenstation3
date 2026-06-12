import type { SubscriptionPlan } from '@/types/payment'

export type MonthlyPlanKey = 'basic' | 'plus' | 'pro' | 'max'
export type MonthlyPlanLocale = 'en' | 'zh'
export type SubscriptionPlanSelectIntent = 'renew' | 'switch' | 'subscribe'

export interface MonthlyPlanDisplay {
  key: MonthlyPlanKey
  rank: number
  name: string
  badge: string
  priceCny: number
  priceLabel: string
  sevenDayQuotaUsd: number
  quotaLabel: string
  monthlyTotalUsd: number
  monthlyTotalLabel: string
  description: string
  benefits: string[]
  features: string[]
  featured: boolean
}

interface MonthlyPlanBase {
  rank: number
  priceCny: number
  sevenDayQuotaUsd: number
  featured: boolean
}

interface MonthlyPlanCopy {
  name: string
  badge: string
  description: string
  benefits: string[]
  features: string[]
}

export const monthlyPlanKeys: MonthlyPlanKey[] = ['basic', 'plus', 'pro', 'max']

export const monthlyPlanPricesCny: Record<MonthlyPlanKey, number> = {
  basic: 179,
  plus: 399,
  pro: 799,
  max: 1599,
}

export const monthlyPlanRank: Record<MonthlyPlanKey, number> = {
  basic: 1,
  plus: 2,
  pro: 3,
  max: 4,
}

const monthlyPlanBase: Record<MonthlyPlanKey, MonthlyPlanBase> = {
  basic: { rank: monthlyPlanRank.basic, priceCny: monthlyPlanPricesCny.basic, sevenDayQuotaUsd: 50, featured: false },
  plus: { rank: monthlyPlanRank.plus, priceCny: monthlyPlanPricesCny.plus, sevenDayQuotaUsd: 110, featured: false },
  pro: { rank: monthlyPlanRank.pro, priceCny: monthlyPlanPricesCny.pro, sevenDayQuotaUsd: 260, featured: true },
  max: { rank: monthlyPlanRank.max, priceCny: monthlyPlanPricesCny.max, sevenDayQuotaUsd: 550, featured: false },
}

const monthlyPlanCopy: Record<MonthlyPlanKey, Record<MonthlyPlanLocale, MonthlyPlanCopy>> = {
  basic: {
    zh: {
      name: 'Basic 月卡',
      badge: '入门',
      description: '适合轻量试用、小型自动化，以及保持个人编程 Agent 在线。',
      benefits: ['轻量 Claude Code 会话', 'OpenAI 兼容接口调试', '个人脚本和低频项目'],
      features: ['总共可获取：$200', '7 日额度：$50', '可使用充值余额兜底'],
    },
    en: {
      name: 'Basic monthly',
      badge: 'Start',
      description: 'For focused LINX2 trials, small automations, and keeping a personal coding agent online.',
      benefits: ['Light Claude Code sessions', 'OpenAI-compatible API testing', 'Personal scripts and low-frequency projects'],
      features: ['Total obtainable: $200', 'Seven-day quota: $50', 'Recharge balance fallback'],
    },
  },
  plus: {
    zh: {
      name: 'Plus 月卡',
      badge: '日常',
      description: '适合日常开发，把 LINX2 作为个人工作的默认统一网关。',
      benefits: ['日常 Claude Code 编程', 'OpenAI Responses / Chat 工作流', '个人开发者和原型项目'],
      features: ['总共可获取：$440', '7 日额度：$110', '优先扣月卡额度'],
    },
    en: {
      name: 'Plus monthly',
      badge: 'Daily',
      description: 'For regular development days where LINX2 becomes the default gateway for individual work.',
      benefits: ['Daily Claude Code development', 'OpenAI Responses / Chat workflows', 'Solo builders and prototype projects'],
      features: ['Total obtainable: $440', 'Seven-day quota: $110', 'Quota used before recharge balance'],
    },
  },
  pro: {
    zh: {
      name: 'Pro 月卡',
      badge: '热门',
      description: '适合主力项目、更重的 Agent 循环，以及需要更大订阅额度的团队。',
      benefits: ['主力项目和长会话开发', '高频 Claude Code / OpenAI 调用', '小团队共享工作负载'],
      features: ['总共可获取：$1,040', '7 日额度：$260', '适合主力项目'],
    },
    en: {
      name: 'Pro monthly',
      badge: 'Popular',
      description: 'For primary projects, heavier agent loops, and teams that need more room before fallback billing.',
      benefits: ['Primary projects and long coding sessions', 'Frequent Claude Code / OpenAI calls', 'Small-team shared workloads'],
      features: ['Total obtainable: $1,040', 'Seven-day quota: $260', 'Built for active projects'],
    },
  },
  max: {
    zh: {
      name: 'Max 月卡',
      badge: '高强度',
      description: '适合并行项目、长会话和高强度 LINX2 流量。',
      benefits: ['多项目并行和高并发使用', '重度 Agent 循环与批量任务', '高频 Claude Code / OpenAI 生产流量'],
      features: ['总共可获取：$2,200', '7 日额度：$550', '最高月卡额度'],
    },
    en: {
      name: 'Max monthly',
      badge: 'Scale',
      description: 'For demanding users running parallel projects, longer sessions, and high-intensity LINX2 traffic.',
      benefits: ['Parallel projects and high-concurrency usage', 'Heavy agent loops and batch tasks', 'High-frequency Claude Code / OpenAI production traffic'],
      features: ['Total obtainable: $2,200', 'Seven-day quota: $550', 'Highest monthly card capacity'],
    },
  },
}

export function normalizeMonthlyPlanLocale(locale: string | null | undefined): MonthlyPlanLocale {
  return String(locale || '').startsWith('zh') ? 'zh' : 'en'
}

export function monthlyPlanKeyFromName(name: string | null | undefined): MonthlyPlanKey | null {
  const normalized = (name || '').toLowerCase().trim()
  if (normalized.startsWith('basic')) return 'basic'
  if (normalized.startsWith('plus')) return 'plus'
  if (normalized.startsWith('pro')) return 'pro'
  if (normalized.startsWith('max')) return 'max'
  return null
}

export function monthlyPlanKeyFromPlan(plan: Pick<SubscriptionPlan, 'name'> | null | undefined): MonthlyPlanKey | null {
  return monthlyPlanKeyFromName(plan?.name)
}

export function formatMonthlyPlanCny(value: number): string {
  return `¥${Number.isFinite(value) ? value : 0}`
}

export function formatMonthlyPlanUsd(value: number): string {
  const amount = Number.isFinite(value) ? value : 0
  return `$${amount.toLocaleString('en-US', { maximumFractionDigits: 2 })}`
}

function quotaLabel(quotaUsd: number, locale: MonthlyPlanLocale): string {
  return `${formatMonthlyPlanUsd(quotaUsd)} / ${locale === 'zh' ? '7 天' : '7 days'}`
}

function displayFor(key: MonthlyPlanKey, locale: MonthlyPlanLocale, overrides?: { priceCny?: number; sevenDayQuotaUsd?: number }): MonthlyPlanDisplay {
  const base = monthlyPlanBase[key]
  const copy = monthlyPlanCopy[key][locale]
  const priceCny = overrides?.priceCny ?? base.priceCny
  const sevenDayQuotaUsd = overrides?.sevenDayQuotaUsd ?? base.sevenDayQuotaUsd
  const monthlyTotalUsd = sevenDayQuotaUsd * 4

  return {
    key,
    rank: base.rank,
    name: copy.name,
    badge: copy.badge,
    priceCny,
    priceLabel: formatMonthlyPlanCny(priceCny),
    sevenDayQuotaUsd,
    quotaLabel: quotaLabel(sevenDayQuotaUsd, locale),
    monthlyTotalUsd,
    monthlyTotalLabel: formatMonthlyPlanUsd(monthlyTotalUsd),
    description: copy.description,
    benefits: copy.benefits,
    features: copy.features,
    featured: base.featured,
  }
}

export function getMonthlyPlanDisplay(key: MonthlyPlanKey, locale: string | null | undefined): MonthlyPlanDisplay {
  return displayFor(key, normalizeMonthlyPlanLocale(locale))
}

export function getMonthlyPlanDisplayFromName(name: string | null | undefined, locale: string | null | undefined): MonthlyPlanDisplay | null {
  const key = monthlyPlanKeyFromName(name)
  return key ? getMonthlyPlanDisplay(key, locale) : null
}

export function getMonthlyPlanDisplayFromPlan(plan: SubscriptionPlan, locale: string | null | undefined): MonthlyPlanDisplay | null {
  const key = monthlyPlanKeyFromPlan(plan)
  if (!key) return null
  return displayFor(key, normalizeMonthlyPlanLocale(locale), {
    priceCny: plan.price,
    sevenDayQuotaUsd: plan.seven_day_quota_usd ?? monthlyPlanBase[key].sevenDayQuotaUsd,
  })
}

export function getMonthlyPlanCards(locale: string | null | undefined): MonthlyPlanDisplay[] {
  return monthlyPlanKeys.map(key => getMonthlyPlanDisplay(key, locale))
}

export function displayMonthlyPlanName(name: string | null | undefined, locale: string | null | undefined): string | null {
  if (!name) return null
  return getMonthlyPlanDisplayFromName(name, locale)?.name ?? name
}

export function compareMonthlyPlanKeys(a: MonthlyPlanKey, b: MonthlyPlanKey): number {
  return monthlyPlanRank[a] - monthlyPlanRank[b]
}
