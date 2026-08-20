import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { announcementsAPI } from '@/api'
import type { UserAnnouncement } from '@/types'

const THROTTLE_MS = 20 * 60 * 1000 // 20 minutes

export interface FetchAnnouncementOptions {
  force?: boolean
  autoPopup?: boolean
}

export const useAnnouncementStore = defineStore('announcements', () => {
  // State
  const announcements = ref<UserAnnouncement[]>([])
  const loading = ref(false)
  const lastFetchTime = ref(0)
  const popupQueue = ref<UserAnnouncement[]>([])
  const currentPopup = ref<UserAnnouncement | null>(null)
  const popupPosition = ref(0)
  const popupTotal = ref(0)
  let popupTransitionTimer: number | null = null

  // Session-scoped dedup set — not reactive, used as plain lookup only
  let shownPopupIds = new Set<number>()

  // Getters
  const unreadCount = computed(() =>
    announcements.value.filter((a) => !a.read_at).length
  )

  // Actions
  async function fetchAnnouncements(options: FetchAnnouncementOptions = {}) {
    const { force = false, autoPopup = true } = options
    const now = Date.now()
    if (!force && lastFetchTime.value > 0 && now - lastFetchTime.value < THROTTLE_MS) {
      return
    }

    // Set immediately to prevent concurrent duplicate requests
    lastFetchTime.value = now

    try {
      loading.value = true
      const all = await announcementsAPI.list(false)
      announcements.value = all.slice(0, 20).map((announcement) => ({ ...announcement }))
      if (autoPopup) enqueueNewPopups()
    } catch (err: any) {
      // Revert throttle timestamp on failure so retry is allowed
      lastFetchTime.value = 0
      console.error('Failed to fetch announcements:', err)
    } finally {
      loading.value = false
    }
  }

  function enqueueNewPopups() {
    const newPopups = announcements.value.filter(
      (a) => a.notify_mode === 'popup' && !a.read_at && !shownPopupIds.has(a.id)
    )

    for (const p of newPopups) {
      const alreadyQueued = popupQueue.value.some((q) => q.id === p.id)
      if (!alreadyQueued && currentPopup.value?.id !== p.id) {
        popupQueue.value.push(p)
        popupTotal.value += 1
      }
    }

    if (!currentPopup.value && popupTransitionTimer === null) {
      showNextPopup()
    }
  }

  function showNextPopup() {
    const next = popupQueue.value.shift()
    if (!next) {
      currentPopup.value = null
      return
    }
    currentPopup.value = next
    shownPopupIds.add(next.id)
    popupPosition.value += 1
  }

  function cancelPopupTransition() {
    if (popupTransitionTimer === null) return
    window.clearTimeout(popupTransitionTimer)
    popupTransitionTimer = null
  }

  async function advancePopup(): Promise<boolean> {
    if (!currentPopup.value) return true
    const id = currentPopup.value.id
    currentPopup.value = null

    if (popupQueue.value.length > 0) {
      popupTransitionTimer = window.setTimeout(() => {
        popupTransitionTimer = null
        showNextPopup()
      }, 300)
    } else {
      popupPosition.value = 0
      popupTotal.value = 0
    }

    try {
      await markAsRead(id)
      return true
    } catch (error) {
      console.error('Failed to mark announcement as read:', error)
      return false
    }
  }

  function snoozePopupBatch(): void {
    cancelPopupTransition()
    if (currentPopup.value) shownPopupIds.add(currentPopup.value.id)
    for (const item of popupQueue.value) shownPopupIds.add(item.id)
    currentPopup.value = null
    popupQueue.value = []
    popupPosition.value = 0
    popupTotal.value = 0
  }

  async function markAsRead(id: number): Promise<void> {
    await announcementsAPI.markRead(id)
    const ann = announcements.value.find((a) => a.id === id)
    if (ann) {
      ann.read_at = new Date().toISOString()
    }
  }

  async function markAllAsRead() {
    const unread = announcements.value.filter((a) => !a.read_at)
    if (unread.length === 0) return

    try {
      loading.value = true
      await Promise.all(unread.map((a) => announcementsAPI.markRead(a.id)))
      announcements.value.forEach((a) => {
        if (!a.read_at) {
          a.read_at = new Date().toISOString()
        }
      })
    } catch (err: any) {
      console.error('Failed to mark all as read:', err)
      throw err
    } finally {
      loading.value = false
    }
  }

  function reset() {
    cancelPopupTransition()
    announcements.value = []
    lastFetchTime.value = 0
    shownPopupIds = new Set()
    popupQueue.value = []
    currentPopup.value = null
    popupPosition.value = 0
    popupTotal.value = 0
    loading.value = false
  }

  return {
    // State
    announcements,
    loading,
    currentPopup,
    popupPosition,
    popupTotal,
    // Getters
    unreadCount,
    // Actions
    fetchAnnouncements,
    advancePopup,
    snoozePopupBatch,
    markAsRead,
    markAllAsRead,
    reset,
  }
})
