import type { Account } from '@/types'

export const DEFAULT_KIRO_API_REGION = 'us-east-1'

export const KIRO_API_REGIONS = [
  'us-east-1',
  'us-east-2',
  'us-west-1',
  'us-west-2',
  'af-south-1',
  'ap-east-1',
  'ap-south-2',
  'ap-southeast-3',
  'ap-southeast-5',
  'ap-southeast-4',
  'ap-south-1',
  'ap-southeast-6',
  'ap-northeast-3',
  'ap-northeast-2',
  'ap-southeast-1',
  'ap-southeast-2',
  'ap-east-2',
  'ap-southeast-7',
  'ap-northeast-1',
  'ca-central-1',
  'ca-west-1',
  'eu-central-1',
  'eu-west-1',
  'eu-west-2',
  'eu-south-1',
  'eu-west-3',
  'eu-south-2',
  'eu-north-1',
  'eu-central-2',
  'il-central-1',
  'mx-central-1',
  'me-south-1',
  'me-central-1',
  'sa-east-1'
] as const

export interface KiroAPIRegionOption {
  value: string
  label: string
  disabled?: boolean
}

export function resolveKiroAPIRegion(value: unknown): string {
  if (typeof value !== 'string') return DEFAULT_KIRO_API_REGION
  return value.trim() || DEFAULT_KIRO_API_REGION
}

export function buildKiroAPIRegionOptions(
  currentValue: unknown,
  labelFor: (region: string, legacy: boolean) => string
): KiroAPIRegionOption[] {
  const options: KiroAPIRegionOption[] = KIRO_API_REGIONS.map(region => ({
    value: region,
    label: labelFor(region, false)
  }))
  const currentRegion = resolveKiroAPIRegion(currentValue)

  if (!KIRO_API_REGIONS.some(region => region === currentRegion)) {
    options.push({
      value: currentRegion,
      label: labelFor(currentRegion, true),
      disabled: true
    })
  }

  return options
}

function readBaseUrl(account: Pick<Account, 'credentials'> | null | undefined): string {
  if (!account?.credentials) return ''
  const raw = (account.credentials as Record<string, unknown>).base_url
  return typeof raw === 'string' ? raw.trim() : ''
}

export function isKiroRelayAccount(account: Pick<Account, 'platform' | 'type' | 'credentials'> | null | undefined): boolean {
  if (!account || account.platform !== 'kiro' || account.type !== 'apikey') return false
  return readBaseUrl(account) !== ''
}

export function isKiroDirectApiKeyAccount(account: Pick<Account, 'platform' | 'type' | 'credentials'> | null | undefined): boolean {
  if (!account || account.platform !== 'kiro' || account.type !== 'apikey') return false
  return readBaseUrl(account) === ''
}
