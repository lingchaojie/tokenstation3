import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import PlatformTypeBadge from '../PlatformTypeBadge.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

describe('PlatformTypeBadge Cursor', () => {
  it('renders the Cursor OAuth badge with its dedicated icon and color', () => {
    const wrapper = mount(PlatformTypeBadge, {
      props: { platform: 'cursor', type: 'oauth' },
    })

    expect(wrapper.text()).toContain('Cursor')
    expect(wrapper.text()).toContain('OAuth')
    expect(wrapper.find('[data-testid="cursor-platform-icon"]').exists()).toBe(true)
    expect(wrapper.html()).toContain('bg-cyan-100')
  })
})
