import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import HomeView from '../HomeView.vue'

const { appState, authState, fetchPublicSettingsMock, checkAuthMock, getPublicModelCatalogMock, getPublicModelPricingMock, getPublicPlansMock, getChatModelsMock } = vi.hoisted(() => ({
  appState: {
    cachedPublicSettings: null as null | {
      site_name?: string
      site_logo?: string
      site_subtitle?: string
      doc_url?: string
      home_content?: string
      announcement_banners?: Array<{ id: string; text_zh: string; text_en: string }>
      announcement_banner_interval_ms?: number
    },
    siteName: 'LINX2.AI',
    siteLogo: '',
    siteSubtitle: 'AI Gateway Platform',
    docUrl: '',
    publicSettingsLoaded: true,
  },
  authState: {
    isAuthenticated: false,
    isAdmin: false,
    user: null as null | { email: string },
  },
  fetchPublicSettingsMock: vi.fn(),
  checkAuthMock: vi.fn(),
  getPublicModelCatalogMock: vi.fn(),
  getPublicModelPricingMock: vi.fn(),
  getPublicPlansMock: vi.fn(),
  getChatModelsMock: vi.fn(),
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    get cachedPublicSettings() {
      return appState.cachedPublicSettings
    },
    get siteName() {
      return appState.siteName
    },
    get siteLogo() {
      return appState.siteLogo
    },
    get siteSubtitle() {
      return appState.siteSubtitle
    },
    get docUrl() {
      return appState.docUrl
    },
    get publicSettingsLoaded() {
      return appState.publicSettingsLoaded
    },
    fetchPublicSettings: fetchPublicSettingsMock,
  }),
  useAuthStore: () => ({
    get user() {
      return authState.user
    },
    get isAuthenticated() {
      return authState.isAuthenticated
    },
    get isAdmin() {
      return authState.isAdmin
    },
    checkAuth: checkAuthMock,
  }),
}))

vi.mock('@/api/settings', () => ({
  getPublicModelCatalog: getPublicModelCatalogMock,
  getPublicModelPricing: getPublicModelPricingMock,
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    getPublicPlans: getPublicPlansMock,
  },
}))

vi.mock('@/api/chat', () => ({
  chatAPI: {
    listModels: getChatModelsMock,
  },
}))

const messages: Record<string, string> = {
  'home.docs': '文档',
  'home.footer.allRightsReserved': '保留所有权利。',
  'home.getStarted': '立即开始',
  'home.goToDashboard': '进入控制台',
  'home.login': '登录',
  'home.dashboard': '控制台',
  'home.switchToLight': '切换到浅色模式',
  'home.switchToDark': '切换到深色模式',
  'home.viewDocs': '查看文档',
  'home.tags.subscriptionToApi': 'AI 网关平台',
  'home.tags.stickySession': '会话保持',
  'home.tags.realtimeBilling': '实时计费',
  'home.features.unifiedGateway': '统一网关',
  'home.features.unifiedGatewayDesc': '统一网关说明',
  'home.features.multiAccount': '多账号池',
  'home.features.multiAccountDesc': '多账号池说明',
  'home.features.balanceQuota': '余额配额',
  'home.features.balanceQuotaDesc': '余额配额说明',
  'home.providers.title': '服务商',
  'home.providers.description': '服务商说明',
  'home.providers.claude': 'Claude',
  'home.providers.gemini': 'Gemini',
  'home.providers.antigravity': 'Antigravity',
  'home.providers.more': '更多',
  'home.providers.supported': '已支持',
  'home.providers.soon': '即将支持',
  'nav.modelMarketplace': '模型广场',
  'chat.openWebChat': '开始网页对话',
  'chat.openWebChatShort': '对话',
  'chat.title': '网页对话',
  'chat.newChat': '新对话',
  'chat.searchConversations': '搜索对话',
  'chat.modelSelector': '选择模型',
  'chat.composerPlaceholder': '输入消息',
  'chat.send': '发送',
  'chat.attachImage': '上传图片',
  'modelCatalog.chatNow': '立即对话',
  'modelCatalog.context': '上下文',
  'modelCatalog.pending': '待确认',
  'modelCatalog.modality.text': '文本',
  'modelCatalog.modality.image': '图像',
  'modelCatalog.pricing.input': '输入',
  'modelCatalog.pricing.output': '输出',
  'modelCatalog.pricing.cacheRead': '缓存读取',
}

const modelPricingFixture = {
  providers: [
    {
      provider: 'Anthropic',
      accent_color: '#d97745',
      models: [
        { name: 'Claude Opus 4.8', model: 'claude-opus-4-8', input_per_million: 15, output_per_million: 75, cache_read_per_million: 1.5 },
        { name: 'Claude Opus 4.7', model: 'claude-opus-4-7', input_per_million: 15, output_per_million: 75, cache_read_per_million: 1.5 },
        { name: 'Claude Opus 4.6', model: 'claude-opus-4-6', input_per_million: 15, output_per_million: 75, cache_read_per_million: 1.5 },
        { name: 'Claude Sonnet 4.6', model: 'claude-sonnet-4-6', input_per_million: 3, output_per_million: 15, cache_read_per_million: 0.3 },
        { name: 'Claude Sonnet 4.5', model: 'claude-sonnet-4-5', input_per_million: 3, output_per_million: 15, cache_read_per_million: 0.3 },
      ],
    },
    {
      provider: 'OpenAI',
      accent_color: '#27a644',
      models: [
        { name: 'GPT-5.5', model: 'gpt-5.5', input_per_million: 5, output_per_million: 30, cache_read_per_million: 0.5 },
        { name: 'GPT-5.4', model: 'gpt-5.4', input_per_million: 2.5, output_per_million: 15, cache_read_per_million: 0.25 },
        { name: 'GPT-5.4 Mini', model: 'gpt-5.4-mini', input_per_million: 0.75, output_per_million: 4.5, cache_read_per_million: 0.075 },
        { name: 'GPT-5.3 Codex', model: 'gpt-5.3-codex', input_per_million: 1.25, output_per_million: 10, cache_read_per_million: 0.125 },
        { name: 'GPT-4o', model: 'gpt-4o', input_per_million: 2.5, output_per_million: 10, cache_read_per_million: 1.25 },
      ],
    },
  ],
}

const modelCatalogFixture = {
  updated_at: '2026-06-21',
  providers: [
    { key: 'anthropic', name: 'Anthropic', accent_color: '#d97745', model_count: 2 },
    { key: 'openai', name: 'OpenAI', accent_color: '#27a644', model_count: 3 },
    { key: 'gemini', name: 'Gemini', accent_color: '#4f8df5', model_count: 1 },
    { key: 'qwen', name: 'Alibaba Cloud', accent_color: '#7c6df2', model_count: 1 },
    { key: 'deepseek', name: 'DeepSeek', accent_color: '#4b6bfb', model_count: 1 },
  ],
  models: [
    {
      provider: 'anthropic',
      provider_name: 'Anthropic',
      model_name: 'claude-opus-4-8',
      display_name: 'Claude Opus 4.8',
      modalities: ['text'],
      description: 'Highest-capability Claude model for complex reasoning.',
      context_window: 1000000,
      features: ['tool use', 'prompt caching'],
      pricing: { currency: 'USD', unit: '1M tokens', input_per_million: 5, output_per_million: 25, cache_read_per_million: 0.5 },
      price_status: 'confirmed',
      released_at: '2026-06-21',
      release_status: 'unverified',
      source_url: 'https://docs.anthropic.com/en/docs/about-claude/pricing',
      updated_at: '2026-06-21',
    },
    {
      provider: 'anthropic',
      provider_name: 'Anthropic',
      model_name: 'claude-sonnet-4-6',
      display_name: 'Claude Sonnet 4.6',
      modalities: ['text'],
      description: 'Balanced Claude Sonnet model for coding.',
      context_window: 1000000,
      features: ['tool use'],
      pricing: { currency: 'USD', unit: '1M tokens', input_per_million: 3, output_per_million: 15, cache_read_per_million: 0.3 },
      price_status: 'confirmed',
      released_at: '2026-06-21',
      release_status: 'unverified',
      source_url: 'https://docs.anthropic.com/en/docs/about-claude/pricing',
      updated_at: '2026-06-21',
    },
    {
      provider: 'openai',
      provider_name: 'OpenAI',
      model_name: 'gpt-5.5',
      display_name: 'GPT-5.5',
      modalities: ['text'],
      description: 'OpenAI frontier text model.',
      context_window: 1050000,
      features: ['tool use', 'prompt caching'],
      pricing: { currency: 'USD', unit: '1M tokens', input_per_million: 5, output_per_million: 30, cache_read_per_million: 0.5 },
      price_status: 'confirmed',
      released_at: '2026-06-21',
      release_status: 'unverified',
      source_url: 'https://openai.com/api/pricing/',
      updated_at: '2026-06-21',
    },
    {
      provider: 'openai',
      provider_name: 'OpenAI',
      model_name: 'gpt-5.4-mini',
      display_name: 'GPT-5.4 Mini',
      modalities: ['text'],
      description: 'Lower-cost OpenAI model for fast production text.',
      context_window: 400000,
      features: ['tool use'],
      pricing: { currency: 'USD', unit: '1M tokens', input_per_million: 0.75, output_per_million: 4.5, cache_read_per_million: 0.075 },
      price_status: 'confirmed',
      released_at: '2026-06-20',
      release_status: 'unverified',
      source_url: 'https://openai.com/api/pricing/',
      updated_at: '2026-06-21',
    },
    {
      provider: 'openai',
      provider_name: 'OpenAI',
      model_name: 'gpt-image-2',
      display_name: 'GPT-Image-2',
      modalities: ['image'],
      description: 'OpenAI image generation model.',
      context_window: 0,
      features: ['image generation', 'multi-resolution'],
      pricing: {
        currency: 'USD',
        unit: '1M tokens',
        input_per_million: 2.5,
        output_per_million: 5,
        price_lines: [{ label: '1K image', amount: 0.21, unit: 'image' }],
      },
      price_status: 'confirmed',
      released_at: '2026-06-15',
      release_status: 'unverified',
      source_url: 'https://openai.com/api/pricing/',
      updated_at: '2026-06-21',
    },
    {
      provider: 'gemini',
      provider_name: 'Gemini',
      model_name: 'gemini-3.5-flash',
      display_name: 'Gemini 3.5 Flash',
      modalities: ['text'],
      description: 'Gemini model retained in the public reference catalog.',
      context_window: 1048576,
      features: ['tool use', 'prompt caching'],
      pricing: { currency: 'USD', unit: '1M tokens', input_per_million: 1.5, output_per_million: 9, cache_read_per_million: 0.15 },
      price_status: 'confirmed',
      released_at: '2026-06-21',
      release_status: 'unverified',
      source_url: 'https://ai.google.dev/gemini-api/docs/pricing',
      updated_at: '2026-06-21',
    },
    {
      provider: 'qwen',
      provider_name: 'Alibaba Cloud',
      model_name: 'qwen3.6-plus',
      display_name: 'Qwen3.6 Plus',
      modalities: ['text'],
      description: 'Qwen Plus model listed by the reference catalog.',
      context_window: 1000000,
      features: ['agentic coding'],
      pricing: { currency: 'USD', unit: '1M tokens', note: 'Pending confirmation' },
      price_status: 'unverified',
      released_at: '2026-06-21',
      release_status: 'unverified',
      source_url: '',
      updated_at: '2026-06-21',
    },
    {
      provider: 'deepseek',
      provider_name: 'DeepSeek',
      model_name: 'deepseek-v3.2',
      display_name: 'DeepSeek V3.2',
      modalities: ['text'],
      description: 'DeepSeek model retained from the reference catalog.',
      context_window: 1000000,
      features: ['thinking', 'context caching'],
      pricing: { currency: 'USD', unit: '1M tokens', input_per_million: 0.14, output_per_million: 0.28, cache_read_per_million: 0.0028 },
      price_status: 'confirmed',
      released_at: '2026-06-18',
      release_status: 'unverified',
      source_url: 'https://api-docs.deepseek.com/quick_start/pricing',
      updated_at: '2026-06-21',
    },
  ],
}

function webChatModel(provider: string, model: string, displayName: string) {
  return {
    provider,
    platform: provider,
    key_type: provider,
    model,
    display_name: displayName,
    supports_text: true,
    supports_image_input: true,
    supports_file_context: true,
    supports_artifact_output: true,
    supports_thinking: true,
    thinking_efforts: ['low', 'medium', 'high'],
    supports_web_search: provider !== 'gemini',
    supports_image_generation: model.includes('image'),
    image_generation_sizes: model.includes('image') ? ['1024x1024', '1536x1024'] : [],
    image_generation_aspect_ratios: model.includes('image') ? ['1:1', '3:2'] : [],
    image_generation_qualities: model.includes('image') ? ['low', 'medium', 'high'] : [],
    image_generation_output_formats: model.includes('image') ? ['png', 'webp'] : [],
    image_generation_backgrounds: model.includes('image') ? ['opaque', 'auto'] : [],
    price_status: 'confirmed',
  }
}

const webChatModelsFixture = [
  webChatModel('anthropic', 'claude-opus-4-8', 'Claude Opus 4.8'),
  webChatModel('anthropic', 'claude-sonnet-4-6', 'Claude Sonnet 4.6'),
  webChatModel('openai', 'gpt-5.5', 'GPT-5.5'),
  webChatModel('openai', 'gpt-5.4-mini', 'GPT-5.4 Mini'),
  webChatModel('openai', 'gpt-image-2', 'GPT-Image-2'),
]

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
      locale: { value: 'zh' },
    }),
  }
})

function mountHome() {
  return mount(HomeView, {
    global: {
      stubs: {
        RouterLink: {
          props: ['to'],
          methods: {
            hrefFor(to: string | { path: string; query?: Record<string, string> }) {
              if (typeof to === 'string') return to
              const query = to.query
                ? Object.entries(to.query).map(([key, value]) => `${key}=${encodeURIComponent(value)}`).join('&')
                : ''
              return query ? `${to.path}?${query}` : to.path
            },
          },
          template: '<a :href="hrefFor(to)"><slot /></a>',
        },
        LocaleSwitcher: { template: '<div data-testid="locale-switcher" />' },
        Icon: { template: '<svg data-testid="icon" />' },
        ModelIcon: { template: '<span data-testid="model-icon" />' },
        transition: true,
      },
    },
  })
}

describe('HomeView landing page', () => {
  beforeEach(() => {
    appState.cachedPublicSettings = null
    appState.siteName = 'LINX2.AI'
    appState.siteLogo = ''
    appState.siteSubtitle = 'AI Gateway Platform'
    appState.docUrl = ''
    appState.publicSettingsLoaded = true
    authState.isAuthenticated = false
    authState.isAdmin = false
    authState.user = null
    fetchPublicSettingsMock.mockReset()
    checkAuthMock.mockReset()
    getPublicModelCatalogMock.mockReset()
    getPublicModelPricingMock.mockReset()
    getPublicPlansMock.mockReset()
    getChatModelsMock.mockReset()
    fetchPublicSettingsMock.mockResolvedValue({})
    getPublicModelCatalogMock.mockResolvedValue(modelCatalogFixture)
    getPublicModelPricingMock.mockResolvedValue(modelPricingFixture)
    getPublicPlansMock.mockResolvedValue([])
    getChatModelsMock.mockResolvedValue(webChatModelsFixture)
    document.documentElement.classList.remove('dark')
    localStorage.clear()

    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: vi.fn().mockReturnValue({ matches: false }),
    })
  })

  it('renders the configured site subtitle in the header brand area', async () => {
    appState.cachedPublicSettings = {
      site_name: 'LINX2.AI',
      site_subtitle: 'Link 2 All AI Model',
    }

    const wrapper = mountHome()
    await flushPromises()

    expect(wrapper.get('header a').text()).toContain('LINX2.AI')
    expect(wrapper.get('header a').text()).toContain('Link 2 All AI Model')
    expect(wrapper.text()).toContain('一个网关密钥，接入 Claude 与 OpenAI 模型。')
  })

  it('renders the LINX2.AI gateway landing shell with subscription plans by default', async () => {
    appState.cachedPublicSettings = {
      site_name: 'Fuse API',
      site_subtitle: 'Custom subtitle should not replace the approved hero copy',
      doc_url: 'https://docs.example.test',
    }

    const wrapper = mountHome()
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('Fuse API')
    expect(text).toContain('AI 网关平台 · Claude / OpenAI 兼容路由')
    expect(text).toContain('一个网关密钥，接入 Claude 与 OpenAI 模型。')
    expect(text).toContain('Claude Code')
    expect(text).toContain('Codex')
    expect(text).toContain('可用路由')
    expect(text).toContain('Anthropic Messages')
    expect(text).toContain('OpenAI Responses')
    expect(text).toContain('OpenAI Chat Completions')
    expect(text).toContain('OpenAI Images')
    expect(text).toContain('ANTHROPIC_API_KEY')
    expect(text).toContain('OPENAI_API_KEY')
    expect(text).toContain('{LINX2_AI_API}')
    expect(text).toContain('<5s')
    expect(text).not.toContain('ANTHROPIC_AUTH_TOKEN')
    expect(text).not.toMatch(/(^|\s)API_KEY=lx2_/)
    expect(text).not.toContain('订阅转 API')
    expect(text).not.toContain('Subscription to API')
    expect(text).not.toContain('AI Coding API')
    expect(text).not.toContain('Gemini')

    expect(text).toContain('LINX2.AI 订阅方案')
    expect(text).toContain('Basic 月卡')
    expect(text).toContain('Plus 月卡')
    expect(text).toContain('Pro 月卡')
    expect(text).toContain('Max 月卡')
    expect(text).toContain('¥179')
    expect(text).toContain('¥399')
    expect(text).toContain('¥799')
    expect(text).toContain('¥1599')
    expect(text).toContain('$50 / 7 天')
    expect(text).toContain('$110 / 7 天')
    expect(text).toContain('$260 / 7 天')
    expect(text).toContain('$550 / 7 天')
    expect(text).toContain('总共可获取 $200')
    expect(text).toContain('总共可获取 $440')
    expect(text).toContain('总共可获取 $1,040')
    expect(text).toContain('总共可获取 $2,200')
    expect(text).toContain('每周发放充值额度')
    expect(text).toContain('所有档位都支持 Claude Code 与 OpenAI 兼容网关')
    expect(text).toContain('订阅套餐额度用完，任务和 Coding 却停不下来？')
    expect(text).toContain('轻量 Claude Code 会话')
    expect(text).toContain('OpenAI 兼容接口调试')
    expect(text).toContain('高频 Claude Code / OpenAI 生产流量')
    expect(text).toContain('价格透明，上游模型价格直传')
    expect(text).toContain('按每百万 Token 计价')
    expect(text).toContain('Anthropic')
    expect(text).toContain('OpenAI')
    expect(text).toContain('Claude Opus 4.8')
    expect(text).toContain('Claude Sonnet 4.6')
    expect(text).toContain('GPT-5.5')
    expect(text).toContain('GPT-5.4 Mini')
    expect(text).toContain('GPT-5.3 Codex')
    expect(text).toContain('$75.00')
    expect(text).toContain('$0.075')
    expect(text).not.toContain('GPT-Image-2')
    expect(text).not.toContain('Alibaba Cloud')
    expect(text).not.toContain('Qwen3.6 Plus')
    expect(text).not.toContain('Claude Mythos 5')
    expect(text).not.toContain('GPT-4o')
    expect(getPublicModelPricingMock).toHaveBeenCalledTimes(1)
    expect(getPublicModelCatalogMock).not.toHaveBeenCalled()

    const headerNav = wrapper.get('[data-testid="homepage-header-actions"]')
    expect(headerNav.text()).toContain('能力')
    expect(headerNav.text()).not.toContain('模型广场')
    expect(headerNav.text()).not.toContain('对话')
    expect(headerNav.text()).toContain('价格')
    expect(headerNav.find('a[href="#pricing"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="homepage-chat-entry"]').exists()).toBe(false)

    const routeGrid = wrapper.get('[data-testid="homepage-route-grid"]')
    expect(routeGrid.text()).toContain('Anthropic Messages')
    expect(routeGrid.text()).toContain('OpenAI Responses')
    expect(routeGrid.text()).toContain('OpenAI Chat Completions')
    expect(routeGrid.text()).toContain('OpenAI Images')

    const pricingGrid = wrapper.get('[data-testid="linear-pricing-grid"]')
    expect(pricingGrid.findAll('[data-testid="pricing-plan-card"]').length).toBe(4)
    expect(pricingGrid.find('[data-testid="pricing-model-row"]').exists()).toBe(false)
    const pricingCards = pricingGrid.findAll('[data-testid="pricing-plan-card"]')
    const expectedPlans = [
      { name: 'Basic 月卡', price: '¥179', quota: '$50 / 7 天', monthlyTotal: '$200' },
      { name: 'Plus 月卡', price: '¥399', quota: '$110 / 7 天', monthlyTotal: '$440' },
      { name: 'Pro 月卡', price: '¥799', quota: '$260 / 7 天', monthlyTotal: '$1,040' },
      { name: 'Max 月卡', price: '¥1599', quota: '$550 / 7 天', monthlyTotal: '$2,200' },
    ]
    expectedPlans.forEach((plan, index) => {
      const card = pricingCards[index]
      const cardText = card.text()
      expect(card.classes()).toEqual(expect.arrayContaining(['flex', 'h-full', 'flex-col']))
      expect(cardText).toContain(plan.name)
      expect(cardText).toContain(plan.price)
      expect(cardText).toContain('/ 月')
      expect(cardText).toContain(plan.quota)
      expect(cardText).toContain(`总共可获取 ${plan.monthlyTotal}`)
      const planCta = card.get('a[href="/purchase?tab=subscription"]')
      expect(planCta.classes()).toContain('mt-auto')
      expect(planCta.text()).toContain('选择方案')
      expect(planCta.attributes('aria-label')).toContain(plan.name)
    })
    const pricingCtaLabels = pricingCards.map((card) => card.get('a[href="/purchase?tab=subscription"]').attributes('aria-label'))
    expect(new Set(pricingCtaLabels).size).toBe(expectedPlans.length)

    const subscriptionSection = wrapper.get('section#pricing')
    const modelPricingSection = wrapper.get('section#model-pricing')
    expect(subscriptionSection.text()).toContain('LINX2.AI 订阅方案')
    expect(subscriptionSection.find('[data-testid="homepage-model-catalog-grid"]').exists()).toBe(false)
    expect(modelPricingSection.text()).toContain('价格透明，上游模型价格直传')
    expect(modelPricingSection.text()).toContain('Claude Opus 4.8')
    expect(modelPricingSection.text()).not.toContain('Claude Mythos 5')
    expect(subscriptionSection.element.compareDocumentPosition(modelPricingSection.element) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()

    const modelRows = wrapper.findAll('[data-testid="homepage-model-pricing-row"]')
    expect(modelRows).toHaveLength(8)
    const toggles = wrapper.findAll('[data-testid="homepage-model-pricing-toggle"]')
    expect(toggles).toHaveLength(2)
    expect(toggles[0].text()).toContain('展开更多模型')
    await toggles[0].trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-testid="homepage-model-pricing-row"]')).toHaveLength(9)
    expect(wrapper.text()).toContain('Claude Sonnet 4.5')
    expect(wrapper.text()).not.toContain('Claude Mythos 5')

    const header = wrapper.get('header')
    expect(header.classes()).toContain('bg-linear-canvas/90')
    expect(header.classes()).not.toContain('bg-linear-canvas/88')

    const footerBrand = wrapper.get('[data-testid="homepage-footer-brand"]')
    expect(footerBrand.classes()).toContain('items-center')
    expect(footerBrand.text()).toContain('LINX2.AI')

    expect(text).not.toContain('GitHub')
    expect(wrapper.get('img[alt="Fuse API logo"]').attributes('src')).toBe('/linx2-icon.png')
    const headerCta = wrapper.get('a[href="/login"]')
    expect(headerCta.text()).toContain('立即开始')
    expect(headerCta.classes()).toContain('bg-primary-500')
    expect(headerCta.classes()).not.toContain('ui-theme-toggle')

    const themeToggle = wrapper.get('[data-testid="homepage-theme-toggle"]')
    expect(themeToggle.classes()).toContain('ui-theme-toggle')
    expect(themeToggle.classes()).not.toContain('bg-primary-500')

    const accentBadges = wrapper.findAll('.ui-accent-badge')
    expect(accentBadges.length).toBeGreaterThanOrEqual(6)

    const accentDots = wrapper.findAll('.ui-accent-dot')
    expect(accentDots.length).toBeGreaterThanOrEqual(2)

    const docsLinks = wrapper.findAll('a[href="https://docs.example.test/"]')
    expect(docsLinks.length).toBeGreaterThan(0)
    expect(docsLinks[0].text()).toContain('文档')
    expect(wrapper.get('header a[href="#pricing"]').text()).toContain('价格')
  })

  it('renders homepage limited-seat ribbons from public subscription plans', async () => {
    getPublicPlansMock.mockResolvedValue([
      {
        id: 11,
        name: 'Pro monthly',
        description: 'Public Pro desc',
        price: 799,
        original_price: 999,
        seven_day_quota_usd: 260,
        features: [],
        validity_days: 30,
        validity_unit: 'day',
        for_sale: true,
        sort_order: 30,
        seat_limit: 100,
        seat_used: 12,
        seat_available: 88,
        seat_full: false,
        seat_over_limit: false,
        virtual_seat_start: 4900,
        virtual_seat_total: 5000,
      },
      {
        id: 12,
        name: 'Max monthly',
        description: 'Public Max desc',
        price: 1599,
        original_price: 0,
        seven_day_quota_usd: 550,
        features: [],
        validity_days: 30,
        validity_unit: 'day',
        for_sale: true,
        sort_order: 40,
        seat_limit: null,
        seat_used: 99,
        virtual_seat_start: 9900,
        virtual_seat_total: 10000,
      },
    ])

    const wrapper = mountHome()
    await flushPromises()

    const pricingGrid = wrapper.get('[data-testid="linear-pricing-grid"]')
    const ribbons = pricingGrid.findAll('[data-testid="homepage-limited-seat-ribbon"]')
    expect(getPublicPlansMock).toHaveBeenCalledTimes(1)
    expect(ribbons).toHaveLength(1)
    expect(ribbons[0].text()).toContain('限时名额：4912/5000')
    expect(ribbons[0].classes()).toEqual(expect.arrayContaining([
      'w-[220px]',
      'right-[-54px]',
      'top-7',
      'from-orange-950',
      'via-orange-800',
      'to-orange-700',
    ]))
    expect(pricingGrid.text()).not.toContain('9999/10000')
  })

  it('keeps static homepage pricing when public subscription plans fail to load', async () => {
    getPublicPlansMock.mockRejectedValue(new Error('network'))

    const wrapper = mountHome()
    await flushPromises()

    const pricingGrid = wrapper.get('[data-testid="linear-pricing-grid"]')
    expect(pricingGrid.findAll('[data-testid="pricing-plan-card"]')).toHaveLength(4)
    expect(pricingGrid.text()).toContain('Basic 月卡')
    expect(pricingGrid.text()).toContain('¥179')
    expect(pricingGrid.find('[data-testid="homepage-limited-seat-ribbon"]').exists()).toBe(false)
  })

  it('renders the orange-X LINX2.AI wordmark for the default brand', async () => {
    const wrapper = mountHome()
    await flushPromises()

    const wordmark = wrapper.get('.linx-wordmark')
    expect(wordmark.attributes('aria-label')).toBe('LINX2.AI')
    expect(wordmark.text()).toBe('LINX2.AI')
    expect(wordmark.get('.text-primary-400').text()).toBe('X')
  })

  it('routes authenticated admin users to the dashboard CTA', async () => {
    authState.isAuthenticated = true
    authState.isAdmin = true
    authState.user = { email: 'admin@example.com' }

    const wrapper = mountHome()
    await flushPromises()

    const headerCta = wrapper.get('header a[href="/admin/dashboard"]')
    const userInitial = headerCta.get('.ui-avatar-identity-sm')
    expect(userInitial.text()).toBe('A')
    expect(userInitial.classes()).not.toContain('bg-white/15')
    expect(headerCta.text()).toContain('进入控制台')
    expect(headerCta.attributes('aria-label')).toBe('进入控制台')
    expect(wrapper.text()).toContain('控制台')
  })

  it('shows an accessible labelled sign-in CTA in the header', async () => {
    const wrapper = mountHome()
    await flushPromises()

    const headerCta = wrapper.get('header a[href="/login"]')
    const headerCtaLabel = headerCta.get('span[data-testid="header-cta-label"]')

    expect(headerCta.attributes('aria-label')).toBe('立即开始')
    expect(headerCta.classes()).toContain('h-10')
    expect(headerCtaLabel.text()).toBe('立即开始')
  })

  it('hides model-branch chat and marketplace entries from visitors', async () => {
    const wrapper = mountHome()
    await flushPromises()

    const header = wrapper.get('[data-testid="homepage-header-actions"]')
    expect(header.text()).not.toContain('对话')
    expect(header.text()).not.toContain('模型广场')
    expect(wrapper.find('header a[href="/login?redirect=%2Fchat"]').exists()).toBe(false)
    expect(wrapper.find('header a[href="/models"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="homepage-chat-entry"]').exists()).toBe(false)
  })

  it('shows model-branch chat and marketplace entries to regular users', async () => {
    authState.isAuthenticated = true
    authState.isAdmin = false
    authState.user = { email: 'user@example.com' }

    const wrapper = mountHome()
    await flushPromises()

    const header = wrapper.get('[data-testid="homepage-header-actions"]')
    expect(header.text()).toContain('对话')
    expect(header.text()).toContain('模型广场')
    expect(wrapper.get('header a[href="/chat"]').text()).toContain('对话')
    expect(wrapper.get('header a[href="/models"]').text()).toContain('模型广场')
    expect(wrapper.find('[data-testid="homepage-chat-entry"]').exists()).toBe(true)
  })

  it('shows model-branch chat and marketplace entries to admins', async () => {
    authState.isAuthenticated = true
    authState.isAdmin = true
    authState.user = { email: 'admin@example.com' }

    const wrapper = mountHome()
    await flushPromises()

    const chatCta = wrapper.get('header a[href="/chat"]')
    expect(chatCta.text()).toContain('对话')
    expect(wrapper.get('header a[href="/models"]').text()).toContain('模型广场')

    const chatEntry = wrapper.get('[data-testid="homepage-chat-entry"]')
    expect(chatEntry.find('[data-testid="homepage-chat-demo-rail"]').exists()).toBe(true)
    expect(chatEntry.find('[data-testid="homepage-chat-demo-model-selector"]').exists()).toBe(true)
    expect(chatEntry.find('[data-testid="homepage-chat-demo-message-list"]').exists()).toBe(true)
    expect(chatEntry.find('[data-testid="homepage-chat-demo-composer"]').exists()).toBe(true)
    expect(chatEntry.text()).toContain('GPT-Image-2')
    expect(chatEntry.text()).toContain('Thinking')
    expect(chatEntry.text()).toContain('Generate')
    expect(chatEntry.find('textarea').exists()).toBe(true)
    expect(chatEntry.text()).toContain('新对话')
  })

  it('renders the authenticated homepage model catalog from dynamically available web chat models', async () => {
    authState.isAuthenticated = true
    authState.isAdmin = false
    authState.user = { email: 'user@example.com' }

    const wrapper = mountHome()
    await flushPromises()

    expect(getPublicModelCatalogMock).toHaveBeenCalledTimes(1)
    expect(getChatModelsMock).toHaveBeenCalledTimes(1)
    expect(getPublicModelPricingMock).not.toHaveBeenCalled()
    const modelPricingSection = wrapper.get('section#model-pricing')
    expect(modelPricingSection.text()).toContain('真实模型目录，直接发起对话')
    expect(modelPricingSection.text()).toContain('公开模型目录 · 价格与能力')
    expect(modelPricingSection.find('[data-testid="homepage-model-pricing-table"]').exists()).toBe(false)

    const modelCards = wrapper.findAll('[data-testid="homepage-model-catalog-card"]')
    expect(modelCards).toHaveLength(webChatModelsFixture.length)
    expect(modelCards[0].text()).toContain('Claude Opus 4.8')
    expect(modelCards[4].text()).toContain('立即对话')
    expect(modelCards[4].find('a[href="/chat?provider=openai&model=gpt-image-2"]').exists()).toBe(true)
    expect(modelPricingSection.text()).not.toContain('Gemini 3.5 Flash')
    expect(modelPricingSection.text()).not.toContain('Qwen3.6 Plus')
    expect(modelPricingSection.text()).not.toContain('DeepSeek V3.2')
    expect(wrapper.find('[data-testid="homepage-model-catalog-toggle"]').exists()).toBe(false)
  })

  it('renders URL custom home content in a full-page iframe before the default landing page', async () => {
    appState.cachedPublicSettings = {
      site_name: 'Fuse API',
      home_content: 'https://landing.example.test',
    }

    const wrapper = mountHome()
    await flushPromises()

    const iframe = wrapper.get('iframe')
    expect(iframe.attributes('src')).toBe('https://landing.example.test')
    expect(iframe.attributes('title')).toBe('Fuse API custom home content')
    expect(wrapper.find('.linear-landing').exists()).toBe(false)
  })

  it('renders Markdown custom home content before the default landing page', async () => {
    appState.cachedPublicSettings = {
      home_content: '# Custom Home\n\nThis is **custom** content.',
    }

    const wrapper = mountHome()
    await flushPromises()

    expect(wrapper.get('h1').text()).toBe('Custom Home')
    expect(wrapper.html()).toContain('<strong>custom</strong>')
    expect(wrapper.find('.linear-landing').exists()).toBe(false)
  })

  it('renders HTML custom home content before the default landing page', async () => {
    appState.cachedPublicSettings = {
      home_content: '<section data-testid="custom-home">Custom Home</section>',
    }

    const wrapper = mountHome()
    await flushPromises()

    expect(wrapper.html()).toContain('data-testid="custom-home"')
    expect(wrapper.text()).toContain('Custom Home')
    expect(wrapper.find('.linear-landing').exists()).toBe(false)
  })

  it('renders a Linear-style product console landing experience without decorative mesh glow', async () => {
    appState.cachedPublicSettings = {
      site_name: 'Fuse API',
      doc_url: 'https://docs.example.test',
    }

    const wrapper = mountHome()
    await flushPromises()

    expect(wrapper.find('.linear-landing').exists()).toBe(true)
    expect(wrapper.find('[data-testid="linear-product-console"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="linear-pricing-grid"]').exists()).toBe(true)
    expect(wrapper.html()).not.toContain('bg-mesh-gradient')
    expect(wrapper.html()).not.toContain('blur-3xl')
    expect(wrapper.text()).toContain('API Gateway Console')
    expect(wrapper.text()).toContain('Base URL')
  })

  it('honors light mode without forcing a local dark scope', async () => {
    localStorage.setItem('theme', 'light')
    document.documentElement.classList.remove('dark')

    const wrapper = mountHome()
    await flushPromises()

    const landing = wrapper.get('.linear-landing')
    expect(landing.classes()).not.toContain('dark')
    expect(landing.classes()).toContain('bg-linear-canvas')
    expect(wrapper.find('[data-testid="linear-product-console"] .linx-panel-strong').exists()).toBe(true)
  })

  it('空列表回退到默认公告文案', async () => {
    appState.cachedPublicSettings = {
      announcement_banners: [],
      announcement_banner_interval_ms: 3000,
    }
    const wrapper = mountHome()
    await flushPromises()
    expect(wrapper.text()).toContain('统一 Claude Code')
  })

  it('多条 banner 按间隔轮换', async () => {
    vi.useFakeTimers()
    appState.cachedPublicSettings = {
      announcement_banners: [
        { id: 'a', text_zh: '甲', text_en: 'Alpha' },
        { id: 'b', text_zh: '乙', text_en: 'Beta' },
      ],
      announcement_banner_interval_ms: 3000,
    }
    const wrapper = mountHome()
    await flushPromises()
    expect(wrapper.text()).toContain('甲')
    vi.advanceTimersByTime(3000)
    await flushPromises()
    expect(wrapper.text()).toContain('乙')
    vi.useRealTimers()
  })

  it('单条 banner 不轮换', async () => {
    vi.useFakeTimers()
    appState.cachedPublicSettings = {
      announcement_banners: [{ id: 'a', text_zh: '甲', text_en: 'Alpha' }],
      announcement_banner_interval_ms: 3000,
    }
    const wrapper = mountHome()
    await flushPromises()
    vi.advanceTimersByTime(9000)
    await flushPromises()
    expect(wrapper.text()).toContain('甲')
    vi.useRealTimers()
  })
})
