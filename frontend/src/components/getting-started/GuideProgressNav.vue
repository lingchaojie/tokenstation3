<template>
  <div class="min-w-0">
    <div
      class="mb-4 flex items-center justify-between rounded-xl border border-gray-200 bg-white p-3 dark:border-linear-hairline dark:bg-linear-surface-1 lg:hidden"
    >
      <div class="min-w-0">
        <p class="text-xs font-medium uppercase tracking-[0.14em] text-gray-500 dark:text-linear-ink-tertiary">
          {{ t('gettingStarted.chrome.progress') }}
        </p>
        <p class="truncate text-sm font-semibold text-gray-950 dark:text-linear-ink">
          {{ currentStepIndex + 1 }} / {{ GUIDE_STEP_IDS.length }} · {{ stepTitle(currentStep) }}
        </p>
      </div>
      <button
        ref="menuButton"
        type="button"
        data-testid="mobile-step-menu-button"
        class="ml-3 inline-flex shrink-0 items-center gap-2 rounded-lg border border-gray-200 px-3 py-2 text-sm font-medium text-gray-700 outline-none transition-colors focus-visible:ring-2 focus-visible:ring-primary-500/50 focus-visible:ring-offset-2 motion-reduce:transition-none dark:border-linear-hairline dark:text-linear-ink dark:focus-visible:ring-offset-linear-canvas"
        :aria-label="t('gettingStarted.chrome.openStepMenu')"
        :aria-expanded="drawerOpen"
        aria-controls="guide-mobile-step-drawer"
        @click="openDrawer"
      >
        <Icon name="menu" size="sm" aria-hidden="true" />
        {{ t('gettingStarted.chrome.mobileStepMenu') }}
      </button>
    </div>

    <aside class="linx-panel hidden p-3 lg:block" :aria-label="t('gettingStarted.chrome.progress')">
      <p class="px-2 pb-3 text-xs font-medium uppercase tracking-[0.14em] text-gray-500 dark:text-linear-ink-tertiary">
        {{ t('gettingStarted.chrome.progress') }}
      </p>
      <nav>
        <ol class="space-y-1">
          <li v-for="step in GUIDE_STEP_IDS" :key="step">
            <StepButton :step="step" @select="selectStep" />
          </li>
        </ol>
      </nav>
    </aside>

    <div
      v-if="drawerOpen"
      ref="drawerDialog"
      id="guide-mobile-step-drawer"
      data-testid="mobile-step-drawer"
      class="fixed inset-0 z-50 bg-black/50 lg:hidden"
      role="dialog"
      aria-modal="true"
      :aria-label="t('gettingStarted.chrome.mobileStepMenu')"
      @click.self="closeDrawer"
      @keydown="handleDrawerKeydown"
    >
      <div class="ml-auto flex h-full w-[min(88vw,22rem)] flex-col bg-white p-4 shadow-2xl dark:bg-linear-surface-1">
        <div class="mb-4 flex items-center justify-between">
          <h2 class="text-base font-semibold text-gray-950 dark:text-linear-ink">
            {{ t('gettingStarted.chrome.mobileStepMenu') }}
          </h2>
          <button
            ref="closeButton"
            type="button"
            data-testid="mobile-step-menu-close"
            class="rounded-lg p-2 text-gray-500 outline-none transition-colors hover:bg-gray-100 focus-visible:ring-2 focus-visible:ring-primary-500/50 focus-visible:ring-offset-2 motion-reduce:transition-none dark:text-linear-ink-subtle dark:hover:bg-linear-surface-2 dark:focus-visible:ring-offset-linear-surface-1"
            :aria-label="t('gettingStarted.chrome.closeStepMenu')"
            @click="closeDrawer"
          >
            <Icon name="x" size="md" aria-hidden="true" />
          </button>
        </div>
        <nav class="min-h-0 overflow-y-auto">
          <ol class="space-y-1">
            <li v-for="step in GUIDE_STEP_IDS" :key="step">
              <StepButton :step="step" @select="selectStep" />
            </li>
          </ol>
        </nav>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, nextTick, ref, type PropType } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { GUIDE_STEP_IDS } from './curriculum'
import type { BeginnerGuideStepId } from '@/api/beginnerGuide'

const props = defineProps<{
  currentStep: BeginnerGuideStepId
  completedSteps: BeginnerGuideStepId[]
}>()

const emit = defineEmits<{
  select: [step: BeginnerGuideStepId]
}>()

const { t } = useI18n()
const drawerOpen = ref(false)
const menuButton = ref<HTMLButtonElement | null>(null)
const closeButton = ref<HTMLButtonElement | null>(null)
const drawerDialog = ref<HTMLElement | null>(null)

const completedSet = computed(() => new Set(props.completedSteps))
const firstIncompleteIndex = computed(() => {
  const index = GUIDE_STEP_IDS.findIndex((step) => !completedSet.value.has(step))
  return index === -1 ? GUIDE_STEP_IDS.length - 1 : index
})
const currentStepIndex = computed(() => Math.max(0, GUIDE_STEP_IDS.indexOf(props.currentStep)))

function stepTitle(step: BeginnerGuideStepId): string {
  return t(`gettingStarted.steps.${step}.title`)
}

function stepState(step: BeginnerGuideStepId): 'completed' | 'current' | 'upcoming' {
  if (step === props.currentStep) return 'current'
  if (completedSet.value.has(step)) return 'completed'
  return 'upcoming'
}

function canSelect(step: BeginnerGuideStepId): boolean {
  const index = GUIDE_STEP_IDS.indexOf(step)
  return index !== -1 && index <= firstIncompleteIndex.value
}

function openDrawer(): void {
  drawerOpen.value = true
  void nextTick(() => closeButton.value?.focus())
}

function closeDrawer(): void {
  if (!drawerOpen.value) return
  drawerOpen.value = false
  void nextTick(() => menuButton.value?.focus())
}

function handleDrawerKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    event.preventDefault()
    event.stopPropagation()
    closeDrawer()
    return
  }
  if (event.key !== 'Tab') return

  const focusable = Array.from(
    drawerDialog.value?.querySelectorAll<HTMLElement>(
      'button:not([disabled]), a[href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
    ) ?? []
  )
  const first = focusable[0]
  const last = focusable.at(-1)
  if (!first || !last) return

  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

function selectStep(step: BeginnerGuideStepId): void {
  if (!GUIDE_STEP_IDS.includes(step) || !canSelect(step)) return
  emit('select', step)
  closeDrawer()
}

const StepButton = defineComponent({
  name: 'GuideProgressStepButton',
  props: {
    step: {
      type: String as PropType<BeginnerGuideStepId>,
      required: true
    }
  },
  emits: {
    select: (_step: BeginnerGuideStepId) => true
  },
  setup(buttonProps, { emit: emitButton }) {
    return () => {
      const state = stepState(buttonProps.step)
      const title = stepTitle(buttonProps.step)
      const index = GUIDE_STEP_IDS.indexOf(buttonProps.step)
      const isCompleted = state === 'completed'
      const isCurrent = state === 'current'
      return h(
        'button',
        {
          type: 'button',
          class: [
            'flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm outline-none transition-colors focus-visible:ring-2 focus-visible:ring-primary-500/50 focus-visible:ring-offset-2 motion-reduce:transition-none dark:focus-visible:ring-offset-linear-canvas',
            isCurrent
              ? 'bg-primary-500/10 font-semibold text-primary-700 dark:text-primary-300'
              : 'text-gray-600 hover:bg-gray-100 dark:text-linear-ink-subtle dark:hover:bg-linear-surface-2',
            !canSelect(buttonProps.step) ? 'cursor-not-allowed opacity-45' : ''
          ],
          disabled: !canSelect(buttonProps.step),
          'data-guide-step': buttonProps.step,
          'data-state': state,
          'aria-current': isCurrent ? 'step' : undefined,
          'aria-label': `${title}${isCompleted ? ' ✓' : ''}`,
          onClick: () => emitButton('select', buttonProps.step)
        },
        [
          h(
            'span',
            {
              class: [
                'flex h-7 w-7 shrink-0 items-center justify-center rounded-full border text-xs font-semibold',
                isCompleted
                  ? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-600 dark:text-emerald-300'
                  : isCurrent
                    ? 'border-primary-500/40 bg-primary-500/10 text-primary-600 dark:text-primary-300'
                    : 'border-gray-200 text-gray-500 dark:border-linear-hairline dark:text-linear-ink-tertiary'
              ],
              'aria-hidden': 'true'
            },
            isCompleted
              ? [h(Icon, { name: 'check', size: 'sm', 'data-testid': 'completed-icon' })]
              : String(index + 1)
          ),
          h('span', { class: 'min-w-0 flex-1' }, title),
          isCurrent
            ? h(Icon, {
                name: 'chevronRight',
                size: 'sm',
                class: 'shrink-0',
                'aria-hidden': 'true'
              })
            : null
        ]
      )
    }
  }
})
</script>
