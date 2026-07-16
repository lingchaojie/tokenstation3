import { describe, expect, it } from 'vitest'
import type { WebChatModel } from '@/api/chat'
import { sortWebChatModelsByReleaseDate } from '@/utils/webChatModelSort'

const baseModel: WebChatModel = {
  provider: 'openai',
  platform: 'openai',
  key_type: 'openai',
  model: 'base',
  display_name: 'Base',
  supports_text: true,
  supports_image_input: false,
  supports_file_context: false,
  supports_artifact_output: false,
  supports_thinking: false,
  thinking_efforts: [],
  supports_web_search: false,
  supports_image_generation: false,
  image_generation_sizes: [],
  image_generation_aspect_ratios: [],
  image_generation_qualities: [],
  image_generation_output_formats: [],
  image_generation_backgrounds: [],
  price_status: 'confirmed',
}

describe('sortWebChatModelsByReleaseDate', () => {
  it('sorts known release dates newest first and unknown releases last', () => {
    const sorted = sortWebChatModelsByReleaseDate([
      { ...baseModel, model: 'unknown', display_name: 'Unknown' },
      { ...baseModel, model: 'old', display_name: 'Old', released_at: '2026-01-01' },
      { ...baseModel, model: 'new', display_name: 'New', released_at: '2026-07-09' },
    ])

    expect(sorted.map((model) => model.model)).toEqual(['new', 'old', 'unknown'])
  })
})
