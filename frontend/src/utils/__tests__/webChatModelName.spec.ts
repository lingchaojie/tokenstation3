import { describe, expect, it } from 'vitest'

import { formatWebChatModelName } from '../webChatModelName'

describe('formatWebChatModelName', () => {
  it.each([
    ['gpt-5.4', 'GPT-5.4'],
    ['gpt-5.4-mini', 'GPT-5.4 Mini'],
    ['gpt-5.4-pro', 'GPT-5.4 Pro'],
    ['gpt-5.6-luna', 'GPT-5.6 Luna'],
    ['gpt-5.6-sol', 'GPT-5.6 Sol'],
    ['gpt-5.6-terra', 'GPT-5.6 Terra'],
    ['gpt-image-2', 'GPT Image 2'],
    ['claude-haiku-4-5-20251001', 'Claude Haiku 4.5'],
    ['claude-opus-4-5-20251101', 'Claude Opus 4.5'],
    ['claude-sonnet-4-5-20250929-thinking', 'Claude Sonnet 4.5'],
    ['claude-sonnet-5-thinking', 'Claude Sonnet 5'],
    ['claude-3-5-haiku-20241022', 'Claude Haiku 3.5'],
  ])('formats %s as %s', (model, expected) => {
    expect(formatWebChatModelName({ model })).toBe(expected)
  })

  it('overrides a machine-like fallback display name for a known family', () => {
    expect(formatWebChatModelName({
      provider: 'openai',
      model: 'gpt-5.6-luna',
      displayName: 'gpt-5.6-luna',
    })).toBe('GPT-5.6 Luna')
  })

  it('prefers a supplied human name for an unknown model', () => {
    expect(formatWebChatModelName({
      provider: 'vendor',
      model: 'vendor-model',
      displayName: 'Vendor Prime',
    })).toBe('Vendor Prime')
  })

  it('only removes recognized trailing routing metadata from unknown models', () => {
    expect(formatWebChatModelName({ model: 'vendor-model-20250101-thinking' })).toBe('vendor-model')
    expect(formatWebChatModelName({ model: 'vendor-2025-model' })).toBe('vendor-2025-model')
  })

  it('trims inputs and returns an empty string for an empty model and display name', () => {
    expect(formatWebChatModelName({ model: '  ', displayName: '  ' })).toBe('')
  })
})
