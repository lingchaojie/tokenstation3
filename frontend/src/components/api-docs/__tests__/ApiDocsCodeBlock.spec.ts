import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { copyToClipboardMock } = vi.hoisted(() => ({
  copyToClipboardMock: vi.fn()
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: copyToClipboardMock })
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

import ApiDocsCodeBlock from '../ApiDocsCodeBlock.vue'

describe('ApiDocsCodeBlock', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    copyToClipboardMock.mockReset()
    copyToClipboardMock.mockResolvedValue(true)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('copies the unchanged example and announces success', async () => {
    const wrapper = mount(ApiDocsCodeBlock, {
      props: { label: 'curl', language: 'bash', code: 'curl https://example.test' }
    })
    await wrapper.get('[data-testid="api-docs-copy"]').trigger('click')
    expect(copyToClipboardMock).toHaveBeenCalledWith(
      'curl https://example.test',
      'apiDocs.copied'
    )
    expect(wrapper.get('[role="status"]').text()).toContain('apiDocs.copied')
  })

  it('renders code as escaped text and confines horizontal scrolling to the pre', () => {
    const code = '<img src=x onerror=alert(1)>'
    const wrapper = mount(ApiDocsCodeBlock, {
      props: { label: 'HTML-looking text', language: 'text', code }
    })

    expect(wrapper.get('code').text()).toBe(code)
    expect(wrapper.get('code').classes()).toContain('language-text')
    expect(wrapper.html()).toContain('&lt;img')
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.get('pre').classes()).toContain('overflow-x-auto')
    expect(wrapper.get('[data-testid="api-docs-code-block"]').classes()).not.toContain(
      'overflow-x-auto'
    )
    expect(wrapper.findAll('.overflow-x-auto')).toHaveLength(1)
  })

  it('keeps copied feedback for two seconds', async () => {
    const wrapper = mount(ApiDocsCodeBlock, {
      props: { label: 'cURL', language: 'bash', code: 'curl https://example.test' }
    })

    await wrapper.get('[data-testid="api-docs-copy"]').trigger('click')
    expect(wrapper.get('[role="status"]').text()).toBe('apiDocs.copied')

    vi.advanceTimersByTime(1999)
    expect(wrapper.get('[role="status"]').text()).toBe('apiDocs.copied')

    vi.advanceTimersByTime(1)
    await wrapper.vm.$nextTick()
    expect(wrapper.get('[role="status"]').text()).toBe('')
    wrapper.unmount()
  })

  it('clears the outstanding copied-feedback timer specifically on unmount', async () => {
    const clearTimeoutSpy = vi.spyOn(window, 'clearTimeout')
    const wrapper = mount(ApiDocsCodeBlock, {
      props: { label: 'cURL', language: 'bash', code: 'curl https://example.test' }
    })

    await wrapper.get('[data-testid="api-docs-copy"]').trigger('click')
    expect(vi.getTimerCount()).toBe(1)
    expect(clearTimeoutSpy).not.toHaveBeenCalled()

    wrapper.unmount()
    expect(clearTimeoutSpy).toHaveBeenCalledTimes(1)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('keeps feedback quiet when copying fails and exposes only a copy action', async () => {
    copyToClipboardMock.mockResolvedValueOnce(false)
    const wrapper = mount(ApiDocsCodeBlock, {
      props: { label: 'cURL', language: 'bash', code: 'curl https://example.test' }
    })

    const copyButton = wrapper.get('[data-testid="api-docs-copy"]')
    expect(copyButton.attributes('type')).toBe('button')
    expect(copyButton.attributes('aria-label')).toBe('apiDocs.copy')
    expect(wrapper.get('[role="status"]').attributes('aria-live')).toBe('polite')
    expect(wrapper.findAll('button')).toHaveLength(1)

    await copyButton.trigger('click')
    expect(wrapper.get('[role="status"]').text()).toBe('')
    expect(wrapper.text().toLowerCase()).not.toContain('run')
    expect(wrapper.text().toLowerCase()).not.toContain('send')
  })
})
