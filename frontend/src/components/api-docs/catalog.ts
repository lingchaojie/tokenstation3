import type {
  ApiDocsNavGroup,
  ApiDocsPage,
  ApiEndpointDefinition,
  ApiParameter
} from './types'

export const API_DOCS_CAPABILITY_TAGS = [
  'Messages',
  'Responses',
  'Chat Completions',
  'Images',
  'Tools',
  'Streaming'
] as const

export const API_DOCS_NAV: ApiDocsNavGroup[] = [
  {
    id: 'quickstart',
    labelKey: 'apiDocs.nav.quickstart',
    pageIds: ['quickstart', 'authentication']
  },
  {
    id: 'clients',
    labelKey: 'apiDocs.nav.clients',
    pageIds: ['client-integration']
  },
  {
    id: 'reference',
    labelKey: 'apiDocs.nav.reference',
    pageIds: [
      'messages',
      'count-tokens',
      'responses',
      'chat-completions',
      'models',
      'image-generations',
      'image-edits'
    ]
  },
  {
    id: 'advanced',
    labelKey: 'apiDocs.nav.advanced',
    pageIds: ['capabilities']
  },
  {
    id: 'platform',
    labelKey: 'apiDocs.nav.platform',
    pageIds: ['errors', 'request-id', 'key-security']
  }
]

export const API_DOCS_PAGES: ApiDocsPage[] = [
  {
    id: 'quickstart',
    kind: 'guide',
    path: '/docs',
    titleKey: 'apiDocs.pages.quickstart.title',
    summaryKey: 'apiDocs.pages.quickstart.summary',
    keywords: ['quickstart', 'base URL', 'first request', 'models']
  },
  {
    id: 'authentication',
    kind: 'guide',
    path: '/docs/guide/authentication',
    titleKey: 'apiDocs.pages.authentication.title',
    summaryKey: 'apiDocs.pages.authentication.summary',
    keywords: ['authentication', 'API key', 'Bearer', 'x-api-key']
  },
  {
    id: 'client-integration',
    kind: 'guide',
    path: '/docs/guide/client-integration',
    titleKey: 'apiDocs.pages.clientIntegration.title',
    summaryKey: 'apiDocs.pages.clientIntegration.summary',
    keywords: ['client', 'SDK', 'configuration', 'CLI']
  },
  {
    id: 'capabilities',
    kind: 'guide',
    path: '/docs/guide/capabilities',
    titleKey: 'apiDocs.pages.capabilities.title',
    summaryKey: 'apiDocs.pages.capabilities.summary',
    keywords: ['streaming', 'tools', 'structured output', 'reasoning', 'prompt cache']
  },
  {
    id: 'messages',
    kind: 'endpoint',
    path: '/docs/api-reference/messages',
    titleKey: 'apiDocs.pages.messages.title',
    summaryKey: 'apiDocs.pages.messages.summary',
    keywords: ['Anthropic', 'messages', 'streaming', 'tools'],
    endpointId: 'messages'
  },
  {
    id: 'count-tokens',
    kind: 'endpoint',
    path: '/docs/api-reference/count-tokens',
    titleKey: 'apiDocs.pages.countTokens.title',
    summaryKey: 'apiDocs.pages.countTokens.summary',
    keywords: ['Anthropic', 'token count', 'preflight'],
    endpointId: 'count-tokens'
  },
  {
    id: 'responses',
    kind: 'endpoint',
    path: '/docs/api-reference/responses',
    titleKey: 'apiDocs.pages.responses.title',
    summaryKey: 'apiDocs.pages.responses.summary',
    keywords: ['OpenAI', 'responses', 'streaming', 'tools', 'reasoning'],
    endpointId: 'responses'
  },
  {
    id: 'chat-completions',
    kind: 'endpoint',
    path: '/docs/api-reference/chat-completions',
    titleKey: 'apiDocs.pages.chatCompletions.title',
    summaryKey: 'apiDocs.pages.chatCompletions.summary',
    keywords: ['OpenAI', 'chat completions', 'streaming', 'tools'],
    endpointId: 'chat-completions'
  },
  {
    id: 'models',
    kind: 'endpoint',
    path: '/docs/api-reference/models',
    titleKey: 'apiDocs.pages.models.title',
    summaryKey: 'apiDocs.pages.models.summary',
    keywords: ['models', 'available models', 'API key'],
    endpointId: 'models'
  },
  {
    id: 'image-generations',
    kind: 'endpoint',
    path: '/docs/api-reference/image-generations',
    titleKey: 'apiDocs.pages.imageGenerations.title',
    summaryKey: 'apiDocs.pages.imageGenerations.summary',
    keywords: ['images', 'generation', 'prompt'],
    endpointId: 'image-generations'
  },
  {
    id: 'image-edits',
    kind: 'endpoint',
    path: '/docs/api-reference/image-edits',
    titleKey: 'apiDocs.pages.imageEdits.title',
    summaryKey: 'apiDocs.pages.imageEdits.summary',
    keywords: ['images', 'edits', 'multipart'],
    endpointId: 'image-edits'
  },
  {
    id: 'errors',
    kind: 'platform',
    path: '/docs/platform/errors',
    titleKey: 'apiDocs.pages.errors.title',
    summaryKey: 'apiDocs.pages.errors.summary',
    keywords: ['errors', 'HTTP status', 'troubleshooting']
  },
  {
    id: 'request-id',
    kind: 'platform',
    path: '/docs/platform/request-id',
    titleKey: 'apiDocs.pages.requestId.title',
    summaryKey: 'apiDocs.pages.requestId.summary',
    keywords: ['request ID', 'correlation', 'support']
  },
  {
    id: 'key-security',
    kind: 'platform',
    path: '/docs/platform/key-security',
    titleKey: 'apiDocs.pages.keySecurity.title',
    summaryKey: 'apiDocs.pages.keySecurity.summary',
    keywords: ['API key', 'security', 'quota', 'IP rules', 'expiration']
  }
]

function bodyParameter(
  name: string,
  required: boolean,
  type: string,
  descriptionKey = `apiDocs.parameters.${name.replace(/_([a-z])/g, (_, letter: string) => letter.toUpperCase())}`
): ApiParameter {
  return { name, location: 'body', required, type, descriptionKey }
}

const gatewayErrors = ['API_KEY_REQUIRED', 'INVALID_API_KEY']
const anthropicErrors = [...gatewayErrors, 'invalid_request_error', 'rate_limit_error']
const openAiErrors = [...gatewayErrors, 'invalid_request_error', 'rate_limit_error']

export const API_ENDPOINTS: ApiEndpointDefinition[] = [
  {
    id: 'messages',
    pageId: 'messages',
    method: 'POST',
    path: '/v1/messages',
    protocol: 'anthropic',
    titleKey: 'apiDocs.pages.messages.title',
    summaryKey: 'apiDocs.pages.messages.summary',
    parameters: [
      bodyParameter('model', true, 'string'),
      bodyParameter('max_tokens', true, 'integer'),
      bodyParameter('messages', true, 'array'),
      bodyParameter('stream', false, 'boolean'),
      bodyParameter('tools', false, 'array')
    ],
    errorCodes: anthropicErrors,
    supportsStreaming: true
  },
  {
    id: 'count-tokens',
    pageId: 'count-tokens',
    method: 'POST',
    path: '/v1/messages/count_tokens',
    protocol: 'anthropic',
    titleKey: 'apiDocs.pages.countTokens.title',
    summaryKey: 'apiDocs.pages.countTokens.summary',
    parameters: [
      bodyParameter('model', true, 'string'),
      bodyParameter('messages', true, 'array'),
      bodyParameter('system', false, 'string | array'),
      bodyParameter('tools', false, 'array')
    ],
    errorCodes: anthropicErrors,
    supportsStreaming: false
  },
  {
    id: 'responses',
    pageId: 'responses',
    method: 'POST',
    path: '/v1/responses',
    protocol: 'openai',
    titleKey: 'apiDocs.pages.responses.title',
    summaryKey: 'apiDocs.pages.responses.summary',
    parameters: [
      bodyParameter('model', true, 'string'),
      bodyParameter('input', true, 'string | array'),
      bodyParameter('stream', false, 'boolean'),
      bodyParameter('tools', false, 'array'),
      bodyParameter('text', false, 'object'),
      bodyParameter('reasoning', false, 'object')
    ],
    errorCodes: openAiErrors,
    supportsStreaming: true
  },
  {
    id: 'chat-completions',
    pageId: 'chat-completions',
    method: 'POST',
    path: '/v1/chat/completions',
    protocol: 'openai',
    titleKey: 'apiDocs.pages.chatCompletions.title',
    summaryKey: 'apiDocs.pages.chatCompletions.summary',
    parameters: [
      bodyParameter('model', true, 'string'),
      bodyParameter('messages', true, 'array'),
      bodyParameter('stream', false, 'boolean'),
      bodyParameter('tools', false, 'array'),
      bodyParameter('response_format', false, 'object')
    ],
    errorCodes: openAiErrors,
    supportsStreaming: true
  },
  {
    id: 'models',
    pageId: 'models',
    method: 'GET',
    path: '/v1/models',
    protocol: 'common',
    titleKey: 'apiDocs.pages.models.title',
    summaryKey: 'apiDocs.pages.models.summary',
    parameters: [],
    errorCodes: gatewayErrors,
    supportsStreaming: false
  },
  {
    id: 'image-generations',
    pageId: 'image-generations',
    method: 'POST',
    path: '/v1/images/generations',
    protocol: 'openai',
    titleKey: 'apiDocs.pages.imageGenerations.title',
    summaryKey: 'apiDocs.pages.imageGenerations.summary',
    parameters: [
      bodyParameter('model', true, 'string'),
      bodyParameter('prompt', true, 'string'),
      bodyParameter('size', false, 'string'),
      bodyParameter('n', false, 'integer'),
      bodyParameter('quality', false, 'string')
    ],
    errorCodes: openAiErrors,
    supportsStreaming: false
  },
  {
    id: 'image-edits',
    pageId: 'image-edits',
    method: 'POST',
    path: '/v1/images/edits',
    protocol: 'openai',
    titleKey: 'apiDocs.pages.imageEdits.title',
    summaryKey: 'apiDocs.pages.imageEdits.summary',
    parameters: [
      bodyParameter('model', true, 'string'),
      bodyParameter('image', true, 'file'),
      bodyParameter('prompt', true, 'string'),
      bodyParameter('size', false, 'string')
    ],
    errorCodes: openAiErrors,
    supportsStreaming: false
  }
]

export function findApiDocsPage(path: string): ApiDocsPage | undefined {
  const normalized = path.length > 1 ? path.replace(/\/+$/, '') : path
  return API_DOCS_PAGES.find((page) => page.path === normalized)
}
