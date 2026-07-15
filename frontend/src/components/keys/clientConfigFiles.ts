import type { GroupPlatform } from '@/types'

export type SupportedGuideClient = 'claude_code' | 'codex' | 'opencode' | 'cc_switch'
export type SupportedGuideOS = 'macos' | 'windows' | 'linux'
export type WindowsGuideShell = 'powershell' | 'cmd'

export interface ClientConfigInput {
  client: SupportedGuideClient
  os: SupportedGuideOS
  platform: GroupPlatform | 'unified'
  apiKey: string
  baseUrl: string
  allowMessagesDispatch?: boolean
  windowsShell?: WindowsGuideShell
}

export interface ClientConfigFile {
  path: string
  content: string
  hintKey?: string
  hint?: string
}

function gatewayRoots(baseUrl: string): { bare: string; v1: string } {
  const bare = baseUrl.trim().replace(/\/v1\/?$/, '').replace(/\/+$/, '')
  return { bare, v1: `${bare}/v1` }
}

function openCodePath(os: SupportedGuideOS): string {
  return os === 'windows'
    ? '%userprofile%\\.config\\opencode\\opencode.json'
    : '~/.config/opencode/opencode.json'
}

function buildOpenCodeFile(
  input: ClientConfigInput,
  provider: 'anthropic' | 'openai',
  baseUrl: string,
  pathSuffix?: string
): ClientConfigFile {
  const isAnthropic = provider === 'anthropic'
  const model = isAnthropic ? 'claude-fable-5' : 'gpt-5.5'
  const content = JSON.stringify(
    {
      $schema: 'https://opencode.ai/config.json',
      model: `${provider}/${model}`,
      provider: {
        [provider]: {
          npm: isAnthropic ? '@ai-sdk/anthropic' : '@ai-sdk/openai',
          options: {
            baseURL: baseUrl,
            apiKey: input.apiKey
          },
          models: {
            [model]: {
              name: isAnthropic ? 'Claude Fable 5' : 'GPT-5.5'
            }
          }
        }
      },
      ...(!isAnthropic
        ? {
            agent: {
              build: { options: { store: false } },
              plan: { options: { store: false } }
            }
          }
        : {})
    },
    null,
    2
  )

  return {
    path: `${openCodePath(input.os)}${pathSuffix ? ` (${pathSuffix})` : ''}`,
    content,
    hintKey: 'keys.useKeyModal.opencode.hint'
  }
}

function buildCcSwitchClaudeFile(endpoint: string, apiKey: string): ClientConfigFile {
  return {
    path: 'CC Switch → Claude Code → Custom',
    content: `App: Claude Code
Preset: Custom
Name: TokenStation
Endpoint: ${endpoint}
API Key: ${apiKey}`,
    hintKey: 'keys.useKeyModal.ccSwitch.hint'
  }
}

function buildCcSwitchCodexFile(endpoint: string, apiKey: string): ClientConfigFile {
  return {
    path: 'CC Switch → Codex → Custom',
    content: `App: Codex
Preset: Custom
Name: TokenStation
Endpoint: ${endpoint}
API Key: ${apiKey}
Model: gpt-5.5
Wire API: responses`,
    hintKey: 'keys.useKeyModal.ccSwitch.hint'
  }
}

export function buildClientConfigFiles(input: ClientConfigInput): ClientConfigFile[] {
  const { bare, v1 } = gatewayRoots(input.baseUrl)

  if (input.client === 'claude_code') {
    const isWindows = input.os === 'windows'
    const windowsShell = input.windowsShell ?? 'powershell'
    let path = 'Terminal'
    let content = `export ANTHROPIC_BASE_URL="${bare}"
export ANTHROPIC_AUTH_TOKEN="${input.apiKey}"
export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
export CLAUDE_CODE_ATTRIBUTION_HEADER=0`

    if (isWindows && windowsShell === 'cmd') {
      path = 'Command Prompt'
      content = `set ANTHROPIC_BASE_URL=${bare}
set ANTHROPIC_AUTH_TOKEN=${input.apiKey}
set CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
set CLAUDE_CODE_ATTRIBUTION_HEADER=0`
    } else if (isWindows) {
      path = 'PowerShell'
      content = `$env:ANTHROPIC_BASE_URL="${bare}"
$env:ANTHROPIC_AUTH_TOKEN="${input.apiKey}"
$env:CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
$env:CLAUDE_CODE_ATTRIBUTION_HEADER=0`
    }

    const settingsPath = isWindows
      ? '%userprofile%\\.claude\\settings.json'
      : '~/.claude/settings.json'
    const settingsContent = `{
  "env": {
    "ANTHROPIC_BASE_URL": "${bare}",
    "ANTHROPIC_AUTH_TOKEN": "${input.apiKey}",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
    "CLAUDE_CODE_ATTRIBUTION_HEADER": "0"
  }
}`

    return [
      { path, content },
      {
        path: settingsPath,
        content: settingsContent,
        hint: 'VSCode Claude Code'
      }
    ]
  }

  if (input.client === 'opencode') {
    if (input.platform === 'unified') {
      return [
        buildOpenCodeFile(input, 'anthropic', v1, 'Claude'),
        buildOpenCodeFile(input, 'openai', v1, 'OpenAI')
      ]
    }
    if (input.platform === 'anthropic' || input.platform === 'antigravity') {
      const endpoint = input.platform === 'antigravity' ? `${bare}/antigravity/v1` : v1
      return [buildOpenCodeFile(input, 'anthropic', endpoint)]
    }
    return [buildOpenCodeFile(input, 'openai', v1)]
  }

  if (input.client === 'cc_switch') {
    if (input.platform === 'unified') {
      return [
        buildCcSwitchClaudeFile(bare, input.apiKey),
        buildCcSwitchCodexFile(v1, input.apiKey)
      ]
    }
    if (input.platform === 'anthropic' || input.platform === 'antigravity') {
      const endpoint = input.platform === 'antigravity' ? `${bare}/antigravity` : bare
      return [buildCcSwitchClaudeFile(endpoint, input.apiKey)]
    }
    return [buildCcSwitchCodexFile(v1, input.apiKey)]
  }

  const configDir = input.os === 'windows' ? '%userprofile%\\.codex' : '~/.codex'
  const configContent = `model_provider = "OpenAI"
model = "gpt-5.5"
review_model = "gpt-5.5"
model_reasoning_effort = "xhigh"
disable_response_storage = true
network_access = "enabled"
windows_wsl_setup_acknowledged = true

[model_providers.OpenAI]
name = "OpenAI"
base_url = "${v1}"
wire_api = "responses"
requires_openai_auth = true

[features]
goals = true`
  const authContent = `{
  "OPENAI_API_KEY": "${input.apiKey}"
}`

  return [
    {
      path: `${configDir}/config.toml`,
      content: configContent,
      hintKey: 'keys.useKeyModal.openai.configTomlHint'
    },
    {
      path: `${configDir}/auth.json`,
      content: authContent
    }
  ]
}
