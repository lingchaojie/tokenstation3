const DEFAULT_POST_AUTH_REDIRECT = '/dashboard'
const POST_AUTH_SENTINEL_ORIGIN = 'https://post-auth.invalid'
const MAX_PERCENT_DECODE_DEPTH = 8

function hasURLControlCharacters(value: string): boolean {
  for (let index = 0; index < value.length; index += 1) {
    const codePoint = value.charCodeAt(index)
    if (codePoint <= 0x1f || codePoint === 0x7f) {
      return true
    }
  }

  return false
}

function staysOnSentinelOrigin(candidate: string): boolean {
  if (!candidate.startsWith('/') || hasURLControlCharacters(candidate)) {
    return false
  }

  try {
    return new URL(candidate, POST_AUTH_SENTINEL_ORIGIN).origin === POST_AUTH_SENTINEL_ORIGIN
  } catch {
    return false
  }
}

function isSafeInternalPath(path: string): boolean {
  let candidate = path

  for (let depth = 0; depth <= MAX_PERCENT_DECODE_DEPTH; depth += 1) {
    if (!staysOnSentinelOrigin(candidate)) {
      return false
    }

    let decoded: string
    try {
      decoded = decodeURIComponent(candidate)
    } catch {
      return false
    }

    if (decoded === candidate) {
      return true
    }
    candidate = decoded
  }

  return false
}

export function resolvePostAuthRedirect(
  value: unknown,
  fallback = DEFAULT_POST_AUTH_REDIRECT,
): string {
  if (typeof value !== 'string') {
    return fallback
  }

  if (hasURLControlCharacters(value)) {
    return fallback
  }

  const path = value.trim()
  if (!isSafeInternalPath(path)) {
    return fallback
  }

  return path
}
