const DEFAULT_POST_AUTH_REDIRECT = '/dashboard'
const POST_AUTH_VALIDATION_ORIGIN = 'https://post-auth-base.invalid'
const MAX_PERCENT_DECODE_DEPTH = 8
const HEX_BYTE = /^[\da-f]{2}$/i

type LeadingEscapeResult = 'malformed' | 'percent' | 'unsafe' | 'ordinary'

function hasURLControlCharacters(value: string): boolean {
  for (let index = 0; index < value.length; index += 1) {
    const codeUnit = value.charCodeAt(index)
    if (codeUnit <= 0x1f || codeUnit === 0x7f) {
      return true
    }
  }

  return false
}

function readPercentEncodedByte(value: string, offset: number): number | undefined {
  if (value[offset] !== '%') {
    return undefined
  }

  const encodedByte = value.slice(offset + 1, offset + 3)
  if (encodedByte.length !== 2 || !HEX_BYTE.test(encodedByte)) {
    return undefined
  }

  return Number.parseInt(encodedByte, 16)
}

function getUTF8SequenceLength(firstByte: number): number {
  if (firstByte <= 0x7f) return 1
  if (firstByte >= 0xc2 && firstByte <= 0xdf) return 2
  if (firstByte >= 0xe0 && firstByte <= 0xef) return 3
  if (firstByte >= 0xf0 && firstByte <= 0xf4) return 4
  return 0
}

function inspectLeadingEscape(value: string): LeadingEscapeResult {
  const firstByte = readPercentEncodedByte(value, 0)
  if (firstByte === undefined) {
    return 'malformed'
  }

  if (firstByte === 0x25) return 'percent'
  if (firstByte === 0x2f || firstByte === 0x5c || firstByte <= 0x1f || firstByte === 0x7f) {
    return 'unsafe'
  }
  if (firstByte <= 0x7e) return 'ordinary'

  const sequenceLength = getUTF8SequenceLength(firstByte)
  if (sequenceLength === 0) {
    return 'malformed'
  }

  for (let index = 1; index < sequenceLength; index += 1) {
    if (readPercentEncodedByte(value, index * 3) === undefined) {
      return 'malformed'
    }
  }

  try {
    decodeURIComponent(value.slice(0, sequenceLength * 3))
  } catch {
    return 'malformed'
  }

  return 'ordinary'
}

function hasUnsafeLeadingEscapeChain(path: string): boolean {
  let leading = path.slice(1)
  if (!leading.startsWith('%')) {
    return false
  }

  let decodedLiteralPercent = false
  for (let depth = 0; depth <= MAX_PERCENT_DECODE_DEPTH; depth += 1) {
    const result = inspectLeadingEscape(leading)
    switch (result) {
      case 'malformed':
        return !decodedLiteralPercent
      case 'unsafe':
        return true
      case 'ordinary':
        return false
      case 'percent':
        if (depth === MAX_PERCENT_DECODE_DEPTH) {
          return true
        }
        decodedLiteralPercent = true
        leading = '%' + leading.slice(3)
        break
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
