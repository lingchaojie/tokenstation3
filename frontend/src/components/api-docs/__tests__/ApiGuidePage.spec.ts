import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { translateMock } = vi.hoisted(() => ({
  translateMock: vi.fn<(key: string) => string>()
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: vi.fn().mockResolvedValue(true) })
}))

vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: translateMock })
}))

import zhApiDocs from '@/i18n/locales/zh/apiDocs'
import ApiGuidePage from '../ApiGuidePage.vue'
import { buildGuidePage } from '../guideContent'
import type { ApiDocsGuideDefinition } from '../types'

const definition: ApiDocsGuideDefinition = {
  pageId: 'quickstart',
  sections: [
    {
      id: 'first-section',
      titleKey: 'guide.first.title',
      blocks: [
        { kind: 'paragraph', textKey: 'guide.first.paragraph' },
        { kind: 'callout', tone: 'info', textKey: 'guide.first.info' },
        { kind: 'callout', tone: 'warning', textKey: 'guide.first.warning' },
        { kind: 'code', label: 'Example file', language: 'json', code: '{"ok":true}' },
        {
          kind: 'table',
          columns: [
            { kind: 'raw', value: 'HTTP' },
            { kind: 'raw', value: 'Code' }
          ],
          rows: [[
            { kind: 'raw', value: '401' },
            { kind: 'raw', value: 'INVALID_API_KEY' }
          ]]
        },
        {
          kind: 'links',
          links: [
            { labelKey: 'guide.first.internalLink', to: '/keys' },
            { labelKey: 'guide.first.externalLink', to: 'https://example.com/docs' }
          ]
        }
      ]
    },
    {
      id: 'second-section',
      titleKey: 'guide.second.title',
      blocks: [{ kind: 'paragraph', textKey: 'guide.second.paragraph' }]
    }
  ]
}

function mountDefinition(definition: ApiDocsGuideDefinition) {
  return mount(ApiGuidePage, {
    props: { definition },
    global: {
      stubs: {
        RouterLink: {
          props: ['to'],
          template: '<a :href="to"><slot /></a>'
        }
      }
    }
  })
}

function mountGuide() {
  return mountDefinition(definition)
}

function localeValue(locale: object, key: string): string {
  let value: unknown = locale
  for (const segment of key.split('.')) {
    if (typeof value !== 'object' || value === null || !(segment in value)) return key
    value = (value as Record<string, unknown>)[segment]
  }
  return typeof value === 'string' ? value : key
}

describe('ApiGuidePage', () => {
  beforeEach(() => {
    translateMock.mockReset()
    translateMock.mockImplementation((key) => `localized:${key}`)
  })

  it('renders stable localized headings in article order and exposes them', () => {
    const wrapper = mountGuide()

    expect(wrapper.findAll('h2[id]').map((heading) => heading.attributes('id'))).toEqual([
      'first-section',
      'second-section'
    ])
    expect(wrapper.findAll('h2').map((heading) => heading.text())).toEqual([
      'localized:guide.first.title',
      'localized:guide.second.title'
    ])
    expect(
      (wrapper.vm as unknown as { headings: Array<{ id: string; label: string }> }).headings
    ).toEqual([
      { id: 'first-section', label: 'localized:guide.first.title' },
      { id: 'second-section', label: 'localized:guide.second.title' }
    ])
  })

  it('renders localized paragraphs and both semantic callout tones', () => {
    const wrapper = mountGuide()
    const callouts = wrapper.findAll('[data-testid="guide-callout"]')

    expect(wrapper.findAll('[data-testid="guide-paragraph"]').map((block) => block.text())).toEqual([
      'localized:guide.first.paragraph',
      'localized:guide.second.paragraph'
    ])
    expect(callouts.map((callout) => callout.text())).toEqual([
      'localized:guide.first.info',
      'localized:guide.first.warning'
    ])
    expect(callouts.every((callout) => callout.attributes('role') === 'note')).toBe(true)
    expect(callouts[0].attributes('data-tone')).toBe('info')
    expect(callouts[1].attributes('data-tone')).toBe('warning')
  })

  it('delegates unchanged code to ApiDocsCodeBlock and renders a semantic table', () => {
    const wrapper = mountGuide()
    const codeBlock = wrapper.getComponent({ name: 'ApiDocsCodeBlock' })
    const table = wrapper.get('[data-testid="guide-table"]')

    expect(codeBlock.props()).toMatchObject({
      label: 'Example file',
      language: 'json',
      code: '{"ok":true}'
    })
    expect(table.findAll('thead th').map((header) => header.text())).toEqual(['HTTP', 'Code'])
    expect(table.findAll('thead th').every((header) => header.attributes('scope') === 'col')).toBe(
      true
    )
    expect(table.findAll('tbody td').map((cell) => cell.text())).toEqual([
      '401',
      'INVALID_API_KEY'
    ])
  })

  it('localizes link labels and protects external official-source links', () => {
    const wrapper = mountGuide()
    const links = wrapper.findAll('[data-testid="guide-link"]')

    expect(links.map((link) => link.text())).toEqual([
      'localized:guide.first.internalLink',
      'localized:guide.first.externalLink'
    ])
    expect(links[0].attributes('href')).toBe('/keys')
    expect(links[0].attributes('target')).toBeUndefined()
    expect(links[1].attributes('href')).toBe('https://example.com/docs')
    expect(links[1].attributes('target')).toBe('_blank')
    expect(links[1].attributes('rel')).toBe('noopener noreferrer')
  })

  it('localizes Chinese table columns, actions, windows, rules, and values', () => {
    translateMock.mockImplementation((key) => localeValue(zhApiDocs, key))
    const errors = mountDefinition(buildGuidePage('errors', 'https://gateway.example.com/v1/'))
    const security = mountDefinition(
      buildGuidePage('key-security', 'https://gateway.example.com/v1/')
    )
    const errorTable = errors.get('[data-testid="guide-table"]')
    const securityTables = security.findAll('[data-testid="guide-table"]')

    expect(errorTable.findAll('thead th').map((header) => header.text())).toEqual([
      'HTTP',
      '代码',
      '建议操作'
    ])
    expect(errorTable.findAll('tbody tr')).toHaveLength(17)
    expect(errorTable.text()).toContain('INVALID_API_KEY')
    expect(errorTable.text()).toContain('检查密钥或重新创建密钥。')
    expect(errorTable.text()).not.toContain('Recommended action')
    expect(errorTable.text()).not.toContain('Code')

    expect(securityTables[0].findAll('thead th').map((header) => header.text())).toEqual([
      '周期'
    ])
    expect(securityTables[0].text()).toContain('5h')
    expect(securityTables[0].text()).toContain('1d')
    expect(securityTables[0].text()).toContain('7d')
    expect(securityTables[1].findAll('thead th').map((header) => header.text())).toEqual([
      '规则',
      '匹配的 IP/CIDR'
    ])
    expect(securityTables[1].text()).toContain('白名单')
    expect(securityTables[1].text()).toContain('仅允许匹配项')
    expect(securityTables[1].text()).toContain('黑名单')
    expect(securityTables[1].text()).toContain('拒绝匹配项')
    expect(securityTables[1].text()).not.toMatch(/Rule|Matching|Whitelist|Allowed|Blacklist|Denied/)
  })
})
