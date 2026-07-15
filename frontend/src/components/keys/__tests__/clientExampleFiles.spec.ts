import { describe, expect, it } from 'vitest'

import {
  buildOpenCodeConfigFile,
  buildPythonSdkExample,
  buildWorkBuddyConfigFile
} from '../clientExampleFiles'
import { DOCS_API_KEY_PLACEHOLDER, resolveGatewayEndpoints } from '../clientConfigFiles'

const endpoints = resolveGatewayEndpoints('https://gateway.example.com/v1/')

describe('client example files', () => {
  it('preserves the rich OpenCode OpenAI model catalog', () => {
    const file = buildOpenCodeConfigFile({
      platform: 'openai',
      baseUrl: endpoints.v1,
      apiKey: DOCS_API_KEY_PLACEHOLDER,
      path: 'opencode.json'
    })
    const parsed = JSON.parse(file.content)

    expect(file.hintKey).toBe('keys.useKeyModal.opencode.hint')
    expect(parsed.model).toBe('openai/gpt-5.5')
    expect(parsed.provider.openai.options).toEqual({
      baseURL: 'https://gateway.example.com/v1',
      apiKey: '$LINX2_API_KEY'
    })
    expect(parsed.provider.openai.models['gpt-5.6'].variants).toHaveProperty('max')
    expect(parsed.provider.openai.models['gpt-5.4-mini'].limit.context).toBe(400000)
  })

  it('preserves the rich Anthropic OpenCode model catalog', () => {
    const file = buildOpenCodeConfigFile({
      platform: 'anthropic',
      baseUrl: endpoints.v1,
      apiKey: DOCS_API_KEY_PLACEHOLDER,
      path: 'opencode.json'
    })
    const parsed = JSON.parse(file.content)

    expect(parsed.model).toBe('anthropic/claude-fable-5')
    expect(parsed.provider.anthropic.models['claude-fable-5'].options.thinking.type).toBe('adaptive')
    expect(parsed.provider.anthropic.models).toHaveProperty('claude-opus-4-8')
  })

  it('builds WorkBuddy from the same gateway and displayed models', () => {
    const file = buildWorkBuddyConfigFile({
      os: 'macos',
      platform: 'unified',
      endpoints,
      apiKey: DOCS_API_KEY_PLACEHOLDER
    })
    const parsed = JSON.parse(file.content)

    expect(file.path).toBe('~/.workbuddy/models.json')
    expect(file.hintKey).toBe('keys.useKeyModal.workBuddy.hint')
    expect(parsed.availableModels).toEqual(['gpt-5.5', 'claude-sonnet-5', 'claude-opus-4-8'])
    expect(parsed.models.every((model: { url: string }) => model.url === endpoints.chatCompletions)).toBe(true)
  })

  it.each([
    ['anthropic', 'anthropic_client.py', 'base_url="https://gateway.example.com"', 'model="claude-opus-4-8"'],
    ['openai', 'openai_client.py', 'base_url="https://gateway.example.com/v1"', 'model="gpt-5.5"'],
    ['image', 'gpt_image_2_client.py', 'base_url="https://gateway.example.com/v1"', 'model="gpt-image-2"']
  ] as const)('builds the current %s Python example', (kind, path, baseLine, modelLine) => {
    const file = buildPythonSdkExample({
      kind,
      endpoints,
      apiKey: DOCS_API_KEY_PLACEHOLDER
    })

    expect(file.path).toBe(path)
    expect(file.content).toContain(baseLine)
    expect(file.content).toContain(modelLine)
    expect(file.content).toContain('$LINX2_API_KEY')
  })
})
