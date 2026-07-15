import { GUIDE_VARIANTS } from '@/components/getting-started/curriculum'
import { buildPythonSdkExample } from '@/components/keys/clientExampleFiles'
import {
  buildClientConfigFiles,
  DOCS_API_KEY_PLACEHOLDER,
  resolveGatewayEndpoints,
  type ClientConfigFile,
  type SupportedGuideClient
} from '@/components/keys/clientConfigFiles'

import { API_DOCS_PAGES } from './catalog'
import { buildEndpointExamples } from './examples'
import type {
  ApiDocsBlock,
  ApiDocsGuideDefinition,
  ApiDocsGuideSection,
  ApiDocsPageId
} from './types'

const gatewayCodeRows = [
  ['400', 'api_key_in_query_deprecated'],
  ['401', 'API_KEY_REQUIRED'],
  ['401', 'INVALID_API_KEY'],
  ['401', 'API_KEY_DISABLED'],
  ['401', 'USER_NOT_FOUND'],
  ['401', 'USER_INACTIVE'],
  ['403', 'ACCESS_DENIED'],
  ['403', 'API_KEY_EXPIRED'],
  ['403', 'GROUP_DELETED'],
  ['403', 'GROUP_DISABLED'],
  ['403', 'GROUP_NOT_ALLOWED'],
  ['403', 'INSUFFICIENT_BALANCE'],
  ['403', 'SUBSCRIPTION_INVALID'],
  ['429', 'API_KEY_QUOTA_EXHAUSTED'],
  ['429', 'USAGE_LIMIT_EXCEEDED'],
  ['500', 'INTERNAL_ERROR'],
  ['500', 'SUBSCRIPTION_MAINTENANCE_FAILED']
] as string[][]

const clientSections: Array<{
  client: SupportedGuideClient
  id: string
  titleKey: string
}> = [
  {
    client: 'claude_code',
    id: 'claude-code',
    titleKey: 'gettingStarted.clients.claude_code'
  },
  { client: 'codex', id: 'codex-cli', titleKey: 'gettingStarted.clients.codex' },
  { client: 'opencode', id: 'opencode', titleKey: 'gettingStarted.clients.opencode' },
  { client: 'cc_switch', id: 'cc-switch', titleKey: 'gettingStarted.clients.cc_switch' }
]

function paragraph(textKey: string): ApiDocsBlock {
  return { kind: 'paragraph', textKey }
}

function callout(tone: 'info' | 'warning', textKey: string): ApiDocsBlock {
  return { kind: 'callout', tone, textKey }
}

function code(label: string, language: string, content: string): ApiDocsBlock {
  return { kind: 'code', label, language, code: content }
}

function languageForFile(file: ClientConfigFile): string {
  if (file.path.endsWith('.py')) return 'python'
  if (file.path.includes('.json')) return 'json'
  if (file.path.includes('.toml')) return 'toml'
  if (file.path === 'PowerShell') return 'powershell'
  if (file.path === 'Terminal') return 'bash'
  return 'text'
}

function fileBlock(file: ClientConfigFile): ApiDocsBlock {
  return code(file.path, languageForFile(file), file.content)
}

function buildQuickstart(baseUrl: string): ApiDocsGuideSection[] {
  const endpoints = resolveGatewayEndpoints(baseUrl)
  const firstRequest = buildEndpointExamples('responses', baseUrl)
  const models = buildEndpointExamples('models', baseUrl)

  return [
    {
      id: 'base-url',
      titleKey: 'apiDocs.sections.overview',
      blocks: [
        paragraph('apiDocs.guides.quickstart.intro'),
        paragraph('apiDocs.guides.quickstart.baseUrl'),
        code('Base URL', 'text', endpoints.v1)
      ]
    },
    {
      id: 'api-key',
      titleKey: 'apiDocs.sections.authentication',
      blocks: [
        paragraph('apiDocs.guides.quickstart.apiKey'),
        callout('warning', 'apiDocs.guides.authentication.safety'),
        {
          kind: 'links',
          links: [{ labelKey: 'apiDocs.apiKeys', to: '/keys' }]
        }
      ]
    },
    {
      id: 'first-request',
      titleKey: 'apiDocs.sections.request',
      blocks: [
        paragraph('apiDocs.guides.quickstart.firstRequest'),
        code('cURL', 'bash', firstRequest.curl)
      ]
    },
    {
      id: 'available-models',
      titleKey: 'apiDocs.pages.models.title',
      blocks: [
        paragraph('apiDocs.guides.quickstart.models'),
        code('cURL', 'bash', models.curl),
        {
          kind: 'links',
          links: [
            { labelKey: 'apiDocs.beginnerGuide', to: '/getting-started' },
            { labelKey: 'apiDocs.apiKeys', to: '/keys' }
          ]
        }
      ]
    }
  ]
}

function buildAuthentication(): ApiDocsGuideSection[] {
  return [
    {
      id: 'bearer',
      titleKey: 'apiDocs.sections.authentication',
      blocks: [
        paragraph('apiDocs.guides.authentication.intro'),
        paragraph('apiDocs.guides.authentication.bearer'),
        code('Authorization', 'http', `Authorization: Bearer ${DOCS_API_KEY_PLACEHOLDER}`)
      ]
    },
    {
      id: 'x-api-key',
      titleKey: 'apiDocs.guides.authentication.xApiKey',
      blocks: [
        paragraph('apiDocs.guides.authentication.xApiKey'),
        code('x-api-key', 'http', `x-api-key: ${DOCS_API_KEY_PLACEHOLDER}`)
      ]
    },
    {
      id: 'key-safety',
      titleKey: 'apiDocs.sections.security',
      blocks: [
        callout('warning', 'apiDocs.guides.authentication.safety'),
        {
          kind: 'links',
          links: [{ labelKey: 'apiDocs.apiKeys', to: '/keys' }]
        }
      ]
    },
    {
      id: 'deprecated-query',
      titleKey: 'apiDocs.guides.authentication.deprecatedQuery',
      blocks: [callout('warning', 'apiDocs.guides.authentication.deprecatedQuery')]
    }
  ]
}

function buildClientSection(
  client: SupportedGuideClient,
  id: string,
  titleKey: string,
  baseUrl: string
): ApiDocsGuideSection {
  const variant = GUIDE_VARIANTS.find((candidate) =>
    candidate.client === client && candidate.os === 'macos'
  )
  if (!variant) throw new Error(`Missing macOS guide variant for ${client}`)

  const installBlocks = variant.installCommand
    ? [code('macOS', 'bash', variant.installCommand)]
    : []
  const configBlocks = buildClientConfigFiles({
    client,
    os: 'macos',
    platform: 'unified',
    apiKey: DOCS_API_KEY_PLACEHOLDER,
    baseUrl
  }).map(fileBlock)

  return {
    id,
    titleKey,
    blocks: [
      paragraph('apiDocs.guides.clientIntegration.installation'),
      ...installBlocks,
      {
        kind: 'links',
        links: [
          {
            labelKey: 'gettingStarted.installation.officialSource',
            to: variant.officialSourceUrl
          }
        ]
      },
      paragraph('apiDocs.guides.clientIntegration.configuration'),
      ...configBlocks,
      callout('info', 'apiDocs.guides.clientIntegration.windowsNote')
    ]
  }
}

function buildClientIntegration(baseUrl: string): ApiDocsGuideSection[] {
  const endpoints = resolveGatewayEndpoints(baseUrl)
  const clientGuideSections = clientSections.map(({ client, id, titleKey }) =>
    buildClientSection(client, id, titleKey, baseUrl)
  )
  const sdkFiles = (['anthropic', 'openai', 'image'] as const).map((kind) =>
    buildPythonSdkExample({
      kind,
      endpoints,
      apiKey: DOCS_API_KEY_PLACEHOLDER
    })
  )

  return [
    ...clientGuideSections,
    {
      id: 'python-sdk',
      titleKey: 'apiDocs.labels.python',
      blocks: [
        paragraph('apiDocs.guides.clientIntegration.intro'),
        ...sdkFiles.map(fileBlock)
      ]
    }
  ]
}

function buildCapabilities(): ApiDocsGuideSection[] {
  return [
    {
      id: 'streaming',
      titleKey: 'apiDocs.sections.streaming',
      blocks: [
        paragraph('apiDocs.guides.capabilities.intro'),
        paragraph('apiDocs.guides.capabilities.streaming'),
        code('JSON', 'json', '{"stream":true}')
      ]
    },
    {
      id: 'tools',
      titleKey: 'apiDocs.guides.capabilities.tools',
      blocks: [
        paragraph('apiDocs.guides.capabilities.tools'),
        code(
          'JSON',
          'json',
          '{"tools":[{"type":"function","name":"get_weather","description":"Get weather"}]}'
        )
      ]
    },
    {
      id: 'structured-output',
      titleKey: 'apiDocs.guides.capabilities.structuredOutput',
      blocks: [
        paragraph('apiDocs.guides.capabilities.structuredOutput'),
        code(
          'JSON',
          'json',
          '{"text":{"format":{"type":"json_schema","name":"answer","schema":{"type":"object"}}}}'
        )
      ]
    },
    {
      id: 'reasoning',
      titleKey: 'apiDocs.guides.capabilities.reasoning',
      blocks: [
        paragraph('apiDocs.guides.capabilities.reasoning'),
        code('JSON', 'json', '{"reasoning":{"effort":"high"}}')
      ]
    },
    {
      id: 'prompt-cache',
      titleKey: 'apiDocs.guides.capabilities.promptCache',
      blocks: [
        paragraph('apiDocs.guides.capabilities.promptCache'),
        code(
          'Anthropic cache_control (5m)',
          'json',
          '{"type":"text","text":"Reusable context","cache_control":{"type":"ephemeral","ttl":"5m"}}'
        ),
        code(
          'Anthropic cache_control (1h)',
          'json',
          '{"type":"text","text":"Long-lived context","cache_control":{"type":"ephemeral","ttl":"1h"}}'
        )
      ]
    }
  ]
}

function buildErrors(): ApiDocsGuideSection[] {
  return [
    {
      id: 'gateway-envelope',
      titleKey: 'apiDocs.guides.errors.gatewayEnvelope',
      blocks: [
        paragraph('apiDocs.guides.errors.gatewayEnvelope'),
        code(
          'Gateway error',
          'json',
          '{"code":"INVALID_API_KEY","message":"Invalid API key"}'
        )
      ]
    },
    {
      id: 'gateway-codes',
      titleKey: 'apiDocs.pages.errors.title',
      blocks: [
        { kind: 'table', columns: ['HTTP', 'Code'], rows: gatewayCodeRows },
        callout('info', 'apiDocs.errors.actions.INSUFFICIENT_BALANCE'),
        callout('info', 'apiDocs.errors.actions.USER_NOT_FOUND')
      ]
    },
    {
      id: 'anthropic-envelope',
      titleKey: 'apiDocs.guides.errors.protocolEnvelope',
      blocks: [
        paragraph('apiDocs.guides.errors.protocolEnvelope'),
        code(
          'Anthropic',
          'json',
          '{"type":"error","error":{"type":"invalid_request_error","message":"model is required"}}'
        )
      ]
    },
    {
      id: 'openai-envelope',
      titleKey: 'apiDocs.guides.errors.protocolEnvelope',
      blocks: [
        paragraph('apiDocs.guides.errors.protocolEnvelope'),
        callout('warning', 'apiDocs.errors.gatewayEnvelopeWarning'),
        code(
          'OpenAI',
          'json',
          '{"error":{"type":"invalid_request_error","message":"model is required"}}'
        )
      ]
    },
    {
      id: 'stream-errors',
      titleKey: 'apiDocs.sections.streaming',
      blocks: [
        paragraph('apiDocs.guides.errors.streamFailures'),
        code(
          'HTTP 200 stream errors',
          'text',
          [
            'HTTP 200 (stream started)',
            '',
            'Anthropic Messages',
            'event: error',
            'data: {"type":"error","error":{"type":"api_error","message":"Stream failed"}}',
            '',
            'OpenAI Responses',
            'event: response.failed',
            'data: {"type":"response.failed","error":{"message":"Stream failed"}}',
            '',
            'Chat Completions',
            'data: {"type":"error","error":{"type":"upstream_error","message":"Stream failed"}}'
          ].join('\n')
        )
      ]
    }
  ]
}

function buildRequestId(): ApiDocsGuideSection[] {
  return [
    {
      id: 'headers',
      titleKey: 'apiDocs.guides.requestId.headers',
      blocks: [
        paragraph('apiDocs.guides.requestId.intro'),
        paragraph('apiDocs.guides.requestId.headers'),
        code('HTTP', 'http', ['X-Request-ID: ...', 'X-Client-Request-ID: ...'].join('\n'))
      ]
    },
    {
      id: 'support-checklist',
      titleKey: 'apiDocs.guides.requestId.supportChecklist',
      blocks: [paragraph('apiDocs.guides.requestId.supportChecklist')]
    },
    {
      id: 'redaction',
      titleKey: 'apiDocs.guides.requestId.redaction',
      blocks: [callout('warning', 'apiDocs.guides.requestId.redaction')]
    }
  ]
}

function buildKeySecurity(): ApiDocsGuideSection[] {
  return [
    {
      id: 'expiration',
      titleKey: 'apiDocs.guides.keySecurity.expiration',
      blocks: [
        paragraph('apiDocs.guides.keySecurity.intro'),
        paragraph('apiDocs.guides.keySecurity.expiration')
      ]
    },
    {
      id: 'quota',
      titleKey: 'apiDocs.guides.keySecurity.quota',
      blocks: [paragraph('apiDocs.guides.keySecurity.quota')]
    },
    {
      id: 'rate-windows',
      titleKey: 'apiDocs.guides.keySecurity.rateWindows',
      blocks: [
        paragraph('apiDocs.guides.keySecurity.rateWindows'),
        { kind: 'table', columns: ['Window'], rows: [['5h'], ['1d'], ['7d']] }
      ]
    },
    {
      id: 'ip-rules',
      titleKey: 'apiDocs.guides.keySecurity.ipRules',
      blocks: [
        paragraph('apiDocs.guides.keySecurity.ipRules'),
        {
          kind: 'table',
          columns: ['Rule', 'Matching IP/CIDR'],
          rows: [
            ['Whitelist', 'Allowed'],
            ['Blacklist', 'Denied']
          ]
        }
      ]
    }
  ]
}

export function buildGuidePage(
  pageId: ApiDocsPageId,
  baseUrl: string
): ApiDocsGuideDefinition {
  const page = API_DOCS_PAGES.find(({ id }) => id === pageId)
  if (!page || (page.kind !== 'guide' && page.kind !== 'platform')) {
    throw new Error(`API docs page ${pageId} is not guide content`)
  }

  switch (pageId) {
    case 'quickstart':
      return { pageId, sections: buildQuickstart(baseUrl) }
    case 'authentication':
      return { pageId, sections: buildAuthentication() }
    case 'client-integration':
      return { pageId, sections: buildClientIntegration(baseUrl) }
    case 'capabilities':
      return { pageId, sections: buildCapabilities() }
    case 'errors':
      return { pageId, sections: buildErrors() }
    case 'request-id':
      return { pageId, sections: buildRequestId() }
    case 'key-security':
      return { pageId, sections: buildKeySecurity() }
    default:
      throw new Error(`API docs page ${pageId} has no guide definition`)
  }
}
