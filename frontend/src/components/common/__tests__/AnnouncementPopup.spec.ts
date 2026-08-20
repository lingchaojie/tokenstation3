import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const appState = vi.hoisted(() => ({
  siteName: 'LINX2.AI',
  siteLogo: '',
  showError: vi.fn(),
}))

vi.mock('@/stores/app', () => ({ useAppStore: () => appState }))
vi.mock('@/api', () => ({
  announcementsAPI: {
    list: vi.fn(),
    markRead: vi.fn(),
  },
}))
vi.mock('@/utils/format', () => ({
  formatRelativeWithDateTime: (value: string) => `formatted:${value}`,
}))

import AnnouncementPopup from '../AnnouncementPopup.vue'
import AnnouncementBell from '../AnnouncementBell.vue'
import { announcementsAPI } from '@/api'
import { useAnnouncementStore } from '@/stores/announcements'

const announcementMarkdownStyles = readFileSync(
  resolve(process.cwd(), 'src/styles/announcement-markdown.css'),
  'utf8',
)

const mountedWrappers: VueWrapper[] = []

function track<T extends VueWrapper>(wrapper: T): T {
  mountedWrappers.push(wrapper)
  return wrapper
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

const announcement = {
  id: 1,
  title: 'Preview announcement',
  content: '## Preview heading\n\n<div>HTML content</div><script>window.__xss = true</script>',
  status: 'draft' as const,
  notify_mode: 'popup' as const,
  targeting: { any_of: [] },
  created_at: '2026-07-24T07:30:00Z',
  updated_at: '2026-07-24T07:30:00Z',
}

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })

  return { promise, resolve, reject }
}

function pressTab(target: Element, shiftKey = false): KeyboardEvent {
  const event = new KeyboardEvent('keydown', {
    key: 'Tab',
    shiftKey,
    bubbles: true,
    cancelable: true,
  })
  target.dispatchEvent(event)
  return event
}

describe('AnnouncementPopup', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    appState.siteName = 'LINX2.AI'
    appState.siteLogo = ''
    appState.showError.mockReset()
    vi.mocked(announcementsAPI.list).mockReset()
    vi.mocked(announcementsAPI.markRead).mockReset()
      .mockResolvedValue({ message: 'ok' })
  })

  afterEach(() => {
    for (const wrapper of mountedWrappers.splice(0)) wrapper.unmount()
    vi.useRealTimers()
    document.body.innerHTML = ''
    document.body.style.overflow = ''
  })

  it('renders mixed Markdown and HTML inside the shared styled container', async () => {
    const store = useAnnouncementStore()
    store.currentPopup = {
      id: 1,
      title: 'Mixed content announcement',
      content: [
        '## Markdown heading',
        '',
        '<div><h3>HTML heading</h3><ul><li>HTML list item</li></ul></div>',
        '',
        '<table><thead><tr><th>Status</th></tr></thead><tbody><tr><td>OK</td></tr></tbody></table>',
        '<script>window.__announcementXss = true</script>',
      ].join('\n'),
      notify_mode: 'popup',
      created_at: '2026-07-24T07:30:00Z',
      updated_at: '2026-07-24T07:30:00Z',
    }

    const wrapper = mount(AnnouncementPopup)
    await wrapper.vm.$nextTick()

    const content = document.body.querySelector('.markdown-body')
    expect(content?.querySelector('h2')?.textContent).toBe('Markdown heading')
    expect(content?.querySelector('h3')?.textContent).toBe('HTML heading')
    expect(content?.querySelector('li')?.textContent).toBe('HTML list item')
    expect(content?.querySelector('table td')?.textContent).toBe('OK')
    expect(content?.querySelector('script')).toBeNull()

    wrapper.unmount()
  })

  it.each(['h2', 'h3', 'ul', 'li', 'blockquote', 'table', 'th', 'td', 'code'])(
    'loads a shared style rule for mixed-content <%s> elements',
    (element) => {
      expect(announcementMarkdownStyles).toContain(`.markdown-body ${element}`)
    },
  )

  it('previews an admin announcement without marking it as read', async () => {
    const store = useAnnouncementStore()
    const advance = vi.spyOn(store, 'advancePopup')
    const snooze = vi.spyOn(store, 'snoozePopupBatch')
    const wrapper = mount(AnnouncementPopup, {
      props: {
        announcement,
        preview: true,
      },
    })

    expect(document.body.textContent).toContain('Preview announcement')
    expect(document.body.querySelector('.markdown-body h2')?.textContent).toBe('Preview heading')
    expect(document.body.querySelector('.markdown-body script')).toBeNull()
    expect(document.body.textContent).toContain('common.close')
    expect(document.body.querySelector('[data-testid="announcement-popup-progress"]')?.textContent)
      .toContain('1 / 1')
    expect(document.body.querySelector('[data-testid="announcement-popup-snooze"]')).toBeNull()

    document.body.querySelector<HTMLButtonElement>('[data-testid="announcement-popup-advance"]')
      ?.click()
    await wrapper.vm.$nextTick()
    document.body.querySelector<HTMLButtonElement>('[data-testid="announcement-popup-close"]')
      ?.click()
    await wrapper.vm.$nextTick()

    expect(wrapper.emitted('close')).toHaveLength(2)
    expect(advance).not.toHaveBeenCalled()
    expect(snooze).not.toHaveBeenCalled()

    await wrapper.setProps({ announcement: null })
    expect(document.body.style.overflow).toBe('')
    wrapper.unmount()
  })

  it('renders site branding and batch progress', async () => {
    const store = useAnnouncementStore()
    store.currentPopup = announcement
    store.popupPosition = 1
    store.popupTotal = 3
    appState.siteName = 'Example Station'
    appState.siteLogo = 'https://example.com/logo.png'
    const wrapper = mount(AnnouncementPopup)
    await wrapper.vm.$nextTick()

    const dialog = document.body.querySelector('[data-testid="announcement-popup"]')
    expect(dialog?.getAttribute('role')).toBe('dialog')
    expect(dialog?.getAttribute('aria-modal')).toBe('true')
    expect(dialog?.getAttribute('aria-labelledby')).toBe('announcement-popup-title')
    expect(document.body.querySelector('[data-testid="announcement-popup-brand"]')?.textContent)
      .toContain('Example Station')
    expect(document.body.querySelector<HTMLImageElement>('[data-testid="announcement-popup-logo"]')?.src)
      .toBe('https://example.com/logo.png')
    expect(document.body.querySelector('[data-testid="announcement-popup-progress"]')?.textContent)
      .toContain('1 / 3')
    expect(document.body.querySelector('[data-testid="announcement-popup-advance"]')?.textContent)
      .toContain('announcements.next')
    expect(dialog?.querySelector('time')?.textContent)
      .toContain('formatted:2026-07-24T07:30:00Z')
    expect(document.body.querySelector('[data-testid="announcement-popup-close"]')?.getAttribute('aria-label'))
      .toBe('common.close')

    wrapper.unmount()
  })

  it('falls back to the bundled logo when the configured URL is unsafe', () => {
    const store = useAnnouncementStore()
    store.currentPopup = announcement
    appState.siteName = ''
    appState.siteLogo = 'javascript:alert(1)'
    const wrapper = mount(AnnouncementPopup)

    const logo = document.body.querySelector<HTMLImageElement>('[data-testid="announcement-popup-logo"]')
    expect(logo?.getAttribute('src')).toBe('/linx2-icon.png')
    expect(logo?.getAttribute('alt')).toBe('LINX2.AI')

    wrapper.unmount()
  })

  it('snoozes without reading', async () => {
    const store = useAnnouncementStore()
    store.currentPopup = announcement
    const snooze = vi.spyOn(store, 'snoozePopupBatch')
    const advance = vi.spyOn(store, 'advancePopup')
    const wrapper = mount(AnnouncementPopup)

    document.body.querySelector<HTMLButtonElement>('[data-testid="announcement-popup-snooze"]')
      ?.click()
    await wrapper.vm.$nextTick()

    expect(snooze).toHaveBeenCalledOnce()
    expect(advance).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('closes the batch without reading and restores body scrolling', async () => {
    const store = useAnnouncementStore()
    store.currentPopup = announcement
    const snooze = vi.spyOn(store, 'snoozePopupBatch')
    const advance = vi.spyOn(store, 'advancePopup')
    const wrapper = mount(AnnouncementPopup)

    expect(document.body.style.overflow).toBe('hidden')
    document.body.querySelector<HTMLButtonElement>('[data-testid="announcement-popup-close"]')
      ?.click()
    await wrapper.vm.$nextTick()

    expect(snooze).toHaveBeenCalledOnce()
    expect(advance).not.toHaveBeenCalled()
    expect(document.body.style.overflow).toBe('')
    wrapper.unmount()
  })

  it('advances separately from snooze and restores body scrolling on completion', async () => {
    const store = useAnnouncementStore()
    store.currentPopup = announcement
    store.popupPosition = 1
    store.popupTotal = 1
    const advance = vi.spyOn(store, 'advancePopup').mockImplementation(async () => {
      store.currentPopup = null
      store.popupPosition = 0
      store.popupTotal = 0
      return true
    })
    const snooze = vi.spyOn(store, 'snoozePopupBatch')
    const wrapper = mount(AnnouncementPopup)

    document.body.querySelector<HTMLButtonElement>('[data-testid="announcement-popup-advance"]')
      ?.click()
    await flushPromises()

    expect(advance).toHaveBeenCalledOnce()
    expect(snooze).not.toHaveBeenCalled()
    expect(document.body.style.overflow).toBe('')
    wrapper.unmount()
  })

  it('keeps scroll locked while the next item appears before read persistence settles', async () => {
    vi.useFakeTimers()
    const read = createDeferred<{ message: string }>()
    vi.mocked(announcementsAPI.list).mockResolvedValue([
      { ...announcement, id: 1, title: 'First announcement' },
      { ...announcement, id: 2, title: 'Second announcement' },
    ])
    vi.mocked(announcementsAPI.markRead).mockReturnValue(read.promise)
    const readSettled = vi.fn()
    void read.promise.then(readSettled)
    const store = useAnnouncementStore()
    await store.fetchAnnouncements({ force: true, autoPopup: true })
    const wrapper = track(mount(AnnouncementPopup))

    document.body.querySelector<HTMLButtonElement>('[data-testid="announcement-popup-advance"]')
      ?.click()
    await wrapper.vm.$nextTick()

    expect(store.currentPopup).toBeNull()
    expect(document.body.style.overflow).toBe('hidden')

    await vi.advanceTimersByTimeAsync(300)
    await wrapper.vm.$nextTick()

    expect(store.currentPopup?.id).toBe(2)
    expect(document.body.querySelector('#announcement-popup-title')?.textContent)
      .toContain('Second announcement')
    expect(document.body.style.overflow).toBe('hidden')
    expect(readSettled).not.toHaveBeenCalled()

    read.resolve({ message: 'ok' })
    await flushPromises()
  })

  it('moves focus into the popup, traps Tab, and restores the exact launcher on Escape', async () => {
    const launcher = document.createElement('button')
    launcher.textContent = 'Open announcements'
    document.body.appendChild(launcher)
    launcher.focus()

    const store = useAnnouncementStore()
    store.currentPopup = announcement
    store.popupPosition = 1
    store.popupTotal = 1
    const snooze = vi.spyOn(store, 'snoozePopupBatch')
    const advance = vi.spyOn(store, 'advancePopup')
    const wrapper = track(mount(AnnouncementPopup))
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()

    const close = document.body.querySelector<HTMLElement>(
      '[data-testid="announcement-popup-close"]',
    )!
    const last = document.body.querySelector<HTMLElement>(
      '[data-testid="announcement-popup-advance"]',
    )!
    expect(document.activeElement).toBe(close)

    last.focus()
    const forward = pressTab(last)
    expect(forward.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(close)

    const backward = pressTab(close, true)
    expect(backward.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(last)

    document.dispatchEvent(new KeyboardEvent('keydown', {
      key: 'Escape',
      bubbles: true,
      cancelable: true,
    }))
    await wrapper.vm.$nextTick()

    expect(snooze).toHaveBeenCalledOnce()
    expect(advance).not.toHaveBeenCalled()
    expect(document.activeElement).toBe(launcher)
  })

  it('emits preview close on Escape without reading or snoozing', async () => {
    const store = useAnnouncementStore()
    const snooze = vi.spyOn(store, 'snoozePopupBatch')
    const advance = vi.spyOn(store, 'advancePopup')
    const wrapper = track(mount(AnnouncementPopup, {
      props: { announcement, preview: true },
    }))
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()

    document.dispatchEvent(new KeyboardEvent('keydown', {
      key: 'Escape',
      bubbles: true,
      cancelable: true,
    }))
    await wrapper.vm.$nextTick()

    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(snooze).not.toHaveBeenCalled()
    expect(advance).not.toHaveBeenCalled()
  })

  it('warns when read persistence fails', async () => {
    const store = useAnnouncementStore()
    store.currentPopup = announcement
    store.popupPosition = 1
    store.popupTotal = 2
    vi.spyOn(store, 'advancePopup').mockResolvedValue(false)
    const wrapper = mount(AnnouncementPopup)

    document.body.querySelector<HTMLButtonElement>('[data-testid="announcement-popup-advance"]')
      ?.click()
    await flushPromises()

    expect(appState.showError).toHaveBeenCalledWith('announcements.readSaveFailed')
    wrapper.unmount()
  })

  it('uses the acknowledgement label on the last item', () => {
    const store = useAnnouncementStore()
    store.currentPopup = announcement
    store.popupPosition = 2
    store.popupTotal = 2
    const wrapper = mount(AnnouncementPopup)

    expect(document.body.querySelector('[data-testid="announcement-popup-advance"]')?.textContent)
      .toContain('announcements.acknowledge')

    wrapper.unmount()
  })

  it('restores body scrolling when the popup becomes null and on unmount', async () => {
    const store = useAnnouncementStore()
    store.currentPopup = announcement
    const wrapper = mount(AnnouncementPopup)

    expect(document.body.style.overflow).toBe('hidden')
    store.currentPopup = null
    await wrapper.vm.$nextTick()
    expect(document.body.style.overflow).toBe('')

    store.currentPopup = announcement
    await wrapper.vm.$nextTick()
    expect(document.body.style.overflow).toBe('hidden')

    wrapper.unmount()
    expect(document.body.style.overflow).toBe('')
  })

  it('keeps an active popup locked when an inactive popup mounts and unmounts', async () => {
    document.body.style.overflow = 'clip'
    const store = useAnnouncementStore()
    store.currentPopup = announcement
    const activePopup = mount(AnnouncementPopup)

    expect(document.body.style.overflow).toBe('hidden')

    const inactivePopup = mount(AnnouncementPopup, {
      props: {
        announcement: null,
        preview: true,
      },
    })
    expect(document.body.style.overflow).toBe('hidden')

    inactivePopup.unmount()
    expect(document.body.style.overflow).toBe('hidden')

    store.currentPopup = { ...announcement, id: 2 }
    await activePopup.vm.$nextTick()
    store.currentPopup = null
    await activePopup.vm.$nextTick()
    expect(document.body.style.overflow).toBe('clip')

    activePopup.unmount()
    expect(document.body.style.overflow).toBe('clip')
  })

  it('keeps the bell modal locked until its last concurrent popup owner releases', async () => {
    document.body.style.overflow = 'scroll'
    const bell = mount(AnnouncementBell)
    await bell.get('button').trigger('click')
    expect(document.body.style.overflow).toBe('hidden')

    const store = useAnnouncementStore()
    store.currentPopup = announcement
    const popup = mount(AnnouncementPopup)
    expect(document.body.style.overflow).toBe('hidden')

    popup.unmount()
    expect(document.body.style.overflow).toBe('hidden')

    bell.unmount()
    expect(document.body.style.overflow).toBe('scroll')
  })
})
