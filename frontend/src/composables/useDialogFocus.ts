import { nextTick, onMounted, onUnmounted, watch, type Ref } from 'vue'

const TABBABLE_SELECTOR = [
  'a[href]',
  'area[href]',
  'button',
  'input',
  'select',
  'textarea',
  'iframe',
  'object',
  'embed',
  'summary',
  '[contenteditable]',
  '[tabindex]',
].join(', ')

interface DialogFocusOptions {
  dialogRef: Ref<HTMLElement | null>
  isOpen: () => boolean
  onEscape: () => void
  closeOnEscape?: () => boolean
  onOpened?: () => void
}

export function useDialogFocus({
  dialogRef,
  isOpen,
  onEscape,
  closeOnEscape = () => true,
  onOpened,
}: DialogFocusOptions): void {
  let previousActiveElement: HTMLElement | null = null

  function firstSummary(details: HTMLElement): HTMLElement | null {
    return (
      Array.from(details.children).find((child) => child.tagName === 'SUMMARY') as
        | HTMLElement
        | undefined
    ) ?? null
  }

  function isNativeSummary(element: HTMLElement): boolean {
    const details = element.parentElement
    return (
      element.tagName === 'SUMMARY'
      && details?.tagName === 'DETAILS'
      && firstSummary(details) === element
    )
  }

  function isValidContentEditable(element: HTMLElement): boolean {
    const value = element.getAttribute('contenteditable')
    if (value === null) return false
    const normalized = value.toLowerCase()
    return normalized === '' || normalized === 'true' || normalized === 'plaintext-only'
  }

  function hasTabStopSemantics(element: HTMLElement): boolean {
    if (element.hasAttribute('tabindex')) return element.tabIndex >= 0
    if (element.tagName === 'SUMMARY') return isNativeSummary(element)
    if (element.hasAttribute('contenteditable')) return isValidContentEditable(element)
    return element.tabIndex >= 0
  }

  function isVisible(element: HTMLElement): boolean {
    let candidate: HTMLElement | null = element
    while (candidate) {
      if (
        candidate.hidden
        || candidate.hasAttribute('inert')
        || candidate.getAttribute('aria-hidden') === 'true'
      ) {
        return false
      }
      const style = window.getComputedStyle(candidate)
      if (style.display === 'none' || style.visibility === 'hidden') return false
      if (
        candidate !== element
        && candidate.tagName === 'DETAILS'
        && !candidate.hasAttribute('open')
      ) {
        const summary = firstSummary(candidate)
        if (summary === null || !summary.contains(element)) return false
      }
      if (candidate === dialogRef.value) break
      candidate = candidate.parentElement
    }
    return true
  }

  function getTabbableElements(): HTMLElement[] {
    if (!dialogRef.value) return []
    return Array.from(dialogRef.value.querySelectorAll<HTMLElement>(TABBABLE_SELECTOR)).filter(
      (element) => (
        !element.matches(':disabled')
        && hasTabStopSemantics(element)
        && isVisible(element)
      ),
    )
  }

  function focusDialogStart(): void {
    const target = getTabbableElements()[0] ?? dialogRef.value
    target?.focus()
  }

  function restorePreviousFocus(): void {
    if (previousActiveElement?.isConnected) previousActiveElement.focus()
    previousActiveElement = null
  }

  function handleKeydown(event: KeyboardEvent): void {
    if (isOpen() && closeOnEscape() && event.key === 'Escape') {
      onEscape()
      return
    }
    if (!isOpen() || event.key !== 'Tab' || !dialogRef.value) return

    const tabbable = getTabbableElements()
    if (tabbable.length === 0) {
      event.preventDefault()
      dialogRef.value.focus()
      return
    }

    const first = tabbable[0]
    const last = tabbable[tabbable.length - 1]
    const active = document.activeElement
    const focusLeftDialog = active === null || !dialogRef.value.contains(active)
    if (event.shiftKey && (active === first || focusLeftDialog)) {
      event.preventDefault()
      last.focus()
    } else if (!event.shiftKey && (active === last || focusLeftDialog)) {
      event.preventDefault()
      first.focus()
    }
  }

  watch(
    isOpen,
    async (open) => {
      if (!open) {
        restorePreviousFocus()
        return
      }

      const active = document.activeElement
      previousActiveElement = active instanceof HTMLElement ? active : null
      await nextTick()
      if (!isOpen()) return
      onOpened?.()
      focusDialogStart()
    },
    { immediate: true },
  )

  onMounted(() => document.addEventListener('keydown', handleKeydown))
  onUnmounted(() => {
    document.removeEventListener('keydown', handleKeydown)
    restorePreviousFocus()
  })
}
