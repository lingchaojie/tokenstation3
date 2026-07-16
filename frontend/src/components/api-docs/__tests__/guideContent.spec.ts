import { readFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

import { GUIDE_VARIANTS } from '@/components/getting-started/curriculum'
import { buildPythonSdkExample } from '@/components/keys/clientExampleFiles'
import {
  buildClientConfigFiles,
  DOCS_API_KEY_PLACEHOLDER,
  EXAMPLE_MODELS,
  resolveGatewayEndpoints
} from '@/components/keys/clientConfigFiles'
import enApiDocs from '@/i18n/locales/en/apiDocs'
import enGettingStarted from '@/i18n/locales/en/gettingStarted'
import zhApiDocs from '@/i18n/locales/zh/apiDocs'
import zhGettingStarted from '@/i18n/locales/zh/gettingStarted'
import { buildEndpointExamples } from '../examples'
import { buildGuidePage } from '../guideContent'
import type { ApiDocsBlock, ApiDocsPageId } from '../types'

const BASE_URL = 'https://gateway.example.com/v1/'

function sectionIds(pageId: ApiDocsPageId): string[] {
  return buildGuidePage(pageId, BASE_URL).sections.map(({ id }) => id)
}

function codeBlocks(pageId: ApiDocsPageId): Extract<ApiDocsBlock, { kind: 'code' }>[] {
  return buildGuidePage(pageId, BASE_URL).sections.flatMap(({ blocks }) =>
    blocks.filter((block): block is Extract<ApiDocsBlock, { kind: 'code' }> =>
      block.kind === 'code'
    )
  )
}

function hasLocaleKey(locale: object, key: string): boolean {
  let value: unknown = locale
  for (const segment of key.split('.')) {
    if (typeof value !== 'object' || value === null || !(segment in value)) return false
    value = (value as Record<string, unknown>)[segment]
  }
  return typeof value === 'string'
}

function localeText(locale: object, key: string): string {
  let value: unknown = locale
  for (const segment of key.split('.')) {
    if (typeof value !== 'object' || value === null || !(segment in value)) return key
    value = (value as Record<string, unknown>)[segment]
  }
  return typeof value === 'string' ? value : key
}

describe('buildGuidePage', () => {
  it('builds every approved guide and platform page with stable section IDs', () => {
    expect(sectionIds('quickstart')).toEqual([
      'base-url',
      'api-key',
      'first-request',
      'available-models'
    ])
    expect(sectionIds('authentication')).toEqual([
      'bearer',
      'x-api-key',
      'key-safety',
      'deprecated-query'
    ])
    expect(sectionIds('client-integration')).toEqual([
      'claude-code',
      'codex-cli',
      'opencode',
      'cc-switch',
      'python-sdk'
    ])
    expect(sectionIds('capabilities')).toEqual([
      'streaming',
      'tools',
      'structured-output',
      'reasoning',
      'prompt-cache'
    ])
    expect(sectionIds('errors')).toEqual([
      'gateway-envelope',
      'gateway-codes',
      'anthropic-envelope',
      'openai-envelope',
      'stream-errors'
    ])
    expect(sectionIds('request-id')).toEqual(['headers', 'support-checklist', 'redaction'])
    expect(sectionIds('key-security')).toEqual([
      'expiration',
      'quota',
      'rate-windows',
      'ip-rules'
    ])
  })

  it('keeps approved content free of excluded APIs and operational claims', () => {
    const pageIds: ApiDocsPageId[] = [
      'quickstart',
      'authentication',
      'client-integration',
      'capabilities',
      'errors',
      'request-id',
      'key-security'
    ]
    const content = JSON.stringify(pageIds.map((pageId) => buildGuidePage(pageId, BASE_URL)))

    expect(content).not.toMatch(
      /gemini|embedding|video|failover|scheduler|stability|\/v1\/usage|\/v1\/balance/i
    )
  })

  it('reuses endpoint examples for the quickstart request and model lookup', () => {
    const codes = codeBlocks('quickstart').map(({ code }) => code)

    expect(codes).toContain(buildEndpointExamples('responses', BASE_URL).curl)
    expect(codes).toContain(buildEndpointExamples('models', BASE_URL).curl)
    expect(JSON.stringify(buildGuidePage('quickstart', BASE_URL))).toContain(
      DOCS_API_KEY_PLACEHOLDER
    )
  })

  it('covers both authentication headers and request-correlation safety guidance', () => {
    const authentication = JSON.stringify(buildGuidePage('authentication', BASE_URL))
    const requestId = JSON.stringify(buildGuidePage('request-id', BASE_URL))

    expect(authentication).toContain(`Authorization: Bearer ${DOCS_API_KEY_PLACEHOLDER}`)
    expect(authentication).toContain(`x-api-key: ${DOCS_API_KEY_PLACEHOLDER}`)
    expect(authentication).toContain('apiDocs.guides.authentication.safety')
    expect(authentication).toContain('apiDocs.guides.authentication.deprecatedQuery')
    expect(requestId).toContain('X-Request-ID')
    expect(requestId).toContain('X-Client-Request-ID')
    expect(requestId).toContain('apiDocs.guides.requestId.supportChecklist')
    expect(requestId).toContain('apiDocs.guides.requestId.redaction')
  })

  it('reuses macOS curriculum metadata and shared client configuration builders', () => {
    const page = buildGuidePage('client-integration', BASE_URL)
    const serialized = JSON.stringify(page)
    const sectionByClient = {
      claude_code: 'claude-code',
      codex: 'codex-cli',
      opencode: 'opencode',
      cc_switch: 'cc-switch'
    } as const

    for (const [client, sectionId] of Object.entries(sectionByClient)) {
      const variant = GUIDE_VARIANTS.find((candidate) =>
        candidate.client === client && candidate.os === 'macos'
      )!
      const section = page.sections.find(({ id }) => id === sectionId)!
      const sectionText = JSON.stringify(section)
      const sectionCodeBlocks = section.blocks.filter(
        (block): block is Extract<ApiDocsBlock, { kind: 'code' }> => block.kind === 'code'
      )
      const expectedFiles = buildClientConfigFiles({
        client: client as keyof typeof sectionByClient,
        os: 'macos',
        platform: 'unified',
        apiKey: DOCS_API_KEY_PLACEHOLDER,
        baseUrl: BASE_URL
      })

      if (variant.installCommand) expect(sectionText).toContain(variant.installCommand)
      expect(sectionText).toContain(variant.officialSourceUrl)
      for (const file of expectedFiles) {
        expect(sectionCodeBlocks).toContainEqual(
          expect.objectContaining({ label: file.path, code: file.content })
        )
      }
    }

    expect(serialized).toContain('$LINX2_API_KEY')
    expect(serialized).toContain('ANTHROPIC_BASE_URL')
    expect(serialized).toContain('wire_api = \\"responses\\"')
    expect(serialized).toContain('opencode.json')
    expect(serialized).toContain('CC Switch')
    expect(serialized).toContain('apiDocs.guides.clientIntegration.windowsNote')
  })

  it('renders Windows curriculum actions and shared unified configurations alongside macOS', () => {
    const page = buildGuidePage('client-integration', BASE_URL)
    const sectionByClient = {
      claude_code: 'claude-code',
      codex: 'codex-cli',
      opencode: 'opencode',
      cc_switch: 'cc-switch'
    } as const

    for (const [client, sectionId] of Object.entries(sectionByClient)) {
      const variant = GUIDE_VARIANTS.find((candidate) =>
        candidate.client === client && candidate.os === 'windows'
      )!
      const section = page.sections.find(({ id }) => id === sectionId)!
      const sectionCodeBlocks = section.blocks.filter(
        (block): block is Extract<ApiDocsBlock, { kind: 'code' }> => block.kind === 'code'
      )
      const sectionLinks = section.blocks.flatMap((block) =>
        block.kind === 'links' ? block.links : []
      )
      const expectedFiles = buildClientConfigFiles({
        client: client as keyof typeof sectionByClient,
        os: 'windows',
        platform: 'unified',
        apiKey: DOCS_API_KEY_PLACEHOLDER,
        baseUrl: BASE_URL
      })

      expect(JSON.stringify(section)).toContain('apiDocs.guides.clientIntegration.windowsNote')
      if (variant.installCommand) {
        expect(sectionCodeBlocks).toContainEqual(
          expect.objectContaining({ label: 'Windows', code: variant.installCommand })
        )
      }
      expect(sectionLinks.map(({ to }) => to)).toContain(variant.officialSourceUrl)
      if (variant.desktopDownloadUrl) {
        expect(sectionLinks.map(({ to }) => to)).toContain(variant.desktopDownloadUrl)
      }
      for (const file of expectedFiles) {
        expect(sectionCodeBlocks).toContainEqual(
          expect.objectContaining({ label: file.path, code: file.content })
        )
      }
    }
  })

  it('appends all three shared Python SDK examples', () => {
    const codes = codeBlocks('client-integration').map(({ code }) => code)
    const endpoints = resolveGatewayEndpoints(BASE_URL)

    for (const kind of ['anthropic', 'openai', 'image'] as const) {
      const file = buildPythonSdkExample({
        kind,
        endpoints,
        apiKey: DOCS_API_KEY_PLACEHOLDER
      })
      expect(codes).toContain(file.content)
    }
  })

  it('preserves approved gateway code/status pairs and exact protocol envelopes', () => {
    const page = buildGuidePage('errors', BASE_URL)
    const gatewayCodes = page.sections
      .find(({ id }) => id === 'gateway-codes')!
      .blocks.find((block): block is Extract<ApiDocsBlock, { kind: 'table' }> =>
        block.kind === 'table'
      )!
    const codes = codeBlocks('errors').map(({ code }) => code)

    expect(gatewayCodes.rows).toEqual([
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
    ].map(([status, errorCode]) => [
      { kind: 'raw', value: status },
      { kind: 'raw', value: errorCode },
      { kind: 'localized', textKey: `apiDocs.errors.actions.${errorCode}` }
    ]))
    expect(gatewayCodes.columns).toEqual([
      { kind: 'raw', value: 'HTTP' },
      { kind: 'localized', textKey: 'apiDocs.tables.code' },
      { kind: 'localized', textKey: 'apiDocs.tables.recommendedAction' }
    ])
    expect(codes).toContain('{"code":"INVALID_API_KEY","message":"Invalid API key"}')
    expect(codes).toContain(
      '{"type":"error","error":{"type":"invalid_request_error","message":"model is required"}}'
    )
    expect(codes).toContain(
      '{"error":{"type":"invalid_request_error","message":"model is required"}}'
    )
  })

  it('matches the live Responses and handler-specific streaming error contracts', async () => {
    const streamSection = buildGuidePage('errors', BASE_URL).sections.find(
      ({ id }) => id === 'stream-errors'
    )!
    const streamBlock = streamSection.blocks.find(
      (block): block is Extract<ApiDocsBlock, { kind: 'code' }> => block.kind === 'code'
    )!
    const handlerRoot = resolve(process.cwd(), '../backend/internal/handler')
    const [responsesSource, gatewaySource, openAiGatewaySource] = await Promise.all([
      readFile(resolve(handlerRoot, 'stream_error_event.go'), 'utf8'),
      readFile(resolve(handlerRoot, 'gateway_handler.go'), 'utf8'),
      readFile(resolve(handlerRoot, 'openai_gateway_handler.go'), 'utf8')
    ])

    expect(responsesSource).toContain('type responsesFailedEvent struct')
    expect(responsesSource).toContain('Response responsesFailedBody `json:"response"`')
    expect(responsesSource).toContain('ID     string               `json:"id"`')
    expect(responsesSource).toContain('Object string               `json:"object"`')
    expect(responsesSource).toContain('Model  string               `json:"model,omitempty"`')
    expect(responsesSource).toContain('Status string               `json:"status"`')
    expect(responsesSource).toContain('Output []any                `json:"output"`')
    expect(responsesSource).toContain('Error  responsesFailedError `json:"error"`')
    expect(responsesSource).toContain('Code    string `json:"code"`')
    expect(responsesSource).toContain('Message string `json:"message"`')
    expect(responsesSource).toContain(
      'fmt.Fprintf(c.Writer, "event: response.failed\\ndata: %s\\n\\n", payload)'
    )
    expect(gatewaySource).toContain(
      '`data: {"type":"error","error":{"type":` + strconv.Quote(errType)'
    )
    expect(openAiGatewaySource).toContain(
      '"event: error\\ndata: " + `{"error":{"type":` + strconv.Quote(errType)'
    )

    expect(streamBlock.code).toBe([
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
    ].join('\n'))

    const dataObjects = streamBlock.code
      .split('\n')
      .filter((line) => line.startsWith('data: '))
      .map((line) => JSON.parse(line.slice('data: '.length)))
    expect(dataObjects).toEqual([
      {
        type: 'response.failed',
        response: {
          id: 'resp_request_id',
          object: 'response',
          model: EXAMPLE_MODELS.openai,
          status: 'failed',
          output: [],
          error: { code: 'upstream_error', message: 'Stream failed' }
        }
      },
      {
        type: 'error',
        error: { type: 'api_error', message: 'Stream failed' }
      },
      {
        error: { type: 'upstream_error', message: 'Stream failed' }
      }
    ])
  })

  it('documents cache TTLs, key rate windows, and both IP/CIDR rule modes', () => {
    const capabilitiesPage = buildGuidePage('capabilities', BASE_URL)
    const capabilities = JSON.stringify(capabilitiesPage)
    const promptCache = JSON.stringify(
      capabilitiesPage.sections.find(({ id }) => id === 'prompt-cache')
    )
    const security = JSON.stringify(buildGuidePage('key-security', BASE_URL))

    expect(promptCache).toContain('Anthropic')
    expect(capabilities).toContain('cache_control')
    expect(capabilities).toContain('5m')
    expect(capabilities).toContain('1h')
    expect(security).toContain('5h')
    expect(security).toContain('1d')
    expect(security).toContain('7d')
    expect(security).toContain('apiDocs.tables.matchingIpCidr')
    expect(security).toContain('apiDocs.tables.whitelist')
    expect(security).toContain('apiDocs.tables.blacklist')
  })

  it('rejects endpoint pages that are not guide or platform content', () => {
    expect(() => buildGuidePage('responses', BASE_URL)).toThrow(/responses/)
  })

  it('references existing Chinese and English locale leaves for every localized block', () => {
    const localePairs = [
      { ...enApiDocs, ...enGettingStarted },
      { ...zhApiDocs, ...zhGettingStarted }
    ]
    const pageIds: ApiDocsPageId[] = [
      'quickstart',
      'authentication',
      'client-integration',
      'capabilities',
      'errors',
      'request-id',
      'key-security'
    ]
    const localizedKeys = pageIds.flatMap((pageId) =>
      buildGuidePage(pageId, BASE_URL).sections.flatMap((section) => [
        section.titleKey,
        ...section.blocks.flatMap((block) => {
          if (block.kind === 'paragraph' || block.kind === 'callout') return [block.textKey]
          if (block.kind === 'links') return block.links.map(({ labelKey }) => labelKey)
          return []
        })
      ])
    )

    for (const locale of localePairs) {
      expect(localizedKeys.filter((key) => !hasLocaleKey(locale, key))).toEqual([])
    }
  })

  it('uses dedicated concise unique bilingual titles for every guide section', () => {
    const pageIds: ApiDocsPageId[] = [
      'quickstart',
      'authentication',
      'client-integration',
      'capabilities',
      'errors',
      'request-id',
      'key-security'
    ]
    const sections = pageIds.flatMap((pageId) => buildGuidePage(pageId, BASE_URL).sections)
    const titleKeys = sections.map(({ titleKey }) => titleKey)
    const enLocale = { ...enApiDocs, ...enGettingStarted }
    const zhLocale = { ...zhApiDocs, ...zhGettingStarted }
    const enTitles = titleKeys.map((key) => localeText(enLocale, key))
    const zhTitles = titleKeys.map((key) => localeText(zhLocale, key))

    expect(titleKeys.every((key) => key.startsWith('apiDocs.guideSectionTitles.'))).toBe(true)
    expect(new Set(titleKeys).size).toBe(sections.length)
    expect(new Set(enTitles).size).toBe(sections.length)
    expect(new Set(zhTitles).size).toBe(sections.length)
    expect(enTitles.every((title) => title.length <= 32)).toBe(true)
    expect(zhTitles.every((title) => title.length <= 20)).toBe(true)
    expect(enTitles).toContain('Anthropic error envelope')
    expect(enTitles).toContain('OpenAI error envelope')

    for (const section of sections) {
      const bodyKeys = section.blocks.flatMap((block) => {
        if (block.kind === 'paragraph' || block.kind === 'callout') return [block.textKey]
        if (block.kind === 'links') return block.links.map(({ labelKey }) => labelKey)
        return []
      })
      expect(bodyKeys).not.toContain(section.titleKey)
    }
  })
})
