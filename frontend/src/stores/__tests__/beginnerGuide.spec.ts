import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import type {
  BeginnerGuideProgressV1,
  BeginnerGuideState,
  PatchBeginnerGuideStateRequest
} from '@/api/beginnerGuide'
import { useBeginnerGuideStore } from '../beginnerGuide'

const {
  apiClientGetMock,
  apiClientPatchMock,
  getBeginnerGuideStateMock,
  patchBeginnerGuideStateMock
} = vi.hoisted(() => ({
  apiClientGetMock: vi.fn(),
  apiClientPatchMock: vi.fn(),
  getBeginnerGuideStateMock: vi.fn(),
  patchBeginnerGuideStateMock: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: {
    get: apiClientGetMock,
    patch: apiClientPatchMock
  }
}))

vi.mock('@/api/beginnerGuide', async () => {
  const actual = await vi.importActual<typeof import('@/api/beginnerGuide')>(
    '@/api/beginnerGuide'
  )
  return {
    ...actual,
    getBeginnerGuideState: getBeginnerGuideStateMock,
    patchBeginnerGuideState: patchBeginnerGuideStateMock
  }
})

const ANONYMOUS_PROGRESS_KEY = 'beginner_guide_progress_v1'
const retryKey = (userId: number | string) => `beginner_guide_prompt_retry_v1:${userId}`

const defaultProgress = (): BeginnerGuideProgressV1 => ({
  version: 1,
  client: 'claude_code',
  os: 'macos',
  currentStep: 'understand',
  completedSteps: []
})

const progress = (
  overrides: Partial<BeginnerGuideProgressV1> = {}
): BeginnerGuideProgressV1 => ({
  ...defaultProgress(),
  ...overrides
})

const state = (overrides: Partial<BeginnerGuideState> = {}): BeginnerGuideState => ({
  prompt_state: 'eligible',
  progress: null,
  completed_at: null,
  ...overrides
})

describe('beginner guide API', () => {
  it('uses the authenticated guide endpoints and unwraps response data', async () => {
    const remote = state()
    const patch: PatchBeginnerGuideStateRequest = { prompt_state: 'suppressed' }
    apiClientGetMock.mockResolvedValueOnce({ data: remote })
    apiClientPatchMock.mockResolvedValueOnce({ data: state({ prompt_state: 'suppressed' }) })
    const api = await vi.importActual<typeof import('@/api/beginnerGuide')>(
      '@/api/beginnerGuide'
    )

    await expect(api.getBeginnerGuideState()).resolves.toEqual(remote)
    await expect(api.patchBeginnerGuideState(patch)).resolves.toEqual(
      state({ prompt_state: 'suppressed' })
    )
    expect(apiClientGetMock).toHaveBeenCalledWith('/user/beginner-guide')
    expect(apiClientPatchMock).toHaveBeenCalledWith('/user/beginner-guide', patch)
  })
})

describe('useBeginnerGuideStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('removes invalid local JSON and falls back to safe progress', async () => {
    localStorage.setItem(ANONYMOUS_PROGRESS_KEY, '{invalid-json')

    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: false, userId: null, enteringGuide: true })

    expect(store.progress).toEqual(defaultProgress())
    expect(localStorage.getItem(ANONYMOUS_PROGRESS_KEY)).toBeNull()
    expect(store.showPrompt).toBe(false)
  })

  it('serializes exactly the five allowed progress fields from hostile input', async () => {
    localStorage.setItem(
      ANONYMOUS_PROGRESS_KEY,
      JSON.stringify({
        ...progress({ currentStep: 'install', completedSteps: ['choose', 'understand'] }),
        api_key: 'sk-secret-1',
        apiKey: 'sk-secret-2',
        key: 'sk-secret-3',
        selectedKeyId: 99
      })
    )

    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: false, userId: null })
    await store.completeStep('terminal')

    const serialized = localStorage.getItem(ANONYMOUS_PROGRESS_KEY)
    expect(serialized).not.toBeNull()
    const saved = JSON.parse(serialized!) as Record<string, unknown>
    expect(Object.keys(saved).sort()).toEqual(
      ['client', 'completedSteps', 'currentStep', 'os', 'version'].sort()
    )
    expect(saved).toEqual(
      progress({
        currentStep: 'install',
        completedSteps: ['understand', 'choose', 'terminal']
      })
    )
    expect(serialized).not.toMatch(/api_key|apiKey|selectedKeyId|sk-secret|"key"/)
  })

  it.each([
    ['client', () => useBeginnerGuideStore().selectClient('codex')],
    ['operating system', () => useBeginnerGuideStore().selectOS('linux')]
  ])(
    'changing the %s preserves invariant completion and invalidates selection-specific steps',
    async (_label, changeSelection) => {
      localStorage.setItem(
        ANONYMOUS_PROGRESS_KEY,
        JSON.stringify(
          progress({
            currentStep: 'configure',
            completedSteps: [
              'understand',
              'choose',
              'terminal',
              'install',
              'api_key',
              'configure',
              'first_run',
              'troubleshoot'
            ]
          })
        )
      )
      const store = useBeginnerGuideStore()
      await store.initialize({ authenticated: false, userId: null })

      await changeSelection()

      expect(store.progress.currentStep).toBe('terminal')
      expect(store.progress.completedSteps).toEqual(['understand', 'choose', 'api_key'])
    }
  )

  it('merges account and anonymous progress in curriculum order while preferring the active anonymous flow', async () => {
    localStorage.setItem(
      ANONYMOUS_PROGRESS_KEY,
      JSON.stringify(
        progress({
          client: 'claude_code',
          os: 'linux',
          currentStep: 'configure',
          completedSteps: ['troubleshoot', 'api_key', 'understand']
        })
      )
    )
    getBeginnerGuideStateMock.mockResolvedValueOnce(
      state({
        progress: progress({
          client: 'codex',
          os: 'windows',
          currentStep: 'terminal',
          completedSteps: ['first_run', 'install', 'terminal']
        })
      })
    )
    patchBeginnerGuideStateMock.mockImplementationOnce(
      async (patch: PatchBeginnerGuideStateRequest) =>
        state({ prompt_state: 'suppressed', progress: patch.progress ?? null })
    )

    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 42 })

    const merged = progress({
      client: 'claude_code',
      os: 'linux',
      currentStep: 'configure',
      completedSteps: [
        'understand',
        'terminal',
        'install',
        'api_key',
        'first_run',
        'troubleshoot'
      ]
    })
    expect(store.progress).toEqual(merged)
    expect(patchBeginnerGuideStateMock).toHaveBeenCalledWith({
      prompt_state: 'suppressed',
      progress: merged
    })
    expect(localStorage.getItem(ANONYMOUS_PROGRESS_KEY)).toBeNull()
  })

  it('normalizes hostile progress before an authenticated merge leaves the browser', async () => {
    localStorage.setItem(
      ANONYMOUS_PROGRESS_KEY,
      JSON.stringify({ ...progress(), apiKey: 'local-secret', selectedKeyId: 7 })
    )
    getBeginnerGuideStateMock.mockResolvedValueOnce(
      state({
        progress: {
          ...progress({ client: 'codex', completedSteps: ['terminal'] }),
          api_key: 'remote-secret',
          key: 'remote-secret-2'
        } as BeginnerGuideProgressV1
      })
    )
    patchBeginnerGuideStateMock.mockImplementationOnce(
      async (patch: PatchBeginnerGuideStateRequest) =>
        state({ prompt_state: 'suppressed', progress: patch.progress ?? null })
    )

    await useBeginnerGuideStore().initialize({ authenticated: true, userId: 42 })

    const outgoing = patchBeginnerGuideStateMock.mock.calls[0]?.[0]
    expect(Object.keys(outgoing.progress).sort()).toEqual(
      ['client', 'completedSteps', 'currentStep', 'os', 'version'].sort()
    )
    expect(JSON.stringify(outgoing)).not.toMatch(
      /api_key|apiKey|selectedKeyId|local-secret|remote-secret|"key"/
    )
  })

  it('retains anonymous progress and remains usable when account merge persistence fails', async () => {
    const anonymous = progress({ currentStep: 'api_key', completedSteps: ['understand'] })
    localStorage.setItem(ANONYMOUS_PROGRESS_KEY, JSON.stringify(anonymous))
    getBeginnerGuideStateMock.mockResolvedValueOnce(
      state({ progress: progress({ completedSteps: ['choose'] }) })
    )
    patchBeginnerGuideStateMock.mockRejectedValue(new Error('offline'))

    const store = useBeginnerGuideStore()
    await expect(
      store.initialize({ authenticated: true, userId: 42 })
    ).resolves.toBeUndefined()
    await expect(store.completeStep('api_key')).resolves.toBeUndefined()

    expect(store.progress.completedSteps).toEqual(['understand', 'choose', 'api_key'])
    expect(localStorage.getItem(ANONYMOUS_PROGRESS_KEY)).not.toBeNull()
    expect(localStorage.getItem(retryKey(42))).toBe('1')
  })

  it('keeps in-memory progress usable when browser persistence throws', async () => {
    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: false, userId: null })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementationOnce(() => {
      throw new Error('quota exceeded')
    })

    await expect(store.completeStep('understand')).resolves.toBeUndefined()

    expect(store.progress.completedSteps).toEqual(['understand'])
  })

  it('retains parsed anonymous progress when its canonical rewrite cannot be persisted', async () => {
    const anonymous = progress({
      client: 'codex',
      os: 'linux',
      currentStep: 'install',
      completedSteps: ['understand', 'terminal']
    })
    localStorage.setItem(ANONYMOUS_PROGRESS_KEY, JSON.stringify(anonymous))
    vi.spyOn(Storage.prototype, 'setItem').mockImplementationOnce(() => {
      throw new Error('quota exceeded')
    })

    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: false, userId: null })

    expect(store.progress).toEqual(anonymous)
    expect(localStorage.getItem(ANONYMOUS_PROGRESS_KEY)).not.toBeNull()
  })

  it('fails closed without a welcome popup when prompt state cannot be fetched', async () => {
    getBeginnerGuideStateMock.mockRejectedValueOnce(new Error('network'))
    const store = useBeginnerGuideStore()

    await expect(
      store.initialize({ authenticated: true, userId: 42 })
    ).resolves.toBeUndefined()

    expect(store.showPrompt).toBe(false)
    expect(patchBeginnerGuideStateMock).not.toHaveBeenCalled()
  })

  it('hides failed suppression locally and retries its account-scoped marker on next initialization', async () => {
    getBeginnerGuideStateMock.mockResolvedValueOnce(state())
    patchBeginnerGuideStateMock.mockRejectedValueOnce(new Error('offline'))
    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 42 })
    expect(store.showPrompt).toBe(true)

    await expect(store.suppressPrompt()).resolves.toBeUndefined()

    expect(store.showPrompt).toBe(false)
    expect(store.promptState).toBe('suppressed')
    expect(localStorage.getItem(retryKey(42))).toBe('1')
    expect(localStorage.getItem(retryKey(99))).toBeNull()

    setActivePinia(createPinia())
    getBeginnerGuideStateMock.mockResolvedValueOnce(state())
    patchBeginnerGuideStateMock.mockResolvedValueOnce(state({ prompt_state: 'suppressed' }))
    const retriedStore = useBeginnerGuideStore()

    await retriedStore.initialize({ authenticated: true, userId: 42 })

    expect(patchBeginnerGuideStateMock).toHaveBeenLastCalledWith({
      prompt_state: 'suppressed'
    })
    expect(localStorage.getItem(retryKey(42))).toBeNull()
    expect(retriedStore.showPrompt).toBe(false)
  })

  it('suppresses an eligible prompt when an authenticated user enters the guide without anonymous progress', async () => {
    getBeginnerGuideStateMock.mockResolvedValueOnce(state())
    patchBeginnerGuideStateMock.mockResolvedValueOnce(state({ prompt_state: 'suppressed' }))

    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 42, enteringGuide: true })

    expect(patchBeginnerGuideStateMock).toHaveBeenCalledWith({
      prompt_state: 'suppressed'
    })
    expect(store.promptState).toBe('suppressed')
    expect(store.showPrompt).toBe(false)
  })

  it('syncs authenticated progress changes without importing an auth store', async () => {
    const accountProgress = progress()
    getBeginnerGuideStateMock.mockResolvedValueOnce(
      state({ prompt_state: 'suppressed', progress: accountProgress })
    )
    patchBeginnerGuideStateMock.mockImplementationOnce(
      async (patch: PatchBeginnerGuideStateRequest) =>
        state({ prompt_state: 'suppressed', progress: patch.progress ?? null })
    )
    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 42 })

    await store.completeStep('understand')

    expect(patchBeginnerGuideStateMock).toHaveBeenCalledWith({
      progress: progress({ completedSteps: ['understand'] })
    })
  })

  it('marks completion locally, sends only normalized progress, and never downgrades completed', async () => {
    const accountProgress = progress({
      currentStep: 'troubleshoot',
      completedSteps: ['understand', 'api_key']
    })
    getBeginnerGuideStateMock.mockResolvedValueOnce(
      state({ prompt_state: 'suppressed', progress: accountProgress })
    )
    patchBeginnerGuideStateMock.mockResolvedValueOnce(
      state({
        prompt_state: 'completed',
        progress: accountProgress,
        completed_at: '2026-07-15T00:00:00Z'
      })
    )
    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 42 })

    await store.completeGuide()

    expect(patchBeginnerGuideStateMock).toHaveBeenCalledWith({
      prompt_state: 'completed',
      progress: accountProgress
    })
    const completionPayload = patchBeginnerGuideStateMock.mock.calls[0]?.[0]
    expect(Object.keys(completionPayload.progress).sort()).toEqual(
      ['client', 'completedSteps', 'currentStep', 'os', 'version'].sort()
    )
    expect(store.promptState).toBe('completed')
    expect(store.showPrompt).toBe(false)

    patchBeginnerGuideStateMock.mockClear()
    await store.suppressPrompt()
    expect(store.promptState).toBe('completed')
    expect(patchBeginnerGuideStateMock).not.toHaveBeenCalled()
  })

  it('does not downgrade completed during repeated initialization for the same account', async () => {
    getBeginnerGuideStateMock
      .mockResolvedValueOnce(
        state({
          prompt_state: 'completed',
          completed_at: '2026-07-15T00:00:00Z'
        })
      )
      .mockResolvedValueOnce(state({ prompt_state: 'eligible' }))
    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 42 })
    expect(store.promptState).toBe('completed')

    await store.initialize({ authenticated: true, userId: 42 })

    expect(store.promptState).toBe('completed')
    expect(store.showPrompt).toBe(false)
  })
})
