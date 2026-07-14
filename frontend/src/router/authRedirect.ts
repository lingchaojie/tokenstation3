const DEFAULT_POST_AUTH_REDIRECT = '/dashboard'
const MAX_ENCODED_SEPARATOR_DEPTH = 8

function hasUnsafeLeadingSeparator(path: string): boolean {
  let leading = path.slice(1)

  for (let depth = 0; depth < MAX_ENCODED_SEPARATOR_DEPTH; depth += 1) {
    if (leading.startsWith('/') || leading.startsWith('\\')) {
      return true
    }

    const encodedByte = leading.match(/^%([\da-f]{2})/i)
    if (!encodedByte) {
      return false
    }

    const decodedByte = String.fromCharCode(Number.parseInt(encodedByte[1], 16))
    if (decodedByte !== '%' && decodedByte !== '/' && decodedByte !== '\\') {
      return false
    }

    leading = decodedByte + leading.slice(encodedByte[0].length)
  }

  return true
}

export function resolvePostAuthRedirect(
  value: unknown,
  fallback = DEFAULT_POST_AUTH_REDIRECT,
): string {
  if (typeof value !== 'string') {
    return fallback
  }

  const path = value.trim()
  if (!path.startsWith('/') || hasUnsafeLeadingSeparator(path)) {
    return fallback
  }

  return path
}
