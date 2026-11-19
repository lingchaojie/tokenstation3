import type { GroupPlatform } from '@/types'
import { DEFAULT_OPENAI_CLIENT_MODEL } from '@/constants/clientModels'

export type SupportedGuideClient = 'claude_code' | 'codex' | 'opencode' | 'cc_switch'
export type SupportedGuideOS = 'macos' | 'windows' | 'linux'
export type WindowsGuideShell = 'powershell' | 'cmd'
export type CodexAuthMode = 'legacy' | 'api-key'

export interface ClientConfigInput {
  client: SupportedGuideClient
  os: SupportedGuideOS
  platform: GroupPlatform | 'unified'
  apiKey: string
  baseUrl: string
  allowMessagesDispatch?: boolean
  windowsShell?: WindowsGuideShell
  codexAuthMode?: CodexAuthMode
  codexModel?: string
  codexReasoningEffort?: string | null
  codexModelCatalogPath?: string
  codexModelCatalogUrl?: string
}

export interface ClientConfigFile {
  path: string
  content: string
  hintKey?: string
  hint?: string
}

export function getDefaultCodexAuthMode(platform?: ClientConfigInput['platform']): CodexAuthMode {
  return platform === 'openai' || platform === 'unified' ? 'api-key' : 'legacy'
}

function gatewayRoot(baseUrl: string): string {
  return baseUrl.trim().replace(/\/v1\/?$/, '').replace(/\/+$/, '')
}

function openCodePath(os: SupportedGuideOS): string {
  return os === 'windows'
    ? '%userprofile%\\.config\\opencode\\opencode.json'
    : '~/.config/opencode/opencode.json'
}

function escapeTomlBasicString(value: string): string {
  return value
    .replace(/\\/g, '\\\\')
    .replace(/"/g, '\\"')
    .replace(/\n/g, '\\n')
    .replace(/\r/g, '\\r')
    .replace(/\t/g, '\\t')
}

function buildOpenCodeFile(
  input: ClientConfigInput,
  provider: 'anthropic' | 'openai',
  baseUrl: string,
  pathSuffix?: string
): ClientConfigFile {
  const isAnthropic = provider === 'anthropic'
  const openAIModel = input.platform === 'openai' || input.platform === 'unified'
    ? { id: DEFAULT_OPENAI_CLIENT_MODEL, name: 'GPT-6.1 Sol' }
    : { id: 'gpt-5.5', name: 'GPT-5.5' }
  const model = isAnthropic ? { id: 'claude-fable-5', name: 'Claude Fable 5' } : openAIModel
  const content = JSON.stringify(
    {
      $schema: 'https://opencode.ai/config.json',
      model: `${provider}/${model.id}`,
      provider: {
        [provider]: {
          npm: isAnthropic ? '@ai-sdk/anthropic' : '@ai-sdk/openai',
          options: {
            baseURL: baseUrl,
            apiKey: input.apiKey
          },
          models: {
            [model.id]: {
              name: model.name
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
Model: ${DEFAULT_OPENAI_CLIENT_MODEL}
Wire API: responses`,
    hintKey: 'keys.useKeyModal.ccSwitch.hint'
  }
}

export function buildClientConfigFiles(input: ClientConfigInput): ClientConfigFile[] {
  const bare = gatewayRoot(input.baseUrl)

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
        buildOpenCodeFile(input, 'anthropic', bare, 'Claude'),
        buildOpenCodeFile(input, 'openai', bare, 'OpenAI')
      ]
    }
    if (input.platform === 'anthropic' || input.platform === 'antigravity') {
      const endpoint = input.platform === 'antigravity' ? `${bare}/antigravity` : bare
      return [buildOpenCodeFile(input, 'anthropic', endpoint)]
    }
    return [buildOpenCodeFile(input, 'openai', bare)]
  }

  if (input.client === 'cc_switch') {
    if (input.platform === 'unified') {
      return [
        buildCcSwitchClaudeFile(bare, input.apiKey),
        buildCcSwitchCodexFile(bare, input.apiKey)
      ]
    }
    if (input.platform === 'anthropic' || input.platform === 'antigravity') {
      const endpoint = input.platform === 'antigravity' ? `${bare}/antigravity` : bare
      return [buildCcSwitchClaudeFile(endpoint, input.apiKey)]
    }
    return [buildCcSwitchCodexFile(bare, input.apiKey)]
  }

  const configDir = input.os === 'windows' ? '%userprofile%\\.codex' : '~/.codex'
  const defaultAuthMode = getDefaultCodexAuthMode(input.platform)
  const nativeImageEnabled = (input.codexAuthMode ?? defaultAuthMode) === 'api-key'
  const providerAuthConfig = nativeImageEnabled
    ? `requires_openai_auth = false
http_headers = { "x-openai-actor-authorization" = "local-image-extension" }`
    : 'requires_openai_auth = false'
  const codexModel = escapeTomlBasicString(input.codexModel || DEFAULT_OPENAI_CLIENT_MODEL)
  const reasoningEffortLine = input.codexReasoningEffort === null
    ? ''
    : `model_reasoning_effort = "${escapeTomlBasicString(input.codexReasoningEffort || 'xhigh')}"\n`
  const modelCatalogLine = input.codexModelCatalogPath && !input.codexModelCatalogUrl
    ? `model_catalog_json = "${escapeTomlBasicString(input.codexModelCatalogPath)}"\n`
    : ''
  const remoteCatalogLine = input.codexModelCatalogUrl
    ? `model_catalog_url = "${escapeTomlBasicString(input.codexModelCatalogUrl)}"\n`
    : ''
  const configContent = `model_provider = "linx2ai"
model = "${codexModel}"
review_model = "${codexModel}"
${defaultAuthMode === 'api-key' ? 'web_search = "live"\n' : ''}${reasoningEffortLine}${modelCatalogLine}disable_response_storage = true
network_access = "enabled"
windows_wsl_setup_acknowledged = true

[model_providers.linx2ai]
name = "linx2ai"
base_url = "${bare}"
${remoteCatalogLine}wire_api = "responses"
${providerAuthConfig}

[features]
${input.codexModelCatalogUrl ? 'api_key_model_discovery = true\n' : ''}${nativeImageEnabled ? 'image_generation = true\n' : ''}goals = true`
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
