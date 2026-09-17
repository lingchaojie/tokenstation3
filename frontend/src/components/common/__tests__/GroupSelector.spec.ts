import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import GroupSelector from '../GroupSelector.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const groups = [
  { id: 1, name: 'Anthropic', platform: 'anthropic', status: 'active' },
  { id: 2, name: 'OpenAI', platform: 'openai', status: 'active' },
] as any

describe('GroupSelector', () => {
  it('renders all concrete groups', () => {
    const wrapper = mount(GroupSelector, {
      props: { modelValue: [], groups },
      global: {
        stubs: {
          GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' },
          Icon: true,
        },
      },
    })
    expect(wrapper.text()).toContain('Anthropic')
    expect(wrapper.text()).toContain('OpenAI')
  })
})
