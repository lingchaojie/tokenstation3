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
  type BeginnerGuideStepId,
  type PatchBeginnerGuideStateRequest
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

interface NormalizedBeginnerGuideState {
  promptState: BeginnerGuidePromptState
  progress: BeginnerGuideProgressV1 | null
  completedAt: string | null
}

function normalizeRemoteState(value: unknown): NormalizedBeginnerGuideState | null {
  if (!isRecord(value)) {
    return null
  }
  const remotePrompt = normalizePromptState(value.prompt_state)
  if (remotePrompt === null) {
    return null
  }
  if (value.progress !== null && normalizeProgress(value.progress) === null) {
    return null
  }
  if (value.completed_at !== null && typeof value.completed_at !== 'string') {
    return null
  }
  return {
    promptState: remotePrompt,
    progress: value.progress === null ? null : normalizeProgress(value.progress),
    completedAt: value.completed_at
  }
}

type OwnerContext =
  | {
      authenticated: false
      owner: 'anonymous'
      generation: number
      userId: null
    }
  | {
      authenticated: true
      owner: string
      generation: number
      userId: number | string
    }

type RemoteWriteOutcome =
  | { status: 'success'; remote: unknown }
  | { status: 'failure' }
  | { status: 'stale' }

export const useBeginnerGuideStore = defineStore('beginnerGuide', () => {
  const progress = ref<BeginnerGuideProgressV1>(defaultProgress())
  const promptState = ref<BeginnerGuidePromptState>('suppressed')
  const completedAt = ref<string | null>(null)
  const showPrompt = ref(false)

  let generation = 0
  let currentContext: OwnerContext = {
    authenticated: false,
    owner: 'anonymous',
    generation,
    userId: null
  }
  let initializationRequestEpoch = 0
  let hasAnonymousProgress = false
  let localProgressRevision = 0
  let localPromptRevision = 0
  const remoteWriteTails = new Map<string, Promise<void>>()
  const latestPromptSuppressionAttempt = new Map<string, number>()
  let promptSuppressionAttemptCounter = 0

  function isCurrent(context: OwnerContext): boolean {
    return (
      currentContext.generation === context.generation && currentContext.owner === context.owner
    )
  }

  function isCurrentInitialization(context: OwnerContext, requestEpoch: number): boolean {
    return isCurrent(context) && initializationRequestEpoch === requestEpoch
  }

  function beginPromptSuppressionAttempt(
    context: Extract<OwnerContext, { authenticated: true }>
  ): number {
    const attempt = ++promptSuppressionAttemptCounter
    latestPromptSuppressionAttempt.set(context.owner, attempt)
    setPromptRetry(context.userId)
    return attempt
  }

  function clearPromptSuppressionAttempt(
    context: Extract<OwnerContext, { authenticated: true }>,
    attempt: number
  ): void {
    if (latestPromptSuppressionAttempt.get(context.owner) !== attempt) {
      return
    }
    latestPromptSuppressionAttempt.delete(context.owner)
    clearPromptRetry(context.userId)
  }

  function replaceProgress(next: BeginnerGuideProgressV1): void {
    progress.value = canonicalProgress(next)
    localProgressRevision += 1
  }

  function setPromptState(next: BeginnerGuidePromptState): void {
    if (promptState.value === 'completed' && next !== 'completed') {
      return
    }
    promptState.value = next
  }

  function replacePromptState(next: BeginnerGuidePromptState): void {
    setPromptState(next)
    localPromptRevision += 1
  }

  function applyRemoteState(
    remote: NormalizedBeginnerGuideState,
    fallback: BeginnerGuideProgressV1,
    expectedProgressRevision: number,
    expectedPromptRevision: number
  ): void {
    if (
      remote.promptState === 'completed' ||
      localPromptRevision === expectedPromptRevision
    ) {
      setPromptState(remote.promptState)
    }
    if (localProgressRevision === expectedProgressRevision) {
      progress.value = remote.progress ?? canonicalProgress(fallback)
    }
    if (remote.completedAt !== null) {
      completedAt.value = remote.completedAt
    } else if (promptState.value !== 'completed') {
      completedAt.value = null
    }
  }

  function enqueueRemoteWrite(
    context: Extract<OwnerContext, { authenticated: true }>,
    patch: PatchBeginnerGuideStateRequest
  ): Promise<RemoteWriteOutcome> {
    const previous = remoteWriteTails.get(context.owner) ?? Promise.resolve()
    const operation = previous.then(async (): Promise<RemoteWriteOutcome> => {
      if (!isCurrent(context)) {
        return { status: 'stale' }
      }
      try {
        return {
          status: 'success',
          remote: await patchBeginnerGuideState(patch)
        }
      } catch {
        return { status: 'failure' }
      }
    })
    const tail = operation.then(() => undefined)
    remoteWriteTails.set(context.owner, tail)
    void tail.then(() => {
      if (remoteWriteTails.get(context.owner) === tail) {
        remoteWriteTails.delete(context.owner)
      }
    })
    return operation
  }

  async function syncProgress(): Promise<void> {
    const safeProgress = canonicalProgress(progress.value)
    progress.value = safeProgress
    const context = currentContext
    const progressRevision = localProgressRevision
    const promptRevision = localPromptRevision
    const hadAnonymousProgress = hasAnonymousProgress
    if (!context.authenticated) {
      writeAnonymousProgress(safeProgress)
      hasAnonymousProgress = true
      return
    }

    const outcome = await enqueueRemoteWrite(context, { progress: safeProgress })
    if (outcome.status === 'success') {
      const remote = normalizeRemoteState(outcome.remote)
      if (remote !== null && isCurrent(context)) {
        applyRemoteState(remote, safeProgress, progressRevision, promptRevision)
        if (
          hadAnonymousProgress &&
          localProgressRevision === progressRevision
        ) {
          removeAnonymousProgress()
          hasAnonymousProgress = false
        }
        return
      }
    }
    if (
      outcome.status !== 'stale' &&
      isCurrent(context) &&
      hadAnonymousProgress &&
      localProgressRevision === progressRevision
    ) {
      writeAnonymousProgress(safeProgress)
    }
  }

  async function persistSuppression(context: OwnerContext = currentContext): Promise<boolean> {
    if (isCurrent(context)) {
      showPrompt.value = false
    }
    if (promptState.value === 'completed') {
      if (context.authenticated) {
        clearPromptRetry(context.userId)
      }
      return true
    }
    if (isCurrent(context)) {
      replacePromptState('suppressed')
    }
    if (!context.authenticated) {
      return true
    }

    const suppressionAttempt = beginPromptSuppressionAttempt(context)
    const fallbackProgress = canonicalProgress(progress.value)
    const progressRevision = localProgressRevision
    const promptRevision = localPromptRevision
    const outcome = await enqueueRemoteWrite(context, { prompt_state: 'suppressed' })
    if (outcome.status === 'success') {
      const remote = normalizeRemoteState(outcome.remote)
      if (remote !== null) {
        clearPromptSuppressionAttempt(context, suppressionAttempt)
        if (isCurrent(context)) {
          applyRemoteState(remote, fallbackProgress, progressRevision, promptRevision)
        }
        return true
      }
    }
    return outcome.status === 'stale' || !isCurrent(context)
  }

  async function initialize(input: BeginnerGuideInitialization): Promise<void> {
    const nextOwner = input.authenticated ? `user:${String(input.userId)}` : 'anonymous'
    const sameOwner = currentContext.owner === nextOwner
    const preserveCompleted = sameOwner && promptState.value === 'completed'
    if (!sameOwner) {
      generation += 1
    }
    initializationRequestEpoch += 1
    const requestEpoch = initializationRequestEpoch
    const context: OwnerContext = input.authenticated
      ? {
          authenticated: true,
          owner: nextOwner,
          generation,
          userId: input.userId
        }
      : {
          authenticated: false,
          owner: 'anonymous',
          generation,
          userId: null
        }
    currentContext = context
    showPrompt.value = false
    if (!preserveCompleted) {
      promptState.value = 'suppressed'
      localPromptRevision += 1
      completedAt.value = null
    }

    const anonymous = readAnonymousProgress()
    hasAnonymousProgress = anonymous !== null
    if (anonymous !== null) {
      replaceProgress(anonymous)
    } else if (!sameOwner) {
      replaceProgress(defaultProgress())
    }
    const initializationRevision = localProgressRevision
    const initializationPromptRevision = localPromptRevision
    const hadPendingWriteAtStart = remoteWriteTails.has(context.owner)
    if (!context.authenticated) {
      return
    }

    let response: unknown
    try {
      response = await getBeginnerGuideState()
    } catch {
      return
    }
    if (!isCurrentInitialization(context, requestEpoch)) {
      return
    }
    const remote = normalizeRemoteState(response)
    if (remote === null) {
      return
    }

    if (
      remote.progress !== null &&
      anonymous === null &&
      !hadPendingWriteAtStart &&
      localProgressRevision === initializationRevision
    ) {
      progress.value = remote.progress
    }
    if (
      remote.promptState === 'completed' ||
      localPromptRevision === initializationPromptRevision
    ) {
      setPromptState(remote.promptState)
    }
    if (remote.completedAt !== null) {
      completedAt.value = remote.completedAt
    } else if (promptState.value !== 'completed') {
      completedAt.value = null
    }

    if (anonymous !== null) {
      const activeAnonymous =
        localProgressRevision === initializationRevision
          ? anonymous
          : canonicalProgress(progress.value)
      const merged = mergeProgress(remote.progress, activeAnonymous)
      replaceProgress(merged)
      const mergeRevision = localProgressRevision
      showPrompt.value = false
      if (remote.promptState !== 'completed') {
        replacePromptState('suppressed')
      }
      const mergePromptRevision = localPromptRevision
      const outcome = await enqueueRemoteWrite(context, {
        prompt_state: 'suppressed',
        progress: merged
      })
      if (outcome.status === 'success') {
        const saved = normalizeRemoteState(outcome.remote)
        if (saved !== null) {
          clearPromptRetry(context.userId)
          if (isCurrentInitialization(context, requestEpoch)) {
            applyRemoteState(saved, merged, mergeRevision, mergePromptRevision)
            if (localProgressRevision === mergeRevision) {
              removeAnonymousProgress()
              hasAnonymousProgress = false
            }
          }
          return
        }
      }
      if (remote.promptState === 'eligible') {
        setPromptRetry(context.userId)
      }
      return
    }

    const retrySuppression = hasPromptRetry(context.userId)
    if (retrySuppression || input.enteringGuide === true) {
      showPrompt.value = false
      if (remote.promptState === 'eligible') {
        await persistSuppression(context)
      } else {
        clearPromptRetry(context.userId)
      }
      return
    }

    showPrompt.value = promptState.value === 'eligible'
  }

  function invalidateSelectionSpecificProgress(): void {
    replaceProgress({
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
    })
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
    replaceProgress({
      version: 1,
      client: progress.value.client,
      os: progress.value.os,
      currentStep: step,
      completedSteps: [...progress.value.completedSteps]
    })
    await syncProgress()
  }

  async function completeStep(step: BeginnerGuideStepId): Promise<void> {
    if (!isStepId(step)) {
      return
    }
    const completed = new Set<BeginnerGuideStepId>(progress.value.completedSteps)
    completed.add(step)
    replaceProgress({
      version: 1,
      client: progress.value.client,
      os: progress.value.os,
      currentStep: progress.value.currentStep,
      completedSteps: BEGINNER_GUIDE_STEP_ORDER.filter((candidate) => completed.has(candidate))
    })
    await syncProgress()
  }

  async function suppressPrompt(): Promise<boolean> {
    return persistSuppression(currentContext)
  }

  async function completeGuide(): Promise<void> {
    const context = currentContext
    const safeProgress = canonicalProgress(progress.value)
    progress.value = safeProgress
    const progressRevision = localProgressRevision
    const hadAnonymousProgress = hasAnonymousProgress
    showPrompt.value = false
    replacePromptState('completed')
    const promptRevision = localPromptRevision
    if (!context.authenticated) {
      writeAnonymousProgress(safeProgress)
      hasAnonymousProgress = true
      return
    }

    const outcome = await enqueueRemoteWrite(context, {
      prompt_state: 'completed',
      progress: safeProgress
    })
    if (outcome.status === 'success') {
      const remote = normalizeRemoteState(outcome.remote)
      if (remote !== null) {
        clearPromptRetry(context.userId)
        if (isCurrent(context)) {
          applyRemoteState(remote, safeProgress, progressRevision, promptRevision)
          if (
            hadAnonymousProgress &&
            localProgressRevision === progressRevision
          ) {
            removeAnonymousProgress()
            hasAnonymousProgress = false
          }
        }
        return
      }
    }
    if (
      outcome.status !== 'stale' &&
      isCurrent(context) &&
      hadAnonymousProgress &&
      localProgressRevision === progressRevision
    ) {
      writeAnonymousProgress(safeProgress)
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
