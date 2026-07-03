import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import AvailableChannelsTable from './AvailableChannelsTable.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => ({
      'availableChannels.exclusive': 'Exclusive',
      'availableChannels.public': 'Public',
      'availableChannels.exclusiveTooltip': 'Exclusive group',
      'availableChannels.publicTooltip': 'Public group',
    }[key] ?? key),
  }),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    cachedPublicSettings: {
      server_utc_offset: 0,
    },
  }),
}))

vi.mock('@/components/icons/Icon.vue', () => ({
  default: { template: '<span class="icon-stub" />' },
}))

vi.mock('@/components/common/PlatformIcon.vue', () => ({
  default: { template: '<span class="platform-icon-stub" />' },
}))

vi.mock('./SupportedModelChip.vue', () => ({
  default: { template: '<span class="model-chip-stub">{{ model.name }}</span>', props: ['model'] },
}))

describe('AvailableChannelsTable', () => {
  it('hides backend group multipliers from regular users', () => {
    const wrapper = mount(AvailableChannelsTable, {
      props: {
        columns: {
          name: 'Name',
          description: 'Description',
          platform: 'Platform',
          groups: 'Groups',
          supportedModels: 'Models',
        },
        loading: false,
        pricingKeyPrefix: 'availableChannels.pricing',
        noPricingLabel: 'No pricing',
        noModelsLabel: 'No models',
        emptyLabel: 'Empty',
        rows: [
          {
            name: 'Primary',
            description: 'Stable lane',
            platforms: [
              {
                platform: 'openai',
                groups: [
                  {
                    id: 1,
                    name: 'Standard',
                    platform: 'openai',
                    subscription_type: 'standard',
                    rate_multiplier: 1.5,
                    is_exclusive: false,
                  },
                ],
                supported_models: [],
              },
            ],
          },
        ],
      },
    })

    expect(wrapper.text()).toContain('Standard')
    expect(wrapper.text()).not.toContain('1.5x')
  })
})
