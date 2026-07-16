import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { describe, expect, it } from 'vitest'

import GuideTroubleshooting from '../GuideTroubleshooting.vue'

const messages = {
  gettingStarted: {
    steps: { troubleshoot: { title: () => 'Troubleshoot' } },
    chrome: {
      copy: () => 'Copy',
      copied: () => 'Copied',
      copyFailed: () => 'Copy failed',
      manualCopy: () => 'Copy manually'
    },
    troubleshooting: {
      version: () => 'Check version',
      filePath: () => 'Check path',
      baseUrl: () => 'Check base URL',
      restart: () => 'Restart',
      authentication: () => 'Check authentication',
      connection: () => 'Check connection',
      shell: () => 'Check shell',
      permissions: () => 'Check permissions',
      officialSource: () => 'Official source'
    }
  }
}

describe('GuideTroubleshooting', () => {
  function mountTroubleshooting(client: 'opencode' | 'cc_switch', os: 'macos' | 'windows') {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: messages } })
    return mount(GuideTroubleshooting, {
      props: {
        variant: {
          client,
          os,
          shell: os === 'windows' ? 'powershell' : 'terminal',
          ...(client === 'opencode'
            ? {
                installCommand: 'install',
                verifyCommand: 'opencode --version',
                launchCommand: 'opencode',
                diagnosticCommands: ['opencode auth list'],
                officialSourceUrl: 'https://opencode.ai/docs'
              }
            : {
                diagnosticCommands: [],
                officialSourceUrl: 'https://github.com/farion1231/cc-switch'
              }),
          verifiedAt: '2026-07-15'
        },
        baseUrl: 'https://gateway.example.test'
      },
      global: { plugins: [i18n] }
    })
  }

  it('renders the official source as a keyboard-visible reduced-motion link', () => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: messages } })
    const wrapper = mount(GuideTroubleshooting, {
      props: {
        variant: {
          client: 'claude_code',
          os: 'macos',
          shell: 'terminal',
          installCommand: 'install',
          verifyCommand: 'claude --version',
          launchCommand: 'claude',
          diagnosticCommands: [],
          officialSourceUrl: 'https://code.claude.com/docs/en/installation',
          verifiedAt: '2026-07-15'
        },
        baseUrl: 'https://gateway.example.test'
      },
      global: { plugins: [i18n] }
    })

    const source = wrapper.get('[data-troubleshooting-branch="official-source"]')
    expect(source.element.tagName).toBe('A')
    expect(source.attributes('href')).toBe('https://code.claude.com/docs/en/installation')
    expect(source.attributes('rel')).toBe('noopener noreferrer')
    expect(source.classes()).toContain('focus-visible:ring-2')
    expect(source.classes()).toContain('motion-reduce:transition-none')
  })

  it('shows the OpenCode config path', () => {
    const wrapper = mountTroubleshooting('opencode', 'macos')

    expect(wrapper.get('[data-troubleshooting-branch="file-path"]').text()).toContain(
      '~/.config/opencode/opencode.json'
    )
  })

  it('points CC Switch users to the provider screen instead of a Codex file', () => {
    const wrapper = mountTroubleshooting('cc_switch', 'windows')
    const filePath = wrapper.get('[data-troubleshooting-branch="file-path"]').text()

    expect(filePath).toContain('CC Switch → Providers')
    expect(filePath).not.toContain('.codex')
  })
})
