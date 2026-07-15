import type { GroupPlatform } from '@/types'
import {
  EXAMPLE_MODELS,
  resolveGatewayEndpoints,
  type ClientConfigFile,
  type SupportedGuideOS
} from './clientConfigContract'
import { buildOpenCodeConfigFile } from './clientExampleFiles'

export {
  DOCS_API_KEY_PLACEHOLDER,
  EXAMPLE_MODELS,
  resolveGatewayEndpoints,
  type ClientConfigFile,
  type GatewayEndpoints,
  type SupportedGuideOS
} from './clientConfigContract'

export type SupportedGuideClient = 'claude_code' | 'codex' | 'opencode' | 'cc_switch'
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

function openCodePath(os: SupportedGuideOS): string {
  return os === 'windows'
    ? '%userprofile%\\.config\\opencode\\opencode.json'
    : '~/.config/opencode/opencode.json'
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
Model: ${EXAMPLE_MODELS.openai}
Wire API: responses`,
    hintKey: 'keys.useKeyModal.ccSwitch.hint'
  }
}

export function buildClientConfigFiles(input: ClientConfigInput): ClientConfigFile[] {
  const endpoints = resolveGatewayEndpoints(input.baseUrl)
  const { bare, v1 } = endpoints

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
        buildOpenCodeConfigFile({
          platform: 'anthropic',
          baseUrl: v1,
          apiKey: input.apiKey,
          path: `${openCodePath(input.os)} (Claude)`
        }),
        buildOpenCodeConfigFile({
          platform: 'openai',
          baseUrl: v1,
          apiKey: input.apiKey,
          path: `${openCodePath(input.os)} (OpenAI)`
        })
      ]
    }
    if (input.platform === 'anthropic' || input.platform === 'antigravity') {
      const endpoint = input.platform === 'antigravity' ? `${bare}/antigravity/v1` : v1
      return [buildOpenCodeConfigFile({
        platform: 'anthropic',
        baseUrl: endpoint,
        apiKey: input.apiKey,
        path: openCodePath(input.os)
      })]
    }
    return [buildOpenCodeConfigFile({
      platform: 'openai',
      baseUrl: v1,
      apiKey: input.apiKey,
      path: openCodePath(input.os)
    })]
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
model = "${EXAMPLE_MODELS.openai}"
review_model = "${EXAMPLE_MODELS.openai}"
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
