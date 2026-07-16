import type { WebChatModel } from '@/api/chat'

export function sortWebChatModelsByReleaseDate(models: WebChatModel[]): WebChatModel[] {
  return [...models].sort((left, right) => {
    const leftRelease = (left.released_at ?? '').trim()
    const rightRelease = (right.released_at ?? '').trim()
    if (leftRelease !== rightRelease) {
      if (!leftRelease) return 1
      if (!rightRelease) return -1
      return rightRelease.localeCompare(leftRelease)
    }
    const leftName = (left.display_name || left.model).trim().toLowerCase()
    const rightName = (right.display_name || right.model).trim().toLowerCase()
    if (leftName !== rightName) {
      return leftName.localeCompare(rightName)
    }
    return left.model.localeCompare(right.model)
  })
}
