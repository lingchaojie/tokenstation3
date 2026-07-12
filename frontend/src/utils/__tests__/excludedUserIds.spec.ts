import { describe, expect, it } from 'vitest'
import {
  encodeExcludedUserIds,
  normalizeExcludedUserIds,
  withEncodedExcludedUserIds
} from '@/utils/excludedUserIds'

describe('excluded user IDs', () => {
  it('normalizes to sorted unique positive safe integers', () => {
    expect(normalizeExcludedUserIds([9, 3, 9, 0, -1, Number.MAX_SAFE_INTEGER + 1])).toEqual([3, 9])
  })

  it('encodes normalized IDs as a comma-separated query value', () => {
    expect(encodeExcludedUserIds([9, 3, 9])).toBe('3,9')
  })

  it('omits an empty exclusion list', () => {
    expect(encodeExcludedUserIds([])).toBeUndefined()
    expect(encodeExcludedUserIds()).toBeUndefined()
  })

  it('maps API params without mutating the caller input', () => {
    const params = { page: 2, exclude_user_ids: [9, 3, 9] }

    const encoded = withEncodedExcludedUserIds(params)

    expect(encoded).toEqual({ page: 2, exclude_user_ids: '3,9' })
    expect(params).toEqual({ page: 2, exclude_user_ids: [9, 3, 9] })
  })
})
