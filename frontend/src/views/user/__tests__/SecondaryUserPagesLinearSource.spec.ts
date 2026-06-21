import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const userDir = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const componentsDir = resolve(userDir, '..', '..', 'components')

const profileSource = readFileSync(resolve(userDir, 'ProfileView.vue'), 'utf8')
const redeemSource = readFileSync(resolve(userDir, 'RedeemView.vue'), 'utf8')
const affiliateSource = readFileSync(resolve(userDir, 'AffiliateView.vue'), 'utf8')
const availableChannelsSource = readFileSync(resolve(userDir, 'AvailableChannelsView.vue'), 'utf8')
const channelStatusSource = readFileSync(resolve(userDir, 'ChannelStatusView.vue'), 'utf8')
const customPageSource = readFileSync(resolve(userDir, 'CustomPageView.vue'), 'utf8')
const modelCatalogSource = readFileSync(resolve(userDir, 'ModelCatalogView.vue'), 'utf8')
const monitorCardSource = readFileSync(resolve(componentsDir, 'user/monitor/MonitorCard.vue'), 'utf8')
const monitorHeroSource = readFileSync(resolve(componentsDir, 'user/monitor/MonitorHero.vue'), 'utf8')
const channelsTableSource = readFileSync(resolve(componentsDir, 'channels/AvailableChannelsTable.vue'), 'utf8')
const profileBalanceNotifyCardSource = readFileSync(
  resolve(componentsDir, 'user/profile/ProfileBalanceNotifyCard.vue'),
  'utf8'
)
const profileAccountBindingsCardSource = readFileSync(
  resolve(componentsDir, 'user/profile/ProfileAccountBindingsCard.vue'),
  'utf8'
)
const profileIdentityBindingsSectionSource = readFileSync(
  resolve(componentsDir, 'user/profile/ProfileIdentityBindingsSection.vue'),
  'utf8'
)

describe('Secondary user pages Linear contract', () => {
  it('wraps secondary user pages with page-specific Linear classes', () => {
    expect(profileSource).toContain('linear-profile-page')
    expect(redeemSource).toContain('linear-redeem-page')
    expect(affiliateSource).toContain('linear-affiliate-page')
    expect(availableChannelsSource).toContain('linear-available-channels-page')
    expect(channelStatusSource).toContain('linear-channel-status-page')
    expect(customPageSource).toContain('linear-custom-page')
    expect(modelCatalogSource).toContain('linear-model-catalog-page')
  })

  it('uses Linear panels in monitor and channel components', () => {
    expect(monitorCardSource).toContain('linx-panel')
    expect(monitorHeroSource).toContain('linx-panel-strong')
    expect(channelsTableSource).toContain('linx-panel')
  })

  it('uses Linear profile card classes for balance notifications', () => {
    expect(profileBalanceNotifyCardSource).toContain('class="linx-panel p-5"')
    expect(profileBalanceNotifyCardSource).toContain(
      'text-sm font-semibold tracking-[-0.02em] text-gray-950 dark:text-linear-ink'
    )
    expect(profileBalanceNotifyCardSource).toContain(
      'text-sm text-gray-500 dark:text-linear-ink-subtle'
    )
    expect(profileBalanceNotifyCardSource).not.toContain('class="card"')
  })

  it('embeds profile account bindings without a nested legacy card surface', () => {
    expect(profileAccountBindingsCardSource).toContain('class="linx-panel p-5"')
    expect(profileAccountBindingsCardSource).toContain('embedded')
    expect(profileAccountBindingsCardSource).not.toContain('class="card"')
    expect(profileIdentityBindingsSectionSource).toContain("props.embedded ? 'space-y-4'")
    expect(profileIdentityBindingsSectionSource).toContain('v-if="!props.embedded"')
    expect(profileIdentityBindingsSectionSource).toContain(
      'text-sm font-semibold tracking-[-0.02em] text-gray-950 dark:text-linear-ink'
    )
    expect(profileIdentityBindingsSectionSource).toContain(
      'text-sm text-gray-500 dark:text-linear-ink-subtle'
    )
  })
})
