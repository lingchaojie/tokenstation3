<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl space-y-6">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 class="text-2xl font-semibold text-gray-950 dark:text-white">
            {{ t('admin.captureSettings.title') }}
          </h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.captureSettings.description') }}
          </p>
        </div>
        <button
          data-test="capture-save"
          type="button"
          class="btn btn-primary"
          :disabled="loading || saving || !settings"
          @click="save"
        >
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>

      <div v-if="loading" class="card flex items-center justify-center py-16 text-gray-500">
        {{ t('common.loading') }}
      </div>

      <template v-else>
        <section class="card overflow-hidden">
          <CardHeader
            :title="t('admin.captureSettings.infrastructure.title')"
            :description="t('admin.captureSettings.infrastructure.description')"
          />
          <div class="space-y-5 p-6">
            <div class="flex flex-wrap items-center gap-3">
              <span
                class="inline-flex items-center rounded-full px-2.5 py-1 text-xs font-medium"
                :class="infrastructureBadgeClass"
              >
                {{ infrastructureLabel }}
              </span>
              <span v-if="settings?.initialization_error" class="text-sm text-red-600 dark:text-red-400">
                {{ t('admin.captureSettings.infrastructure.failedHint') }}
              </span>
            </div>
            <dl class="grid gap-4 text-sm sm:grid-cols-2 lg:grid-cols-4">
              <InfoItem :label="t('admin.captureSettings.infrastructure.database')" :value="settings?.database || '—'" />
              <InfoItem :label="t('admin.captureSettings.infrastructure.table')" :value="settings?.table || '—'" />
              <InfoItem
                :label="t('admin.captureSettings.infrastructure.spoolMaxBytes')"
                :value="formatIECBytes(settings?.spool_max_bytes ?? 0)"
              />
              <InfoItem
                :label="t('admin.captureSettings.infrastructure.spoolMinFreeBytes')"
                :value="formatIECBytes(settings?.spool_min_free_bytes ?? 0)"
              />
            </dl>
          </div>
        </section>

        <section class="card overflow-hidden">
          <CardHeader
            :title="t('admin.captureSettings.runtime.title')"
            :description="t('admin.captureSettings.runtime.description')"
          />
          <div class="divide-y divide-gray-100 dark:divide-dark-700">
            <SettingRow
              data-test="capture-master"
              :title="t('admin.captureSettings.runtime.master')"
              :description="masterDescription"
            >
              <Toggle v-model="form.enabled" :disabled="masterEnableDisabled" />
            </SettingRow>
          </div>
        </section>

        <section class="card overflow-hidden">
          <CardHeader
            :title="t('admin.captureSettings.platforms.title')"
            :description="t('admin.captureSettings.platforms.description')"
          />
          <div class="divide-y divide-gray-100 dark:divide-dark-700">
            <SettingRow :title="t('admin.captureSettings.platforms.anthropic')">
              <Toggle v-model="form.platforms.anthropic" />
            </SettingRow>
            <SettingRow :title="t('admin.captureSettings.platforms.kiro')">
              <Toggle v-model="form.platforms.kiro" />
            </SettingRow>
            <SettingRow :title="t('admin.captureSettings.platforms.gemini')">
              <Toggle v-model="form.platforms.gemini" />
            </SettingRow>
            <SettingRow :title="t('admin.captureSettings.platforms.antigravity')">
              <Toggle v-model="form.platforms.antigravity" />
            </SettingRow>
            <SettingRow :title="t('admin.captureSettings.platforms.grok')">
              <Toggle v-model="form.platforms.grok" />
            </SettingRow>
            <SettingRow
              data-test="capture-cursor"
              :title="t('admin.captureSettings.platforms.cursor')"
              :description="t('admin.captureSettings.platforms.cursorDescription')"
            >
              <Toggle v-model="form.platforms.cursor" />
            </SettingRow>
            <SettingRow
              data-test="capture-openai"
              :title="t('admin.captureSettings.platforms.openai')"
              :description="t('admin.captureSettings.platforms.openaiDescription')"
            >
              <Toggle v-model="form.platforms.openai" />
            </SettingRow>
          </div>
        </section>

        <section class="card overflow-hidden">
          <CardHeader
            :title="t('admin.captureSettings.models.title')"
            :description="t('admin.captureSettings.models.description')"
          />
          <div class="grid gap-6 p-6 lg:grid-cols-2">
            <div>
              <label class="input-label">{{ t('admin.captureSettings.models.anthropic') }}</label>
              <textarea
                data-test="capture-models-anthropic"
                class="input min-h-28 font-mono text-sm"
                :value="form.model_allowlists.anthropic.join('\n')"
                :placeholder="t('admin.captureSettings.models.placeholder')"
                @input="setModelAllowlist('anthropic', $event)"
              ></textarea>
            </div>
            <div>
              <label class="input-label">{{ t('admin.captureSettings.models.kiro') }}</label>
              <textarea
                data-test="capture-models-kiro"
                class="input min-h-28 font-mono text-sm"
                :value="form.model_allowlists.kiro.join('\n')"
                :placeholder="t('admin.captureSettings.models.placeholder')"
                @input="setModelAllowlist('kiro', $event)"
              ></textarea>
            </div>
          </div>
        </section>

        <div class="grid gap-6 lg:grid-cols-2">
          <section class="card overflow-hidden">
            <CardHeader :title="t('admin.captureSettings.outcomes.title')" />
            <div class="divide-y divide-gray-100 dark:divide-dark-700">
              <SettingRow :title="t('admin.captureSettings.outcomes.success')">
                <Toggle v-model="form.outcomes.success" />
              </SettingRow>
              <SettingRow :title="t('admin.captureSettings.outcomes.terminalError')">
                <Toggle v-model="form.outcomes.terminal_error" />
              </SettingRow>
            </div>
          </section>

          <section class="card overflow-hidden">
            <CardHeader :title="t('admin.captureSettings.content.title')" />
            <div class="border-b border-amber-200 bg-amber-50 px-6 py-3 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-300">
              {{ t('admin.captureSettings.content.warning') }}
            </div>
            <div class="divide-y divide-gray-100 dark:divide-dark-700">
              <SettingRow :title="t('admin.captureSettings.content.rawRequest')">
                <Toggle v-model="form.content.raw_request" />
              </SettingRow>
              <SettingRow :title="t('admin.captureSettings.content.rawResponse')">
                <Toggle v-model="form.content.raw_response" />
              </SettingRow>
              <SettingRow :title="t('admin.captureSettings.content.requestHeaders')">
                <Toggle v-model="form.content.request_headers" />
              </SettingRow>
              <SettingRow :title="t('admin.captureSettings.content.responseHeaders')">
                <Toggle v-model="form.content.response_headers" />
              </SettingRow>
            </div>
          </section>
        </div>

        <section class="card overflow-visible">
          <CardHeader
            :title="t('admin.captureSettings.scope.title')"
            :description="t('admin.captureSettings.scope.description')"
          />
          <div class="grid gap-6 p-6 lg:grid-cols-2">
            <GroupSelector v-model="form.group_ids" :groups="groups" searchable />
            <div>
              <label class="input-label">{{ t('admin.captureSettings.scope.users') }}</label>
              <OpenAIFastPolicyUserSelector v-model="form.user_ids" />
            </div>
          </div>
        </section>

        <section class="card overflow-hidden">
          <CardHeader
            :title="t('admin.captureSettings.health.title')"
            :description="t('admin.captureSettings.health.description')"
          />
          <div v-if="settings?.provisioned && !settings.delivery_ready" class="border-b border-amber-200 bg-amber-50 px-6 py-4 text-sm text-amber-900 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-200">
            <div class="font-medium">{{ t('admin.captureSettings.health.deliveryDown') }}</div>
            <div v-if="settings.ready" class="mt-1">{{ t('admin.captureSettings.health.deliveryDownHint') }}</div>
          </div>
          <div v-if="spoolCapReached" class="border-b border-red-200 bg-red-50 px-6 py-4 text-sm text-red-800 dark:border-red-800 dark:bg-red-900/20 dark:text-red-200">
            {{ t('admin.captureSettings.health.spoolCap') }}
          </div>
          <div v-if="freeReserveReached" class="border-b border-red-200 bg-red-50 px-6 py-4 text-sm text-red-800 dark:border-red-800 dark:bg-red-900/20 dark:text-red-200">
            {{ t('admin.captureSettings.health.freeReserve') }}
          </div>
          <div class="grid gap-3 p-6 sm:grid-cols-2 lg:grid-cols-4">
            <Metric :label="t('admin.captureSettings.health.spoolUsage')" :value="`${formatIECBytes(settings?.spool_used_bytes ?? 0)} / ${formatIECBytes(settings?.spool_max_bytes ?? 0)}`" />
            <Metric :label="t('admin.captureSettings.health.filesystemFree')" :value="formatIECBytes(settings?.filesystem_free_bytes ?? 0)" />
            <Metric :label="t('admin.captureSettings.health.readyRecords')" :value="settings?.ready_records ?? 0" />
            <Metric :label="t('admin.captureSettings.health.backlogAge')" :value="formatDuration(settings?.oldest_ready_age_seconds ?? 0)" />
            <Metric :label="t('admin.captureSettings.health.uploadRetries')" :value="settings?.upload_retries ?? 0" />
            <Metric :label="t('admin.captureSettings.health.sidecarRestarts')" :value="settings?.sidecar_restart_count ?? 0" />
            <Metric :label="t('admin.captureSettings.health.dropped')" :value="settings?.dropped_records ?? 0" danger />
            <Metric :label="t('admin.captureSettings.health.lastUploadAt')" :value="formatOptionalDate(settings?.last_upload_at)" />
          </div>
          <dl class="grid gap-4 border-t border-gray-100 p-6 text-sm dark:border-dark-700 sm:grid-cols-2">
            <InfoItem :label="t('admin.captureSettings.health.currentBatch')" :value="settings?.current_batch_id || '—'" />
            <InfoItem :label="t('admin.captureSettings.health.healthSource')" :value="settings?.health_source_id || '—'" />
          </dl>
          <div v-if="lossReasons.length" class="border-t border-gray-100 p-6 dark:border-dark-700">
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.captureSettings.health.lossReasons') }}</h3>
            <dl class="mt-3 grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
              <InfoItem v-for="item in lossReasons" :key="item.reason" :label="lossReasonLabel(item.reason)" :value="String(item.count)" />
            </dl>
          </div>
        </section>

        <section class="card overflow-hidden">
          <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-6 py-4 dark:border-dark-700">
            <div>
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t('admin.captureSettings.history.title') }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t('admin.captureSettings.history.description') }}
              </p>
            </div>
            <div class="flex rounded-lg bg-gray-100 p-1 dark:bg-dark-700">
              <button
                v-for="range in historyRanges"
                :key="range"
                :data-test="`history-${range}`"
                type="button"
                class="rounded-md px-3 py-1.5 text-xs font-medium"
                :class="historyRange === range ? 'bg-white text-gray-900 shadow dark:bg-dark-600 dark:text-white' : 'text-gray-500'"
                @click="loadHistory(range)"
              >
                {{ range }}
              </button>
            </div>
          </div>
          <div v-if="historyLoading" class="p-8 text-center text-sm text-gray-500">
            {{ t('common.loading') }}
          </div>
          <div v-else-if="history.length === 0" class="p-8 text-center text-sm text-gray-500">
            {{ t('admin.captureSettings.history.empty') }}
          </div>
          <div v-else class="overflow-x-auto">
            <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
              <thead class="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-dark-800">
                <tr>
                  <th class="px-6 py-3">{{ t('admin.captureSettings.history.time') }}</th>
                  <th class="px-6 py-3">{{ t('admin.captureSettings.history.reason') }}</th>
                  <th class="px-6 py-3">{{ t('admin.captureSettings.history.records') }}</th>
                  <th class="px-6 py-3">{{ t('admin.captureSettings.history.bytes') }}</th>
                  <th class="px-6 py-3">{{ t('admin.captureSettings.history.spoolPeak') }}</th>
                  <th class="px-6 py-3">{{ t('admin.captureSettings.history.backlog') }}</th>
                  <th class="px-6 py-3">{{ t('admin.captureSettings.history.retriesRestarts') }}</th>
                  <th class="px-6 py-3">{{ t('admin.captureSettings.history.error') }}</th>
                  <th class="px-6 py-3">{{ t('admin.captureSettings.history.instance') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                <tr v-for="event in history" :key="`${event.minute_bucket}-${event.instance_id}-${event.reason}`">
                  <td class="whitespace-nowrap px-6 py-3 text-gray-600 dark:text-gray-300">{{ formatDate(event.minute_bucket) }}</td>
                  <td class="px-6 py-3 font-mono text-xs text-red-600 dark:text-red-400">{{ event.reason }}</td>
                  <td class="px-6 py-3 text-gray-900 dark:text-white">{{ event.dropped_records }}</td>
                  <td class="px-6 py-3 text-gray-900 dark:text-white">{{ formatBytes(event.dropped_bytes) }}</td>
                  <td data-test="capture-history-spool" class="whitespace-nowrap px-6 py-3 text-gray-500">
                    {{ formatIECBytes(event.spool_used_bytes_peak) }}
                  </td>
                  <td class="whitespace-nowrap px-6 py-3 text-gray-500">
                    {{ event.ready_records_peak }} / {{ formatDuration(event.oldest_ready_age_seconds_peak) }}
                  </td>
                  <td class="whitespace-nowrap px-6 py-3 text-gray-500">
                    {{ event.upload_retries }} / {{ event.sidecar_restarts }}
                  </td>
                  <td data-test="capture-history-error" class="max-w-sm truncate px-6 py-3 text-gray-500" :title="event.last_error">
                    {{ event.last_error || '—' }}
                  </td>
                  <td class="px-6 py-3 text-gray-500">{{ event.instance_id }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import Toggle from '@/components/common/Toggle.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import OpenAIFastPolicyUserSelector from '@/views/admin/settings/OpenAIFastPolicyUserSelector.vue'
import { adminAPI } from '@/api/admin'
import type {
  CaptureHealthEvent,
  CaptureHistoryRange,
  CaptureRuntimePolicy,
} from '@/api/admin/captureSettings'
import type { AdminGroup } from '@/types'
import { useAppStore, useCaptureHealthStore } from '@/stores'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
const appStore = useAppStore()
const captureStore = useCaptureHealthStore()
const groups = ref<AdminGroup[]>([])
const loading = ref(true)
const saving = ref(false)
const historyLoading = ref(false)
const historyRange = ref<CaptureHistoryRange>('24h')
const history = ref<CaptureHealthEvent[]>([])
const historyRanges: CaptureHistoryRange[] = ['24h', '7d', '30d']

const defaultPolicy = (): CaptureRuntimePolicy => ({
  version: 1,
  enabled: false,
  platforms: { anthropic: true, kiro: true, openai: false, gemini: true, antigravity: true, grok: true, cursor: true },
  outcomes: { success: true, terminal_error: true },
  content: { raw_request: true, raw_response: true, request_headers: true, response_headers: true },
  model_allowlists: { anthropic: ['claude-fable-5', 'claude-opus-5'], kiro: ['claude-fable-5', 'claude-opus-5'] },
  group_ids: [],
  user_ids: [],
})

const form = reactive<CaptureRuntimePolicy>(defaultPolicy())
const settings = computed(() => captureStore.settings)
const masterEnableDisabled = computed(() => !settings.value?.provisioned || (!settings.value.ready && !form.enabled))
const masterDescription = computed(() => {
  if (!settings.value?.provisioned) return t('admin.captureSettings.runtime.unavailable')
  if (!form.enabled && settings.value.sidecar_running) return t('admin.captureSettings.runtime.draining')
  return t('admin.captureSettings.runtime.masterDescription')
})
const infrastructureLabel = computed(() => {
  if (!settings.value?.provisioned) return t('admin.captureSettings.infrastructure.staticOff')
  if (!settings.value.sidecar_running) return t('admin.captureSettings.infrastructure.sidecarDown')
  if (!settings.value.spool_ready) return t('admin.captureSettings.infrastructure.spoolUnavailable')
  return t('admin.captureSettings.infrastructure.ready')
})
const infrastructureBadgeClass = computed(() => {
  if (!settings.value?.provisioned) return 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300'
  if (!settings.value.ready) return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
  return 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300'
})
const spoolCapReached = computed(() => {
  const current = settings.value
  return Boolean(current?.provisioned && current.spool_max_bytes > 0 && current.spool_used_bytes >= current.spool_max_bytes)
})
const freeReserveReached = computed(() => {
  const current = settings.value
  return Boolean(current?.provisioned && current.spool_min_free_bytes > 0 && current.filesystem_free_bytes <= current.spool_min_free_bytes)
})
const lossReasons = computed(() => Object.entries(settings.value?.dropped_by_reason ?? {})
  .filter(([, count]) => Number(count) > 0)
  .map(([reason, count]) => ({ reason, count })))

function copyPolicy(policy: CaptureRuntimePolicy): void {
  Object.assign(form, {
    ...policy,
    platforms: { ...policy.platforms },
    outcomes: { ...policy.outcomes },
    content: { ...policy.content },
    model_allowlists: {
      anthropic: [...(policy.model_allowlists?.anthropic ?? [])],
      kiro: [...(policy.model_allowlists?.kiro ?? [])],
    },
    group_ids: [...policy.group_ids],
    user_ids: [...policy.user_ids],
  })
}

function normalizedPolicy(): CaptureRuntimePolicy {
  const ids = (values: number[]) => Array.from(new Set(values.filter((id) => Number.isInteger(id) && id > 0))).sort((a, b) => a - b)
  const models = (values: string[]) => Array.from(new Set(values.map((model) => model.trim().toLowerCase()).filter(Boolean))).sort()
  return {
    version: 1,
    enabled: Boolean(form.enabled),
    platforms: { ...form.platforms },
    outcomes: { ...form.outcomes },
    content: { ...form.content },
    model_allowlists: {
      anthropic: models(form.model_allowlists.anthropic),
      kiro: models(form.model_allowlists.kiro),
    },
    group_ids: ids(form.group_ids),
    user_ids: ids(form.user_ids),
  }
}

function setModelAllowlist(platform: 'anthropic' | 'kiro', event: Event): void {
  const value = (event.target as HTMLTextAreaElement).value
  form.model_allowlists[platform] = value.split(/[\n,]/)
}

async function save(): Promise<void> {
  saving.value = true
  try {
    const updated = await adminAPI.capture.updateCaptureSettings(normalizedPolicy())
    captureStore.applySettings(updated)
    copyPolicy(updated.policy)
    captureStore.acknowledgeLoss()
    appStore.showSuccess(t('admin.captureSettings.saved'))
  } catch {
    appStore.showError(t('admin.captureSettings.saveFailed'))
  } finally {
    saving.value = false
  }
}

async function loadHistory(range: CaptureHistoryRange): Promise<void> {
  historyRange.value = range
  historyLoading.value = true
  try {
    const result = await adminAPI.capture.getCaptureHealthHistory(range)
    history.value = result.events
  } catch {
    appStore.showError(t('admin.captureSettings.history.loadFailed'))
  } finally {
    historyLoading.value = false
  }
}

function formatBytes(value: number): string {
  const bytes = Math.max(0, Number(value) || 0)
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let amount = bytes / 1024
  let unit = 0
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024
    unit += 1
  }
  return `${Number(amount.toFixed(amount >= 10 ? 1 : 2))} ${units[unit]}`
}

function formatIECBytes(value: number): string {
  const bytes = Math.max(0, Number(value) || 0)
  if (bytes < 1024) return `${bytes} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let amount = bytes / 1024
  let unit = 0
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024
    unit += 1
  }
  return `${Number(amount.toFixed(amount >= 10 ? 1 : 2))} ${units[unit]}`
}

function formatDuration(value: number): string {
  const seconds = Math.max(0, Math.floor(Number(value) || 0))
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`
  return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`
}

const lossReasonKeys: Record<string, string> = {
  ipc_unavailable: 'ipcUnavailable',
  ipc_backpressure: 'ipcBackpressure',
  sidecar_down: 'sidecarDown',
  spool_cap: 'spoolCap',
  spool_free_reserve: 'spoolFreeReserve',
  spool_corrupt: 'spoolCorrupt',
  pre_commit_disconnect: 'preCommitDisconnect',
}

function lossReasonLabel(reason: string): string {
  const key = lossReasonKeys[reason]
  return key ? t(`admin.captureSettings.lossReasons.${key}`) : reason
}

function formatDate(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

function formatOptionalDate(value?: string | null): string {
  return value ? formatDate(value) : '—'
}

const CardHeader = defineComponent({
  props: { title: { type: String, required: true }, description: { type: String, default: '' } },
  setup(props) {
    return () => h('div', { class: 'border-b border-gray-100 px-6 py-4 dark:border-dark-700' }, [
      h('h2', { class: 'text-lg font-semibold text-gray-900 dark:text-white' }, props.title),
      props.description ? h('p', { class: 'mt-1 text-sm text-gray-500 dark:text-gray-400' }, props.description) : null,
    ])
  },
})

const SettingRow = defineComponent({
  inheritAttrs: false,
  props: { title: { type: String, required: true }, description: { type: String, default: '' } },
  setup(props, { attrs, slots }) {
    return () => h('div', { ...attrs, class: ['flex items-center justify-between gap-6 px-6 py-4', attrs.class] }, [
      h('div', [
        h('div', { class: 'text-sm font-medium text-gray-900 dark:text-white' }, props.title),
        props.description ? h('p', { class: 'mt-1 text-xs text-gray-500 dark:text-gray-400' }, props.description) : null,
      ]),
      slots.default?.(),
    ])
  },
})

const InfoItem = defineComponent({
  props: { label: { type: String, required: true }, value: { type: String, required: true } },
  setup(props) {
    return () => h('div', [
      h('dt', { class: 'text-xs text-gray-500 dark:text-gray-400' }, props.label),
      h('dd', { class: 'mt-1 break-all font-medium text-gray-900 dark:text-white' }, props.value),
    ])
  },
})

const Metric = defineComponent({
  props: { label: { type: String, required: true }, value: { type: [String, Number], required: true }, danger: Boolean },
  setup(props) {
    return () => h('div', { class: 'rounded-lg bg-gray-50 p-4 dark:bg-dark-800' }, [
      h('div', { class: 'text-xs text-gray-500 dark:text-gray-400' }, props.label),
      h('div', { class: ['mt-1 text-xl font-semibold', props.danger ? 'text-red-600 dark:text-red-400' : 'text-gray-950 dark:text-white'] }, String(props.value)),
    ])
  },
})

onMounted(async () => {
  await captureStore.startPolling()
  if (settings.value) copyPolicy(settings.value.policy)
  captureStore.acknowledgeLoss()
  await Promise.all([
    adminAPI.groups.getAll().then((result) => { groups.value = result }).catch(() => { groups.value = [] }),
    loadHistory('24h'),
  ])
  loading.value = false
})

onBeforeUnmount(() => {
  captureStore.stopPolling()
})
</script>
