import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

const frontendRoot = process.cwd()

function source(relativePath: string): string {
  return readFileSync(`${frontendRoot}/${relativePath}`, 'utf8')
}

describe('excluded frontend products remain unreachable', () => {
  it('does not register plugin, Composite, or independent x_search entry points', () => {
    const registries = [
      'src/api/admin/index.ts',
      'src/router/index.ts',
      'src/components/layout/AppSidebar.vue',
      'src/utils/featureFlags.ts',
      'src/i18n/locales/en/admin/index.ts',
      'src/i18n/locales/zh/admin/index.ts',
      'package.json'
    ].map(source).join('\n')

    expect(registries).not.toMatch(/\.s2plugin|plugin_management_enabled|pluginsAPI|['"]\.\/plugins['"]/i)
    expect(registries).not.toMatch(/['"]composite['"]|Composite(?:Group|Route|View)|\/admin\/composite/i)
    expect(registries).not.toContain('/x_search')
    expect(registries).not.toMatch(/PromptAudit|\/admin\/prompt-audit/i)
  })

  it('does not expose excluded Grok audio or upstream billing-rate sync contracts', () => {
    const contracts = [
      'src/types/index.ts',
      'src/api/admin/accounts.ts',
      'src/views/admin/AccountsView.vue'
    ].map(source).join('\n')

    expect(contracts).not.toMatch(/audio_(?:realtime|tts|stt)|custom_voices|allow_live/i)
    expect(contracts).not.toMatch(/(?:passkey|tencent_captcha|aliyun_captcha)_(?:enabled|ticket|randstr)/i)
    expect(contracts).not.toMatch(
      /getUpstreamBillingRates|\/upstream-billing-rates|upstream_billing_rate_sync_enabled|synced_rate_multiplier/
    )
  })
})
