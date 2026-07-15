import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: vi.fn().mockResolvedValue(true) })
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

import ApiEndpointPage from '../ApiEndpointPage.vue'
import { API_ENDPOINTS } from '../catalog'
import { buildEndpointExamples } from '../examples'

function endpoint(id: (typeof API_ENDPOINTS)[number]['id']) {
  return API_ENDPOINTS.find((candidate) => candidate.id === id)!
}

function mountEndpoint(id: (typeof API_ENDPOINTS)[number]['id']) {
  return mount(ApiEndpointPage, {
    props: {
      endpoint: endpoint(id),
      examples: buildEndpointExamples(id, 'https://gateway.example.com')
    }
  })
}

describe('ApiEndpointPage', () => {
  it('renders the endpoint method, path, parameters, examples, and errors', () => {
    const wrapper = mount(ApiEndpointPage, {
      props: {
        endpoint: API_ENDPOINTS.find(({ id }) => id === 'responses')!,
        examples: buildEndpointExamples('responses', 'https://gateway.example.com')
      }
    })
    expect(wrapper.get('[data-testid="endpoint-method"]').text()).toBe('POST')
    expect(wrapper.get('[data-testid="endpoint-path"]').text()).toBe('/v1/responses')
    expect(wrapper.findAll('[data-testid="endpoint-parameter"]').length).toBeGreaterThan(0)
    expect(wrapper.text()).toContain('invalid_request_error')
    expect(wrapper.findAll('[data-testid="api-docs-code-block"]')).toHaveLength(4)
  })

  it('renders stable streaming headings in article order and exposes them', () => {
    const wrapper = mountEndpoint('responses')
    const ids = [
      'overview',
      'authentication',
      'parameters',
      'request',
      'response',
      'streaming',
      'errors'
    ]

    expect(wrapper.findAll('h2[id]').map((heading) => heading.attributes('id'))).toEqual(ids)
    expect(
      (wrapper.vm as unknown as { headings: Array<{ id: string }> }).headings.map(({ id }) => id)
    ).toEqual(ids)
  })

  it('omits the streaming heading and example for non-streaming endpoints', () => {
    const wrapper = mountEndpoint('models')

    expect(wrapper.find('#streaming').exists()).toBe(false)
    expect(
      (wrapper.vm as unknown as { headings: Array<{ id: string }> }).headings.map(({ id }) => id)
    ).not.toContain('streaming')
    expect(wrapper.findAll('[data-testid="api-docs-code-block"]')).toHaveLength(3)
  })

  it('uses a semantic parameter table and the public placeholder authentication header', () => {
    const wrapper = mountEndpoint('responses')

    expect(wrapper.get('[aria-labelledby="authentication"] code').text()).toBe(
      'Authorization: Bearer $LINX2_API_KEY'
    )
    expect(wrapper.get('table').exists()).toBe(true)
    expect(wrapper.findAll('thead th').every((header) => header.attributes('scope') === 'col')).toBe(
      true
    )
    expect(wrapper.get('[data-testid="endpoint-parameter"]').findAll('th')).toHaveLength(1)
    expect(wrapper.get('[data-testid="endpoint-parameter"]').get('th').attributes('scope')).toBe(
      'row'
    )
  })

  it('warns on every OpenAI protocol page without adding excluded protocols', () => {
    for (const id of [
      'responses',
      'chat-completions',
      'image-generations',
      'image-edits'
    ] as const) {
      expect(mountEndpoint(id).get('[data-testid="gateway-envelope-warning"]').text()).toBe(
        'apiDocs.errors.gatewayEnvelopeWarning'
      )
    }

    expect(mountEndpoint('messages').find('[data-testid="gateway-envelope-warning"]').exists()).toBe(
      false
    )
    expect(mountEndpoint('models').find('[data-testid="gateway-envelope-warning"]').exists()).toBe(
      false
    )

    const rendered = mountEndpoint('responses').text().toLowerCase()
    expect(rendered).not.toContain('gemini')
    expect(rendered).not.toContain('/v1beta')
    expect(rendered).not.toContain('embedding')
  })

  it('renders request/response block languages, error chips, and responsive article bounds', () => {
    const wrapper = mountEndpoint('responses')
    const blocks = wrapper.findAllComponents({ name: 'ApiDocsCodeBlock' })

    expect(blocks.map((block) => block.props('language'))).toEqual([
      'bash',
      'python',
      'json',
      'text'
    ])
    expect(wrapper.findAll('[data-testid="endpoint-error"]')).toHaveLength(
      endpoint('responses').errorCodes.length
    )
    expect(wrapper.get('article').classes()).toContain('min-w-0')
    expect(wrapper.get('[data-testid="endpoint-path"]').classes()).toContain('break-all')
  })
})
