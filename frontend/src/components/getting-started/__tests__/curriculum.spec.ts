import { describe, expect, it } from 'vitest'

import { BEGINNER_GUIDE_STEP_ORDER } from '@/api/beginnerGuide'
import enLocale from '@/i18n/locales/en/gettingStarted'
import zhLocale from '@/i18n/locales/zh/gettingStarted'
import {
  GUIDE_CLIENT_IDS,
  GUIDE_OS_IDS,
  GUIDE_STEP_IDS,
  GUIDE_VARIANTS
} from '../curriculum'

const APPROVED_STEP_IDS = [
  'understand',
  'choose',
  'terminal',
  'install',
  'api_key',
  'configure',
  'first_run',
  'troubleshoot'
] as const

const REQUIRED_LOCALE_PATHS = [
  'gettingStarted.title',
  'gettingStarted.discovery.navLabel',
  'gettingStarted.discovery.eyebrow',
  'gettingStarted.discovery.title',
  'gettingStarted.discovery.description',
  'gettingStarted.discovery.homeCta',
  'gettingStarted.discovery.stages.choose',
  'gettingStarted.discovery.stages.install',
  'gettingStarted.discovery.stages.connect',
  'gettingStarted.discovery.stages.firstTask',
  'gettingStarted.dashboard.quickActionTitle',
  'gettingStarted.dashboard.quickActionDescription',
  'gettingStarted.dashboard.sidebarLabel',
  'gettingStarted.welcome.title',
  'gettingStarted.welcome.description',
  'gettingStarted.welcome.start',
  'gettingStarted.welcome.closeLabel',
  'gettingStarted.chrome.guideLabel',
  'gettingStarted.chrome.clientSelector',
  'gettingStarted.chrome.osSelector',
  'gettingStarted.chrome.progress',
  'gettingStarted.chrome.back',
  'gettingStarted.chrome.next',
  'gettingStarted.chrome.copy',
  'gettingStarted.chrome.copied',
  'gettingStarted.chrome.copyFailed',
  'gettingStarted.chrome.manualCopy',
  'gettingStarted.chrome.mobileStepMenu',
  'gettingStarted.chrome.openStepMenu',
  'gettingStarted.chrome.closeStepMenu',
  'gettingStarted.clients.claude_code',
  'gettingStarted.clients.codex',
  'gettingStarted.operatingSystems.macos',
  'gettingStarted.operatingSystems.windows',
  'gettingStarted.operatingSystems.linux',
  'gettingStarted.steps.understand.title',
  'gettingStarted.steps.understand.description',
  'gettingStarted.steps.choose.title',
  'gettingStarted.steps.choose.description',
  'gettingStarted.steps.terminal.title',
  'gettingStarted.steps.terminal.description',
  'gettingStarted.steps.install.title',
  'gettingStarted.steps.install.description',
  'gettingStarted.steps.api_key.title',
  'gettingStarted.steps.api_key.description',
  'gettingStarted.steps.configure.title',
  'gettingStarted.steps.configure.description',
  'gettingStarted.steps.first_run.title',
  'gettingStarted.steps.first_run.description',
  'gettingStarted.steps.troubleshoot.title',
  'gettingStarted.steps.troubleshoot.description',
  'gettingStarted.definitions.model.title',
  'gettingStarted.definitions.model.description',
  'gettingStarted.definitions.agent.title',
  'gettingStarted.definitions.agent.description',
  'gettingStarted.definitions.terminal.title',
  'gettingStarted.definitions.terminal.description',
  'gettingStarted.definitions.gateway.title',
  'gettingStarted.definitions.gateway.description',
  'gettingStarted.definitions.apiKey.title',
  'gettingStarted.definitions.apiKey.description',
  'gettingStarted.terminal.macos.appName',
  'gettingStarted.terminal.macos.openInstructions',
  'gettingStarted.terminal.windows.appName',
  'gettingStarted.terminal.windows.openInstructions',
  'gettingStarted.terminal.linux.appName',
  'gettingStarted.terminal.linux.openInstructions',
  'gettingStarted.terminal.pasteAndRun',
  'gettingStarted.terminal.normalOutput',
  'gettingStarted.installation.explanation',
  'gettingStarted.installation.expectedResult',
  'gettingStarted.installation.restartShell',
  'gettingStarted.installation.officialSource',
  'gettingStarted.apiKey.anonymousTitle',
  'gettingStarted.apiKey.anonymousDescription',
  'gettingStarted.apiKey.login',
  'gettingStarted.apiKey.register',
  'gettingStarted.apiKey.loading',
  'gettingStarted.apiKey.existingTitle',
  'gettingStarted.apiKey.emptyTitle',
  'gettingStarted.apiKey.emptyDescription',
  'gettingStarted.apiKey.create',
  'gettingStarted.apiKey.inactive',
  'gettingStarted.apiKey.incompatible',
  'gettingStarted.apiKey.secretWarning',
  'gettingStarted.configuration.mergeWarning',
  'gettingStarted.configuration.restartInstruction',
  'gettingStarted.configuration.reselectAfterRefresh',
  'gettingStarted.firstRun.promptLabel',
  'gettingStarted.firstRun.prompt',
  'gettingStarted.firstRun.restartInstruction',
  'gettingStarted.firstRun.expectedResult',
  'gettingStarted.firstRun.confirmSuccess',
  'gettingStarted.troubleshooting.version',
  'gettingStarted.troubleshooting.filePath',
  'gettingStarted.troubleshooting.baseUrl',
  'gettingStarted.troubleshooting.restart',
  'gettingStarted.troubleshooting.authentication',
  'gettingStarted.troubleshooting.connection',
  'gettingStarted.troubleshooting.shell',
  'gettingStarted.troubleshooting.permissions',
  'gettingStarted.troubleshooting.officialSource',
  'gettingStarted.troubleshooting.retry',
  'gettingStarted.troubleshooting.retryLoading',
  'gettingStarted.completion.title',
  'gettingStarted.completion.description',
  'gettingStarted.completion.dashboard',
  'gettingStarted.completion.keys',
  'gettingStarted.completion.usage',
  'gettingStarted.warnings.progressUnavailable',
  'gettingStarted.warnings.progressSaveFailed',
  'gettingStarted.warnings.promptSaveFailed'
] as const

function recursiveLeafPaths(value: unknown, prefix = ''): string[] {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return [prefix]
  }

  return Object.entries(value).flatMap(([key, child]) =>
    recursiveLeafPaths(child, prefix ? `${prefix}.${key}` : key)
  )
}

describe('beginner guide curriculum contract', () => {
  it('reuses the canonical approved eight-step order', () => {
    expect(GUIDE_STEP_IDS).toBe(BEGINNER_GUIDE_STEP_ORDER)
    expect(GUIDE_STEP_IDS).toEqual(APPROVED_STEP_IDS)
  })

  it('supports exactly Claude Code and Codex on macOS, Windows, and Linux', () => {
    expect(GUIDE_CLIENT_IDS).toEqual(['claude_code', 'codex'])
    expect(GUIDE_OS_IDS).toEqual(['macos', 'windows', 'linux'])
    expect(GUIDE_VARIANTS).toHaveLength(6)
    expect(GUIDE_VARIANTS.map(({ client, os }) => `${client}:${os}`)).toEqual([
      'claude_code:macos',
      'claude_code:windows',
      'claude_code:linux',
      'codex:macos',
      'codex:windows',
      'codex:linux'
    ])
  })

  it('keeps the official native installer commands exact', () => {
    const commands = Object.fromEntries(
      GUIDE_VARIANTS.map(({ client, os, installCommand }) => [`${client}:${os}`, installCommand])
    )

    expect(commands).toEqual({
      'claude_code:macos': 'curl -fsSL https://claude.ai/install.sh | bash',
      'claude_code:windows': 'irm https://claude.ai/install.ps1 | iex',
      'claude_code:linux': 'curl -fsSL https://claude.ai/install.sh | bash',
      'codex:macos': 'curl -fsSL https://chatgpt.com/codex/install.sh | sh',
      'codex:windows': 'irm https://chatgpt.com/codex/install.ps1 | iex',
      'codex:linux': 'curl -fsSL https://chatgpt.com/codex/install.sh | sh'
    })
  })

  it('records exact verification, launch, diagnostics, shell, and official-source metadata', () => {
    for (const variant of GUIDE_VARIANTS) {
      expect(variant.verifiedAt).toBe('2026-07-15')
      expect(variant.officialSourceUrl).toMatch(/^https:\/\//)
      expect(variant.shell).toBe(variant.os === 'windows' ? 'powershell' : 'terminal')

      if (variant.client === 'claude_code') {
        expect(variant.verifyCommand).toBe('claude --version')
        expect(variant.launchCommand).toBe('claude')
        expect(variant.diagnosticCommands).toEqual(['claude doctor'])
        expect(variant.officialSourceUrl).toBe(
          'https://code.claude.com/docs/en/installation'
        )
      } else {
        expect(variant.verifyCommand).toBe('codex --version')
        expect(variant.launchCommand).toBe('codex')
        expect(variant.diagnosticCommands).toEqual(['codex login status', 'codex doctor'])
        expect(variant.officialSourceUrl).toBe(
          'https://learn.chatgpt.com/docs/codex/cli/install'
        )
      }
    }
  })

  it('contains no unsupported or future-client curriculum text', () => {
    const serialized = JSON.stringify({
      clients: GUIDE_CLIENT_IDS,
      variants: GUIDE_VARIANTS,
      enLocale,
      zhLocale
    }).toLowerCase()

    for (const forbidden of ['opencode', 'workbuddy', 'gemini cli', 'coming soon']) {
      expect(serialized).not.toContain(forbidden)
    }
  })
})

describe('beginner guide locales', () => {
  it('keeps the English and Chinese recursive key sets identical and complete', () => {
    const enPaths = recursiveLeafPaths(enLocale).sort()
    const zhPaths = recursiveLeafPaths(zhLocale).sort()

    expect(enPaths).toEqual(zhPaths)
    expect(enPaths).toEqual([...REQUIRED_LOCALE_PATHS].sort())
  })

  it('keeps commands, source metadata, and markup out of translated prose', () => {
    for (const locale of [enLocale, zhLocale]) {
      const serialized = JSON.stringify(locale)
      expect(serialized).not.toMatch(/curl -fsSL|\birm https:\/\//)
      expect(serialized).not.toContain('verifiedAt')
      expect(serialized).not.toContain('officialSourceUrl')
      expect(serialized).not.toMatch(/<\/?[a-z][^>]*>/i)
    }
  })
})
