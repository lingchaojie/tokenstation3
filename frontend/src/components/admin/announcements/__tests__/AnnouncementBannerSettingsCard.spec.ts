import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AnnouncementBannerSettingsCard from '../AnnouncementBannerSettingsCard.vue'

const mocks = vi.hoisted(() => ({
  getSettings: vi.fn(),
  updateSettings: vi.fn(),
  fetchPublicSettings: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
  showWarning: vi.fn(),
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
    showWarning: mocks.showWarning,
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
    mocks.fetchPublicSettings.mockResolvedValue({ announcement_banners: [] })
  })

  it('loads existing settings', async () => {
    const wrapper = mount(AnnouncementBannerSettingsCard)
    await flushPromises()

    expect(wrapper.get('[data-testid="announcement-banner-zh-0"]').element).toHaveProperty('value', '第一条')
    expect(wrapper.get('[data-testid="announcement-banner-interval"]').element).toHaveProperty('value', '3')
  })

  it('renders the add control with standard button chrome', async () => {
    const wrapper = mount(AnnouncementBannerSettingsCard)
    await flushPromises()

    expect(wrapper.get('[data-testid="announcement-banner-add"]').classes()).toEqual(
      expect.arrayContaining(['btn', 'btn-secondary']),
    )
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
    expect(mocks.fetchPublicSettings).toHaveBeenCalledOnce()
    expect(mocks.fetchPublicSettings).toHaveBeenCalledWith(true)
    expect(mocks.showSuccess).toHaveBeenCalledWith('admin.settings.announcementBanners.saved')
    expect(mocks.showWarning).not.toHaveBeenCalled()
  })

  it('warns that the PUT was saved when both live refresh attempts fail', async () => {
    mocks.fetchPublicSettings.mockResolvedValue(null)
    const wrapper = mount(AnnouncementBannerSettingsCard)
    await flushPromises()

    await wrapper.get('[data-testid="announcement-banner-save"]').trigger('click')
    await flushPromises()

    expect(mocks.updateSettings).toHaveBeenCalledOnce()
    expect(mocks.fetchPublicSettings).toHaveBeenCalledTimes(2)
    expect(mocks.fetchPublicSettings).toHaveBeenNthCalledWith(1, true)
    expect(mocks.fetchPublicSettings).toHaveBeenNthCalledWith(2, true)
    expect(mocks.showWarning).toHaveBeenCalledWith(
      'admin.settings.announcementBanners.liveRefreshFailed',
    )
    expect(mocks.showError).not.toHaveBeenCalledWith(
      'admin.settings.announcementBanners.saveFailed',
    )
    expect(mocks.showSuccess).not.toHaveBeenCalled()
  })

  it('labels an actual PUT rejection as a save failure without refreshing', async () => {
    mocks.updateSettings.mockRejectedValue(new Error('PUT failed'))
    const wrapper = mount(AnnouncementBannerSettingsCard)
    await flushPromises()

    await wrapper.get('[data-testid="announcement-banner-save"]').trigger('click')
    await flushPromises()

    expect(mocks.fetchPublicSettings).not.toHaveBeenCalled()
    expect(mocks.showError).toHaveBeenCalledWith(
      'admin.settings.announcementBanners.saveFailed',
    )
    expect(mocks.showWarning).not.toHaveBeenCalled()
  })

  it('uses server-assigned stable IDs in the next two-field save payload', async () => {
    mocks.getSettings.mockResolvedValue({
      announcement_banners: [],
      announcement_banner_interval_ms: 3000,
    })
    mocks.updateSettings
      .mockResolvedValueOnce({
        announcement_banners: [
          { id: 'server-assigned', text_zh: '新公告', text_en: '' },
        ],
        announcement_banner_interval_ms: 3000,
      })
      .mockImplementationOnce(async (payload) => payload)
    const wrapper = mount(AnnouncementBannerSettingsCard)
    await flushPromises()

    await wrapper.get('[data-testid="announcement-banner-add"]').trigger('click')
    await wrapper.get('[data-testid="announcement-banner-zh-0"]').setValue('新公告')
    await wrapper.get('[data-testid="announcement-banner-save"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="announcement-banner-save"]').trigger('click')
    await flushPromises()

    expect(mocks.updateSettings).toHaveBeenNthCalledWith(2, {
      announcement_banners: [
        { id: 'server-assigned', text_zh: '新公告', text_en: '' },
      ],
      announcement_banner_interval_ms: 3000,
    })
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
