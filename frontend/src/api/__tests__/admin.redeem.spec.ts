import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({
  post: vi.fn(),
}))

vi.mock('../client', () => ({
  apiClient: {
    post,
  },
}))

import { batchUpdate, generate } from '../admin/redeem'
import type { BatchUpdateRedeemCodeFields, RedeemCode } from '../../types'

describe('admin redeem api', () => {
  beforeEach(() => {
    post.mockReset()
    post.mockResolvedValue({ data: [] })
  })

  it('keeps positional subscription group generation payloads compatible', async () => {
    await generate(3, 'subscription', 0, 12, 30, 7)

    expect(post).toHaveBeenCalledWith('/admin/redeem-codes/generate', {
      count: 3,
      type: 'subscription',
      value: 0,
      group_id: 12,
      validity_days: 30,
      expires_in_days: 7,
    })
  })

  it('builds plan-mode subscription generation payloads from options object', async () => {
    await generate(2, 'subscription', 0, {
      planId: 42,
      validityDays: 0,
      expiresInDays: 14,
    })

    expect(post).toHaveBeenCalledWith('/admin/redeem-codes/generate', {
      count: 2,
      type: 'subscription',
      value: 0,
      plan_id: 42,
      validity_days: 0,
      expires_in_days: 14,
    })
  })

  it('preserves negative validity days for legacy subscription group generation', async () => {
    await generate(1, 'subscription', 0, 7, -1)

    expect(post).toHaveBeenCalledWith('/admin/redeem-codes/generate', {
      count: 1,
      type: 'subscription',
      value: 0,
      group_id: 7,
      validity_days: -1,
    })
  })

  it('allows batch update fields to carry nullable plan_id', async () => {
    const fields: BatchUpdateRedeemCodeFields = {
      plan_id: null,
    }

    await batchUpdate([5, 6], fields)

    expect(post).toHaveBeenCalledWith('/admin/redeem-codes/batch-update', {
      ids: [5, 6],
      fields: {
        plan_id: null,
      },
    })
  })

  it('accepts redeem code responses with missing compact plan and preserved plan_id', () => {
    const code: RedeemCode = {
      id: 1,
      code: 'PLAN-CODE',
      type: 'subscription',
      value: 0,
      status: 'active',
      used_by: null,
      used_at: null,
      created_at: '2026-06-15T00:00:00Z',
      plan_id: 42,
      plan: {
        id: 42,
        name: 'Pro Monthly',
        product_name: 'Pro',
        validity_days: 30,
        validity_unit: 'month',
        seven_day_quota_usd: 10,
        for_sale: true,
      },
    }

    const missingPlan: RedeemCode = {
      ...code,
      plan: undefined,
    }

    expect(code.plan_id).toBe(42)
    expect(code.plan?.product_name).toBe('Pro')
    expect(missingPlan.plan).toBeUndefined()
  })
})
