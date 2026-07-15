import type { GroupPlatform } from '@/types'
import {
  EXAMPLE_MODELS,
  type ClientConfigFile,
  type GatewayEndpoints,
  type SupportedGuideOS
} from './clientConfigContract'

export type OpenCodeConfigPlatform =
  | GroupPlatform
  | 'antigravity-claude'
  | 'antigravity-gemini'

export interface OpenCodeConfigInput {
  platform: OpenCodeConfigPlatform
  baseUrl: string
  apiKey: string
  path: string
}

export interface WorkBuddyConfigInput {
  os: SupportedGuideOS
  platform: GroupPlatform | 'unified'
  endpoints: GatewayEndpoints
  apiKey: string
}

export interface PythonSdkExampleInput {
  kind: 'anthropic' | 'openai' | 'image'
  endpoints: GatewayEndpoints
  apiKey: string
}

export function buildPythonSdkExample(input: PythonSdkExampleInput): ClientConfigFile {
  if (input.kind === 'anthropic') {
    return {
      path: 'anthropic_client.py',
      content: `from anthropic import Anthropic

client = Anthropic(
    api_key="${input.apiKey}",
    base_url="${input.endpoints.bare}",
)

with client.messages.stream(
    model="${EXAMPLE_MODELS.anthropic}",
    max_tokens=1024,
    messages=[{"role": "user", "content": "Hello, Claude"}],
) as stream:
    for text in stream.text_stream:
        print(text, end="", flush=True)
print()`
    }
  }

  if (input.kind === 'openai') {
    return {
      path: 'openai_client.py',
      content: `from openai import OpenAI

client = OpenAI(
    api_key="${input.apiKey}",
    base_url="${input.endpoints.v1}",
)

stream = client.responses.create(
    model="${EXAMPLE_MODELS.openai}",
    input="Hello, GPT",
    stream=True,
)

for event in stream:
    if event.type == "response.output_text.delta":
        print(event.delta, end="", flush=True)
print()`
    }
  }

  return {
    path: 'gpt_image_2_client.py',
    content: `from base64 import b64decode
from pathlib import Path

from openai import OpenAI

client = OpenAI(
    api_key="${input.apiKey}",
    base_url="${input.endpoints.v1}",
)

stream = client.images.generate(
    model="${EXAMPLE_MODELS.image}",
    prompt="A fox mascot using an AI gateway",
    size="1024x1024",
    stream=True,
    partial_images=2,
)

for event in stream:
    image_b64 = getattr(event, "b64_json", None)
    if not image_b64:
        continue
    if event.type == "image_generation.partial_image":
        output_path = Path(f"partial_{event.partial_image_index}.png")
    elif event.type == "image_generation.completed":
        output_path = Path("image.png")
    else:
        continue
    output_path.write_bytes(b64decode(image_b64))
    print(f"Wrote {output_path}")`
  }
}

interface WorkBuddyModelConfig {
  id: string
  maxInputTokens: number
  maxOutputTokens: number
  supportsImages: boolean
  supportsReasoning: boolean
}

function workBuddyModelsForPlatform(
  platform: WorkBuddyConfigInput['platform']
): WorkBuddyModelConfig[] {
  const openAIModels: WorkBuddyModelConfig[] = [
    {
      id: EXAMPLE_MODELS.openai,
      maxInputTokens: 1050000,
      maxOutputTokens: 128000,
      supportsImages: true,
      supportsReasoning: true
    }
  ]
  const claudeModels: WorkBuddyModelConfig[] = [
    {
      id: 'claude-sonnet-5',
      maxInputTokens: 1000000,
      maxOutputTokens: 128000,
      supportsImages: true,
      supportsReasoning: true
    },
    {
      id: 'claude-opus-4-8',
      maxInputTokens: 1000000,
      maxOutputTokens: 128000,
      supportsImages: true,
      supportsReasoning: true
    }
  ]

  if (platform === 'openai') {
    return openAIModels
  }
  if (platform === 'unified') {
    return [...openAIModels, ...claudeModels]
  }
  return claudeModels
}

export function buildWorkBuddyConfigFile(input: WorkBuddyConfigInput): ClientConfigFile {
  const models = workBuddyModelsForPlatform(input.platform).map((model) => ({
    id: model.id,
    // WorkBuddy integrations can send the displayed model name as `model`, so
    // keep `name` identical to the gateway-supported id.
    name: model.id,
    vendor: 'Custom',
    url: input.endpoints.chatCompletions,
    apiKey: input.apiKey,
    maxInputTokens: model.maxInputTokens,
    maxOutputTokens: model.maxOutputTokens,
    supportsToolCall: true,
    supportsImages: model.supportsImages,
    supportsReasoning: model.supportsReasoning
  }))
  const content = JSON.stringify(
    {
      models,
      availableModels: models.map((model) => model.id)
    },
    null,
    2
  )
  const path = input.os === 'windows'
    ? '%userprofile%\\.workbuddy\\models.json'
    : '~/.workbuddy/models.json'

  return {
    path,
    content,
    hintKey: 'keys.useKeyModal.workBuddy.hint'
  }
}

export function buildOpenCodeConfigFile(input: OpenCodeConfigInput): ClientConfigFile {
  const provider: Record<string, any> = {
    [input.platform]: {
      options: {
        baseURL: input.baseUrl,
        apiKey: input.apiKey
      }
    }
  }
  const reasoningVariants = {
    low: {},
    medium: {},
    high: {},
    xhigh: {}
  }
  const maxReasoningVariants = {
    ...reasoningVariants,
    max: {}
  }
  const openaiModel = (name: string, context: number, output = 128000, variants = reasoningVariants) => ({
    name,
    limit: {
      context,
      output
    },
    options: {
      store: false
    },
    variants
  })
  const openaiModels = {
    'gpt-5.6': openaiModel('GPT-5.6 (Sol)', 1050000, 128000, maxReasoningVariants),
    'gpt-5.6-sol': openaiModel('GPT-5.6 Sol', 1050000, 128000, maxReasoningVariants),
    'gpt-5.6-terra': openaiModel('GPT-5.6 Terra', 1050000, 128000, maxReasoningVariants),
    'gpt-5.6-luna': openaiModel('GPT-5.6 Luna', 1050000, 128000, maxReasoningVariants),
    'gpt-5.5': openaiModel('GPT-5.5', 1050000),
    'gpt-5.4': openaiModel('GPT-5.4', 1050000),
    'gpt-5.4-mini': openaiModel('GPT-5.4 Mini', 400000),
    'gpt-5.3-codex-spark': openaiModel('GPT-5.3 Codex Spark', 128000, 32000),
    'gpt-5.2': openaiModel('GPT-5.2', 400000),
    'codex-mini-latest': {
      name: 'Codex Mini',
      limit: {
        context: 200000,
        output: 100000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {}
      }
    }
  }
  const geminiModels = {
    'gemini-2.0-flash': {
      name: 'Gemini 2.0 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      }
    },
    'gemini-2.5-flash': {
      name: 'Gemini 2.5 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      }
    },
    'gemini-2.5-pro': {
      name: 'Gemini 2.5 Pro',
      limit: {
        context: 2097152,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.5-flash': {
      name: 'Gemini 3.5 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      }
    },
    'gemini-3-flash-preview': {
      name: 'Gemini 3 Flash Preview',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      }
    },
    'gemini-3-pro-preview': {
      name: 'Gemini 3 Pro Preview',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.1-pro-preview': {
      name: 'Gemini 3.1 Pro Preview',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    }
  }

  const antigravityGeminiModels = {
    'gemini-2.5-flash': {
      name: 'Gemini 2.5 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'disable'
        }
      }
    },
    'gemini-2.5-flash-lite': {
      name: 'Gemini 2.5 Flash Lite',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-2.5-flash-thinking': {
      name: 'Gemini 2.5 Flash (Thinking)',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3-flash': {
      name: 'Gemini 3 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.1-pro-low': {
      name: 'Gemini 3.1 Pro Low',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.1-pro-high': {
      name: 'Gemini 3.1 Pro High',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-2.5-flash-image': {
      name: 'Gemini 2.5 Flash Image',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image'],
        output: ['image']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.1-flash-image': {
      name: 'Gemini 3.1 Flash Image',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image'],
        output: ['image']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    }
  }
  const claudeModel = (
    name: string,
    context: number,
    output: number,
    thinking?: { type: 'adaptive' } | { type: 'enabled'; budgetTokens: number }
  ) => ({
    name,
    limit: {
      context,
      output
    },
    modalities: {
      input: ['text', 'image', 'pdf'],
      output: ['text']
    },
    ...(thinking ? { options: { thinking } } : {})
  })
  const claudeAdaptive = (name: string) => claudeModel(name, 1048576, 128000, { type: 'adaptive' })
  const claudeThinking = (name: string, output = 128000) =>
    claudeModel(name, 200000, output, { budgetTokens: 24576, type: 'enabled' })
  const claudeModels = {
    'claude-fable-5': claudeAdaptive('Claude Fable 5'),
    'claude-mythos-5': claudeAdaptive('Claude Mythos 5'),
    'claude-opus-4-8': claudeModel('Claude Opus 4.8', 200000, 128000),
    'claude-opus-4-7': claudeModel('Claude Opus 4.7', 200000, 128000),
    'claude-opus-4-6': claudeModel('Claude Opus 4.6', 200000, 128000),
    'claude-opus-4-5-20251101': claudeModel('Claude Opus 4.5', 200000, 128000),
    'claude-sonnet-4-6': claudeThinking('Claude Sonnet 4.6', 64000),
    'claude-sonnet-4-5-20250929': claudeModel('Claude Sonnet 4.5', 200000, 64000),
    'claude-haiku-4-5-20251001': claudeModel('Claude Haiku 4.5', 200000, 64000),
    'claude-sonnet-4-20250514': claudeModel('Claude Sonnet 4', 200000, 64000),
    'claude-opus-4-20250514': claudeModel('Claude Opus 4', 200000, 128000),
    'claude-opus-4-1-20250805': claudeModel('Claude Opus 4.1', 200000, 128000),
    'claude-3-7-sonnet-20250219': claudeModel('Claude Sonnet 3.7', 200000, 64000),
    'claude-3-5-sonnet-20241022': claudeModel('Claude Sonnet 3.5 20241022', 200000, 8192),
    'claude-3-5-sonnet-20240620': claudeModel('Claude Sonnet 3.5 20240620', 200000, 8192),
    'claude-3-5-haiku-20241022': claudeModel('Claude Haiku 3.5', 200000, 8192)
  }
  const antigravityClaudeModels = {
    'claude-fable-5': claudeAdaptive('Claude Fable 5'),
    'claude-mythos-5': claudeAdaptive('Claude Mythos 5'),
    'claude-opus-4-8': claudeModel('Claude Opus 4.8', 200000, 128000),
    'claude-opus-4-7': claudeModel('Claude Opus 4.7', 200000, 128000),
    'claude-opus-4-6': claudeModel('Claude Opus 4.6', 200000, 128000),
    'claude-opus-4-6-thinking': claudeThinking('Claude Opus 4.6 Thinking'),
    'claude-opus-4-5-thinking': claudeThinking('Claude Opus 4.5 Thinking'),
    'claude-sonnet-4-6': claudeThinking('Claude Sonnet 4.6', 64000),
    'claude-sonnet-4-5': claudeModel('Claude Sonnet 4.5', 200000, 64000),
    'claude-sonnet-4-5-thinking': claudeThinking('Claude Sonnet 4.5 Thinking', 64000)
  }
  const grokModels = {
    'grok-4.5': {
      name: 'Grok 4.5',
      limit: { context: 1000000, output: 128000 }
    },
    'grok-4.3': {
      name: 'Grok 4.3',
      limit: { context: 1000000, output: 128000 }
    },
    'grok-build-0.1': {
      name: 'Grok Build 0.1',
      limit: { context: 256000, output: 128000 }
    },
    'grok-composer-2.5-fast': {
      name: 'Grok Composer 2.5 Fast',
      limit: { context: 500000, output: 128000 }
    }
  }

  if (input.platform === 'gemini') {
    provider[input.platform].npm = '@ai-sdk/google'
    provider[input.platform].models = geminiModels
  } else if (input.platform === 'anthropic') {
    provider[input.platform].npm = '@ai-sdk/anthropic'
    provider[input.platform].models = claudeModels
  } else if (input.platform === 'antigravity-claude') {
    provider[input.platform].npm = '@ai-sdk/anthropic'
    provider[input.platform].name = 'Antigravity (Claude)'
    provider[input.platform].models = antigravityClaudeModels
  } else if (input.platform === 'antigravity-gemini') {
    provider[input.platform].npm = '@ai-sdk/google'
    provider[input.platform].name = 'Antigravity (Gemini)'
    provider[input.platform].models = antigravityGeminiModels
  } else if (input.platform === 'openai') {
    provider[input.platform].models = openaiModels
  } else if (input.platform === 'grok') {
    provider[input.platform].npm = '@ai-sdk/openai'
    provider[input.platform].name = 'Grok via Sub2API'
    provider[input.platform].models = grokModels
  }

  const agent =
    input.platform === 'openai'
      ? {
          build: {
            options: {
              store: false
            }
          },
          plan: {
            options: {
              store: false
            }
          }
        }
      : undefined
  const defaultModelByPlatform: Record<string, string> = {
    anthropic: 'anthropic/claude-fable-5',
    openai: 'openai/gpt-5.5',
    gemini: 'gemini/gemini-2.5-flash',
    'antigravity-claude': 'antigravity-claude/claude-fable-5',
    'antigravity-gemini': 'antigravity-gemini/gemini-2.5-flash'
  }
  const smallModelByPlatform: Record<string, string> = {
    anthropic: 'anthropic/claude-haiku-4-5-20251001',
    openai: 'openai/gpt-5.3-codex-spark',
    gemini: 'gemini/gemini-2.0-flash',
    'antigravity-claude': 'antigravity-claude/claude-sonnet-4-5',
    'antigravity-gemini': 'antigravity-gemini/gemini-2.5-flash-lite'
  }
  const defaultModel = defaultModelByPlatform[input.platform]
  const smallModel = smallModelByPlatform[input.platform]

  const content = JSON.stringify(
    {
      $schema: 'https://opencode.ai/config.json',
      ...(defaultModel ? { model: defaultModel } : {}),
      ...(smallModel ? { small_model: smallModel } : {}),
      provider,
      ...(agent ? { agent } : {})
    },
    null,
    2
  )

  return {
    path: input.path,
    content,
    hintKey: 'keys.useKeyModal.opencode.hint'
  }
}
