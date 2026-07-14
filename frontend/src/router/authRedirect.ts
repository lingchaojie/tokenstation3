const DEFAULT_POST_AUTH_REDIRECT = '/dashboard'
const POST_AUTH_VALIDATION_ORIGIN = 'https://post-auth-base.invalid'
const MAX_PERCENT_DECODE_DEPTH = 8
const HEX_BYTE = /^[\da-f]{2}$/i

function hasURLControlCharacters(value: string): boolean {
  for (let index = 0; index < value.length; index += 1) {
    const codeUnit = value.charCodeAt(index)
    if (codeUnit <= 0x1f || codeUnit === 0x7f) {
      return true
    }
  }

  return false
}

function hasUnsafeLeadingEscapeChain(path: string): boolean {
  let leading = path.slice(1)
  if (!leading.startsWith('%')) {
    return false
  }

  for (let depth = 0; depth < MAX_PERCENT_DECODE_DEPTH; depth += 1) {
    let chainEnd = 0
    while (leading[chainEnd] === '%') {
      const encodedByte = leading.slice(chainEnd + 1, chainEnd + 3)
      if (encodedByte.length !== 2 || !HEX_BYTE.test(encodedByte)) {
        return true
      }
      chainEnd += 3
    }

    let decodedChain: string
    try {
      decodedChain = decodeURIComponent(leading.slice(0, chainEnd))
    } catch {
      return true
    }

    if (
      decodedChain.includes('/') ||
      decodedChain.includes('\\') ||
      hasURLControlCharacters(decodedChain)
    ) {
      return true
    }

    leading = decodedChain + leading.slice(chainEnd)
    if (!leading.startsWith('%')) {
      return false
    }
  }

  return true
}

function isSafeInternalPath(path: string): boolean {
  if (!path.startsWith('/')) {
    return false
  }

  const firstPathCharacter = path[1]
  if (firstPathCharacter === '/' || firstPathCharacter === '\\') {
    return false
  }

  if (hasUnsafeLeadingEscapeChain(path)) {
    return false
  }

  try {
    return new URL(path, POST_AUTH_VALIDATION_ORIGIN).origin === POST_AUTH_VALIDATION_ORIGIN
  } catch {
    return false
  }
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
