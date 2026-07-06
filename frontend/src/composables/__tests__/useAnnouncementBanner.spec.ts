import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'

import { useAnnouncementBanner, type UseAnnouncementBannerOptions } from '@/composables/useAnnouncementBanner'

const { appState } = vi.hoisted(() => ({
  appState: {
    cachedPublicSettings: null as null | {
      announcement_banners?: Array<{ id: string; text_zh: string; text_en: string }>
      announcement_banner_interval_ms?: number
    },
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    get cachedPublicSettings() {
      return appState.cachedPublicSettings
    },
  }),
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ locale: { value: 'zh' } }),
}))

// Mount a tiny host so the composable runs inside a component setup (it uses
// onMounted / onBeforeUnmount). The composable's return is exposed for asserting.
function mountBanner(options?: UseAnnouncementBannerOptions) {
  let api!: ReturnType<typeof useAnnouncementBanner>
  const Host = defineComponent({
    setup() {
      api = useAnnouncementBanner(options)
      return () => h('div')
    },
  })
  const wrapper = mount(Host)
  return { wrapper, api: () => api }
}

describe('useAnnouncementBanner', () => {
  beforeEach(() => {
    appState.cachedPublicSettings = null
    sessionStorage.clear()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('hides the bar when no banners configured and no fallback given', async () => {
    appState.cachedPublicSettings = { announcement_banners: [], announcement_banner_interval_ms: 3000 }
    const { api } = mountBanner()
    await flushPromises()
    expect(api().visible.value).toBe(false)
  })

  it('falls back to provided text (bar visible) when list empty', async () => {
    appState.cachedPublicSettings = { announcement_banners: [], announcement_banner_interval_ms: 3000 }
    const { api } = mountBanner({ fallbackText: () => '默认文案' })
    await flushPromises()
    expect(api().visible.value).toBe(true)
    expect(api().currentBannerText.value).toBe('默认文案')
  })

  it('shows configured banners and rotates by the interval', async () => {
    vi.useFakeTimers()
    appState.cachedPublicSettings = {
      announcement_banners: [
        { id: 'a', text_zh: '甲', text_en: 'Alpha' },
        { id: 'b', text_zh: '乙', text_en: 'Beta' },
      ],
      announcement_banner_interval_ms: 3000,
    }
    const { api } = mountBanner()
    await flushPromises()
    expect(api().visible.value).toBe(true)
    expect(api().currentBannerText.value).toBe('甲')
    vi.advanceTimersByTime(3000)
    await flushPromises()
    expect(api().currentBannerText.value).toBe('乙')
  })

  it('does not rotate with a single configured banner', async () => {
    vi.useFakeTimers()
    appState.cachedPublicSettings = {
      announcement_banners: [{ id: 'a', text_zh: '甲', text_en: 'Alpha' }],
      announcement_banner_interval_ms: 3000,
    }
    const { api } = mountBanner()
    await flushPromises()
    vi.advanceTimersByTime(9000)
    await flushPromises()
    expect(api().currentBannerText.value).toBe('甲')
  })

  it('dismiss hides the bar and persists to sessionStorage when a key is given', async () => {
    appState.cachedPublicSettings = {
      announcement_banners: [{ id: 'a', text_zh: '甲', text_en: 'Alpha' }],
      announcement_banner_interval_ms: 3000,
    }
    const { api } = mountBanner({ dismissKey: 'k' })
    await flushPromises()
    expect(api().visible.value).toBe(true)
    api().dismissAnnouncement()
    expect(api().visible.value).toBe(false)
    expect(sessionStorage.getItem('k')).toBe('1')
  })

  it('starts dismissed when the session key was already set', async () => {
    sessionStorage.setItem('k', '1')
    appState.cachedPublicSettings = {
      announcement_banners: [{ id: 'a', text_zh: '甲', text_en: 'Alpha' }],
      announcement_banner_interval_ms: 3000,
    }
    const { api } = mountBanner({ dismissKey: 'k' })
    await flushPromises()
    expect(api().visible.value).toBe(false)
  })
})
