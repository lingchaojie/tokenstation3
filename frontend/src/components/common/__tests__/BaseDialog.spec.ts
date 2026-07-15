import { defineComponent, nextTick, ref, type Ref } from 'vue'
import { mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import BaseDialog from '../BaseDialog.vue'

interface MountedDialog {
  wrapper: VueWrapper
  show: Ref<boolean>
}

let mounted: MountedDialog | null = null

function mountDialog(slot: string, showCloseButton = false): MountedDialog {
  const show = ref(false)
  const Harness = defineComponent({
    components: { BaseDialog },
    setup: () => ({ show }),
    template: `
      <div>
        <button data-testid="launcher" type="button">Open dialog</button>
        <button data-testid="background-before" type="button">Background before</button>
        <BaseDialog
          :show="show"
          title="Focus trap"
          :show-close-button="${showCloseButton}"
          @close="show = false"
        >
          ${slot}
        </BaseDialog>
        <button data-testid="background-after" type="button">Background after</button>
      </div>
    `
  })

  const wrapper = mount(Harness, { attachTo: document.body })
  mounted = { wrapper, show }
  return mounted
}

async function openDialog(dialog: MountedDialog): Promise<HTMLElement> {
  const launcher = dialog.wrapper.get<HTMLElement>('[data-testid="launcher"]').element
  launcher.focus()
  dialog.show.value = true
  await nextTick()
  await nextTick()
  return launcher
}

function pressTab(target: Element, shiftKey = false): KeyboardEvent {
  const event = new KeyboardEvent('keydown', {
    key: 'Tab',
    shiftKey,
    bubbles: true,
    cancelable: true
  })
  target.dispatchEvent(event)
  return event
}

afterEach(() => {
  mounted?.wrapper.unmount()
  mounted = null
  document.body.innerHTML = ''
  document.body.classList.remove('modal-open')
})

describe('BaseDialog focus management', () => {
  it('focuses the first tabbable control while skipping disabled, hidden, and negative-tabindex controls', async () => {
    const dialog = mountDialog(`
      <button type="button" disabled data-testid="disabled-control">Disabled</button>
      <a href="#ignored" tabindex="-1" data-testid="negative-tabindex">Ignored link</a>
      <button type="button" hidden data-testid="hidden-control">Hidden</button>
      <input type="hidden" value="not tabbable" data-testid="hidden-input" />
      <button type="button" data-testid="first-control">First</button>
      <button type="button" data-testid="last-control">Last</button>
    `)

    await openDialog(dialog)

    expect(document.activeElement).toBe(
      document.body.querySelector('[data-testid="first-control"]')
    )
  })

  it('wraps Tab from the last dialog control to the first without reaching background controls', async () => {
    const dialog = mountDialog(`
      <button type="button" data-testid="first-control">First</button>
      <button type="button" disabled>Disabled</button>
      <button type="button" data-testid="last-control">Last</button>
    `)
    await openDialog(dialog)
    const first = document.body.querySelector<HTMLElement>('[data-testid="first-control"]')!
    const last = document.body.querySelector<HTMLElement>('[data-testid="last-control"]')!
    last.focus()

    const event = pressTab(last)

    expect(event.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(first)
    expect(document.activeElement).not.toBe(
      dialog.wrapper.get('[data-testid="background-after"]').element
    )
  })

  it('wraps Shift+Tab from the first dialog control to the last without reaching background controls', async () => {
    const dialog = mountDialog(`
      <button type="button" data-testid="first-control">First</button>
      <button type="button" data-testid="last-control">Last</button>
    `)
    await openDialog(dialog)
    const first = document.body.querySelector<HTMLElement>('[data-testid="first-control"]')!
    const last = document.body.querySelector<HTMLElement>('[data-testid="last-control"]')!
    first.focus()

    const event = pressTab(first, true)

    expect(event.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(last)
    expect(document.activeElement).not.toBe(
      dialog.wrapper.get('[data-testid="background-before"]').element
    )
  })

  it('closes on Escape and restores focus to the launcher after the parent hides it', async () => {
    const dialog = mountDialog('<button type="button">Inside</button>')
    const launcher = await openDialog(dialog)

    document.activeElement?.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })
    )
    await nextTick()

    expect(dialog.show.value).toBe(false)
    expect(document.activeElement).toBe(launcher)
  })

  it('focuses the panel fallback and keeps Tab there when the dialog has no tabbable controls', async () => {
    const dialog = mountDialog('<p>Read-only dialog content</p>')
    await openDialog(dialog)
    const panel = document.body.querySelector<HTMLElement>('.modal-content')!

    expect(panel.getAttribute('tabindex')).toBe('-1')
    expect(document.activeElement).toBe(panel)

    const event = pressTab(panel)

    expect(event.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(panel)
  })

  it('treats the close button and first native summary as the focus-trap boundaries', async () => {
    const dialog = mountDialog(
      '<details><summary data-testid="native-summary">More options</summary></details>',
      true
    )
    await openDialog(dialog)
    const close = document.body.querySelector<HTMLElement>('[aria-label="Close modal"]')!
    const summary = document.body.querySelector<HTMLElement>('[data-testid="native-summary"]')!

    expect(document.activeElement).toBe(close)
    summary.focus()
    const forward = pressTab(summary)
    expect(forward.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(close)

    const backward = pressTab(close, true)
    expect(backward.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(summary)
  })

  it.each(['', 'TRUE', 'plaintext-only'])(
    'treats contenteditable="%s" as tabbable while excluding contenteditable="false"',
    async (contenteditable) => {
      const dialog = mountDialog(`
        <div contenteditable="false" data-testid="not-editable">Not editable</div>
        <div contenteditable="${contenteditable}" data-testid="editable">Editable</div>
        <button type="button" data-testid="last-control">Last</button>
      `)

      await openDialog(dialog)

      expect(document.activeElement).toBe(
        document.body.querySelector('[data-testid="editable"]')
      )
      const last = document.body.querySelector<HTMLElement>('[data-testid="last-control"]')!
      last.focus()
      const event = pressTab(last)
      expect(event.defaultPrevented).toBe(true)
      expect(document.activeElement).toBe(
        document.body.querySelector('[data-testid="editable"]')
      )
    }
  )

  it('excludes closed-details content while retaining each first summary through nested details', async () => {
    const dialog = mountDialog(`
      <details open>
        <summary data-testid="outer-summary">Outer summary</summary>
        <details>
          <summary data-testid="inner-summary">Inner summary</summary>
          <button type="button" data-testid="inner-hidden">Inner hidden action</button>
        </details>
        <button type="button" data-testid="outer-action">Outer action</button>
      </details>
      <details>
        <summary data-testid="closed-summary">Closed summary</summary>
        <button type="button" data-testid="closed-hidden">Closed hidden action</button>
        <summary data-testid="second-summary">Not the first summary</summary>
        <details open>
          <summary data-testid="nested-hidden-summary">Nested hidden summary</summary>
        </details>
      </details>
    `)
    await openDialog(dialog)
    const outerSummary = document.body.querySelector<HTMLElement>(
      '[data-testid="outer-summary"]'
    )!
    const closedSummary = document.body.querySelector<HTMLElement>(
      '[data-testid="closed-summary"]'
    )!

    expect(document.activeElement).toBe(outerSummary)
    closedSummary.focus()
    const forward = pressTab(closedSummary)
    expect(forward.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(outerSummary)

    const backward = pressTab(outerSummary, true)
    expect(backward.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(closedSummary)
  })

  it('keeps a nested closed details first summary reachable through an open details ancestor', async () => {
    const dialog = mountDialog(`
      <details open>
        <summary data-testid="outer-summary">Outer summary</summary>
        <details>
          <summary data-testid="inner-summary">Inner summary</summary>
          <button type="button">Inner hidden action</button>
        </details>
      </details>
    `)
    await openDialog(dialog)
    const outerSummary = document.body.querySelector<HTMLElement>(
      '[data-testid="outer-summary"]'
    )!
    const innerSummary = document.body.querySelector<HTMLElement>(
      '[data-testid="inner-summary"]'
    )!

    expect(document.activeElement).toBe(outerSummary)
    innerSummary.focus()
    const forward = pressTab(innerSummary)
    expect(forward.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(outerSummary)

    const backward = pressTab(outerSummary, true)
    expect(backward.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(innerSummary)
  })

  it('does not treat a focusable closed details element as its own hidden descendant', async () => {
    const dialog = mountDialog(`
      <details tabindex="0" data-testid="focusable-details">
        <summary data-testid="details-summary">Summary</summary>
        <button type="button">Hidden action</button>
      </details>
    `)
    await openDialog(dialog)
    const details = document.body.querySelector<HTMLElement>(
      '[data-testid="focusable-details"]'
    )!
    const summary = document.body.querySelector<HTMLElement>('[data-testid="details-summary"]')!

    expect(document.activeElement).toBe(details)
    summary.focus()
    const event = pressTab(summary)
    expect(event.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(details)
  })

  it('skips controls hidden by inert, aria-hidden, display-none, and visibility-hidden ancestors', async () => {
    const dialog = mountDialog(`
      <div inert><button type="button">Inert action</button></div>
      <div aria-hidden="true"><button type="button">Aria-hidden action</button></div>
      <div style="display: none"><button type="button">Display-hidden action</button></div>
      <div style="visibility: hidden"><button type="button">Visibility-hidden action</button></div>
      <button type="button" data-testid="visible-action">Visible action</button>
    `)

    await openDialog(dialog)

    expect(document.activeElement).toBe(
      document.body.querySelector('[data-testid="visible-action"]')
    )
  })

  it('removes its document keydown listener and body lock when unmounted', async () => {
    const close = vi.fn()
    const wrapper = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: true, title: 'Unmount cleanup', onClose: close },
      slots: { default: '<button type="button">Inside</button>' }
    })
    await nextTick()
    await nextTick()
    expect(document.body.classList.contains('modal-open')).toBe(true)

    wrapper.unmount()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))

    expect(close).not.toHaveBeenCalled()
    expect(document.body.classList.contains('modal-open')).toBe(false)
  })
})
