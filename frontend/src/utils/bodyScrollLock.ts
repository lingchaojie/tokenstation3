const owners = new Set<symbol>()
let previousOverflow: string | null = null

export function createBodyScrollLock() {
  const owner = Symbol('body-scroll-lock-owner')

  function acquire() {
    if (owners.has(owner)) return
    if (owners.size === 0) {
      previousOverflow = document.body.style.overflow
    }
    owners.add(owner)
    document.body.style.overflow = 'hidden'
  }

  function release() {
    if (!owners.delete(owner)) return
    if (owners.size === 0) {
      document.body.style.overflow = previousOverflow ?? ''
      previousOverflow = null
    }
  }

  return { acquire, release }
}
