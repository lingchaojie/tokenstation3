/** Step-up (sudo) 2FA composable for sensitive admin actions. */
import { ref } from 'vue'

const STEP_UP_REQUIRED = 'STEP_UP_REQUIRED'
const STEP_UP_TOTP_NOT_ENABLED = 'STEP_UP_TOTP_NOT_ENABLED'
const STEP_UP_ADMIN_API_KEY_FORBIDDEN = 'STEP_UP_ADMIN_API_KEY_FORBIDDEN'

export class StepUpCancelledError extends Error {
  readonly code = 'STEP_UP_CANCELLED'
  constructor() {
    super('step-up verification cancelled by user')
    this.name = 'StepUpCancelledError'
  }
}

export function isStepUpCancelled(err: unknown): boolean {
  return err instanceof StepUpCancelledError
}

interface ApiError {
  code?: string | number
  reason?: string
}

function markerOf(err: unknown): string {
  const value = (err ?? {}) as ApiError
  return [value.code, value.reason]
    .map((item) => (typeof item === 'string' ? item : ''))
    .find((item) => item.startsWith('STEP_UP')) || ''
}

export function isStepUpRequired(err: unknown): boolean {
  return markerOf(err) === STEP_UP_REQUIRED
}

export function isStepUpBlocked(err: unknown): boolean {
  const marker = markerOf(err)
  return marker === STEP_UP_TOTP_NOT_ENABLED || marker === STEP_UP_ADMIN_API_KEY_FORBIDDEN
}

export function stepUpBlockReason(err: unknown): string {
  return markerOf(err)
}

export type StepUpController = ReturnType<typeof useStepUp>

export function useStepUp() {
  const visible = ref(false)
  const blockedReason = ref('')
  let resolver: ((ok: boolean) => void) | null = null

  function openDialog(): Promise<boolean> {
    visible.value = true
    return new Promise((resolve) => {
      resolver = resolve
    })
  }

  function onVerified() {
    visible.value = false
    resolver?.(true)
    resolver = null
  }

  function onCancel() {
    visible.value = false
    resolver?.(false)
    resolver = null
  }

  async function run<T>(action: () => Promise<T>): Promise<T> {
    try {
      return await action()
    } catch (err) {
      if (isStepUpBlocked(err) || !isStepUpRequired(err)) throw err
      const ok = await openDialog()
      if (!ok) throw new StepUpCancelledError()
      return await action()
    }
  }

  return { visible, blockedReason, prompt: openDialog, onVerified, onCancel, run }
}
