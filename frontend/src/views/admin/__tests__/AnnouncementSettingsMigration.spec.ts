import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const announcementsSource = readFileSync(resolve(process.cwd(), 'src/views/admin/AnnouncementsView.vue'), 'utf8')
const settingsSource = readFileSync(resolve(process.cwd(), 'src/views/admin/SettingsView.vue'), 'utf8')

describe('announcement settings ownership', () => {
  it('renders the rolling settings card from the announcement page', () => {
    expect(announcementsSource).toContain('import AnnouncementBannerSettingsCard')
    expect(announcementsSource).toContain('<AnnouncementBannerSettingsCard')
  })
  it('removes rolling announcement writes from generic settings', () => {
    expect(settingsSource).not.toContain('announcement_banners')
    expect(settingsSource).not.toContain('announcement_banner_interval_ms')
    expect(settingsSource).not.toContain('announcementIntervalSeconds')
    expect(settingsSource).not.toContain('addAnnouncementBanner')
    expect(settingsSource).not.toContain('moveAnnouncementBanner')
    expect(settingsSource).not.toContain('removeAnnouncementBanner')
  })
})
