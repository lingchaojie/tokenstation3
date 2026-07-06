import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useAppStore } from '@/stores'

const DEFAULT_BANNER_INTERVAL_MS = 3000

export interface UseAnnouncementBannerOptions {
  /**
   * Getter for the fallback text shown when no banners are configured.
   * If omitted, an empty banner list means the bar is hidden entirely
   * (used by the authenticated app shell). The homepage passes its built-in
   * bilingual marketing copy so the bar always shows something there.
   */
  fallbackText?: () => string
  /**
   * When set, dismissal is remembered for the browser session under this
   * sessionStorage key. When omitted, dismissal is in-memory only (resets on
   * reload), matching the original homepage behavior.
   */
  dismissKey?: string
}

/**
 * Shared rolling-announcement logic used by both the public homepage and the
 * authenticated app shell. Reads banners + interval from public settings,
 * picks the current-locale text (falling back to the other language), rotates
 * with the configured interval when there is more than one banner, and exposes
 * a dismiss action.
 */
export function useAnnouncementBanner(options: UseAnnouncementBannerOptions = {}) {
  const appStore = useAppStore()
  const { locale } = useI18n()

  const localeCode = computed(() => (String(locale.value).startsWith('zh') ? 'zh' : 'en'))

  // Each banner takes the current-locale text, falling back to the other
  // language; empty-on-both entries are dropped. Empty list falls back to the
  // provided fallbackText, or stays empty (bar hidden) when none is given.
  const banners = computed<string[]>(() => {
    const raw = appStore.cachedPublicSettings?.announcement_banners ?? []
    const zh = localeCode.value === 'zh'
    const texts = raw
      .map((b) => {
        const primary = zh ? b.text_zh : b.text_en
        const fallback = zh ? b.text_en : b.text_zh
        return (primary || fallback || '').trim()
      })
      .filter((s) => s.length > 0)
    if (texts.length > 0) return texts
    const fallback = options.fallbackText?.()
    return fallback ? [fallback] : []
  })

  const bannerIntervalMs = computed(() => {
    const v = appStore.cachedPublicSettings?.announcement_banner_interval_ms
    return typeof v === 'number' && v > 0 ? v : DEFAULT_BANNER_INTERVAL_MS
  })

  const dismissedInSession =
    !!options.dismissKey &&
    typeof sessionStorage !== 'undefined' &&
    sessionStorage.getItem(options.dismissKey) === '1'

  const showAnnouncement = ref(!dismissedInSession)
  const currentBannerIndex = ref(0)
  const currentBannerText = computed(
    () => banners.value[currentBannerIndex.value] ?? banners.value[0] ?? '',
  )
  // The bar renders only when not dismissed and there is at least one banner.
  const visible = computed(() => showAnnouncement.value && banners.value.length > 0)

  let bannerTimer: ReturnType<typeof setInterval> | null = null

  function stopBannerRotation() {
    if (bannerTimer !== null) {
      clearInterval(bannerTimer)
      bannerTimer = null
    }
  }

  function startBannerRotation() {
    stopBannerRotation()
    if (!showAnnouncement.value) return
    if (banners.value.length <= 1) return
    bannerTimer = setInterval(() => {
      currentBannerIndex.value = (currentBannerIndex.value + 1) % banners.value.length
    }, bannerIntervalMs.value)
  }

  function dismissAnnouncement() {
    showAnnouncement.value = false
    stopBannerRotation()
    if (options.dismissKey && typeof sessionStorage !== 'undefined') {
      sessionStorage.setItem(options.dismissKey, '1')
    }
  }

  onMounted(startBannerRotation)

  // Rebuild the timer when the banner set, interval, or visibility changes
  // (public settings load asynchronously, so banners often arrive after mount).
  watch([() => banners.value.length, bannerIntervalMs, showAnnouncement], () => {
    if (currentBannerIndex.value >= banners.value.length) {
      currentBannerIndex.value = 0
    }
    startBannerRotation()
  })

  onBeforeUnmount(stopBannerRotation)

  return {
    visible,
    showAnnouncement,
    banners,
    currentBannerIndex,
    currentBannerText,
    dismissAnnouncement,
  }
}
