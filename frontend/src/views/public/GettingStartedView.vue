<template>
  <GuideShell
    :current-step="guideStore.progress.currentStep"
    :completed-steps="guideStore.progress.completedSteps"
    @select-step="handleSelectStep"
  >
    <div class="mb-5 grid gap-4 sm:grid-cols-2">
      <fieldset class="linx-panel min-w-0 p-4">
        <legend class="px-1 text-sm font-semibold text-gray-950 dark:text-linear-ink">
          {{ t('gettingStarted.chrome.clientSelector') }}
        </legend>
        <div class="mt-2 grid grid-cols-2 gap-2">
          <button
            v-for="client in GUIDE_CLIENT_IDS"
            :key="client"
            type="button"
            :data-client-option="client"
            :aria-pressed="guideStore.progress.client === client"
            class="rounded-lg border px-3 py-2.5 text-sm font-medium transition-colors"
            :class="
              guideStore.progress.client === client
                ? 'border-primary-500 bg-primary-500/10 text-primary-700 dark:text-primary-300'
                : 'border-gray-200 text-gray-600 hover:bg-gray-50 dark:border-linear-hairline dark:text-linear-ink-subtle dark:hover:bg-linear-surface-2'
            "
            @click="guideStore.selectClient(client)"
          >
            {{ t(`gettingStarted.clients.${client}`) }}
          </button>
        </div>
      </fieldset>

      <fieldset class="linx-panel min-w-0 p-4">
        <legend class="px-1 text-sm font-semibold text-gray-950 dark:text-linear-ink">
          {{ t('gettingStarted.chrome.osSelector') }}
        </legend>
        <div class="mt-2 grid grid-cols-3 gap-2">
          <button
            v-for="os in GUIDE_OS_IDS"
            :key="os"
            type="button"
            :data-os-option="os"
            :aria-pressed="guideStore.progress.os === os"
            class="rounded-lg border px-2 py-2.5 text-sm font-medium transition-colors"
            :class="
              guideStore.progress.os === os
                ? 'border-primary-500 bg-primary-500/10 text-primary-700 dark:text-primary-300'
                : 'border-gray-200 text-gray-600 hover:bg-gray-50 dark:border-linear-hairline dark:text-linear-ink-subtle dark:hover:bg-linear-surface-2'
            "
            @click="guideStore.selectOS(os)"
          >
            {{ t(`gettingStarted.operatingSystems.${os}`) }}
          </button>
        </div>
      </fieldset>
    </div>

    <GuideStepPanel
      :step-id="activeStep"
      :step-number="activeStepIndex + 1"
      :step-count="GUIDE_STEP_IDS.length"
      :title="t(`gettingStarted.steps.${activeStep}.title`)"
      :description="t(`gettingStarted.steps.${activeStep}.description`)"
      :back-disabled="activeStepIndex === 0"
      :next-disabled="taskEightPlaceholder"
      :next-label="activeStep === 'first_run' ? t('gettingStarted.firstRun.confirmSuccess') : undefined"
      @back="handleBack"
      @next="handleNext"
    >
      <div v-if="activeStep === 'understand'" class="grid gap-3 sm:grid-cols-2">
        <article
          v-for="definition in definitionIds"
          :key="definition"
          class="rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-linear-hairline dark:bg-linear-canvas"
        >
          <h2 class="text-sm font-semibold text-gray-950 dark:text-linear-ink">
            {{ t(`gettingStarted.definitions.${definition}.title`) }}
          </h2>
          <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-linear-ink-subtle">
            {{ t(`gettingStarted.definitions.${definition}.description`) }}
          </p>
        </article>
      </div>

      <section v-else-if="activeStep === 'choose'" class="rounded-xl border border-primary-500/20 bg-primary-500/5 p-4">
        <p class="text-sm leading-6 text-gray-700 dark:text-linear-ink-subtle">
          {{ t('gettingStarted.steps.choose.description') }}
        </p>
        <p class="mt-2 text-sm font-medium text-primary-700 dark:text-primary-300">
          {{ t(`gettingStarted.clients.${guideStore.progress.client}`) }} ·
          {{ t(`gettingStarted.operatingSystems.${guideStore.progress.os}`) }}
        </p>
      </section>

      <div v-else-if="activeStep === 'terminal'" class="space-y-5">
        <section class="rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-linear-hairline dark:bg-linear-canvas">
          <h2 class="text-base font-semibold text-gray-950 dark:text-linear-ink">
            {{ t(`gettingStarted.terminal.${guideStore.progress.os}.appName`) }}
          </h2>
          <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-linear-ink-subtle">
            {{ t(`gettingStarted.terminal.${guideStore.progress.os}.openInstructions`) }}
          </p>
          <p class="mt-3 text-sm leading-6 text-gray-600 dark:text-linear-ink-subtle">
            {{ t('gettingStarted.terminal.pasteAndRun') }}
          </p>
          <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-linear-ink-subtle">
            {{ t('gettingStarted.terminal.normalOutput') }}
          </p>
        </section>
        <GuideCommandBlock :command="selectedVariant.verifyCommand" />
      </div>

      <div v-else-if="activeStep === 'install'" class="space-y-5">
        <p class="text-sm leading-6 text-gray-600 dark:text-linear-ink-subtle">
          {{ t('gettingStarted.installation.explanation') }}
        </p>
        <GuideCommandBlock :command="selectedVariant.installCommand" />
        <details class="rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-linear-hairline dark:bg-linear-canvas">
          <summary class="cursor-pointer text-sm font-semibold text-gray-950 dark:text-linear-ink">
            {{ t('gettingStarted.installation.expectedResult') }}
          </summary>
          <p class="mt-3 text-sm leading-6 text-gray-600 dark:text-linear-ink-subtle">
            {{ t('gettingStarted.installation.restartShell') }}
          </p>
        </details>
        <a
          :href="selectedVariant.officialSourceUrl"
          target="_blank"
          rel="noopener noreferrer"
          class="inline-flex items-center gap-2 text-sm font-medium text-primary-600 hover:text-primary-500 dark:text-primary-300"
        >
          {{ t('gettingStarted.installation.officialSource') }}
          <Icon name="externalLink" size="sm" aria-hidden="true" />
        </a>
      </div>

      <section
        v-else-if="activeStep === 'api_key' || activeStep === 'configure'"
        data-testid="task-8-placeholder"
        class="rounded-xl border border-amber-400/30 bg-amber-500/10 p-4"
      >
        <p class="text-sm leading-6 text-gray-700 dark:text-linear-ink-subtle">
          {{
            activeStep === 'api_key'
              ? t('gettingStarted.apiKey.secretWarning')
              : t('gettingStarted.configuration.mergeWarning')
          }}
        </p>
      </section>

      <div v-else-if="activeStep === 'first_run'" class="space-y-5">
        <p class="text-sm leading-6 text-gray-600 dark:text-linear-ink-subtle">
          {{ t('gettingStarted.firstRun.restartInstruction') }}
        </p>
        <GuideCommandBlock :command="selectedVariant.launchCommand" />
        <div>
          <h2 class="mb-2 text-sm font-semibold text-gray-950 dark:text-linear-ink">
            {{ t('gettingStarted.firstRun.promptLabel') }}
          </h2>
          <GuideCommandBlock :command="t('gettingStarted.firstRun.prompt')" />
        </div>
        <p class="rounded-xl border border-emerald-500/20 bg-emerald-500/5 p-4 text-sm leading-6 text-gray-700 dark:text-linear-ink-subtle">
          {{ t('gettingStarted.firstRun.expectedResult') }}
        </p>
      </div>

      <GuideTroubleshooting
        v-else-if="activeStep === 'troubleshoot'"
        :variant="selectedVariant"
        :base-url="displayBaseUrl"
      />

      <section
        v-if="guideStore.promptState === 'completed'"
        class="rounded-xl border border-emerald-500/25 bg-emerald-500/5 p-5"
        aria-live="polite"
      >
        <h2 class="text-lg font-semibold text-gray-950 dark:text-linear-ink">
          {{ t('gettingStarted.completion.title') }}
        </h2>
        <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-linear-ink-subtle">
          {{ t('gettingStarted.completion.description') }}
        </p>
        <div class="mt-4 flex flex-wrap gap-2">
          <router-link
            v-for="destination in completionDestinations"
            :key="destination.path"
            :to="destination.path"
            data-testid="completion-link"
            class="inline-flex items-center rounded-lg border border-emerald-500/30 px-3 py-2 text-sm font-medium text-emerald-700 hover:bg-emerald-500/10 dark:text-emerald-300"
          >
            {{ t(destination.labelKey) }}
          </router-link>
        </div>
      </section>
    </GuideStepPanel>
  </GuideShell>
</template>

<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import GuideShell from '@/components/getting-started/GuideShell.vue'
import GuideStepPanel from '@/components/getting-started/GuideStepPanel.vue'
import GuideCommandBlock from '@/components/getting-started/GuideCommandBlock.vue'
import GuideTroubleshooting from '@/components/getting-started/GuideTroubleshooting.vue'
import {
  GUIDE_CLIENT_IDS,
  GUIDE_OS_IDS,
  GUIDE_STEP_IDS,
  GUIDE_VARIANTS
} from '@/components/getting-started/curriculum'
import { useAppStore, useAuthStore, useBeginnerGuideStore } from '@/stores'
import type {
  BeginnerGuideOS,
  BeginnerGuideStepId
} from '@/api/beginnerGuide'

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const guideStore = useBeginnerGuideStore()

const definitionIds = ['model', 'agent', 'terminal', 'gateway', 'apiKey'] as const
const activeStep = computed(() => guideStore.progress.currentStep)
const activeStepIndex = computed(() => Math.max(0, GUIDE_STEP_IDS.indexOf(activeStep.value)))
const selectedVariant = computed(() => {
  const variant = GUIDE_VARIANTS.find(
    (candidate) =>
      candidate.client === guideStore.progress.client && candidate.os === guideStore.progress.os
  )
  if (!variant) {
    throw new Error('Unsupported beginner guide variant')
  }
  return variant
})
const taskEightPlaceholder = computed(
  () => activeStep.value === 'api_key' || activeStep.value === 'configure'
)
const displayBaseUrl = computed(() => appStore.apiBaseUrl || window.location.origin)
const completionDestinations = computed(() => [
  {
    path: authStore.isAdmin ? '/admin/my-account/dashboard' : '/dashboard',
    labelKey: 'gettingStarted.completion.dashboard'
  },
  { path: '/keys', labelKey: 'gettingStarted.completion.keys' },
  { path: '/usage', labelKey: 'gettingStarted.completion.usage' }
])

function detectBrowserOS(): BeginnerGuideOS {
  const platform = `${navigator.platform || ''} ${navigator.userAgent || ''}`.toLowerCase()
  if (platform.includes('win')) return 'windows'
  if (platform.includes('mac')) return 'macos'
  if (platform.includes('linux') || platform.includes('x11')) return 'linux'
  return 'macos'
}

function hasAnonymousProgress(): boolean {
  try {
    return localStorage.getItem('beginner_guide_progress_v1') !== null
  } catch {
    return false
  }
}

watch(
  [() => authStore.isAuthenticated, () => authStore.user?.id],
  async ([authenticated, userId]) => {
    const hadAnonymousProgress = hasAnonymousProgress()
    if (authenticated && userId !== undefined) {
      await guideStore.initialize({ authenticated: true, userId, enteringGuide: true })
      return
    }

    await guideStore.initialize({ authenticated: false, userId: null, enteringGuide: true })
    if (!hadAnonymousProgress) {
      await guideStore.selectOS(detectBrowserOS())
    }
  },
  { immediate: true }
)

function canNavigateTo(step: BeginnerGuideStepId): boolean {
  const index = GUIDE_STEP_IDS.indexOf(step)
  if (index === -1) return false
  const completed = new Set(guideStore.progress.completedSteps)
  return GUIDE_STEP_IDS.slice(0, index).every((candidate) => completed.has(candidate))
}

async function handleSelectStep(step: BeginnerGuideStepId): Promise<void> {
  if (!canNavigateTo(step)) return
  await guideStore.goToStep(step)
}

async function handleBack(): Promise<void> {
  if (activeStepIndex.value <= 0) return
  await guideStore.goToStep(GUIDE_STEP_IDS[activeStepIndex.value - 1])
}

async function handleNext(): Promise<void> {
  if (taskEightPlaceholder.value) return
  const step = activeStep.value
  await guideStore.completeStep(step)
  if (step === 'troubleshoot') {
    await guideStore.completeGuide()
    return
  }
  const next = GUIDE_STEP_IDS[activeStepIndex.value + 1]
  if (next) {
    await guideStore.goToStep(next)
  }
}
</script>
