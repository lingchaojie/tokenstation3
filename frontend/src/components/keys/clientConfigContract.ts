export const DOCS_API_KEY_PLACEHOLDER = '$LINX2_API_KEY'

export const EXAMPLE_MODELS = {
  anthropic: 'claude-opus-4-8',
  anthropicOpenCode: 'claude-fable-5',
  openai: 'gpt-5.5',
  image: 'gpt-image-2'
} as const

export type SupportedGuideOS = 'macos' | 'windows' | 'linux'

export interface ClientConfigFile {
  path: string
  content: string
  hintKey?: string
  hint?: string
}

export interface GatewayEndpoints {
  bare: string
  v1: string
  messages: string
  countTokens: string
  responses: string
  chatCompletions: string
  models: string
  imageGenerations: string
  imageEdits: string
}

export function resolveGatewayEndpoints(baseUrl: string): GatewayEndpoints {
  const bare = baseUrl.trim().replace(/\/v1\/?$/, '').replace(/\/+$/, '')
  const v1 = `${bare}/v1`
  return {
    bare,
    v1,
    messages: `${v1}/messages`,
    countTokens: `${v1}/messages/count_tokens`,
    responses: `${v1}/responses`,
    chatCompletions: `${v1}/chat/completions`,
    models: `${v1}/models`,
    imageGenerations: `${v1}/images/generations`,
    imageEdits: `${v1}/images/edits`
  }
}
