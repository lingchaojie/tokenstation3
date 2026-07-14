<template>
  <AppLayout>
    <div class="mx-auto max-w-4xl space-y-6">
      <header>
        <h1 class="text-2xl font-semibold text-gray-950 dark:text-white">
          {{ t('admin.affiliates.checkIn.title') }}
        </h1>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
          {{ t('admin.affiliates.checkIn.description') }}
        </p>
      </header>

      <section class="card">
        <div class="divide-y divide-gray-100 dark:divide-dark-700">
          <div class="flex items-start justify-between gap-6 p-6">
            <div>
              <label class="text-sm font-semibold text-gray-900 dark:text-white">
                {{ t('admin.affiliates.checkIn.enabled') }}
              </label>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t('admin.affiliates.checkIn.enabledHint') }}
              </p>
            </div>
            <Toggle
              v-model="form.enabled"
              data-testid="check-in-enabled"
              :disabled="loading || saving"
            />
          </div>

          <div class="grid gap-5 p-6 md:grid-cols-2">
            <div>
              <label for="check-in-start-at" class="input-label">
                {{ t('admin.affiliates.checkIn.startAt') }}
              </label>
              <input
                id="check-in-start-at"
                v-model="form.startAt"
                data-testid="check-in-start-at"
                type="datetime-local"
                class="input mt-1"
                :disabled="loading || saving"
              />
              <p class="input-hint">{{ t('admin.affiliates.checkIn.startAtHint') }}</p>
            </div>

            <div>
              <label for="check-in-duration-days" class="input-label">
                {{ t('admin.affiliates.checkIn.durationDays') }}
              </label>
              <div class="relative mt-1">
                <input
                  id="check-in-duration-days"
                  v-model.number="form.durationDays"
                  data-testid="check-in-duration-days"
                  type="number"
                  min="1"
                  step="1"
                  class="input pr-12"
                  :disabled="loading || saving"
                />
                <span class="pointer-events-none absolute inset-y-0 right-3 flex items-center text-sm text-gray-400">
                  {{ t('admin.affiliates.checkIn.days') }}
                </span>
              </div>
            </div>

            <div>
              <label for="check-in-reward-amount" class="input-label">
                {{ t('admin.affiliates.checkIn.rewardAmount') }}
              </label>
              <div class="relative mt-1">
                <span class="pointer-events-none absolute inset-y-0 left-3 flex items-center text-gray-400">$</span>
                <input
                  id="check-in-reward-amount"
                  :value="form.rewardAmount"
                  data-testid="check-in-reward-amount"
                  type="number"
                  min="0.00000001"
                  step="0.00000001"
                  class="input pl-8"
                  :disabled="loading || saving"
                  @input="handleRewardInput"
                />
              </div>
              <p class="input-hint">{{ t('admin.affiliates.checkIn.rewardHint') }}</p>
            </div>

            <div>
              <p class="input-label">{{ t('admin.affiliates.checkIn.endAt') }}</p>
              <div
                data-testid="check-in-end-preview"
                class="mt-1 flex h-10 items-center rounded-lg border border-gray-200 bg-gray-50 px-3 text-sm text-gray-700 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300"
              >
                {{ endPreview || '—' }}
              </div>
            </div>
          </div>

          <div class="flex flex-wrap items-center justify-between gap-4 p-6">
            <div class="flex items-center gap-2 text-sm text-gray-500 dark:text-gray-400">
              <span>{{ t('admin.affiliates.checkIn.state') }}</span>
              <span data-testid="check-in-state" class="badge" :class="stateClass">
                {{ stateText }}
              </span>
            </div>
            <button
              data-testid="check-in-save"
              type="button"
              class="btn btn-primary"
              :disabled="loading || saving"
              @click="save"
            >
              <Icon v-if="!saving" name="check" size="sm" class="mr-2" />
              {{ saving ? t('admin.affiliates.checkIn.saving') : t('admin.affiliates.checkIn.save') }}
            </button>
          </div>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import {
  adminCheckInAPI,
  type DailyCheckInAdminConfig,
  type UpdateDailyCheckInAdminConfig,
} from '@/api/admin/checkIn'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useAppStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'
import { isoToUTC8LocalInput, utc8LocalInputToISO } from '@/utils/dailyCheckIn'

const { locale, t } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const saving = ref(false)
const config = ref<DailyCheckInAdminConfig | null>(null)
const form = reactive({
  enabled: false,
  startAt: '',
  durationDays: 0,
  rewardAmount: '10',
})

const endPreview = computed(() => {
  if (!form.startAt || !Number.isInteger(form.durationDays) || form.durationDays <= 0) return ''
  try {
    const start = Date.parse(utc8LocalInputToISO(form.startAt))
    return formatUTC8DateTime(new Date(start + form.durationDays * 24 * 60 * 60 * 1000))
  } catch {
    return ''
  }
})
const stateText = computed(() => t(`admin.affiliates.checkIn.states.${config.value?.state ?? 'disabled'}`))
const stateClass = computed(() => {
  switch (config.value?.state) {
    case 'active':
      return 'badge-success'
    case 'upcoming':
      return 'badge-primary'
    case 'ended':
      return 'badge-warning'
    default:
      return 'badge-gray'
  }
})

function formatUTC8DateTime(value: Date): string {
  return new Intl.DateTimeFormat(locale.value, {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(value)
}

function applyConfig(value: DailyCheckInAdminConfig): void {
  config.value = value
  form.enabled = value.enabled
  form.startAt = isoToUTC8LocalInput(value.start_at)
  form.durationDays = value.duration_days
  form.rewardAmount = String(value.reward_amount)
}

function handleRewardInput(event: Event): void {
  form.rewardAmount = (event.target as HTMLInputElement).value
}

function buildInput(): UpdateDailyCheckInAdminConfig | null {
  const rewardText = String(form.rewardAmount).trim()
  const reward = Number(rewardText)
  if (!/^\d+(?:\.\d{1,8})?$/.test(rewardText) || !Number.isFinite(reward) || reward <= 0) {
    appStore.showError(t('admin.affiliates.checkIn.validation.reward'))
    return null
  }
  if (!Number.isInteger(form.durationDays) || form.durationDays < 0 || (form.enabled && form.durationDays <= 0)) {
    appStore.showError(t('admin.affiliates.checkIn.validation.duration'))
    return null
  }

  let startAt: string | null = null
  if (form.startAt) {
    try {
      startAt = utc8LocalInputToISO(form.startAt)
    } catch {
      appStore.showError(t('admin.affiliates.checkIn.validation.startAt'))
      return null
    }
  }
  if (form.enabled && !startAt) {
    appStore.showError(t('admin.affiliates.checkIn.validation.startAt'))
    return null
  }

  return {
    enabled: form.enabled,
    start_at: startAt,
    duration_days: form.durationDays,
    reward_amount: reward,
  }
}

async function load(): Promise<void> {
  loading.value = true
  try {
    applyConfig(await adminCheckInAPI.getConfig())
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.affiliates.checkIn.loadFailed')))
  } finally {
    loading.value = false
  }
}

async function save(): Promise<void> {
  const input = buildInput()
  if (!input) return

  saving.value = true
  try {
    applyConfig(await adminCheckInAPI.updateConfig(input))
    appStore.showSuccess(t('admin.affiliates.checkIn.saveSuccess'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.affiliates.checkIn.saveFailed')))
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  void load()
})
</script>
