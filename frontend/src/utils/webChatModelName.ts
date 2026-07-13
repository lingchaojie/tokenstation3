export interface WebChatModelNameInput {
  provider?: string
  model: string
  displayName?: string
}

const THINKING_SUFFIX = /-thinking$/i
const DATE_SUFFIX = /-\d{8}$/
const CLAUDE_FAMILY_FIRST = /^claude-(opus|sonnet|haiku)-(\d+)(?:-(\d+))?$/i
const CLAUDE_VERSION_FIRST = /^claude-(\d+)(?:-(\d+))?-(opus|sonnet|haiku)$/i
const GPT_IMAGE = /^gpt-image-(\d+(?:[.-]\d+)*)$/i
const GPT_MODEL = /^gpt-(\d+(?:\.\d+)*)(?:-(.+))?$/i

function stripRoutingSuffixes(value: string): string {
  return value.replace(THINKING_SUFFIX, '').replace(DATE_SUFFIX, '')
}

function titleCaseWords(value: string): string {
  return value
    .split(/[-_.]+/)
    .filter(Boolean)
    .map((word) => {
      const lower = word.toLowerCase()
      return lower.charAt(0).toUpperCase() + lower.slice(1)
    })
    .join(' ')
}

function claudeName(family: string, major: string, minor?: string): string {
  const version = minor ? `${major}.${minor}` : major
  return `Claude ${titleCaseWords(family)} ${version}`
}

export function formatWebChatModelName(input: WebChatModelNameInput): string {
  const model = stripRoutingSuffixes(input.model.trim())
  const displayName = input.displayName?.trim() ?? ''
  if (!model) return displayName

  const familyFirst = model.match(CLAUDE_FAMILY_FIRST)
  if (familyFirst) {
    return claudeName(familyFirst[1], familyFirst[2], familyFirst[3])
  }

  const versionFirst = model.match(CLAUDE_VERSION_FIRST)
  if (versionFirst) {
    return claudeName(versionFirst[3], versionFirst[1], versionFirst[2])
  }

  const image = model.match(GPT_IMAGE)
  if (image) return `GPT Image ${image[1].replace(/-/g, '.')}`

  const gpt = model.match(GPT_MODEL)
  if (gpt) {
    const variant = gpt[2] ? ` ${titleCaseWords(gpt[2])}` : ''
    return `GPT-${gpt[1]}${variant}`
  }

  return displayName || model
}
