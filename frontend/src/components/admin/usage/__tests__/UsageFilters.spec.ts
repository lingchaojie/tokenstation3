import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'

import UsageFilters from '../UsageFilters.vue'

// --- i18n messages (only what UsageFilters needs) ---
const messages: Record<string, string> = {
  'admin.usage.userDeletedBadge': 'deleted',
  'admin.usage.userFilter': 'User',
  'admin.usage.searchUserPlaceholder': 'Search user...',
  'admin.usage.excludedUserFilter': 'Exclude users',
  'admin.usage.searchExcludedUserPlaceholder': 'Search users to exclude...',
  'admin.usage.excludedUserLimit': 'Up to 100 users can be excluded.',
  'usage.apiKeyFilter': 'API Key',
  'admin.usage.searchApiKeyPlaceholder': 'Search API key...',
  'usage.model': 'Model',
  'admin.usage.allModels': 'All Models',
  'admin.usage.account': 'Account',
  'admin.usage.searchAccountPlaceholder': 'Search account...',
  'usage.type': 'Type',
  'admin.usage.allTypes': 'All Types',
  'usage.ws': 'WS',
  'usage.stream': 'Stream',
  'usage.sync': 'Sync',
  'admin.usage.billingType': 'Billing Type',
  'admin.usage.allBillingTypes': 'All Billing Types',
  'admin.usage.billingTypeBalance': 'Balance',
  'admin.usage.billingTypeSubscription': 'Subscription',
  'admin.usage.billingMode': 'Pricing Mode',
  'admin.usage.allBillingModes': 'All Billing Modes',
  'admin.usage.billingModeToken': 'Token',
  'admin.usage.billingModePerRequest': 'Per Request',
  'admin.usage.billingModeImage': 'Image',
  'admin.usage.group': 'Group',
  'admin.usage.allGroups': 'All Groups',
  'common.refresh': 'Refresh',
  'common.reset': 'Reset',
  'admin.usage.cleanup.button': 'Cleanup',
  'usage.exportExcel': 'Export',
}

// Mock vue-i18n
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

// Mock the admin API module — we control searchUsers return value per test
const mockSearchUsers = vi.fn()
const mockSearchApiKeys = vi.fn().mockResolvedValue([])
const mockGroupsList = vi.fn().mockResolvedValue({ items: [] })
const mockGetModelStats = vi.fn().mockResolvedValue({ models: [] })
const mockAccountsList = vi.fn().mockResolvedValue({ items: [] })

vi.mock('@/api/admin', () => ({
  adminAPI: {
    usage: {
      searchUsers: (...args: any[]) => mockSearchUsers(...args),
      searchApiKeys: (...args: any[]) => mockSearchApiKeys(...args),
    },
    groups: { list: (...args: any[]) => mockGroupsList(...args) },
    dashboard: { getModelStats: (...args: any[]) => mockGetModelStats(...args) },
    accounts: { list: (...args: any[]) => mockAccountsList(...args) },
  },
}))

// Default props helper
const defaultFilters = () => ({
  user_id: undefined,
  exclude_user_ids: undefined as number[] | undefined,
  api_key_id: undefined,
  account_id: undefined,
  model: null,
  request_type: null,
  billing_type: null,
  billing_mode: null,
  group_id: null,
  start_date: '',
  end_date: '',
})

function mountFilters(filters = defaultFilters()) {
  return mount(UsageFilters, {
    props: {
      modelValue: filters,
      exporting: false,
      startDate: '2026-05-01',
      endDate: '2026-05-28',
      showActions: false,
      modelOptions: [],
    },
    global: {
      stubs: {
        Select: true,
        Teleport: true,
      },
    },
  })
}

async function searchExcludedUsers(wrapper: ReturnType<typeof mountFilters>, keyword: string) {
  const input = wrapper.get('[data-testid="excluded-user-filter"]')
  await input.trigger('focus')
  await input.setValue(keyword)
  vi.advanceTimersByTime(300)
  await flushPromises()
}

describe('UsageFilters — user search dropdown', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    mockSearchUsers.mockReset()
    mockSearchApiKeys.mockResolvedValue([])
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('(a) labels deleted users with the i18n badge and (b) sorts active users before deleted ones, (c) selection sets user_id', async () => {
    // Arrange: mock returns deleted FIRST (proves sorting re-orders to active-first)
    mockSearchUsers.mockResolvedValue([
      { id: 2, email: 'gone@test.com', deleted: true },
      { id: 1, email: 'active@test.com', deleted: false },
    ])

    const wrapper = mountFilters()

    // Trigger focus (sets showUserDropdown = true) then input (fires debounceUserSearch)
    const input = wrapper.find('input[type="text"]')
    await input.trigger('focus')
    await input.setValue('test')
    await input.trigger('input')

    // Advance debounce timer (300ms) then flush the resolved promise
    vi.advanceTimersByTime(300)
    await flushPromises()

    // --- (b) Sort: active user should appear BEFORE deleted user ---
    // Check the underlying component state via rendered DOM order
    const buttons = wrapper.findAll('.usage-filter-dropdown button[type="button"]')
    const emailTexts = buttons.map((b) => b.text())

    // active@test.com should be listed first
    const activeIdx = emailTexts.findIndex((t) => t.includes('active@test.com'))
    const deletedIdx = emailTexts.findIndex((t) => t.includes('gone@test.com'))
    expect(activeIdx).toBeGreaterThanOrEqual(0)
    expect(deletedIdx).toBeGreaterThanOrEqual(0)
    expect(activeIdx).toBeLessThan(deletedIdx)

    // --- (a) Label: deleted user's button shows the badge text ---
    const deletedButton = buttons[deletedIdx]
    expect(deletedButton.text()).toContain('deleted')

    // active user's button does NOT show the badge text
    const activeButton = buttons[activeIdx]
    expect(activeButton.text()).not.toContain('deleted')

    // --- (c) Selection: clicking active user button sets filters.user_id ---
    await activeButton.trigger('click')
    await flushPromises()

    // The component emits 'update:modelValue' or modifies filters.user_id via toRef
    // selectUser sets filters.value.user_id = u.id and emits 'change'
    const changeEmits = wrapper.emitted('change')
    expect(changeEmits).toBeTruthy()
    expect(changeEmits!.length).toBeGreaterThan(0)

    // Also confirm user_id was set by checking the emitted change came through
    // (the component uses toRef so modelValue is mutated in place and 'change' is emitted)
    expect(wrapper.props('modelValue').user_id).toBe(1)
  })
})

describe('UsageFilters — excluded user multi-select', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    mockSearchUsers.mockReset()
    mockSearchApiKeys.mockResolvedValue([])
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('searches users and sorts active results before deleted results with the deleted badge', async () => {
    mockSearchUsers.mockResolvedValue([
      { id: 2, email: 'gone@test.com', deleted: true },
      { id: 1, email: 'active@test.com', deleted: false },
    ])
    const wrapper = mountFilters()

    await searchExcludedUsers(wrapper, 'test')

    expect(mockSearchUsers).toHaveBeenCalledWith('test')
    const options = wrapper.findAll('[data-testid="excluded-user-option"]')
    expect(options[0].text()).toContain('active@test.com')
    expect(options[0].text()).toContain('#1')
    expect(options[0].text()).not.toContain('deleted')
    expect(options[1].text()).toContain('gone@test.com')
    expect(options[1].text()).toContain('deleted')
    expect(options[1].text()).toContain('#2')
  })

  it('selects two users as removable chips and prevents duplicate IDs', async () => {
    mockSearchUsers.mockImplementation(async (keyword: string) => [{
      id: keyword === 'one' ? 1 : 2,
      email: `${keyword}@test.com`,
      deleted: false,
    }])
    const wrapper = mountFilters()

    await searchExcludedUsers(wrapper, 'one')
    await wrapper.get('[data-testid="excluded-user-option"]').trigger('click')
    await searchExcludedUsers(wrapper, 'two')
    await wrapper.get('[data-testid="excluded-user-option"]').trigger('click')
    await searchExcludedUsers(wrapper, 'one')
    await wrapper.get('[data-testid="excluded-user-option"]').trigger('click')

    expect(wrapper.props('modelValue').exclude_user_ids).toEqual([1, 2])
    expect(wrapper.findAll('[data-testid="excluded-user-chip"]').map((chip) => chip.text())).toEqual([
      'one@test.com ✕',
      'two@test.com ✕',
    ])
    expect(wrapper.emitted('change')).toHaveLength(2)
  })

  it('removes only the clicked chip and its ID', async () => {
    mockSearchUsers.mockImplementation(async (keyword: string) => [{
      id: keyword === 'one' ? 1 : 2,
      email: `${keyword}@test.com`,
      deleted: false,
    }])
    const wrapper = mountFilters()
    await searchExcludedUsers(wrapper, 'one')
    await wrapper.get('[data-testid="excluded-user-option"]').trigger('click')
    await searchExcludedUsers(wrapper, 'two')
    await wrapper.get('[data-testid="excluded-user-option"]').trigger('click')

    await wrapper.findAll('[data-testid="excluded-user-chip"]')[0].get('button').trigger('click')

    expect(wrapper.props('modelValue').exclude_user_ids).toEqual([2])
    expect(wrapper.findAll('[data-testid="excluded-user-chip"]')).toHaveLength(1)
    expect(wrapper.get('[data-testid="excluded-user-chip"]').text()).toContain('two@test.com')
  })

  it('reconciles locally stored chips when the exclusion IDs shrink or reset externally', async () => {
    mockSearchUsers.mockImplementation(async (keyword: string) => [{
      id: keyword === 'one' ? 1 : 2,
      email: `${keyword}@test.com`,
      deleted: false,
    }])
    const wrapper = mountFilters()
    await searchExcludedUsers(wrapper, 'one')
    await wrapper.get('[data-testid="excluded-user-option"]').trigger('click')
    await searchExcludedUsers(wrapper, 'two')
    await wrapper.get('[data-testid="excluded-user-option"]').trigger('click')

    await wrapper.setProps({
      modelValue: { ...wrapper.props('modelValue'), exclude_user_ids: [2] },
    })
    expect(wrapper.findAll('[data-testid="excluded-user-chip"]')).toHaveLength(1)
    expect(wrapper.get('[data-testid="excluded-user-chip"]').text()).toContain('two@test.com')

    await wrapper.setProps({
      modelValue: { ...wrapper.props('modelValue'), exclude_user_ids: undefined },
    })
    expect(wrapper.findAll('[data-testid="excluded-user-chip"]')).toHaveLength(0)
  })

  it('rejects the positive user from exclusions and removes an excluded user selected positively later', async () => {
    const user = { id: 7, email: 'same@test.com', deleted: false }
    mockSearchUsers.mockResolvedValue([user])

    const positiveFirst = mountFilters()
    const positiveInput = positiveFirst.find('.usage-filter-dropdown input:not([data-testid="excluded-user-filter"])')
    await positiveInput.trigger('focus')
    await positiveInput.setValue('same')
    vi.advanceTimersByTime(300)
    await flushPromises()
    const positiveOption = positiveFirst.findAll('.usage-filter-dropdown button').find((button) => button.text().includes('same@test.com'))
    await positiveOption!.trigger('click')
    await searchExcludedUsers(positiveFirst, 'same')
    await positiveFirst.get('[data-testid="excluded-user-option"]').trigger('click')
    expect(positiveFirst.props('modelValue').exclude_user_ids).toBeUndefined()
    expect(positiveFirst.findAll('[data-testid="excluded-user-chip"]')).toHaveLength(0)

    const excludedFirst = mountFilters()
    await searchExcludedUsers(excludedFirst, 'same')
    await excludedFirst.get('[data-testid="excluded-user-option"]').trigger('click')
    const secondPositiveInput = excludedFirst.find('.usage-filter-dropdown input:not([data-testid="excluded-user-filter"])')
    await secondPositiveInput.trigger('focus')
    await secondPositiveInput.setValue('same')
    vi.advanceTimersByTime(300)
    await flushPromises()
    const secondPositiveOption = excludedFirst.findAll('.usage-filter-dropdown button').find((button) => button.text().includes('same@test.com'))
    await secondPositiveOption!.trigger('click')

    expect(excludedFirst.props('modelValue').user_id).toBe(7)
    expect(excludedFirst.props('modelValue').exclude_user_ids).toEqual([])
    expect(excludedFirst.findAll('[data-testid="excluded-user-chip"]')).toHaveLength(0)
  })

  it('shows the limit message and rejects a 101st excluded user', async () => {
    const existingIds = Array.from({ length: 100 }, (_, index) => index + 1)
    mockSearchUsers.mockResolvedValue([{ id: 101, email: 'limit@test.com', deleted: false }])
    const wrapper = mountFilters({ ...defaultFilters(), exclude_user_ids: existingIds })

    expect(wrapper.text()).toContain('Up to 100 users can be excluded.')
    await searchExcludedUsers(wrapper, 'limit')
    await wrapper.get('[data-testid="excluded-user-option"]').trigger('click')

    expect(wrapper.props('modelValue').exclude_user_ids).toEqual(existingIds)
    expect(wrapper.findAll('[data-testid="excluded-user-chip"]')).toHaveLength(0)
    expect(wrapper.emitted('change')).toBeUndefined()
  })

  it('can hide the excluded user control for consumers that must not expose it', () => {
    const wrapper = mount(UsageFilters, {
      props: {
        modelValue: defaultFilters(),
        exporting: false,
        startDate: '2026-05-01',
        endDate: '2026-05-28',
        showActions: false,
        showExcludedUsers: false,
      },
      global: { stubs: { Select: true, Teleport: true } },
    })

    expect(wrapper.find('[data-testid="excluded-user-filter"]').exists()).toBe(false)
  })
})

describe('UsageFilters — model options come from prop (no dup request)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    mockGetModelStats.mockClear()
    mockGroupsList.mockClear()
  })
  afterEach(() => { vi.useRealTimers() })

  it('does not call dashboard.getModelStats on mount and renders model options from prop', async () => {
    const wrapper = mount(UsageFilters, {
      props: {
        modelValue: defaultFilters(),
        exporting: false,
        startDate: '2026-05-01',
        endDate: '2026-05-28',
        showActions: false,
        modelOptions: ['claude-3', 'gpt-4o'],
      },
      global: { stubs: { Select: true, Teleport: true } },
    })
    await flushPromises()

    expect(mockGetModelStats).not.toHaveBeenCalled()

    const opts = (wrapper.vm as any).modelOptions as Array<{ value: string | null; label: string }>
    expect(opts.map((o) => o.value)).toEqual([null, 'claude-3', 'gpt-4o'])
  })
})

describe('UsageFilters — billing labels', () => {
  it('labels billing_mode as pricing mode to distinguish it from billing type', () => {
    const wrapper = mountFilters()

    expect(wrapper.text()).toContain('Billing Type')
    expect(wrapper.text()).toContain('Pricing Mode')
    expect(wrapper.text()).not.toContain('Billing Mode')
  })
})
