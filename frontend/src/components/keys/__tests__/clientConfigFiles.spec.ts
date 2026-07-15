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

const CODEX_CONFIG = `model_provider = "OpenAI"
model = "gpt-5.5"
review_model = "gpt-5.5"
model_reasoning_effort = "xhigh"
disable_response_storage = true
network_access = "enabled"
windows_wsl_setup_acknowledged = true

[model_providers.OpenAI]
name = "OpenAI"
base_url = "https://gateway.example.com/v1"
wire_api = "responses"
requires_openai_auth = true

[features]
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

  it('normalizes a trailing /v1/ to a bare Claude root and one Codex /v1', () => {
    const claudeFiles = buildClientConfigFiles(input())
    const codexFiles = buildClientConfigFiles(input({ client: 'codex' }))

    expect(claudeFiles.every((file) => !file.content.includes('https://gateway.example.com/v1'))).toBe(true)
    expect(codexFiles[0]?.content).toContain('base_url = "https://gateway.example.com/v1"')
    expect(codexFiles[0]?.content).not.toContain('/v1/v1')
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
    expect(parsed.provider.openai.options).toEqual({
      baseURL: 'https://gateway.example.com/v1',
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
      baseURL: 'https://gateway.example.com/v1',
      apiKey: 'sk-test'
    })
    expect(JSON.parse(files[1]?.content ?? '').provider.openai.options).toEqual({
      baseURL: 'https://gateway.example.com/v1',
      apiKey: 'sk-test'
    })
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
Endpoint: https://gateway.example.com/v1
API Key: sk-test
Model: gpt-5.5
Wire API: responses`,
        hintKey: 'keys.useKeyModal.ccSwitch.hint'
      }
    ])
  })

  it('builds only the compatible CC Switch target for typed keys', () => {
    expect(buildClientConfigFiles(input({
      client: 'cc_switch',
      platform: 'anthropic'
    })).map((file) => file.path)).toEqual(['CC Switch → Claude Code → Custom'])

    expect(buildClientConfigFiles(input({
      client: 'cc_switch',
      platform: 'openai'
    })).map((file) => file.path)).toEqual(['CC Switch → Codex → Custom'])
  })
})
