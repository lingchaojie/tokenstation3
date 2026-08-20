import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AnnouncementBannerSettingsCard from '../AnnouncementBannerSettingsCard.vue'

const mocks = vi.hoisted(() => ({
  getSettings: vi.fn(),
  updateSettings: vi.fn(),
  fetchPublicSettings: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api', () => ({
  adminAPI: {
    settings: {
      getSettings: mocks.getSettings,
      updateSettings: mocks.updateSettings,
    },
  },
}))
vi.mock('@/stores', () => ({
  useAppStore: () => ({
    fetchPublicSettings: mocks.fetchPublicSettings,
    showSuccess: mocks.showSuccess,
    showError: mocks.showError,
  }),
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('AnnouncementBannerSettingsCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.getSettings.mockResolvedValue({
      announcement_banners: [
        { id: 'first', text_zh: '第一条', text_en: 'First' },
        { id: 'second', text_zh: '第二条', text_en: 'Second' },
      ],
      announcement_banner_interval_ms: 3000,
    })
    mocks.updateSettings.mockImplementation(async (payload) => payload)
    mocks.fetchPublicSettings.mockResolvedValue(null)
  })

  it('loads existing settings', async () => {
    const wrapper = mount(AnnouncementBannerSettingsCard)
    await flushPromises()

    expect(wrapper.get('[data-testid="announcement-banner-zh-0"]').element).toHaveProperty('value', '第一条')
    expect(wrapper.get('[data-testid="announcement-banner-interval"]').element).toHaveProperty('value', '3')
  })

  it('reorders and saves only owned fields', async () => {
    const wrapper = mount(AnnouncementBannerSettingsCard)
    await flushPromises()

    await wrapper.get('[data-testid="announcement-banner-down-0"]').trigger('click')
    await wrapper.get('[data-testid="announcement-banner-save"]').trigger('click')
    await flushPromises()

    expect(mocks.updateSettings).toHaveBeenCalledWith({
      announcement_banners: [
        { id: 'second', text_zh: '第二条', text_en: 'Second' },
        { id: 'first', text_zh: '第一条', text_en: 'First' },
      ],
      announcement_banner_interval_ms: 3000,
    })
    expect(mocks.fetchPublicSettings).toHaveBeenCalledWith(true)
  })

  it('rejects a fully empty item', async () => {
    mocks.getSettings.mockResolvedValue({ announcement_banners: [], announcement_banner_interval_ms: 3000 })
    const wrapper = mount(AnnouncementBannerSettingsCard)
    await flushPromises()

    await wrapper.get('[data-testid="announcement-banner-add"]').trigger('click')
    await wrapper.get('[data-testid="announcement-banner-save"]').trigger('click')

    expect(mocks.updateSettings).not.toHaveBeenCalled()
    expect(mocks.showError).toHaveBeenCalledWith('admin.settings.announcementBanners.validationTextRequired')
  })
})
