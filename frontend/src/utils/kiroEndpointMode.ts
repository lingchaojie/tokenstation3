import type { KiroEndpointMode } from '@/types'

export function normalizeKiroEndpointMode(value: unknown): KiroEndpointMode {
  return value === 'krs' || value === 'auto' ? value : 'q'
}

export function resolveKiroEndpointModeForGroupPayload(
  platform: unknown,
  value: unknown,
): KiroEndpointMode {
  return platform === 'kiro' ? normalizeKiroEndpointMode(value) : 'q'
}
