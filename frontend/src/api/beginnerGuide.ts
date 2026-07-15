import { apiClient } from './client'

export const BEGINNER_GUIDE_STEP_ORDER = [
  'understand',
  'choose',
  'terminal',
  'install',
  'api_key',
  'configure',
  'first_run',
  'troubleshoot'
] as const

export type BeginnerGuideClient = 'claude_code' | 'codex' | 'opencode' | 'cc_switch'
export type BeginnerGuideOS = 'macos' | 'windows' | 'linux'
export type BeginnerGuideStepId = (typeof BEGINNER_GUIDE_STEP_ORDER)[number]
export type BeginnerGuidePromptState = 'eligible' | 'suppressed' | 'completed'

export interface BeginnerGuideProgressV1 {
  version: 1
  client: BeginnerGuideClient
  os: BeginnerGuideOS
  currentStep: BeginnerGuideStepId
  completedSteps: BeginnerGuideStepId[]
}

export interface BeginnerGuideState {
  prompt_state: BeginnerGuidePromptState
  progress: BeginnerGuideProgressV1 | null
  completed_at: string | null
}

export interface PatchBeginnerGuideStateRequest {
  prompt_state?: Exclude<BeginnerGuidePromptState, 'eligible'>
  progress?: BeginnerGuideProgressV1
}

export async function getBeginnerGuideState(): Promise<BeginnerGuideState> {
  const { data } = await apiClient.get<BeginnerGuideState>('/user/beginner-guide')
  return data
}

export async function patchBeginnerGuideState(
  patch: PatchBeginnerGuideStateRequest
): Promise<BeginnerGuideState> {
  const { data } = await apiClient.patch<BeginnerGuideState>('/user/beginner-guide', patch)
  return data
}
