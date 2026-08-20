import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { announcementsAPI } from '@/api'
import { useAnnouncementStore } from '../announcements'

vi.mock('@/api', () => ({ announcementsAPI: { list: vi.fn(), markRead: vi.fn() } }))

const first = { id: 2, title: 'New', content: 'New body', notify_mode: 'popup' as const,
  created_at: '2026-08-20T02:00:00Z', updated_at: '2026-08-20T02:00:00Z' }
const second = { id: 1, title: 'Older', content: 'Older body', notify_mode: 'popup' as const,
  created_at: '2026-08-19T02:00:00Z', updated_at: '2026-08-19T02:00:00Z' }

describe('announcement popup batches', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks(); vi.useFakeTimers()
    vi.mocked(announcementsAPI.list).mockResolvedValue([first, second])
    vi.mocked(announcementsAPI.markRead).mockResolvedValue({ message: 'ok' })
  })
  afterEach(() => vi.useRealTimers())

  it('loads admin data without auto-enqueueing', async () => {
    const store = useAnnouncementStore()
    await store.fetchAnnouncements({ force: true, autoPopup: false })
    expect(store.announcements).toHaveLength(2)
    expect(store.currentPopup).toBeNull()
  })

  it('marks only the current item read and advances', async () => {
    const store = useAnnouncementStore()
    await store.fetchAnnouncements({ force: true, autoPopup: true })
    expect(store.currentPopup?.id).toBe(2)
    expect(store.popupPosition).toBe(1)
    expect(store.popupTotal).toBe(2)
    await expect(store.advancePopup()).resolves.toBe(true)
    expect(announcementsAPI.markRead).toHaveBeenCalledWith(2)
    await vi.advanceTimersByTimeAsync(300)
    expect(store.currentPopup?.id).toBe(1)
    expect(store.popupPosition).toBe(2)
  })

  it('snoozes the whole batch without reading', async () => {
    const store = useAnnouncementStore()
    await store.fetchAnnouncements({ force: true, autoPopup: true })
    store.snoozePopupBatch()
    expect(store.currentPopup).toBeNull()
    expect(announcementsAPI.markRead).not.toHaveBeenCalled()
    await store.fetchAnnouncements({ force: true, autoPopup: true })
    expect(store.currentPopup).toBeNull()
  })

  it('advances but reports failed persistence', async () => {
    vi.mocked(announcementsAPI.markRead).mockRejectedValueOnce(new Error('offline'))
    const store = useAnnouncementStore()
    await store.fetchAnnouncements({ force: true, autoPopup: true })
    await expect(store.advancePopup()).resolves.toBe(false)
    expect(store.announcements.find((item) => item.id === 2)?.read_at).toBeUndefined()
  })
})
