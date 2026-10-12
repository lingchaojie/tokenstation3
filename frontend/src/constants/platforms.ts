import { reactive, watchSyncEffect } from 'vue'
import type { AccountPlatform, GroupPlatform } from '@/types'
import { listPlatforms } from './platformCatalog'

export type PlatformOption<T extends string = string> = {
  value: T
  label: string
}

/** Concrete account and group platforms registered in the shared catalog. */
export const CONCRETE_PLATFORM_OPTIONS: PlatformOption<AccountPlatform>[] = reactive([])
export const CONCRETE_PLATFORM_VALUES: AccountPlatform[] = reactive([])
export const GROUP_PLATFORM_OPTIONS: PlatformOption<GroupPlatform>[] = reactive([])
export const GROUP_PLATFORM_VALUES: GroupPlatform[] = reactive([])

watchSyncEffect(() => {
  const concrete = listPlatforms().map(spec => ({ value: spec.id, label: spec.display_name }))
  const values = concrete.map(option => option.value)
  CONCRETE_PLATFORM_OPTIONS.splice(0, CONCRETE_PLATFORM_OPTIONS.length, ...concrete)
  CONCRETE_PLATFORM_VALUES.splice(0, CONCRETE_PLATFORM_VALUES.length, ...values)
  GROUP_PLATFORM_OPTIONS.splice(0, GROUP_PLATFORM_OPTIONS.length, ...concrete)
  GROUP_PLATFORM_VALUES.splice(0, GROUP_PLATFORM_VALUES.length, ...values)
})
