import { expectTypeOf } from 'vitest'
import type { ApiKeyTrendParams, UserTrendParams } from '@/api/admin/dashboard'
import { withEncodedExcludedUserIds } from '@/utils/excludedUserIds'

const encoded = withEncodedExcludedUserIds({ page: 2, exclude_user_ids: [9, 3] })

expectTypeOf(encoded).toHaveProperty('exclude_user_ids').toEqualTypeOf<string | undefined>()
expectTypeOf(encoded.exclude_user_ids).not.toEqualTypeOf<number[]>()
expectTypeOf<ApiKeyTrendParams>().not.toHaveProperty('exclude_user_ids')
expectTypeOf<UserTrendParams>().not.toHaveProperty('exclude_user_ids')
