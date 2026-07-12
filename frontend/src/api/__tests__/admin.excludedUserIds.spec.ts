import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({
  get: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: {
    get
  },
  buildGatewayUrl: vi.fn()
}))

import { getStats as getUsageStats, list as listUsage } from '@/api/admin/usage'
import {
  getGroupStats,
  getModelStats,
  getSnapshotV2,
  getUserBreakdown,
  getUsageTrend
} from '@/api/admin/dashboard'
import { listErrorLogs } from '@/api/admin/ops'

type ExcludedUserIdsParams = { exclude_user_ids?: number[] }
type RequestWithExcludedUsers = (params: ExcludedUserIdsParams) => Promise<unknown>

const usageRequests: [string, RequestWithExcludedUsers, boolean][] = [
  ['/admin/usage', listUsage, true],
  ['/admin/usage/stats', getUsageStats, false]
]

const dashboardRequests: [string, RequestWithExcludedUsers][] = [
  ['/admin/dashboard/trend', getUsageTrend],
  ['/admin/dashboard/models', getModelStats],
  ['/admin/dashboard/groups', getGroupStats],
  ['/admin/dashboard/snapshot-v2', getSnapshotV2],
  ['/admin/dashboard/user-breakdown', getUserBreakdown]
]

describe('admin excluded-user API query encoding', () => {
  beforeEach(() => {
    get.mockReset()
    get.mockResolvedValue({ data: {} })
  })

  it.each(usageRequests)('encodes excluded users for %s without mutating params', async (path, request, includesSignal) => {
    const params = { exclude_user_ids: [9, 3, 9] }

    await request(params)

    expect(get).toHaveBeenCalledWith(path, {
      params: { exclude_user_ids: '3,9' },
      ...(includesSignal ? { signal: undefined } : {})
    })
    expect(params).toEqual({ exclude_user_ids: [9, 3, 9] })
  })

  it.each(dashboardRequests)('encodes excluded users for %s without mutating params', async (path, request) => {
    const params = { exclude_user_ids: [9, 3, 9] }

    await request(params)

    expect(get).toHaveBeenCalledWith(path, {
      params: { exclude_user_ids: '3,9' }
    })
    expect(params).toEqual({ exclude_user_ids: [9, 3, 9] })
  })

  it('encodes excluded users for Ops error logs without mutating params', async () => {
    const params = { exclude_user_ids: [9, 3, 9] }

    await listErrorLogs(params)

    expect(get).toHaveBeenCalledWith('/admin/ops/errors', {
      params: { exclude_user_ids: '3,9' }
    })
    expect(params).toEqual({ exclude_user_ids: [9, 3, 9] })
  })

  it.each([
    ...usageRequests.map(([path, request, includesSignal]) => [path, request, includesSignal] as const),
    ...dashboardRequests.map(([path, request]) => [path, request, false] as const),
    ['/admin/ops/errors', listErrorLogs, false] as const
  ])('maps empty exclusions to an omitted-compatible value for %s', async (path, request, includesSignal) => {
    const params = { exclude_user_ids: [] }

    await request(params)

    expect(get).toHaveBeenCalledWith(path, {
      params: { exclude_user_ids: undefined },
      ...(includesSignal ? { signal: undefined } : {})
    })
    expect(params).toEqual({ exclude_user_ids: [] })
  })

  it.each([
    ['/admin/dashboard/trend', getUsageTrend],
    ['/admin/dashboard/models', getModelStats],
    ['/admin/dashboard/groups', getGroupStats],
    ['/admin/dashboard/snapshot-v2', getSnapshotV2]
  ] as const)('preserves undefined params for optional dashboard request %s', async (path, request) => {
    await request()

    expect(get).toHaveBeenCalledWith(path, { params: undefined })
  })
})
