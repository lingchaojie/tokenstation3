<template>
  <Teleport to="body">
    <Transition name="modal">
      <div
        v-if="show"
        class="modal-overlay"
        :style="zIndexStyle"
        :aria-labelledby="dialogId"
        role="dialog"
        aria-modal="true"
        @click.self="handleClose"
      >
        <!-- Modal panel -->
        <div ref="dialogRef" :class="['modal-content', widthClasses]" tabindex="-1" @click.stop>
          <!-- Header -->
          <div class="modal-header">
            <h3 :id="dialogId" class="modal-title">
              {{ title }}
            </h3>
            <button
              v-if="showCloseButton"
              @click="emit('close')"
              class="-mr-2 rounded-xl p-2 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/30 focus-visible:ring-offset-2 dark:text-dark-500 dark:hover:bg-dark-700 dark:hover:text-dark-300 dark:focus-visible:ring-offset-dark-900"
              :aria-label="closeAriaLabel"
            >
              <Icon name="x" size="md" />
            </button>
          </div>

          <!-- Body -->
          <div class="modal-body">
            <slot></slot>
          </div>

          <!-- Footer -->
          <div v-if="$slots.footer" class="modal-footer">
            <slot name="footer"></slot>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, watch, onMounted, onUnmounted, ref, nextTick } from 'vue'
import Icon from '@/components/icons/Icon.vue'

// 生成唯一ID以避免多个对话框时ID冲突
let dialogIdCounter = 0
const dialogId = `modal-title-${++dialogIdCounter}`

// 焦点管理
const dialogRef = ref<HTMLElement | null>(null)
let previousActiveElement: HTMLElement | null = null

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
  '[tabindex]'
].join(', ')

type DialogWidth = 'narrow' | 'normal' | 'wide' | 'extra-wide' | 'full'

interface Props {
  show: boolean
  title: string
  width?: DialogWidth
  closeOnEscape?: boolean
  closeOnClickOutside?: boolean
  showCloseButton?: boolean
  closeAriaLabel?: string
  zIndex?: number
}

interface Emits {
  (e: 'close'): void
}

const props = withDefaults(defineProps<Props>(), {
  width: 'normal',
  closeOnEscape: true,
  closeOnClickOutside: false,
  showCloseButton: true,
  closeAriaLabel: 'Close modal',
  zIndex: 50
})

const emit = defineEmits<Emits>()

// Custom z-index style (overrides the default z-50 from CSS)
const zIndexStyle = computed(() => {
  return props.zIndex !== 50 ? { zIndex: props.zIndex } : undefined
})

const widthClasses = computed(() => {
  // Width guidance: narrow=confirm/short prompts, normal=standard forms,
  // wide=multi-section forms or rich content, extra-wide=analytics/tables,
  // full=full-screen or very dense layouts.
  const widths: Record<DialogWidth, string> = {
    narrow: 'max-w-md',
    normal: 'max-w-lg',
    wide: 'w-full sm:max-w-2xl md:max-w-3xl lg:max-w-4xl',
    'extra-wide': 'w-full sm:max-w-3xl md:max-w-4xl lg:max-w-5xl xl:max-w-6xl',
    full: 'w-full sm:max-w-4xl md:max-w-5xl lg:max-w-6xl xl:max-w-7xl'
  }
  return widths[props.width]
})

const handleClose = () => {
  if (props.closeOnClickOutside) {
    emit('close')
  }
}

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
    element.tagName === 'SUMMARY' &&
    details?.tagName === 'DETAILS' &&
    firstSummary(details) === element
  )
}

function isValidContentEditable(element: HTMLElement): boolean {
  const value = element.getAttribute('contenteditable')
  if (value === null) {
    return false
  }
  const normalized = value.trim().toLowerCase()
  return normalized === '' || normalized === 'true' || normalized === 'plaintext-only'
}

function hasTabStopSemantics(element: HTMLElement): boolean {
  const hasExplicitTabIndex = element.hasAttribute('tabindex')
  if (hasExplicitTabIndex) {
    return element.tabIndex >= 0
  }
  if (element.tagName === 'SUMMARY') {
    return isNativeSummary(element)
  }
  if (element.hasAttribute('contenteditable')) {
    return isValidContentEditable(element)
  }
  return element.tabIndex >= 0
}

function isVisible(element: HTMLElement): boolean {
  let candidate: HTMLElement | null = element
  while (candidate) {
    if (
      candidate.hidden ||
      candidate.hasAttribute('inert') ||
      candidate.getAttribute('aria-hidden') === 'true'
    ) {
      return false
    }
    const style = window.getComputedStyle(candidate)
    if (style.display === 'none' || style.visibility === 'hidden') {
      return false
    }
    if (
      candidate !== element &&
      candidate.tagName === 'DETAILS' &&
      !candidate.hasAttribute('open') &&
      firstSummary(candidate) !== element
    ) {
      return false
    }
    if (candidate === dialogRef.value) {
      break
    }
    candidate = candidate.parentElement
  }
  return true
}

function getTabbableElements(): HTMLElement[] {
  if (!dialogRef.value) {
    return []
  }
  return Array.from(dialogRef.value.querySelectorAll<HTMLElement>(TABBABLE_SELECTOR)).filter(
    (element) =>
      !element.matches(':disabled') &&
      hasTabStopSemantics(element) &&
      isVisible(element)
  )
}

function focusDialogStart(): void {
  const target = getTabbableElements()[0] ?? dialogRef.value
  target?.focus()
}

const handleKeydown = (event: KeyboardEvent) => {
  if (props.show && props.closeOnEscape && event.key === 'Escape') {
    emit('close')
    return
  }
  if (!props.show || event.key !== 'Tab' || !dialogRef.value) {
    return
  }

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

// Prevent body scroll when modal is open and manage focus
watch(
  () => props.show,
  async (isOpen) => {
    if (isOpen) {
      // 保存当前焦点元素
      previousActiveElement = document.activeElement as HTMLElement
      // 使用CSS类而不是直接操作style,更易于管理多个对话框
      document.body.classList.add('modal-open')

      // 等待DOM更新后设置焦点到对话框
      await nextTick()
      focusDialogStart()
    } else {
      document.body.classList.remove('modal-open')
      // 恢复之前的焦点
      if (previousActiveElement && typeof previousActiveElement.focus === 'function') {
        previousActiveElement.focus()
      }
      previousActiveElement = null
    }
  },
  { immediate: true }
)

onMounted(() => {
  document.addEventListener('keydown', handleKeydown)
})

onUnmounted(() => {
  document.removeEventListener('keydown', handleKeydown)
  // 确保组件卸载时移除滚动锁定
  document.body.classList.remove('modal-open')
})
</script>
