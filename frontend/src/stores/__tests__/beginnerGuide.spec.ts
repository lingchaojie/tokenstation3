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

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

async function flushMicrotasks(): Promise<void> {
  await Promise.resolve()
  await Promise.resolve()
  await Promise.resolve()
}

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

  it('retains anonymous progress and retries the current merged snapshot after account merge persistence fails', async () => {
    const anonymous = progress({ currentStep: 'api_key', completedSteps: ['understand'] })
    localStorage.setItem(ANONYMOUS_PROGRESS_KEY, JSON.stringify(anonymous))
    getBeginnerGuideStateMock.mockResolvedValueOnce(
      state({ progress: progress({ completedSteps: ['choose'] }) })
    )
    patchBeginnerGuideStateMock
      .mockRejectedValueOnce(new Error('offline'))
      .mockImplementationOnce(async (patch: PatchBeginnerGuideStateRequest) =>
        state({ prompt_state: 'suppressed', progress: patch.progress ?? null })
      )

    const store = useBeginnerGuideStore()
    await expect(
      store.initialize({ authenticated: true, userId: 42 })
    ).resolves.toBeUndefined()

    expect(store.progress.completedSteps).toEqual(['understand', 'choose'])
    expect(localStorage.getItem(ANONYMOUS_PROGRESS_KEY)).not.toBeNull()
    expect(localStorage.getItem(retryKey(42))).toBe('1')
    expect(store.persistenceIssue).toBe('save')

    await store.retryPersistence()

    const current = progress({
      currentStep: 'api_key',
      completedSteps: ['understand', 'choose']
    })
    expect(patchBeginnerGuideStateMock).toHaveBeenLastCalledWith({
      prompt_state: 'suppressed',
      progress: current
    })
    expect(store.progress).toEqual(current)
    expect(store.persistenceIssue).toBeNull()
    expect(localStorage.getItem(ANONYMOUS_PROGRESS_KEY)).toBeNull()
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
    expect(store.persistenceIssue).toBe('load')
  })

  it.each(['rejected', 'malformed'] as const)(
    'recovers a %s guide GET on explicit retry without replacing the local snapshot',
    async (failureKind) => {
      const remoteProgress = progress({
        client: 'claude_code',
        os: 'windows',
        currentStep: 'install',
        completedSteps: ['choose']
      })
      if (failureKind === 'rejected') {
        getBeginnerGuideStateMock.mockRejectedValueOnce(new Error('offline'))
      } else {
        getBeginnerGuideStateMock.mockResolvedValueOnce(null as unknown as BeginnerGuideState)
      }
      getBeginnerGuideStateMock.mockResolvedValueOnce(
        state({ prompt_state: 'suppressed', progress: remoteProgress })
      )
      patchBeginnerGuideStateMock.mockImplementation(async (patch: PatchBeginnerGuideStateRequest) =>
        state({ prompt_state: 'suppressed', progress: patch.progress ?? null })
      )
      const store = useBeginnerGuideStore()

      await store.initialize({ authenticated: true, userId: 'user-a' })
      await store.selectClient('codex')
      await store.selectOS('linux')
      await store.goToStep('terminal')
      await store.completeStep('understand')
      const localBeforeRetry = progress({
        client: 'codex',
        os: 'linux',
        currentStep: 'terminal',
        completedSteps: ['understand']
      })
      expect(store.progress).toEqual(localBeforeRetry)
      expect(store.persistenceIssue).toBe('load')

      await store.retryPersistence()

      const merged = progress({
        client: 'codex',
        os: 'linux',
        currentStep: 'terminal',
        completedSteps: ['understand', 'choose']
      })
      expect(store.progress).toEqual(merged)
      expect(patchBeginnerGuideStateMock).toHaveBeenLastCalledWith({ progress: merged })
      expect(store.persistenceIssue).toBeNull()
      expect(store.persistenceRetrying).toBe(false)
    }
  )

  it('exposes retrying while a failed GET retry is pending and applies remote progress when local state is unchanged', async () => {
    const retryResponse = deferred<BeginnerGuideState>()
    const remoteProgress = progress({
      client: 'codex',
      os: 'windows',
      currentStep: 'install',
      completedSteps: ['understand', 'choose', 'terminal']
    })
    getBeginnerGuideStateMock
      .mockRejectedValueOnce(new Error('offline'))
      .mockReturnValueOnce(retryResponse.promise)
    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 'user-a' })

    const retry = store.retryPersistence()
    await flushMicrotasks()
    expect(store.persistenceRetrying).toBe(true)

    retryResponse.resolve(state({ prompt_state: 'suppressed', progress: remoteProgress }))
    await retry

    expect(store.progress).toEqual(remoteProgress)
    expect(store.persistenceIssue).toBeNull()
    expect(store.persistenceRetrying).toBe(false)
    expect(patchBeginnerGuideStateMock).not.toHaveBeenCalled()
  })

  it('returns false after a failed suppression while hiding locally and retrying the account-scoped marker', async () => {
    getBeginnerGuideStateMock.mockResolvedValueOnce(state())
    patchBeginnerGuideStateMock.mockRejectedValueOnce(new Error('offline'))
    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 42 })
    expect(store.showPrompt).toBe(true)

    const suppression = store.suppressPrompt()
    expect(localStorage.getItem(retryKey(42))).toBe('1')
    await expect(suppression).resolves.toBe(false)

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

  it('returns true after suppression is confirmed by the account API', async () => {
    getBeginnerGuideStateMock.mockResolvedValueOnce(state())
    patchBeginnerGuideStateMock.mockResolvedValueOnce(
      state({ prompt_state: 'suppressed' })
    )
    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 42 })

    await expect(store.suppressPrompt()).resolves.toBe(true)

    expect(store.showPrompt).toBe(false)
    expect(store.promptState).toBe('suppressed')
    expect(localStorage.getItem(retryKey(42))).toBeNull()
  })

  it('returns false and schedules retry when suppression succeeds with malformed state', async () => {
    getBeginnerGuideStateMock.mockResolvedValueOnce(state())
    patchBeginnerGuideStateMock.mockResolvedValueOnce(
      null as unknown as BeginnerGuideState
    )
    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 42 })

    await expect(store.suppressPrompt()).resolves.toBe(false)

    expect(store.showPrompt).toBe(false)
    expect(localStorage.getItem(retryKey(42))).toBe('1')
  })

  it('treats an eligible suppression response as unconfirmed and retries after reload without reopening', async () => {
    getBeginnerGuideStateMock.mockResolvedValueOnce(state())
    patchBeginnerGuideStateMock.mockResolvedValueOnce(state())
    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 42 })

    await expect(store.suppressPrompt()).resolves.toBe(false)

    expect(store.promptState).toBe('suppressed')
    expect(store.showPrompt).toBe(false)
    expect(localStorage.getItem(retryKey(42))).toBe('1')

    setActivePinia(createPinia())
    getBeginnerGuideStateMock.mockResolvedValueOnce(state())
    patchBeginnerGuideStateMock.mockResolvedValueOnce(
      state({ prompt_state: 'suppressed' })
    )
    const reloadedStore = useBeginnerGuideStore()

    await reloadedStore.initialize({ authenticated: true, userId: 42 })

    expect(patchBeginnerGuideStateMock).toHaveBeenLastCalledWith({
      prompt_state: 'suppressed'
    })
    expect(reloadedStore.promptState).toBe('suppressed')
    expect(reloadedStore.showPrompt).toBe(false)
    expect(localStorage.getItem(retryKey(42))).toBeNull()
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

  it.each(['rejected', 'malformed'] as const)(
    'preserves local progress and retries the current canonical snapshot after a %s progress PATCH',
    async (failureKind) => {
      getBeginnerGuideStateMock.mockResolvedValueOnce(
        state({ prompt_state: 'suppressed', progress: progress() })
      )
      if (failureKind === 'rejected') {
        patchBeginnerGuideStateMock.mockRejectedValueOnce(new Error('offline'))
      } else {
        patchBeginnerGuideStateMock.mockResolvedValueOnce(null as unknown as BeginnerGuideState)
      }
      const retryResponse = deferred<BeginnerGuideState>()
      patchBeginnerGuideStateMock.mockReturnValueOnce(retryResponse.promise)
      const store = useBeginnerGuideStore()
      await store.initialize({ authenticated: true, userId: 'user-a' })

      await store.completeStep('understand')

      const expected = progress({ completedSteps: ['understand'] })
      expect(store.progress).toEqual(expected)
      expect(store.persistenceIssue).toBe('save')

      const retry = store.retryPersistence()
      await flushMicrotasks()
      expect(store.persistenceRetrying).toBe(true)
      expect(patchBeginnerGuideStateMock).toHaveBeenLastCalledWith({ progress: expected })

      retryResponse.resolve(
        state({ prompt_state: 'suppressed', progress: expected })
      )
      await retry

      expect(store.progress).toEqual(expected)
      expect(store.persistenceIssue).toBeNull()
      expect(store.persistenceRetrying).toBe(false)
    }
  )

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

  it.each(['rejected', 'malformed'] as const)(
    'keeps completion local and retries its completed intent after a %s completion PATCH',
    async (failureKind) => {
      const accountProgress = progress({
        currentStep: 'troubleshoot',
        completedSteps: ['understand', 'troubleshoot']
      })
      getBeginnerGuideStateMock.mockResolvedValueOnce(
        state({ prompt_state: 'suppressed', progress: accountProgress })
      )
      if (failureKind === 'rejected') {
        patchBeginnerGuideStateMock.mockRejectedValueOnce(new Error('offline'))
      } else {
        patchBeginnerGuideStateMock.mockResolvedValueOnce(null as unknown as BeginnerGuideState)
      }
      patchBeginnerGuideStateMock.mockResolvedValueOnce(
        state({
          prompt_state: 'completed',
          progress: accountProgress,
          completed_at: '2026-07-15T00:00:00Z'
        })
      )
      const store = useBeginnerGuideStore()
      await store.initialize({ authenticated: true, userId: 'user-a' })

      await store.completeGuide()

      expect(store.promptState).toBe('completed')
      expect(store.progress).toEqual(accountProgress)
      expect(store.persistenceIssue).toBe('save')

      await store.retryPersistence()

      expect(patchBeginnerGuideStateMock).toHaveBeenLastCalledWith({
        prompt_state: 'completed',
        progress: accountProgress
      })
      expect(store.promptState).toBe('completed')
      expect(store.completedAt).toBe('2026-07-15T00:00:00Z')
      expect(store.persistenceIssue).toBeNull()
    }
  )

  it('preserves a failed completion intent when a same-owner refresh also fails before retry', async () => {
    const accountProgress = progress({
      currentStep: 'troubleshoot',
      completedSteps: ['understand', 'troubleshoot']
    })
    getBeginnerGuideStateMock
      .mockResolvedValueOnce(
        state({ prompt_state: 'suppressed', progress: accountProgress })
      )
      .mockRejectedValueOnce(new Error('refresh offline'))
      .mockResolvedValueOnce(
        state({ prompt_state: 'suppressed', progress: accountProgress })
      )
    patchBeginnerGuideStateMock
      .mockRejectedValueOnce(new Error('completion offline'))
      .mockImplementationOnce(async (patch: PatchBeginnerGuideStateRequest) =>
        state({
          prompt_state: patch.prompt_state ?? 'suppressed',
          progress: patch.progress ?? null,
          completed_at:
            patch.prompt_state === 'completed' ? '2026-07-15T00:00:00Z' : null
        })
      )
    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 'user-a' })
    await store.completeGuide()
    expect(store.persistenceIssue).toBe('save')

    await store.initialize({ authenticated: true, userId: 'user-a' })
    expect(store.persistenceIssue).toBe('load')
    expect(store.promptState).toBe('completed')

    await store.retryPersistence()

    expect(patchBeginnerGuideStateMock).toHaveBeenLastCalledWith({
      prompt_state: 'completed',
      progress: accountProgress
    })
    expect(store.promptState).toBe('completed')
    expect(store.completedAt).toBe('2026-07-15T00:00:00Z')
    expect(store.persistenceIssue).toBeNull()
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

  it('discards a delayed user-A initialization after user B becomes current', async () => {
    const userAResponse = deferred<BeginnerGuideState>()
    const userBProgress = progress({
      client: 'codex',
      os: 'linux',
      currentStep: 'install',
      completedSteps: ['understand', 'terminal']
    })
    getBeginnerGuideStateMock
      .mockReturnValueOnce(userAResponse.promise)
      .mockResolvedValueOnce(
        state({ prompt_state: 'suppressed', progress: userBProgress })
      )
    const store = useBeginnerGuideStore()

    const initializeA = store.initialize({ authenticated: true, userId: 'user-a' })
    await store.initialize({ authenticated: true, userId: 'user-b' })
    userAResponse.resolve(
      state({
        prompt_state: 'completed',
        progress: progress({
          os: 'windows',
          currentStep: 'troubleshoot',
          completedSteps: ['troubleshoot']
        }),
        completed_at: '2026-07-15T00:00:00Z'
      })
    )
    await initializeA

    expect(store.progress).toEqual(userBProgress)
    expect(store.promptState).toBe('suppressed')
    expect(store.completedAt).toBeNull()
  })

  it('clears an owner warning on account switch and ignores the stale owner retry result', async () => {
    const userARetry = deferred<BeginnerGuideState>()
    const userBProgress = progress({
      client: 'codex',
      os: 'linux',
      currentStep: 'terminal',
      completedSteps: ['understand', 'choose']
    })
    getBeginnerGuideStateMock
      .mockRejectedValueOnce(new Error('user A offline'))
      .mockReturnValueOnce(userARetry.promise)
      .mockResolvedValueOnce(
        state({ prompt_state: 'suppressed', progress: userBProgress })
      )
    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 'user-a' })
    expect(store.persistenceIssue).toBe('load')

    const retryA = store.retryPersistence()
    await flushMicrotasks()
    expect(store.persistenceRetrying).toBe(true)

    await store.initialize({ authenticated: true, userId: 'user-b' })
    expect(store.progress).toEqual(userBProgress)
    expect(store.persistenceIssue).toBeNull()
    expect(store.persistenceRetrying).toBe(false)

    userARetry.reject(new Error('still offline'))
    await retryA

    expect(store.progress).toEqual(userBProgress)
    expect(store.persistenceIssue).toBeNull()
    expect(store.persistenceRetrying).toBe(false)
  })

  it('does not let a pending user-A progress save overwrite logout state or remove a new anonymous copy', async () => {
    const userAProgress = progress({ currentStep: 'terminal' })
    const anonymous = progress({
      client: 'codex',
      os: 'windows',
      currentStep: 'api_key',
      completedSteps: ['understand', 'choose']
    })
    const saveResponse = deferred<BeginnerGuideState>()
    getBeginnerGuideStateMock.mockResolvedValueOnce(
      state({ prompt_state: 'suppressed', progress: userAProgress })
    )
    patchBeginnerGuideStateMock.mockReturnValueOnce(saveResponse.promise)
    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 'user-a' })

    const pendingSave = store.completeStep('understand')
    await flushMicrotasks()
    expect(patchBeginnerGuideStateMock).toHaveBeenCalledTimes(1)
    localStorage.setItem(ANONYMOUS_PROGRESS_KEY, JSON.stringify(anonymous))
    await store.initialize({ authenticated: false, userId: null })
    saveResponse.reject(new Error('user A save failed'))
    await pendingSave

    expect(store.progress).toEqual(anonymous)
    expect(JSON.parse(localStorage.getItem(ANONYMOUS_PROGRESS_KEY)!)).toEqual(anonymous)
    expect(store.persistenceIssue).toBeNull()
  })

  it('records a failed pending suppression only for its initiating account', async () => {
    const suppressionResponse = deferred<BeginnerGuideState>()
    getBeginnerGuideStateMock
      .mockResolvedValueOnce(state())
      .mockResolvedValueOnce(state({ prompt_state: 'suppressed' }))
    patchBeginnerGuideStateMock.mockReturnValueOnce(suppressionResponse.promise)
    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 'user-a' })

    const pendingSuppression = store.suppressPrompt()
    await flushMicrotasks()
    expect(patchBeginnerGuideStateMock).toHaveBeenCalledTimes(1)
    await store.initialize({ authenticated: true, userId: 'user-b' })
    suppressionResponse.reject(new Error('offline'))
    await expect(pendingSuppression).resolves.toBe(true)

    expect(localStorage.getItem(retryKey('user-a'))).toBe('1')
    expect(localStorage.getItem(retryKey('user-b'))).toBeNull()
    expect(store.promptState).toBe('suppressed')
    expect(store.persistenceIssue).toBeNull()
  })

  it('retries on immediate same-owner initialization without letting the older success clear the newer attempt', async () => {
    const firstSuppression = deferred<BeginnerGuideState>()
    const retrySuppression = deferred<BeginnerGuideState>()
    getBeginnerGuideStateMock
      .mockResolvedValueOnce(state())
      .mockResolvedValueOnce(state())
    patchBeginnerGuideStateMock
      .mockReturnValueOnce(firstSuppression.promise)
      .mockReturnValueOnce(retrySuppression.promise)
    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 'user-a' })

    const pendingSuppression = store.suppressPrompt()
    await flushMicrotasks()
    const pendingReinitialize = store.initialize({ authenticated: true, userId: 'user-a' })
    await flushMicrotasks()

    expect(store.showPrompt).toBe(false)
    expect(localStorage.getItem(retryKey('user-a'))).toBe('1')

    firstSuppression.resolve(state({ prompt_state: 'suppressed' }))
    await expect(pendingSuppression).resolves.toBe(true)
    await flushMicrotasks()

    expect(patchBeginnerGuideStateMock).toHaveBeenCalledTimes(2)
    expect(localStorage.getItem(retryKey('user-a'))).toBe('1')

    retrySuppression.resolve(state({ prompt_state: 'suppressed' }))
    await pendingReinitialize
    expect(localStorage.getItem(retryKey('user-a'))).toBeNull()
    expect(store.showPrompt).toBe(false)
  })

  it('does not reopen on A-B-A and an older success cannot clear the returning owner retry', async () => {
    const firstSuppression = deferred<BeginnerGuideState>()
    const retrySuppression = deferred<BeginnerGuideState>()
    getBeginnerGuideStateMock
      .mockResolvedValueOnce(state())
      .mockResolvedValueOnce(state({ prompt_state: 'suppressed' }))
      .mockResolvedValueOnce(state())
    patchBeginnerGuideStateMock
      .mockReturnValueOnce(firstSuppression.promise)
      .mockReturnValueOnce(retrySuppression.promise)
    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 'user-a' })

    const pendingSuppression = store.suppressPrompt()
    await flushMicrotasks()
    await store.initialize({ authenticated: true, userId: 'user-b' })
    const pendingReturn = store.initialize({ authenticated: true, userId: 'user-a' })
    await flushMicrotasks()

    expect(store.showPrompt).toBe(false)
    expect(localStorage.getItem(retryKey('user-a'))).toBe('1')

    firstSuppression.resolve(state({ prompt_state: 'suppressed' }))
    await expect(pendingSuppression).resolves.toBe(true)
    await flushMicrotasks()

    expect(patchBeginnerGuideStateMock).toHaveBeenCalledTimes(2)
    expect(localStorage.getItem(retryKey('user-a'))).toBe('1')

    retrySuppression.reject(new Error('offline'))
    await pendingReturn
    expect(localStorage.getItem(retryKey('user-a'))).toBe('1')
    expect(store.showPrompt).toBe(false)
  })

  it('does not let pending user-A completion mutate user B or clear B retry state', async () => {
    const completionResponse = deferred<BeginnerGuideState>()
    const userBProgress = progress({
      client: 'codex',
      currentStep: 'choose',
      completedSteps: ['understand']
    })
    getBeginnerGuideStateMock
      .mockResolvedValueOnce(
        state({ prompt_state: 'suppressed', progress: progress({ currentStep: 'first_run' }) })
      )
      .mockResolvedValueOnce(
        state({ prompt_state: 'suppressed', progress: userBProgress })
      )
    patchBeginnerGuideStateMock.mockReturnValueOnce(completionResponse.promise)
    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 'user-a' })

    const pendingCompletion = store.completeGuide()
    await flushMicrotasks()
    expect(patchBeginnerGuideStateMock).toHaveBeenCalledTimes(1)
    await store.initialize({ authenticated: true, userId: 'user-b' })
    localStorage.setItem(retryKey('user-b'), '1')
    completionResponse.reject(new Error('user A completion failed'))
    await pendingCompletion

    expect(store.progress).toEqual(userBProgress)
    expect(store.promptState).toBe('suppressed')
    expect(localStorage.getItem(retryKey('user-b'))).toBe('1')
    expect(store.persistenceIssue).toBeNull()
  })

  it('serializes same-account snapshots and keeps newer optimistic progress while the older save finishes', async () => {
    const firstSaveResponse = deferred<BeginnerGuideState>()
    const secondSaveResponse = deferred<BeginnerGuideState>()
    getBeginnerGuideStateMock.mockResolvedValueOnce(
      state({ prompt_state: 'suppressed', progress: progress() })
    )
    patchBeginnerGuideStateMock
      .mockReturnValueOnce(firstSaveResponse.promise)
      .mockReturnValueOnce(secondSaveResponse.promise)
    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 'user-a' })

    const firstSave = store.completeStep('understand')
    const secondSave = store.completeStep('choose')
    await flushMicrotasks()

    expect(patchBeginnerGuideStateMock).toHaveBeenCalledTimes(1)
    expect(patchBeginnerGuideStateMock).toHaveBeenNthCalledWith(1, {
      progress: progress({ completedSteps: ['understand'] })
    })

    firstSaveResponse.resolve(
      state({
        prompt_state: 'suppressed',
        progress: progress({ completedSteps: ['understand'] })
      })
    )
    await firstSave
    await flushMicrotasks()

    expect(store.progress.completedSteps).toEqual(['understand', 'choose'])
    expect(patchBeginnerGuideStateMock).toHaveBeenCalledTimes(2)
    expect(patchBeginnerGuideStateMock).toHaveBeenNthCalledWith(2, {
      progress: progress({ completedSteps: ['understand', 'choose'] })
    })

    secondSaveResponse.resolve(
      state({
        prompt_state: 'suppressed',
        progress: progress({ completedSteps: ['understand', 'choose'] })
      })
    )
    await secondSave

    expect(store.progress.completedSteps).toEqual(['understand', 'choose'])
  })

  it('does not let an older progress acknowledgement undo newer optimistic suppression', async () => {
    const progressResponse = deferred<BeginnerGuideState>()
    const suppressionResponse = deferred<BeginnerGuideState>()
    getBeginnerGuideStateMock.mockResolvedValueOnce(
      state({ prompt_state: 'eligible', progress: progress() })
    )
    patchBeginnerGuideStateMock
      .mockReturnValueOnce(progressResponse.promise)
      .mockReturnValueOnce(suppressionResponse.promise)
    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 'user-a' })

    const pendingProgress = store.completeStep('understand')
    await flushMicrotasks()
    const pendingSuppression = store.suppressPrompt()
    expect(store.promptState).toBe('suppressed')

    progressResponse.resolve(
      state({
        prompt_state: 'eligible',
        progress: progress({ completedSteps: ['understand'] })
      })
    )
    await pendingProgress
    await flushMicrotasks()

    expect(store.promptState).toBe('suppressed')
    expect(patchBeginnerGuideStateMock).toHaveBeenCalledTimes(2)

    suppressionResponse.resolve(
      state({
        prompt_state: 'suppressed',
        progress: progress({ completedSteps: ['understand'] })
      })
    )
    await pendingSuppression
  })

  it.each([null, undefined])(
    'fails closed when a successful guide GET returns malformed top-level data: %s',
    async (malformed) => {
      getBeginnerGuideStateMock.mockResolvedValueOnce(
        malformed as unknown as BeginnerGuideState
      )
      const store = useBeginnerGuideStore()

      await expect(
        store.initialize({ authenticated: true, userId: 'user-a' })
      ).resolves.toBeUndefined()

      expect(store.showPrompt).toBe(false)
      expect(patchBeginnerGuideStateMock).not.toHaveBeenCalled()
    }
  )

  it('keeps a queued same-account mutation persistable across reinitialization', async () => {
    getBeginnerGuideStateMock
      .mockResolvedValueOnce(
        state({ prompt_state: 'suppressed', progress: progress({ currentStep: 'terminal' }) })
      )
      .mockRejectedValueOnce(new Error('refresh failed'))
    patchBeginnerGuideStateMock.mockImplementationOnce(
      async (patch: PatchBeginnerGuideStateRequest) =>
        state({ prompt_state: 'suppressed', progress: patch.progress ?? null })
    )
    const store = useBeginnerGuideStore()
    await store.initialize({ authenticated: true, userId: 'user-a' })

    const pendingSave = store.completeStep('understand')
    const pendingReinitialize = store.initialize({ authenticated: true, userId: 'user-a' })
    await Promise.all([pendingSave, pendingReinitialize])

    expect(patchBeginnerGuideStateMock).toHaveBeenCalledWith({
      progress: progress({ currentStep: 'terminal', completedSteps: ['understand'] })
    })
    expect(store.progress).toEqual(
      progress({ currentStep: 'terminal', completedSteps: ['understand'] })
    )
  })

  it.each(['failed', 'malformed'] as const)(
    'preserves same-account progress through a %s repeated-initialize GET before the next save',
    async (responseKind) => {
      const existingProgress = progress({
        client: 'codex',
        os: 'linux',
        currentStep: 'install',
        completedSteps: ['understand', 'terminal']
      })
      getBeginnerGuideStateMock.mockResolvedValueOnce(
        state({ prompt_state: 'suppressed', progress: existingProgress })
      )
      if (responseKind === 'failed') {
        getBeginnerGuideStateMock.mockRejectedValueOnce(new Error('refresh failed'))
      } else {
        getBeginnerGuideStateMock.mockResolvedValueOnce(null as unknown as BeginnerGuideState)
      }
      patchBeginnerGuideStateMock.mockImplementationOnce(
        async (patch: PatchBeginnerGuideStateRequest) =>
          state({ prompt_state: 'suppressed', progress: patch.progress ?? null })
      )
      const store = useBeginnerGuideStore()
      await store.initialize({ authenticated: true, userId: 'user-a' })

      await store.initialize({ authenticated: true, userId: 'user-a' })
      await store.completeStep('choose')

      const expected = progress({
        client: 'codex',
        os: 'linux',
        currentStep: 'install',
        completedSteps: ['understand', 'choose', 'terminal']
      })
      expect(patchBeginnerGuideStateMock).toHaveBeenCalledWith({ progress: expected })
      expect(store.progress).toEqual(expected)
    }
  )
})
