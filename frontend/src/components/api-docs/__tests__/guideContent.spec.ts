import { describe, expect, it } from 'vitest'

import { GUIDE_VARIANTS } from '@/components/getting-started/curriculum'
import { buildPythonSdkExample } from '@/components/keys/clientExampleFiles'
import {
  buildClientConfigFiles,
  DOCS_API_KEY_PLACEHOLDER,
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
    ])
    expect(codes).toContain('{"code":"INVALID_API_KEY","message":"Invalid API key"}')
    expect(codes).toContain(
      '{"type":"error","error":{"type":"invalid_request_error","message":"model is required"}}'
    )
    expect(codes).toContain(
      '{"error":{"type":"invalid_request_error","message":"model is required"}}'
    )
  })

  it('documents distinct terminal stream errors after HTTP 200 has started', () => {
    const streamSection = buildGuidePage('errors', BASE_URL).sections.find(
      ({ id }) => id === 'stream-errors'
    )!
    const serialized = JSON.stringify(streamSection)

    expect(serialized).toContain('HTTP 200')
    expect(serialized).toContain('event: error')
    expect(serialized).toContain('response.failed')
    expect(serialized).toContain('Chat Completions')
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
    expect(security).toContain('IP/CIDR')
    expect(security).toContain('Whitelist')
    expect(security).toContain('Blacklist')
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
})
