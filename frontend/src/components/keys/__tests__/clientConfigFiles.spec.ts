import { describe, expect, it } from 'vitest'

import { buildClientConfigFiles, type ClientConfigInput } from '../clientConfigFiles'

const CLAUDE_SETTINGS = `{
  "env": {
    "ANTHROPIC_BASE_URL": "https://gateway.example.com",
    "ANTHROPIC_AUTH_TOKEN": "sk-test",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
    "CLAUDE_CODE_ATTRIBUTION_HEADER": "0"
  }
}`

const CODEX_CONFIG = `model_provider = "linx2ai"
model = "gpt-6.1-sol"
review_model = "gpt-6.1-sol"
web_search = "live"
model_reasoning_effort = "xhigh"
disable_response_storage = true
network_access = "enabled"
windows_wsl_setup_acknowledged = true

[model_providers.linx2ai]
name = "linx2ai"
base_url = "https://gateway.example.com"
wire_api = "responses"
requires_openai_auth = false
http_headers = { "x-openai-actor-authorization" = "local-image-extension" }

[features]
image_generation = true
goals = true`

function input(overrides: Partial<ClientConfigInput> = {}): ClientConfigInput {
  return {
    client: 'claude_code',
    os: 'macos',
    platform: 'unified',
    apiKey: 'sk-test',
    baseUrl: 'https://gateway.example.com/v1/',
    ...overrides
  }
}

describe('buildClientConfigFiles', () => {
  it.each(['macos', 'linux'] as const)(
    'builds the current Claude Code files for %s',
    (os) => {
      expect(buildClientConfigFiles(input({ os }))).toEqual([
        {
          path: 'Terminal',
          content: `export ANTHROPIC_BASE_URL="https://gateway.example.com"
export ANTHROPIC_AUTH_TOKEN="sk-test"
export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
export CLAUDE_CODE_ATTRIBUTION_HEADER=0`
        },
        {
          path: '~/.claude/settings.json',
          content: CLAUDE_SETTINGS,
          hint: 'VSCode Claude Code'
        }
      ])
    }
  )

  it('defaults Windows Claude Code output to PowerShell', () => {
    expect(buildClientConfigFiles(input({ os: 'windows' }))).toEqual([
      {
        path: 'PowerShell',
        content: `$env:ANTHROPIC_BASE_URL="https://gateway.example.com"
$env:ANTHROPIC_AUTH_TOKEN="sk-test"
$env:CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
$env:CLAUDE_CODE_ATTRIBUTION_HEADER=0`
      },
      {
        path: '%userprofile%\\.claude\\settings.json',
        content: CLAUDE_SETTINGS,
        hint: 'VSCode Claude Code'
      }
    ])
  })

  it('builds the current Claude Code CMD output when explicitly selected', () => {
    expect(buildClientConfigFiles(input({ os: 'windows', windowsShell: 'cmd' }))).toEqual([
      {
        path: 'Command Prompt',
        content: `set ANTHROPIC_BASE_URL=https://gateway.example.com
set ANTHROPIC_AUTH_TOKEN=sk-test
set CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
set CLAUDE_CODE_ATTRIBUTION_HEADER=0`
      },
      {
        path: '%userprofile%\\.claude\\settings.json',
        content: CLAUDE_SETTINGS,
        hint: 'VSCode Claude Code'
      }
    ])
  })

  it.each(['macos', 'linux'] as const)(
    'builds the current Codex files for %s',
    (os) => {
      expect(buildClientConfigFiles(input({ client: 'codex', os }))).toEqual([
        {
          path: '~/.codex/config.toml',
          content: CODEX_CONFIG,
          hintKey: 'keys.useKeyModal.openai.configTomlHint'
        },
        {
          path: '~/.codex/auth.json',
          content: `{
  "OPENAI_API_KEY": "sk-test"
}`
        }
      ])
    }
  )

  it('builds the current Codex files for Windows', () => {
    expect(buildClientConfigFiles(input({ client: 'codex', os: 'windows' }))).toEqual([
      {
        path: '%userprofile%\\.codex/config.toml',
        content: CODEX_CONFIG,
        hintKey: 'keys.useKeyModal.openai.configTomlHint'
      },
      {
        path: '%userprofile%\\.codex/auth.json',
        content: `{
  "OPENAI_API_KEY": "sk-test"
}`
      }
    ])
  })

  it('normalizes a trailing /v1/ to the bare gateway root for every generated client', () => {
    const claudeFiles = buildClientConfigFiles(input())
    const codexFiles = buildClientConfigFiles(input({ client: 'codex' }))
    const openCodeFiles = buildClientConfigFiles(input({ client: 'opencode' }))
    const ccSwitchFiles = buildClientConfigFiles(input({ client: 'cc_switch' }))

    expect([...claudeFiles, ...codexFiles, ...openCodeFiles, ...ccSwitchFiles]
      .every((file) => !file.content.includes('https://gateway.example.com/v1'))).toBe(true)
  })

  it('keeps the Claude key only in its required shell and settings values', () => {
    const files = buildClientConfigFiles(input())

    expect(files).toHaveLength(2)
    expect(files[0]?.content.match(/sk-test/g)).toHaveLength(1)
    expect(files[1]?.content.match(/sk-test/g)).toHaveLength(1)
    expect(files.map((file) => file.path).join('\n')).not.toContain('sk-test')
  })

  it('keeps the Codex key only in auth.json', () => {
    const [config, auth] = buildClientConfigFiles(input({ client: 'codex' }))

    expect(config?.content).not.toContain('sk-test')
    expect(config?.content).not.toContain('env_key')
    expect(JSON.parse(auth?.content ?? '')).toEqual({ OPENAI_API_KEY: 'sk-test' })
  })

  it.each([
    ['macos', '~/.config/opencode/opencode.json'],
    ['linux', '~/.config/opencode/opencode.json'],
    ['windows', '%userprofile%\\.config\\opencode\\opencode.json']
  ] as const)('builds an official OpenCode provider config for %s', (os, expectedPath) => {
    const [file] = buildClientConfigFiles(input({
      client: 'opencode',
      os,
      platform: 'openai'
    }))

    expect(file?.path).toBe(expectedPath)
    expect(file?.hintKey).toBe('keys.useKeyModal.opencode.hint')
    const parsed = JSON.parse(file?.content ?? '')
    expect(parsed.$schema).toBe('https://opencode.ai/config.json')
    expect(parsed.model).toBe('openai/gpt-6.1-sol')
    expect(parsed.provider.openai.models).toEqual({ 'gpt-6.1-sol': { name: 'GPT-6.1 Sol' } })
    expect(parsed.provider.openai.options).toEqual({
      baseURL: 'https://gateway.example.com',
      apiKey: 'sk-test'
    })
  })

  it('builds both OpenCode alternatives for a unified key', () => {
    const files = buildClientConfigFiles(input({ client: 'opencode' }))

    expect(files).toHaveLength(2)
    expect(files.map((file) => file.path)).toEqual([
      '~/.config/opencode/opencode.json (Claude)',
      '~/.config/opencode/opencode.json (OpenAI)'
    ])
    expect(JSON.parse(files[0]?.content ?? '').provider.anthropic.options).toEqual({
      baseURL: 'https://gateway.example.com',
      apiKey: 'sk-test'
    })
    const openAI = JSON.parse(files[1]?.content ?? '')
    expect(openAI.model).toBe('openai/gpt-6.1-sol')
    expect(openAI.provider.openai.options).toEqual({
      baseURL: 'https://gateway.example.com',
      apiKey: 'sk-test'
    })
  })

  it('keeps the legacy OpenCode model for another gateway platform', () => {
    const [file] = buildClientConfigFiles(input({ client: 'opencode', platform: 'deepseek' }))
    const parsed = JSON.parse(file?.content ?? '')
    expect(parsed.model).toBe('openai/gpt-5.5')
    expect(parsed.provider.openai.models).toEqual({ 'gpt-5.5': { name: 'GPT-5.5' } })
  })

  it('builds copyable CC Switch fields for a unified key', () => {
    expect(buildClientConfigFiles(input({ client: 'cc_switch' }))).toEqual([
      {
        path: 'CC Switch → Claude Code → Custom',
        content: `App: Claude Code
Preset: Custom
Name: TokenStation
Endpoint: https://gateway.example.com
API Key: sk-test`,
        hintKey: 'keys.useKeyModal.ccSwitch.hint'
      },
      {
        path: 'CC Switch → Codex → Custom',
        content: `App: Codex
Preset: Custom
Name: TokenStation
Endpoint: https://gateway.example.com
API Key: sk-test
Model: gpt-6.1-sol
Wire API: responses`,
        hintKey: 'keys.useKeyModal.ccSwitch.hint'
      }
    ])
  })

  it('uses the Antigravity root for the Anthropic OpenCode provider', () => {
    const [file] = buildClientConfigFiles(input({
      client: 'opencode',
      platform: 'antigravity'
    }))

    const parsed = JSON.parse(file?.content ?? '')
    expect(parsed.provider.anthropic.options.baseURL).toBe('https://gateway.example.com/antigravity')
  })

  it('builds only the compatible CC Switch target for typed keys', () => {
    expect(buildClientConfigFiles(input({
      client: 'cc_switch',
      platform: 'anthropic'
    })).map((file) => file.path)).toEqual(['CC Switch → Claude Code → Custom'])

    const codexFiles = buildClientConfigFiles(input({
      client: 'cc_switch',
      platform: 'openai'
    }))
    expect(codexFiles.map((file) => file.path)).toEqual(['CC Switch → Codex → Custom'])
    expect(codexFiles[0]?.content).toContain('Model: gpt-6.1-sol')
  })
})
