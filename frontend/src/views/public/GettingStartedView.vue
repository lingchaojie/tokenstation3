<template>
  <GuideShell
    :current-step="guideStore.progress.currentStep"
    :completed-steps="guideStore.progress.completedSteps"
    @select-step="handleSelectStep"
  >
    <section
      data-testid="guide-network-warning"
      role="note"
      class="mb-5 rounded-xl border border-amber-400/35 bg-amber-500/10 px-4 py-3 text-sm font-medium text-amber-900 dark:text-amber-200"
    >
      {{ t('gettingStarted.warnings.networkAccess') }}
    </section>

    <section
      v-if="persistenceWarningKey"
      data-testid="guide-persistence-warning"
      role="status"
      aria-live="polite"
      class="mb-5 flex min-w-0 flex-col gap-3 rounded-xl border border-amber-400/35 bg-amber-500/10 p-4 text-sm text-gray-700 dark:text-linear-ink-subtle sm:flex-row sm:items-center sm:justify-between"
    >
      <p>{{ t(persistenceWarningKey) }}</p>
      <button
        type="button"
        data-testid="guide-persistence-retry"
        :disabled="guideStore.persistenceRetrying"
        :aria-busy="guideStore.persistenceRetrying || undefined"
        class="inline-flex shrink-0 items-center justify-center rounded-lg border border-amber-500/40 px-3 py-2 font-medium text-amber-800 outline-none transition-colors hover:bg-amber-500/10 focus-visible:ring-2 focus-visible:ring-amber-500/50 focus-visible:ring-offset-2 motion-reduce:transition-none disabled:cursor-not-allowed disabled:opacity-60 dark:text-amber-200 dark:focus-visible:ring-offset-linear-canvas"
        @click="guideStore.retryPersistence"
      >
        {{
          t(
            guideStore.persistenceRetrying
              ? 'gettingStarted.troubleshooting.retryLoading'
              : 'gettingStarted.troubleshooting.retry'
          )
        }}
      </button>
    </section>

    <div class="mb-5 grid min-w-0 gap-4 sm:grid-cols-2">
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
            :disabled="nextPending"
            :aria-disabled="nextPending || undefined"
            class="rounded-lg border px-3 py-2.5 text-sm font-medium outline-none transition-colors focus-visible:ring-2 focus-visible:ring-primary-500/50 focus-visible:ring-offset-2 motion-reduce:transition-none dark:focus-visible:ring-offset-linear-canvas"
            :class="
              guideStore.progress.client === client
                ? 'border-primary-500 bg-primary-500/10 text-primary-700 dark:text-primary-300'
                : 'border-gray-200 text-gray-600 hover:bg-gray-50 dark:border-linear-hairline dark:text-linear-ink-subtle dark:hover:bg-linear-surface-2'
            "
            @click="selectClient(client)"
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
            :disabled="nextPending"
            :aria-disabled="nextPending || undefined"
            class="rounded-lg border px-2 py-2.5 text-sm font-medium outline-none transition-colors focus-visible:ring-2 focus-visible:ring-primary-500/50 focus-visible:ring-offset-2 motion-reduce:transition-none dark:focus-visible:ring-offset-linear-canvas"
            :class="
              guideStore.progress.os === os
                ? 'border-primary-500 bg-primary-500/10 text-primary-700 dark:text-primary-300'
                : 'border-gray-200 text-gray-600 hover:bg-gray-50 dark:border-linear-hairline dark:text-linear-ink-subtle dark:hover:bg-linear-surface-2'
            "
            @click="selectOS(os)"
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
      :next-disabled="taskEightBlocked"
      :next-loading="nextPending"
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
        <a
          v-if="selectedVariant.desktopDownloadUrl"
          data-testid="guide-desktop-download"
          :href="selectedVariant.desktopDownloadUrl"
          target="_blank"
          rel="noopener noreferrer"
          class="inline-flex items-center justify-center gap-2 rounded-lg bg-primary-500 px-4 py-2.5 text-sm font-medium text-white outline-none transition-colors hover:bg-primary-400 focus-visible:ring-2 focus-visible:ring-primary-500/50 focus-visible:ring-offset-2 motion-reduce:transition-none dark:focus-visible:ring-offset-linear-canvas"
        >
          {{ t('gettingStarted.installation.downloadDesktop') }}
          <Icon name="externalLink" size="sm" aria-hidden="true" />
        </a>
        <p
          v-if="selectedVariant.desktopDownloadUrl"
          data-testid="guide-cli-fallback"
          class="text-sm font-medium text-gray-700 dark:text-linear-ink-subtle"
        >
          {{ t('gettingStarted.installation.cliFallback') }}
        </p>
        <GuideCommandBlock :command="selectedVariant.installCommand" />
        <details class="rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-linear-hairline dark:bg-linear-canvas">
          <summary class="cursor-pointer rounded-md text-sm font-semibold text-gray-950 outline-none focus-visible:ring-2 focus-visible:ring-primary-500/50 focus-visible:ring-offset-2 dark:text-linear-ink dark:focus-visible:ring-offset-linear-canvas">
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
          class="inline-flex items-center gap-2 rounded-md text-sm font-medium text-primary-600 outline-none transition-colors hover:text-primary-500 focus-visible:ring-2 focus-visible:ring-primary-500/50 focus-visible:ring-offset-2 motion-reduce:transition-none dark:text-primary-300 dark:focus-visible:ring-offset-linear-canvas"
        >
          {{ t('gettingStarted.installation.officialSource') }}
          <Icon name="externalLink" size="sm" aria-hidden="true" />
        </a>
      </div>

      <GuideApiKeyStep
        v-else-if="activeStep === 'api_key'"
        :client="guideStore.progress.client"
        :os="guideStore.progress.os"
        :selected-key="selectedKey"
        :reselect-required="reselectRequired"
        @select="handleSelectKey"
      />

      <div v-else-if="activeStep === 'configure'" class="space-y-5">
        <p class="rounded-xl border border-amber-400/30 bg-amber-500/10 p-4 text-sm leading-6 text-gray-700 dark:text-linear-ink-subtle">
          {{ t('gettingStarted.configuration.mergeWarning') }}
        </p>
        <article
          v-for="file in generatedFiles"
          :key="file.path"
          data-testid="guide-config-file"
          class="min-w-0 space-y-3"
        >
          <h2 class="break-all text-sm font-semibold text-gray-950 dark:text-linear-ink" v-text="file.path"></h2>
          <p
            v-if="file.hintKey || file.hint"
            class="text-sm leading-6 text-gray-600 dark:text-linear-ink-subtle"
          >
            {{ file.hintKey ? t(file.hintKey) : file.hint }}
          </p>
          <GuideCommandBlock :command="file.content" />
        </article>
        <p class="rounded-xl border border-primary-500/20 bg-primary-500/5 p-4 text-sm leading-6 text-gray-700 dark:text-linear-ink-subtle">
          {{ t('gettingStarted.configuration.restartInstruction') }}
        </p>
      </div>

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
            class="inline-flex items-center rounded-lg border border-emerald-500/30 px-3 py-2 text-sm font-medium text-emerald-700 outline-none transition-colors hover:bg-emerald-500/10 focus-visible:ring-2 focus-visible:ring-emerald-500/50 focus-visible:ring-offset-2 motion-reduce:transition-none dark:text-emerald-300 dark:focus-visible:ring-offset-linear-canvas"
          >
            {{ t(destination.labelKey) }}
          </router-link>
        </div>
      </section>
    </GuideStepPanel>
  </GuideShell>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import GuideShell from '@/components/getting-started/GuideShell.vue'
import GuideStepPanel from '@/components/getting-started/GuideStepPanel.vue'
import GuideCommandBlock from '@/components/getting-started/GuideCommandBlock.vue'
import GuideApiKeyStep from '@/components/getting-started/GuideApiKeyStep.vue'
import GuideTroubleshooting from '@/components/getting-started/GuideTroubleshooting.vue'
import { buildClientConfigFiles } from '@/components/keys/clientConfigFiles'
import {
  GUIDE_CLIENT_IDS,
  GUIDE_OS_IDS,
  GUIDE_STEP_IDS,
  GUIDE_VARIANTS
} from '@/components/getting-started/curriculum'
import { useAppStore, useAuthStore, useBeginnerGuideStore } from '@/stores'
import type {
  BeginnerGuideClient,
  BeginnerGuideOS,
  BeginnerGuideStepId
} from '@/api/beginnerGuide'
import type { ApiKey, GroupPlatform } from '@/types'

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const guideStore = useBeginnerGuideStore()
const nextPending = ref(false)
const manualOSSelected = ref(false)
const selectedKey = shallowRef<ApiKey | null>(null)
const reselectRequired = ref(false)
let guideOwnerGeneration = 0
let redirectingConfigure = false
let disposed = false

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
const taskEightBlocked = computed(
  () =>
    (activeStep.value === 'api_key' || activeStep.value === 'configure') &&
    selectedKey.value === null
)
const displayBaseUrl = computed(() => appStore.apiBaseUrl || window.location.origin)
const generatedFiles = computed(() => {
  if (!selectedKey.value) return []
  return buildClientConfigFiles({
    client: guideStore.progress.client,
    os: guideStore.progress.os,
    platform: resolveConfigPlatform(selectedKey.value),
    apiKey: selectedKey.value.key,
    baseUrl: appStore.apiBaseUrl || window.location.origin,
    allowMessagesDispatch: selectedKey.value.group?.allow_messages_dispatch ?? false
  })
})
const completionDestinations = computed(() => [
  {
    path: authStore.isAdmin ? '/admin/my-account/dashboard' : '/dashboard',
    labelKey: 'gettingStarted.completion.dashboard'
  },
  { path: '/keys', labelKey: 'gettingStarted.completion.keys' },
  { path: '/usage', labelKey: 'gettingStarted.completion.usage' }
])
const persistenceWarningKey = computed(() => {
  if (guideStore.persistenceIssue === 'load') {
    return 'gettingStarted.warnings.progressUnavailable'
  }
  if (guideStore.persistenceIssue === 'save') {
    return 'gettingStarted.warnings.progressSaveFailed'
  }
  return null
})

function detectBrowserOS(): BeginnerGuideOS {
  const platform = `${navigator.platform || ''} ${navigator.userAgent || ''}`.toLowerCase()
  if (platform.includes('win')) return 'windows'
  if (platform.includes('mac')) return 'macos'
  if (platform.includes('linux') || platform.includes('x11')) return 'linux'
  return 'macos'
}

function hasPersistedAnonymousProgress(): boolean | null {
  try {
    return localStorage.getItem('beginner_guide_progress_v1') !== null
  } catch {
    return null
  }
}

function resolveConfigPlatform(key: ApiKey): GroupPlatform | 'unified' {
  if (key.key_type === 'unified') return 'unified'
  if (key.group?.platform) return key.group.platform
  if (key.key_type === 'anthropic' || key.key_type === 'openai') return key.key_type
  return 'unified'
}

function clearEphemeralConfiguration(): void {
  selectedKey.value = null
}

function handleSelectKey(key: ApiKey): void {
  selectedKey.value = key
  reselectRequired.value = false
}

watch(
  [() => authStore.isAuthenticated, () => authStore.user?.id],
  async ([authenticated, userId]) => {
    guideOwnerGeneration += 1
    nextPending.value = false
    redirectingConfigure = false
    clearEphemeralConfiguration()
    reselectRequired.value = false
    if (authenticated && userId !== undefined) {
      await guideStore.initialize({ authenticated: true, userId, enteringGuide: true })
      return
    }

    await guideStore.initialize({ authenticated: false, userId: null, enteringGuide: true })
    if (!manualOSSelected.value && hasPersistedAnonymousProgress() === false) {
      await guideStore.selectOS(detectBrowserOS())
    }
  },
  { immediate: true }
)

watch(
  [() => guideStore.progress.client, () => guideStore.progress.os],
  ([client, os], previous) => {
    if (previous && (client !== previous[0] || os !== previous[1])) {
      clearEphemeralConfiguration()
    }
  }
)

watch(
  activeStep,
  async (step) => {
    if (
      step !== 'configure' ||
      selectedKey.value !== null ||
      redirectingConfigure ||
      disposed
    ) {
      return
    }
    const ownerGeneration = guideOwnerGeneration
    redirectingConfigure = true
    reselectRequired.value = true
    try {
      await guideStore.goToStep('api_key')
    } finally {
      if (guideOwnerGeneration === ownerGeneration) {
        redirectingConfigure = false
      }
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

async function selectClient(client: BeginnerGuideClient): Promise<void> {
  if (nextPending.value) return
  await guideStore.selectClient(client)
}

async function selectOS(os: BeginnerGuideOS): Promise<void> {
  if (nextPending.value) return
  manualOSSelected.value = true
  await guideStore.selectOS(os)
}

async function handleBack(): Promise<void> {
  if (activeStepIndex.value <= 0) return
  await guideStore.goToStep(GUIDE_STEP_IDS[activeStepIndex.value - 1])
}

async function handleNext(): Promise<void> {
  if (taskEightBlocked.value || nextPending.value) return

  const initiatingStep = activeStep.value
  const initiatingIndex = GUIDE_STEP_IDS.indexOf(initiatingStep)
  const initiatingClient = guideStore.progress.client
  const initiatingOS = guideStore.progress.os
  const initiatingOwner =
    authStore.isAuthenticated && authStore.user?.id !== undefined
      ? `user:${String(authStore.user.id)}`
      : 'anonymous'
  const initiatingOwnerGeneration = guideOwnerGeneration
  if (initiatingIndex === -1) return

  nextPending.value = true
  try {
    await guideStore.completeStep(initiatingStep)

    if (disposed || guideOwnerGeneration !== initiatingOwnerGeneration) {
      return
    }

    const currentOwner =
      authStore.isAuthenticated && authStore.user?.id !== undefined
        ? `user:${String(authStore.user.id)}`
        : 'anonymous'
    if (
      currentOwner !== initiatingOwner ||
      activeStep.value !== initiatingStep ||
      guideStore.progress.client !== initiatingClient ||
      guideStore.progress.os !== initiatingOS ||
      !guideStore.progress.completedSteps.includes(initiatingStep)
    ) {
      return
    }

    if (initiatingStep === 'troubleshoot') {
      await guideStore.completeGuide()
      return
    }
    const next = GUIDE_STEP_IDS[initiatingIndex + 1]
    if (next) {
      await guideStore.goToStep(next)
    }
  } finally {
    if (guideOwnerGeneration === initiatingOwnerGeneration) {
      nextPending.value = false
    }
  }
}

onBeforeUnmount(() => {
  disposed = true
  guideOwnerGeneration += 1
  nextPending.value = false
  clearEphemeralConfiguration()
})
</script>
