import { describe, expect, it } from 'vitest'

import en from '@/i18n/locales/en/apiDocs'
import zh from '@/i18n/locales/zh/apiDocs'
import {
  API_DOCS_CAPABILITY_TAGS,
  API_DOCS_PAGES,
  API_ENDPOINTS,
  findApiDocsPage
} from '../catalog'
import { buildEndpointExamples } from '../examples'

function leafPaths(value: unknown, prefix = ''): string[] {
  if (typeof value !== 'object' || value === null) return [prefix]
  return Object.entries(value).flatMap(([key, child]) =>
    leafPaths(child, prefix ? `${prefix}.${key}` : key)
  )
}

describe('API docs catalog', () => {
  it('contains exactly the approved endpoint allowlist', () => {
    expect(API_ENDPOINTS.map(({ method, path }) => `${method} ${path}`)).toEqual([
      'POST /v1/messages',
      'POST /v1/messages/count_tokens',
      'POST /v1/responses',
      'POST /v1/chat/completions',
      'GET /v1/models',
      'POST /v1/images/generations',
      'POST /v1/images/edits'
    ])
  })

  it('keeps excluded capabilities out of routes, tags, keywords, and examples', () => {
    const searchable = JSON.stringify({ API_DOCS_CAPABILITY_TAGS, API_DOCS_PAGES, API_ENDPOINTS })
    expect(searchable).not.toMatch(/gemini|v1beta|embedding|video|alpha\/search|failover|scheduler|stability/i)
    expect(searchable).not.toContain('/v1/usage')
    expect(searchable).not.toContain('/v1/balance')
  })

  it('has unique IDs and routes and resolves the docs index to quickstart', () => {
    expect(new Set(API_DOCS_PAGES.map(({ id }) => id)).size).toBe(API_DOCS_PAGES.length)
    expect(new Set(API_DOCS_PAGES.map(({ path }) => path)).size).toBe(API_DOCS_PAGES.length)
    expect(findApiDocsPage('/docs')?.id).toBe('quickstart')
  })

  it('keeps Chinese and English locale leaves identical', () => {
    expect(leafPaths(zh).sort()).toEqual(leafPaths(en).sort())
  })

  it('uses shared placeholders and normalized endpoints in every example', () => {
    for (const endpoint of API_ENDPOINTS) {
      const examples = buildEndpointExamples(endpoint.id, 'https://gateway.example.com/v1/')
      expect(examples.curl).toContain('https://gateway.example.com/v1')
      expect(examples.curl).not.toContain('/v1/v1')
      expect(examples.curl).toContain('$LINX2_API_KEY')
      expect(examples.success).not.toContain('$LINX2_API_KEY')
    }
  })
})
