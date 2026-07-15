import { defineComponent, nextTick, ref, type Ref } from 'vue'
import { mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'

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
})
