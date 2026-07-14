<template>
  <section v-if="!authStore.isAuthenticated" class="space-y-4" data-testid="api-key-anonymous">
    <div class="rounded-xl border border-primary-500/20 bg-primary-500/5 p-5">
      <h2 class="text-base font-semibold text-gray-950 dark:text-linear-ink">
        {{ t('gettingStarted.apiKey.anonymousTitle') }}
      </h2>
      <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-linear-ink-subtle">
        {{ t('gettingStarted.apiKey.anonymousDescription') }}
      </p>
      <div class="mt-4 flex flex-wrap gap-2">
        <router-link
          to="/login?redirect=/getting-started"
          data-testid="api-key-login"
          class="inline-flex items-center rounded-lg bg-primary-500 px-4 py-2.5 text-sm font-medium text-white hover:bg-primary-400"
        >
          {{ t('gettingStarted.apiKey.login') }}
        </router-link>
        <router-link
          to="/register?redirect=/getting-started"
          data-testid="api-key-register"
          class="inline-flex items-center rounded-lg border border-gray-200 px-4 py-2.5 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-linear-hairline dark:text-linear-ink dark:hover:bg-linear-surface-2"
        >
          {{ t('gettingStarted.apiKey.register') }}
        </router-link>
      </div>
    </div>
  </section>

  <section v-else class="space-y-5" data-testid="api-key-authenticated">
    <p
      v-if="reselectRequired"
      data-testid="api-key-reselect"
      class="rounded-xl border border-amber-400/30 bg-amber-500/10 p-4 text-sm leading-6 text-gray-700 dark:text-linear-ink-subtle"
    >
      {{ t('gettingStarted.configuration.reselectAfterRefresh') }}
    </p>

    <p class="rounded-xl border border-amber-400/30 bg-amber-500/10 p-4 text-sm leading-6 text-gray-700 dark:text-linear-ink-subtle">
      {{ t('gettingStarted.apiKey.secretWarning') }}
    </p>

    <p
      v-if="loading"
      data-testid="api-key-loading"
      class="rounded-xl border border-gray-200 bg-gray-50 p-4 text-sm text-gray-600 dark:border-linear-hairline dark:bg-linear-canvas dark:text-linear-ink-subtle"
      aria-live="polite"
    >
      {{ t('gettingStarted.apiKey.loading') }}
    </p>

    <div
      v-else-if="loadFailed"
      data-testid="api-key-error"
      class="rounded-xl border border-red-500/20 bg-red-500/5 p-4"
      role="alert"
    >
      <p class="text-sm text-red-700 dark:text-red-300">{{ t('keys.failedToLoad') }}</p>
      <div class="mt-3 flex flex-wrap gap-2">
        <button
          type="button"
          data-testid="api-key-retry"
          class="rounded-lg bg-primary-500 px-3 py-2 text-sm font-medium text-white hover:bg-primary-400"
          @click="loadKeys"
        >
          {{ t('gettingStarted.troubleshooting.retry') }}
        </button>
        <router-link
          to="/keys"
          data-testid="api-key-fallback"
          class="rounded-lg border border-gray-200 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-linear-hairline dark:text-linear-ink"
        >
          {{ t('gettingStarted.completion.keys') }}
        </router-link>
      </div>
    </div>

    <template v-else>
      <fieldset v-if="apiKeys.length" class="space-y-3">
        <legend class="text-base font-semibold text-gray-950 dark:text-linear-ink">
          {{ t('gettingStarted.apiKey.existingTitle') }}
        </legend>
        <div class="grid gap-3">
          <div
            v-for="apiKey in apiKeys"
            :key="apiKey.id"
            class="rounded-xl border border-gray-200 bg-white p-4 dark:border-linear-hairline dark:bg-linear-surface-1"
          >
            <button
              type="button"
              :data-key-id="apiKey.id"
              :disabled="keyExplanation(apiKey) !== null"
              :aria-pressed="selectedKey?.id === apiKey.id"
              class="flex w-full items-center justify-between gap-3 rounded-lg text-left disabled:cursor-not-allowed disabled:opacity-55"
              @click="selectKey(apiKey)"
            >
              <span class="min-w-0">
                <span class="block truncate text-sm font-semibold text-gray-950 dark:text-linear-ink">
                  {{ apiKey.name }}
                </span>
                <span class="mt-1 block text-xs text-gray-500 dark:text-linear-ink-tertiary">
                  {{ t(`keys.status.${apiKey.status}`) }}
                </span>
              </span>
              <span
                v-if="selectedKey?.id === apiKey.id"
                class="shrink-0 text-sm font-semibold text-primary-600 dark:text-primary-300"
              >
                ✓
              </span>
            </button>
            <p
              v-if="keyExplanation(apiKey)"
              :data-key-explanation="apiKey.id"
              class="mt-2 text-xs leading-5 text-amber-700 dark:text-amber-300"
            >
              {{ keyExplanation(apiKey) }}
            </p>
          </div>
        </div>
      </fieldset>

      <div
        v-if="compatibleKeyCount === 0"
        data-testid="api-key-empty"
        class="rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-linear-hairline dark:bg-linear-canvas"
      >
        <h2 class="text-base font-semibold text-gray-950 dark:text-linear-ink">
          {{ t('gettingStarted.apiKey.emptyTitle') }}
        </h2>
        <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-linear-ink-subtle">
          {{ t('gettingStarted.apiKey.emptyDescription') }}
        </p>
      </div>

      <form class="rounded-xl border border-gray-200 p-4 dark:border-linear-hairline" @submit.prevent="createKey">
        <label for="guide-api-key-name" class="block text-sm font-medium text-gray-800 dark:text-linear-ink">
          {{ t('keys.nameLabel') }}
        </label>
        <div class="mt-2 flex flex-col gap-2 sm:flex-row">
          <input
            id="guide-api-key-name"
            v-model="createName"
            data-testid="api-key-name"
            type="text"
            required
            autocomplete="off"
            class="min-w-0 flex-1 rounded-lg border border-gray-300 bg-white px-3 py-2.5 text-sm text-gray-950 outline-none focus:border-primary-500 focus:ring-2 focus:ring-primary-500/20 dark:border-linear-hairline dark:bg-linear-canvas dark:text-linear-ink"
          />
          <button
            type="submit"
            data-testid="api-key-create"
            :disabled="creating || createName.trim().length === 0"
            :aria-busy="creating || undefined"
            class="rounded-lg bg-primary-500 px-4 py-2.5 text-sm font-medium text-white hover:bg-primary-400 disabled:cursor-not-allowed disabled:opacity-55"
          >
            {{ creating ? t('keys.saving') : t('gettingStarted.apiKey.create') }}
          </button>
        </div>
      </form>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { keysAPI } from '@/api/keys'
import { useAppStore, useAuthStore } from '@/stores'
import type { BeginnerGuideClient, BeginnerGuideOS } from '@/api/beginnerGuide'
import type { ApiKey } from '@/types'

const props = defineProps<{
  client: BeginnerGuideClient
  os: BeginnerGuideOS
  selectedKey: ApiKey | null
  reselectRequired?: boolean
}>()

const emit = defineEmits<{
  select: [key: ApiKey]
}>()

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const apiKeys = shallowRef<ApiKey[]>([])
const loading = ref(false)
const loadFailed = ref(false)
const creating = ref(false)
const createName = ref(t('keys.namePlaceholder'))
const owner = computed(() =>
  authStore.isAuthenticated && authStore.user?.id !== undefined
    ? `user:${String(authStore.user.id)}`
    : null
)
const compatibleKeyCount = computed(
  () => apiKeys.value.filter((apiKey) => keyExplanation(apiKey) === null).length
)

let listEpoch = 0
let createEpoch = 0
let selectionIntentEpoch = 0
let disposed = false

function isCompatible(apiKey: ApiKey): boolean {
  if (apiKey.key_type === 'unified') return true
  if (props.client === 'codex') return apiKey.key_type === 'openai'
  if (apiKey.key_type === 'anthropic') return true
  return (
    apiKey.key_type === 'openai' &&
    (apiKey.group?.allow_messages_dispatch ?? false)
  )
}

function keyExplanation(apiKey: ApiKey): string | null {
  if (apiKey.status !== 'active') {
    return `${t(`keys.status.${apiKey.status}`)}. ${t('gettingStarted.apiKey.inactive')}`
  }
  if (!isCompatible(apiKey)) {
    return t('gettingStarted.apiKey.incompatible')
  }
  return null
}

function selectKey(apiKey: ApiKey): void {
  selectionIntentEpoch += 1
  emit('select', apiKey)
}

async function loadKeys(): Promise<void> {
  const initiatingOwner = owner.value
  if (initiatingOwner === null || loading.value) return
  const epoch = ++listEpoch
  loading.value = true
  loadFailed.value = false
  try {
    const page = await keysAPI.list(1, 100)
    if (!disposed && epoch === listEpoch && owner.value === initiatingOwner) {
      apiKeys.value = page.items
    }
  } catch {
    if (!disposed && epoch === listEpoch && owner.value === initiatingOwner) {
      apiKeys.value = []
      loadFailed.value = true
    }
  } finally {
    if (!disposed && epoch === listEpoch && owner.value === initiatingOwner) {
      loading.value = false
    }
  }
}

function errorDetail(error: unknown): string {
  if (typeof error !== 'object' || error === null) return t('keys.failedToSave')
  const response = (error as Record<string, unknown>).response
  if (typeof response !== 'object' || response === null) return t('keys.failedToSave')
  const data = (response as Record<string, unknown>).data
  if (typeof data !== 'object' || data === null) return t('keys.failedToSave')
  const detail = (data as Record<string, unknown>).detail
  return typeof detail === 'string' && detail.trim() ? detail : t('keys.failedToSave')
}

async function createKey(): Promise<void> {
  const initiatingOwner = owner.value
  const name = createName.value.trim()
  if (initiatingOwner === null || creating.value || !name) return
  const epoch = ++createEpoch
  const initiatingSelectionIntent = selectionIntentEpoch
  creating.value = true
  try {
    const created = await keysAPI.create(name)
    if (
      !disposed &&
      epoch === createEpoch &&
      selectionIntentEpoch === initiatingSelectionIntent &&
      owner.value === initiatingOwner
    ) {
      apiKeys.value = [created, ...apiKeys.value.filter((apiKey) => apiKey.id !== created.id)]
      emit('select', created)
    }
  } catch (error: unknown) {
    if (!disposed && epoch === createEpoch && owner.value === initiatingOwner) {
      appStore.showError(errorDetail(error))
    }
  } finally {
    if (!disposed && epoch === createEpoch && owner.value === initiatingOwner) {
      creating.value = false
    }
  }
}

watch(
  owner,
  (nextOwner) => {
    listEpoch += 1
    createEpoch += 1
    selectionIntentEpoch += 1
    apiKeys.value = []
    loading.value = false
    loadFailed.value = false
    creating.value = false
    if (nextOwner !== null) {
      void loadKeys()
    }
  },
  { immediate: true }
)

watch(
  [() => props.client, () => props.os],
  ([client, os], previous) => {
    if (previous && (client !== previous[0] || os !== previous[1])) {
      selectionIntentEpoch += 1
    }
  }
)

onBeforeUnmount(() => {
  disposed = true
  listEpoch += 1
  createEpoch += 1
  selectionIntentEpoch += 1
  apiKeys.value = []
  creating.value = false
})
</script>
