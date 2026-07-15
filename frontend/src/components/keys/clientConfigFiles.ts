import type { GroupPlatform } from '@/types'

export type SupportedGuideClient = 'claude_code' | 'codex'
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
