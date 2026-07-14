import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import { useAppStore } from '@/stores'
import { isDailyCheckInActive } from '@/utils/dailyCheckIn'

export function useDailyCheckInActivity() {
  const appStore = useAppStore()
  const now = ref(Date.now())
  let timer: ReturnType<typeof setInterval> | undefined

  const active = computed(() => isDailyCheckInActive(appStore.cachedPublicSettings, now.value))

  onMounted(() => {
    timer = setInterval(() => {
      now.value = Date.now()
    }, 30_000)
  })

  onBeforeUnmount(() => {
    if (timer) clearInterval(timer)
  })

  return { active }
}
