import { GUIDE_VARIANTS } from '@/components/getting-started/curriculum'
import { buildPythonSdkExample } from '@/components/keys/clientExampleFiles'
import {
  buildClientConfigFiles,
  DOCS_API_KEY_PLACEHOLDER,
  EXAMPLE_MODELS,
  resolveGatewayEndpoints,
  type ClientConfigFile,
  type SupportedGuideClient,
  type SupportedGuideOS
} from '@/components/keys/clientConfigFiles'

import { API_DOCS_PAGES } from './catalog'
import { buildEndpointExamples } from './examples'
import type {
  ApiDocsBlock,
  ApiDocsGuideDefinition,
  ApiDocsGuideSection,
  ApiDocsPageId,
  ApiDocsTableValue
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
] as const

const clientSections: Array<{
  client: SupportedGuideClient
  id: string
  titleKey: string
}> = [
  {
    client: 'claude_code',
    id: 'claude-code',
    titleKey: 'apiDocs.guideSectionTitles.clientIntegration.claudeCode'
  },
  {
    client: 'codex',
    id: 'codex-cli',
    titleKey: 'apiDocs.guideSectionTitles.clientIntegration.codexCli'
  },
  {
    client: 'opencode',
    id: 'opencode',
    titleKey: 'apiDocs.guideSectionTitles.clientIntegration.opencode'
  },
  {
    client: 'cc_switch',
    id: 'cc-switch',
    titleKey: 'apiDocs.guideSectionTitles.clientIntegration.ccSwitch'
  }
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

function raw(value: string): ApiDocsTableValue {
  return { kind: 'raw', value }
}

function localized(textKey: string): ApiDocsTableValue {
  return { kind: 'localized', textKey }
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

function buildClientVariantBlocks(
  client: SupportedGuideClient,
  os: Extract<SupportedGuideOS, 'macos' | 'windows'>,
  baseUrl: string
): ApiDocsBlock[] {
  const variant = GUIDE_VARIANTS.find((candidate) =>
    candidate.client === client && candidate.os === os
  )
  if (!variant) throw new Error(`Missing ${os} guide variant for ${client}`)

  const platformLabel = os === 'macos' ? 'macOS' : 'Windows'
  const installBlocks = variant.installCommand
    ? [code(platformLabel, variant.shell === 'powershell' ? 'powershell' : 'bash', variant.installCommand)]
    : []
  const links: Array<{ labelKey: string; to: string }> = [
    {
      labelKey: 'gettingStarted.installation.officialSource',
      to: variant.officialSourceUrl
    }
  ]
  if (variant.desktopDownloadUrl) {
    links.push({
      labelKey: 'gettingStarted.installation.downloadDesktop',
      to: variant.desktopDownloadUrl
    })
  }
  const configBlocks = buildClientConfigFiles({
    client,
    os,
    platform: 'unified',
    apiKey: DOCS_API_KEY_PLACEHOLDER,
    baseUrl
  }).map(fileBlock)

  return [
    callout(
      'info',
      os === 'macos'
        ? 'apiDocs.guides.clientIntegration.macosNote'
        : 'apiDocs.guides.clientIntegration.windowsNote'
    ),
    ...installBlocks,
    { kind: 'links', links },
    ...configBlocks
  ]
}

function buildQuickstart(baseUrl: string): ApiDocsGuideSection[] {
  const endpoints = resolveGatewayEndpoints(baseUrl)
  const firstRequest = buildEndpointExamples('responses', baseUrl)
  const models = buildEndpointExamples('models', baseUrl)

  return [
    {
      id: 'base-url',
      titleKey: 'apiDocs.guideSectionTitles.quickstart.baseUrl',
      blocks: [
        paragraph('apiDocs.guides.quickstart.intro'),
        paragraph('apiDocs.guides.quickstart.baseUrl'),
        code('Base URL', 'text', endpoints.v1)
      ]
    },
    {
      id: 'api-key',
      titleKey: 'apiDocs.guideSectionTitles.quickstart.apiKey',
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
      titleKey: 'apiDocs.guideSectionTitles.quickstart.firstRequest',
      blocks: [
        paragraph('apiDocs.guides.quickstart.firstRequest'),
        code('cURL', 'bash', firstRequest.curl)
      ]
    },
    {
      id: 'available-models',
      titleKey: 'apiDocs.guideSectionTitles.quickstart.availableModels',
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
      titleKey: 'apiDocs.guideSectionTitles.authentication.bearer',
      blocks: [
        paragraph('apiDocs.guides.authentication.intro'),
        paragraph('apiDocs.guides.authentication.bearer'),
        code('Authorization', 'http', `Authorization: Bearer ${DOCS_API_KEY_PLACEHOLDER}`)
      ]
    },
    {
      id: 'x-api-key',
      titleKey: 'apiDocs.guideSectionTitles.authentication.xApiKey',
      blocks: [
        paragraph('apiDocs.guides.authentication.xApiKey'),
        code('x-api-key', 'http', `x-api-key: ${DOCS_API_KEY_PLACEHOLDER}`)
      ]
    },
    {
      id: 'key-safety',
      titleKey: 'apiDocs.guideSectionTitles.authentication.keySafety',
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
      titleKey: 'apiDocs.guideSectionTitles.authentication.deprecatedQuery',
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
  return {
    id,
    titleKey,
    blocks: [
      paragraph('apiDocs.guides.clientIntegration.installation'),
      paragraph('apiDocs.guides.clientIntegration.configuration'),
      ...buildClientVariantBlocks(client, 'macos', baseUrl),
      ...buildClientVariantBlocks(client, 'windows', baseUrl)
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
      titleKey: 'apiDocs.guideSectionTitles.clientIntegration.pythonSdk',
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
      titleKey: 'apiDocs.guideSectionTitles.capabilities.streaming',
      blocks: [
        paragraph('apiDocs.guides.capabilities.intro'),
        paragraph('apiDocs.guides.capabilities.streaming'),
        code('JSON', 'json', '{"stream":true}')
      ]
    },
    {
      id: 'tools',
      titleKey: 'apiDocs.guideSectionTitles.capabilities.tools',
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
      titleKey: 'apiDocs.guideSectionTitles.capabilities.structuredOutput',
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
      titleKey: 'apiDocs.guideSectionTitles.capabilities.reasoning',
      blocks: [
        paragraph('apiDocs.guides.capabilities.reasoning'),
        code('JSON', 'json', '{"reasoning":{"effort":"high"}}')
      ]
    },
    {
      id: 'prompt-cache',
      titleKey: 'apiDocs.guideSectionTitles.capabilities.promptCache',
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
      titleKey: 'apiDocs.guideSectionTitles.errors.gatewayEnvelope',
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
      titleKey: 'apiDocs.guideSectionTitles.errors.gatewayCodes',
      blocks: [
        {
          kind: 'table',
          columns: [
            raw('HTTP'),
            localized('apiDocs.tables.code'),
            localized('apiDocs.tables.recommendedAction')
          ],
          rows: gatewayCodeRows.map(([status, errorCode]) => [
            raw(status),
            raw(errorCode),
            localized(`apiDocs.errors.actions.${errorCode}`)
          ])
        }
      ]
    },
    {
      id: 'anthropic-envelope',
      titleKey: 'apiDocs.guideSectionTitles.errors.anthropicEnvelope',
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
      titleKey: 'apiDocs.guideSectionTitles.errors.openaiEnvelope',
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
      titleKey: 'apiDocs.guideSectionTitles.errors.streamErrors',
      blocks: [
        paragraph('apiDocs.guides.errors.streamFailures'),
        code(
          'HTTP 200 stream errors',
          'text',
          [
            'HTTP 200 (stream started)',
            '',
            'OpenAI Responses (all gateway handler paths)',
            'event: response.failed',
            `data: {"type":"response.failed","response":{"id":"resp_request_id","object":"response","model":"${EXAMPLE_MODELS.openai}","status":"failed","output":[],"error":{"code":"upstream_error","message":"Stream failed"}}}`,
            '',
            'Anthropic-backed GatewayHandler (for example, Messages)',
            'data: {"type":"error","error":{"type":"api_error","message":"Stream failed"}}',
            '',
            'OpenAI-backed OpenAIGatewayHandler (for example, Chat Completions)',
            'event: error',
            'data: {"error":{"type":"upstream_error","message":"Stream failed"}}'
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
      titleKey: 'apiDocs.guideSectionTitles.requestId.headers',
      blocks: [
        paragraph('apiDocs.guides.requestId.intro'),
        paragraph('apiDocs.guides.requestId.headers'),
        code('HTTP', 'http', ['X-Request-ID: ...', 'X-Client-Request-ID: ...'].join('\n'))
      ]
    },
    {
      id: 'support-checklist',
      titleKey: 'apiDocs.guideSectionTitles.requestId.supportChecklist',
      blocks: [paragraph('apiDocs.guides.requestId.supportChecklist')]
    },
    {
      id: 'redaction',
      titleKey: 'apiDocs.guideSectionTitles.requestId.redaction',
      blocks: [callout('warning', 'apiDocs.guides.requestId.redaction')]
    }
  ]
}

function buildKeySecurity(): ApiDocsGuideSection[] {
  return [
    {
      id: 'expiration',
      titleKey: 'apiDocs.guideSectionTitles.keySecurity.expiration',
      blocks: [
        paragraph('apiDocs.guides.keySecurity.intro'),
        paragraph('apiDocs.guides.keySecurity.expiration')
      ]
    },
    {
      id: 'quota',
      titleKey: 'apiDocs.guideSectionTitles.keySecurity.quota',
      blocks: [paragraph('apiDocs.guides.keySecurity.quota')]
    },
    {
      id: 'rate-windows',
      titleKey: 'apiDocs.guideSectionTitles.keySecurity.rateWindows',
      blocks: [
        paragraph('apiDocs.guides.keySecurity.rateWindows'),
        {
          kind: 'table',
          columns: [localized('apiDocs.tables.window')],
          rows: [[raw('5h')], [raw('1d')], [raw('7d')]]
        }
      ]
    },
    {
      id: 'ip-rules',
      titleKey: 'apiDocs.guideSectionTitles.keySecurity.ipRules',
      blocks: [
        paragraph('apiDocs.guides.keySecurity.ipRules'),
        {
          kind: 'table',
          columns: [
            localized('apiDocs.tables.rule'),
            localized('apiDocs.tables.matchingIpCidr')
          ],
          rows: [
            [
              localized('apiDocs.tables.whitelist'),
              localized('apiDocs.tables.allowed')
            ],
            [
              localized('apiDocs.tables.blacklist'),
              localized('apiDocs.tables.denied')
            ]
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
