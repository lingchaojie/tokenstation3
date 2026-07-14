import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  BEGINNER_GUIDE_STEP_ORDER,
  getBeginnerGuideState,
  patchBeginnerGuideState,
  type BeginnerGuideClient,
  type BeginnerGuideOS,
  type BeginnerGuideProgressV1,
  type BeginnerGuidePromptState,
  type BeginnerGuideState,
  type BeginnerGuideStepId
} from '@/api/beginnerGuide'

const ANONYMOUS_PROGRESS_KEY = 'beginner_guide_progress_v1'
const PROMPT_RETRY_KEY_PREFIX = 'beginner_guide_prompt_retry_v1:'
const SELECTION_INVARIANT_STEPS = new Set<BeginnerGuideStepId>([
  'understand',
  'choose',
  'api_key'
])
const SELECTION_SPECIFIC_STEPS = new Set<BeginnerGuideStepId>([
  'terminal',
  'install',
  'configure',
  'first_run',
  'troubleshoot'
])
const STEP_IDS = new Set<string>(BEGINNER_GUIDE_STEP_ORDER)
const CLIENT_IDS = new Set<string>(['claude_code', 'codex'])
const OS_IDS = new Set<string>(['macos', 'windows', 'linux'])

export type BeginnerGuideInitialization =
  | {
      authenticated: false
      userId: null
      enteringGuide?: boolean
    }
  | {
      authenticated: true
      userId: number | string
      enteringGuide?: boolean
    }

function defaultProgress(): BeginnerGuideProgressV1 {
  return {
    version: 1,
    client: 'claude_code',
    os: 'macos',
    currentStep: 'understand',
    completedSteps: []
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isStepId(value: unknown): value is BeginnerGuideStepId {
  return typeof value === 'string' && STEP_IDS.has(value)
}

function isClient(value: unknown): value is BeginnerGuideClient {
  return typeof value === 'string' && CLIENT_IDS.has(value)
}

function isOS(value: unknown): value is BeginnerGuideOS {
  return typeof value === 'string' && OS_IDS.has(value)
}

function normalizePromptState(value: unknown): BeginnerGuidePromptState | null {
  if (value === 'eligible' || value === 'suppressed' || value === 'completed') {
    return value
  }
  return null
}

function normalizeProgress(value: unknown): BeginnerGuideProgressV1 | null {
  if (!isRecord(value)) {
    return null
  }
  if (
    value.version !== 1 ||
    !isClient(value.client) ||
    !isOS(value.os) ||
    !isStepId(value.currentStep) ||
    !Array.isArray(value.completedSteps) ||
    !value.completedSteps.every(isStepId)
  ) {
    return null
  }

  const completed = new Set<BeginnerGuideStepId>(value.completedSteps)
  return {
    version: 1,
    client: value.client,
    os: value.os,
    currentStep: value.currentStep,
    completedSteps: BEGINNER_GUIDE_STEP_ORDER.filter((step) => completed.has(step))
  }
}

function canonicalProgress(value: BeginnerGuideProgressV1): BeginnerGuideProgressV1 {
  return normalizeProgress(value) ?? defaultProgress()
}

function readAnonymousProgress(): BeginnerGuideProgressV1 | null {
  let raw: string | null
  try {
    raw = localStorage.getItem(ANONYMOUS_PROGRESS_KEY)
  } catch {
    return null
  }
  if (raw === null) {
    return null
  }

  let parsed: unknown
  try {
    parsed = JSON.parse(raw) as unknown
  } catch {
    try {
      localStorage.removeItem(ANONYMOUS_PROGRESS_KEY)
    } catch {
      // Browser persistence is best-effort.
    }
    return null
  }

  const normalized = normalizeProgress(parsed)
  if (normalized === null) {
    try {
      localStorage.removeItem(ANONYMOUS_PROGRESS_KEY)
    } catch {
      // Browser persistence is best-effort.
    }
    return null
  }

  try {
    localStorage.setItem(ANONYMOUS_PROGRESS_KEY, JSON.stringify(normalized))
  } catch {
    // Keep the already parsed copy usable when a canonical rewrite fails.
  }
  return normalized
}

function writeAnonymousProgress(value: BeginnerGuideProgressV1): void {
  try {
    localStorage.setItem(ANONYMOUS_PROGRESS_KEY, JSON.stringify(canonicalProgress(value)))
  } catch {
    // Progress persistence must never block the guide.
  }
}

function removeAnonymousProgress(): void {
  try {
    localStorage.removeItem(ANONYMOUS_PROGRESS_KEY)
  } catch {
    // Progress persistence must never block the guide.
  }
}

function retryKey(userId: number | string): string {
  return `${PROMPT_RETRY_KEY_PREFIX}${String(userId)}`
}

function hasPromptRetry(userId: number | string): boolean {
  try {
    return localStorage.getItem(retryKey(userId)) === '1'
  } catch {
    return false
  }
}

function setPromptRetry(userId: number | string): void {
  try {
    localStorage.setItem(retryKey(userId), '1')
  } catch {
    // Prompt suppression remains local even when retry persistence fails.
  }
}

function clearPromptRetry(userId: number | string): void {
  try {
    localStorage.removeItem(retryKey(userId))
  } catch {
    // A stale retry is safe because suppression is idempotent.
  }
}

function mergeProgress(
  account: BeginnerGuideProgressV1 | null,
  anonymous: BeginnerGuideProgressV1
): BeginnerGuideProgressV1 {
  const completed = new Set<BeginnerGuideStepId>([
    ...(account?.completedSteps ?? []),
    ...anonymous.completedSteps
  ])
  return {
    version: 1,
    client: anonymous.client,
    os: anonymous.os,
    currentStep: anonymous.currentStep,
    completedSteps: BEGINNER_GUIDE_STEP_ORDER.filter((step) => completed.has(step))
  }
}

export const useBeginnerGuideStore = defineStore('beginnerGuide', () => {
  const progress = ref<BeginnerGuideProgressV1>(defaultProgress())
  const promptState = ref<BeginnerGuidePromptState>('suppressed')
  const completedAt = ref<string | null>(null)
  const showPrompt = ref(false)

  let authenticated = false
  let accountUserId: number | string | null = null
  let hasAnonymousProgress = false
  let stateOwner: string | null = null

  function setPromptState(next: BeginnerGuidePromptState): void {
    if (promptState.value === 'completed' && next !== 'completed') {
      return
    }
    promptState.value = next
  }

  function applyRemoteState(remote: unknown, fallback: BeginnerGuideProgressV1): void {
    if (!isRecord(remote)) {
      return
    }
    const remotePrompt = normalizePromptState(remote.prompt_state)
    if (remotePrompt !== null) {
      setPromptState(remotePrompt)
    }
    const remoteProgress = normalizeProgress(remote.progress)
    progress.value = remoteProgress ?? canonicalProgress(fallback)
    if (typeof remote.completed_at === 'string') {
      completedAt.value = remote.completed_at
    } else if (promptState.value !== 'completed') {
      completedAt.value = null
    }
  }

  async function syncProgress(): Promise<void> {
    const safeProgress = canonicalProgress(progress.value)
    progress.value = safeProgress
    if (!authenticated) {
      writeAnonymousProgress(safeProgress)
      hasAnonymousProgress = true
      return
    }

    try {
      const remote = await patchBeginnerGuideState({ progress: safeProgress })
      applyRemoteState(remote, safeProgress)
      if (hasAnonymousProgress) {
        removeAnonymousProgress()
        hasAnonymousProgress = false
      }
    } catch {
      if (hasAnonymousProgress) {
        writeAnonymousProgress(safeProgress)
      }
    }
  }

  async function persistSuppression(): Promise<void> {
    showPrompt.value = false
    if (promptState.value === 'completed') {
      if (accountUserId !== null) {
        clearPromptRetry(accountUserId)
      }
      return
    }
    setPromptState('suppressed')
    if (!authenticated || accountUserId === null) {
      return
    }

    try {
      const remote = await patchBeginnerGuideState({ prompt_state: 'suppressed' })
      applyRemoteState(remote, progress.value)
      clearPromptRetry(accountUserId)
    } catch {
      setPromptRetry(accountUserId)
    }
  }

  async function initialize(input: BeginnerGuideInitialization): Promise<void> {
    const nextOwner = input.authenticated ? `user:${String(input.userId)}` : 'anonymous'
    const preserveCompleted = stateOwner === nextOwner && promptState.value === 'completed'
    stateOwner = nextOwner
    authenticated = input.authenticated
    accountUserId = input.authenticated ? input.userId : null
    showPrompt.value = false
    if (!preserveCompleted) {
      promptState.value = 'suppressed'
      completedAt.value = null
    }

    const anonymous = readAnonymousProgress()
    hasAnonymousProgress = anonymous !== null
    progress.value = anonymous ?? defaultProgress()
    if (!input.authenticated) {
      return
    }

    let remote: BeginnerGuideState
    try {
      remote = await getBeginnerGuideState()
    } catch {
      return
    }

    const remotePrompt = normalizePromptState(remote.prompt_state)
    const remoteProgress = normalizeProgress(remote.progress)
    if (remoteProgress !== null && anonymous === null) {
      progress.value = remoteProgress
    }
    if (remotePrompt === null) {
      return
    }
    setPromptState(remotePrompt)
    if (typeof remote.completed_at === 'string') {
      completedAt.value = remote.completed_at
    } else if (promptState.value !== 'completed') {
      completedAt.value = null
    }

    if (anonymous !== null) {
      const merged = mergeProgress(remoteProgress, anonymous)
      progress.value = merged
      showPrompt.value = false
      if (remotePrompt !== 'completed') {
        setPromptState('suppressed')
      }
      try {
        const saved = await patchBeginnerGuideState({
          prompt_state: 'suppressed',
          progress: merged
        })
        applyRemoteState(saved, merged)
        removeAnonymousProgress()
        hasAnonymousProgress = false
        clearPromptRetry(input.userId)
      } catch {
        if (remotePrompt === 'eligible') {
          setPromptRetry(input.userId)
        }
      }
      return
    }

    const retrySuppression = hasPromptRetry(input.userId)
    if (retrySuppression || input.enteringGuide === true) {
      showPrompt.value = false
      if (remotePrompt === 'eligible') {
        await persistSuppression()
      } else {
        clearPromptRetry(input.userId)
      }
      return
    }

    showPrompt.value = promptState.value === 'eligible'
  }

  function invalidateSelectionSpecificProgress(): void {
    progress.value = {
      version: 1,
      client: progress.value.client,
      os: progress.value.os,
      currentStep: SELECTION_SPECIFIC_STEPS.has(progress.value.currentStep)
        ? 'terminal'
        : progress.value.currentStep,
      completedSteps: BEGINNER_GUIDE_STEP_ORDER.filter(
        (step) =>
          SELECTION_INVARIANT_STEPS.has(step) && progress.value.completedSteps.includes(step)
      )
    }
  }

  async function selectClient(client: BeginnerGuideClient): Promise<void> {
    if (progress.value.client === client) {
      return
    }
    progress.value = {
      version: 1,
      client,
      os: progress.value.os,
      currentStep: progress.value.currentStep,
      completedSteps: [...progress.value.completedSteps]
    }
    invalidateSelectionSpecificProgress()
    await syncProgress()
  }

  async function selectOS(os: BeginnerGuideOS): Promise<void> {
    if (progress.value.os === os) {
      return
    }
    progress.value = {
      version: 1,
      client: progress.value.client,
      os,
      currentStep: progress.value.currentStep,
      completedSteps: [...progress.value.completedSteps]
    }
    invalidateSelectionSpecificProgress()
    await syncProgress()
  }

  async function goToStep(step: BeginnerGuideStepId): Promise<void> {
    if (!isStepId(step)) {
      return
    }
    progress.value = {
      version: 1,
      client: progress.value.client,
      os: progress.value.os,
      currentStep: step,
      completedSteps: [...progress.value.completedSteps]
    }
    await syncProgress()
  }

  async function completeStep(step: BeginnerGuideStepId): Promise<void> {
    if (!isStepId(step)) {
      return
    }
    const completed = new Set<BeginnerGuideStepId>(progress.value.completedSteps)
    completed.add(step)
    progress.value = {
      version: 1,
      client: progress.value.client,
      os: progress.value.os,
      currentStep: progress.value.currentStep,
      completedSteps: BEGINNER_GUIDE_STEP_ORDER.filter((candidate) => completed.has(candidate))
    }
    await syncProgress()
  }

  async function suppressPrompt(): Promise<void> {
    await persistSuppression()
  }

  async function completeGuide(): Promise<void> {
    const safeProgress = canonicalProgress(progress.value)
    progress.value = safeProgress
    showPrompt.value = false
    setPromptState('completed')
    if (!authenticated) {
      writeAnonymousProgress(safeProgress)
      hasAnonymousProgress = true
      return
    }

    try {
      const remote = await patchBeginnerGuideState({
        prompt_state: 'completed',
        progress: safeProgress
      })
      applyRemoteState(remote, safeProgress)
      if (hasAnonymousProgress) {
        removeAnonymousProgress()
        hasAnonymousProgress = false
      }
      if (accountUserId !== null) {
        clearPromptRetry(accountUserId)
      }
    } catch {
      if (hasAnonymousProgress) {
        writeAnonymousProgress(safeProgress)
      }
    }
  }

  return {
    progress,
    promptState,
    completedAt,
    showPrompt,
    initialize,
    selectClient,
    selectOS,
    goToStep,
    completeStep,
    suppressPrompt,
    completeGuide
  }
})
