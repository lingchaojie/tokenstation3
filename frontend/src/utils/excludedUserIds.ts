export const normalizeExcludedUserIds = (ids?: number[]): number[] =>
  Array.from(
    new Set((ids ?? []).filter((id) => Number.isSafeInteger(id) && id > 0))
  ).sort((a, b) => a - b)

export const encodeExcludedUserIds = (ids?: number[]): string | undefined => {
  const normalized = normalizeExcludedUserIds(ids)
  return normalized.length ? normalized.join(',') : undefined
}

export const withEncodedExcludedUserIds = <T extends { exclude_user_ids?: number[] }>(
  params: T
): Omit<T, 'exclude_user_ids'> & { exclude_user_ids: string | undefined } => ({
  ...params,
  exclude_user_ids: encodeExcludedUserIds(params.exclude_user_ids)
})
