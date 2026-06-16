export const BILLING_TYPE_BALANCE = 0
export const BILLING_TYPE_SUBSCRIPTION = 1

export function getBillingTypeLabel(type: number | null | undefined, t: (key: string) => string): string {
  switch (type) {
    case BILLING_TYPE_BALANCE:
      return t('admin.usage.billingTypeBalance')
    case BILLING_TYPE_SUBSCRIPTION:
      return t('admin.usage.billingTypeSubscription')
    default:
      return '-'
  }
}

export function getBillingTypeBadgeClass(type: number | null | undefined): string {
  switch (type) {
    case BILLING_TYPE_SUBSCRIPTION:
      return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
    case BILLING_TYPE_BALANCE:
      return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
    default:
      return 'bg-gray-100 text-gray-700 dark:bg-gray-800 dark:text-gray-300'
  }
}
