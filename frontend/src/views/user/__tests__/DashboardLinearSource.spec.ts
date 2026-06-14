import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const userDir = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const dashboardViewSource = readFileSync(resolve(userDir, 'DashboardView.vue'), 'utf8')
const dashboardContentSource = readFileSync(resolve(userDir, '..', '..', 'components/user/dashboard/UserDashboardContent.vue'), 'utf8')
const statsSource = readFileSync(resolve(userDir, '..', '..', 'components/user/dashboard/UserDashboardStats.vue'), 'utf8')
const chartsSource = readFileSync(resolve(userDir, '..', '..', 'components/user/dashboard/UserDashboardCharts.vue'), 'utf8')
const tokenUsageTrendSource = readFileSync(resolve(userDir, '..', '..', 'components/charts/TokenUsageTrend.vue'), 'utf8')
const recentSource = readFileSync(resolve(userDir, '..', '..', 'components/user/dashboard/UserDashboardRecentUsage.vue'), 'utf8')
const quickActionsSource = readFileSync(resolve(userDir, '..', '..', 'components/user/dashboard/UserDashboardQuickActions.vue'), 'utf8')
const statCardSource = readFileSync(resolve(userDir, '..', '..', 'components/common/StatCard.vue'), 'utf8')

describe('Dashboard Linear page contract', () => {
  it('keeps the route view as a thin wrapper around shared dashboard content', () => {
    expect(dashboardViewSource).toContain('UserDashboardContent')
    expect(dashboardViewSource).not.toContain('usageAPI.getDashboardStats')
    expect(dashboardViewSource).not.toContain('paymentAPI.getCheckoutInfo')
  })

  it('wraps dashboard content in Linear page and panel classes', () => {
    expect(dashboardContentSource).toContain('linear-dashboard-page')
    expect(dashboardContentSource).toContain('linx-panel')
    expect(dashboardContentSource).toContain('lg:grid-cols-[1.2fr_0.8fr]')
  })

  it('uses restrained surface cards for dashboard widgets', () => {
    expect(statsSource).toContain('linx-panel')
    expect(chartsSource).toContain('linx-panel')
    expect(tokenUsageTrendSource).toContain('linx-panel p-5')
    expect(tokenUsageTrendSource).toContain('text-sm font-semibold tracking-[-0.02em] text-gray-950 dark:text-linear-ink')
    expect(tokenUsageTrendSource).toContain('text-xs text-gray-500 dark:text-linear-ink-subtle')
    expect(recentSource).toContain('linx-panel')
    expect(quickActionsSource).toContain('linx-panel')
    expect(statCardSource).not.toContain('shadow-glow')
  })

  it('keeps nested chart panels stacked until very wide screens', () => {
    expect(dashboardContentSource).toContain('lg:grid-cols-[1.2fr_0.8fr]')
    expect(dashboardContentSource).toContain('min-w-0 space-y-5')
    expect(chartsSource).toContain('2xl:grid-cols-2')
    expect(chartsSource).not.toContain('lg:grid-cols-2')
    expect(chartsSource).toContain('min-w-0')
    expect(chartsSource).toContain('xl:flex-row')
  })
})
