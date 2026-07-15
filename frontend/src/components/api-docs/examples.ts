import {
  DOCS_API_KEY_PLACEHOLDER,
  EXAMPLE_MODELS,
  resolveGatewayEndpoints
} from '@/components/keys/clientConfigContract'

import type { ApiEndpointExamples, ApiEndpointId } from './types'

const requestBodies = {
  messages: {
    model: EXAMPLE_MODELS.anthropic,
    max_tokens: 1024,
    messages: [{ role: 'user', content: 'Hello' }]
  },
  'count-tokens': {
    model: EXAMPLE_MODELS.anthropic,
    messages: [{ role: 'user', content: 'Hello' }]
  },
  responses: {
    model: EXAMPLE_MODELS.openai,
    input: 'Hello'
  },
  'chat-completions': {
    model: EXAMPLE_MODELS.openai,
    messages: [{ role: 'user', content: 'Hello' }]
  },
  'image-generations': {
    model: EXAMPLE_MODELS.image,
    prompt: 'A fox mascot using an AI gateway',
    size: '1024x1024'
  }
} as const

function json(value: unknown): string {
  return JSON.stringify(value, null, 2)
}

function jsonCurl(endpoint: string, body: unknown, extraHeaders: string[] = []): string {
  const headers = [
    `-H "Authorization: Bearer ${DOCS_API_KEY_PLACEHOLDER}"`,
    '-H "Content-Type: application/json"',
    ...extraHeaders.map((header) => `-H "${header}"`)
  ]

  return [
    `curl ${endpoint} \\`,
    ...headers.map((header) => `  ${header} \\`),
    `  --data '${JSON.stringify(body)}'`
  ].join('\n')
}

function pythonJsonRequest(endpoint: string, body: unknown, extraHeaders: string[] = []): string {
  const headerLines = [
    `"Authorization": "Bearer ${DOCS_API_KEY_PLACEHOLDER}",`,
    '"Content-Type": "application/json",',
    ...extraHeaders.map((header) => {
      const separator = header.indexOf(':')
      return `"${header.slice(0, separator)}": "${header.slice(separator + 1).trim()}",`
    })
  ]

  return [
    'import requests',
    '',
    `response = requests.post(`,
    `    "${endpoint}",`,
    '    headers={',
    ...headerLines.map((header) => `        ${header}`),
    '    },',
    `    json=${json(body).replace(/\n/g, '\n    ')},`,
    ')',
    'response.raise_for_status()',
    'print(response.json())'
  ].join('\n')
}

export function buildEndpointExamples(
  endpointId: ApiEndpointId,
  baseUrl: string
): ApiEndpointExamples {
  const endpoints = resolveGatewayEndpoints(baseUrl)

  switch (endpointId) {
    case 'messages': {
      const body = requestBodies.messages
      const versionHeader = ['anthropic-version: 2023-06-01']
      return {
        curl: jsonCurl(endpoints.messages, body, versionHeader),
        python: pythonJsonRequest(endpoints.messages, body, versionHeader),
        success: json({
          id: 'msg_01Example',
          type: 'message',
          role: 'assistant',
          model: EXAMPLE_MODELS.anthropic,
          content: [{ type: 'text', text: 'Hello!' }],
          stop_reason: 'end_turn',
          usage: { input_tokens: 9, output_tokens: 4 }
        }),
        stream: [
          'event: content_block_delta',
          'data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}',
          '',
          'event: message_stop',
          'data: {"type":"message_stop"}'
        ].join('\n')
      }
    }
    case 'count-tokens': {
      const body = requestBodies['count-tokens']
      const versionHeader = ['anthropic-version: 2023-06-01']
      return {
        curl: jsonCurl(endpoints.countTokens, body, versionHeader),
        python: pythonJsonRequest(endpoints.countTokens, body, versionHeader),
        success: json({ input_tokens: 9 })
      }
    }
    case 'responses': {
      const body = requestBodies.responses
      return {
        curl: jsonCurl(endpoints.responses, body),
        python: pythonJsonRequest(endpoints.responses, body),
        success: json({
          id: 'resp_01Example',
          object: 'response',
          status: 'completed',
          model: EXAMPLE_MODELS.openai,
          output: [
            {
              id: 'msg_01Example',
              type: 'message',
              role: 'assistant',
              content: [{ type: 'output_text', text: 'Hello!' }]
            }
          ],
          usage: { input_tokens: 9, output_tokens: 4, total_tokens: 13 }
        }),
        stream: [
          'event: response.output_text.delta',
          'data: {"type":"response.output_text.delta","delta":"Hello"}',
          '',
          'event: response.completed',
          'data: {"type":"response.completed","response":{"status":"completed"}}'
        ].join('\n')
      }
    }
    case 'chat-completions': {
      const body = requestBodies['chat-completions']
      return {
        curl: jsonCurl(endpoints.chatCompletions, body),
        python: pythonJsonRequest(endpoints.chatCompletions, body),
        success: json({
          id: 'chatcmpl-01Example',
          object: 'chat.completion',
          model: EXAMPLE_MODELS.openai,
          choices: [
            {
              index: 0,
              message: { role: 'assistant', content: 'Hello!' },
              finish_reason: 'stop'
            }
          ],
          usage: { prompt_tokens: 9, completion_tokens: 4, total_tokens: 13 }
        }),
        stream: [
          'data: {"choices":[{"delta":{"content":"Hello"}}]}',
          '',
          'data: [DONE]'
        ].join('\n')
      }
    }
    case 'models':
      return {
        curl: [
          `curl ${endpoints.models} \\`,
          `  -H "Authorization: Bearer ${DOCS_API_KEY_PLACEHOLDER}"`
        ].join('\n'),
        python: [
          'import requests',
          '',
          `response = requests.get(`,
          `    "${endpoints.models}",`,
          `    headers={"Authorization": "Bearer ${DOCS_API_KEY_PLACEHOLDER}"},`,
          ')',
          'response.raise_for_status()',
          'print(response.json())'
        ].join('\n'),
        success: json({
          object: 'list',
          data: [
            { id: EXAMPLE_MODELS.anthropic, object: 'model' },
            { id: EXAMPLE_MODELS.openai, object: 'model' },
            { id: EXAMPLE_MODELS.image, object: 'model' }
          ]
        })
      }
    case 'image-generations': {
      const body = requestBodies['image-generations']
      return {
        curl: jsonCurl(endpoints.imageGenerations, body),
        python: pythonJsonRequest(endpoints.imageGenerations, body),
        success: json({
          created: 1784160000,
          data: [{ b64_json: 'iVBORw0KGgoAAA...' }]
        })
      }
    }
    case 'image-edits':
      return {
        curl: [
          `curl ${endpoints.imageEdits} \\`,
          `  -H "Authorization: Bearer ${DOCS_API_KEY_PLACEHOLDER}" \\`,
          `  -F "model=${EXAMPLE_MODELS.image}" \\`,
          '  -F "image=@input.png" \\',
          '  -F "prompt=Add a blue background" \\',
          '  -F "size=1024x1024"'
        ].join('\n'),
        python: [
          'import requests',
          '',
          'with open("input.png", "rb") as image:',
          `    response = requests.post(`,
          `        "${endpoints.imageEdits}",`,
          `        headers={"Authorization": "Bearer ${DOCS_API_KEY_PLACEHOLDER}"},`,
          '        files={"image": image},',
          `        data={"model": "${EXAMPLE_MODELS.image}", "prompt": "Add a blue background", "size": "1024x1024"},`,
          '    )',
          'response.raise_for_status()',
          'print(response.json())'
        ].join('\n'),
        success: json({
          created: 1784160000,
          data: [{ b64_json: 'iVBORw0KGgoAAA...' }]
        })
      }
  }
}
