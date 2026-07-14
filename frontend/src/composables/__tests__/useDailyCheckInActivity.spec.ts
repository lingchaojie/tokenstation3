import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'

import { useDailyCheckInActivity } from '@/composables/useDailyCheckInActivity'

const { appState } = vi.hoisted(() => ({
  appState: {
    cachedPublicSettings: {
      daily_check_in_enabled: true,
      daily_check_in_start_at: '2026-07-20T00:00:30Z',
      daily_check_in_end_at: '2026-07-20T00:01:00Z',
    },
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => appState,
}))

function mountActivity() {
  let activity!: ReturnType<typeof useDailyCheckInActivity>
  const Host = defineComponent({
    setup() {
      activity = useDailyCheckInActivity()
      return () => h('div')
    },
  })
  const wrapper = mount(Host)
  return { wrapper, activity: () => activity }
}

describe('useDailyCheckInActivity', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-07-20T00:00:00Z'))
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('reacts at activity boundaries without a network request', async () => {
    const { wrapper, activity } = mountActivity()
    expect(activity().active.value).toBe(false)

    await vi.advanceTimersByTimeAsync(30_000)
    expect(activity().active.value).toBe(true)

    await vi.advanceTimersByTimeAsync(30_000)
    expect(activity().active.value).toBe(false)
    wrapper.unmount()
  })
})
