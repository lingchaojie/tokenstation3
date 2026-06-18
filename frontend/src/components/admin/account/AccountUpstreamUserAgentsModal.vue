<template>
  <BaseDialog
    :show="show"
    :title="dialogTitle"
    width="wide"
    @close="emit('close')"
  >
    <div class="space-y-4">
      <div v-if="account" class="flex items-center justify-between gap-3">
        <div class="min-w-0">
          <p class="truncate text-sm font-medium text-gray-900 dark:text-gray-100">
            {{ account.name }}
          </p>
          <p class="text-xs text-gray-500 dark:text-dark-400">
            {{ account.platform }} / {{ account.type }}
          </p>
        </div>
        <button
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="loading"
          @click="load"
        >
          {{ t('common.refresh') }}
        </button>
      </div>

      <div v-if="loading" class="space-y-2">
        <div v-for="i in 3" :key="i" class="rounded-md border border-gray-200 p-3 dark:border-dark-700">
          <div class="h-4 w-3/4 animate-pulse rounded bg-gray-200 dark:bg-dark-700"></div>
          <div class="mt-2 h-3 w-1/3 animate-pulse rounded bg-gray-200 dark:bg-dark-700"></div>
        </div>
      </div>

      <div
        v-else-if="error"
        class="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-300"
      >
        {{ error }}
      </div>

      <div
        v-else-if="items.length === 0"
        class="rounded-md border border-gray-200 px-4 py-8 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-dark-400"
      >
        {{ t('admin.accounts.upstreamUserAgents.empty') }}
      </div>

      <div v-else class="overflow-hidden rounded-md border border-gray-200 dark:border-dark-700">
        <div
          v-for="item in items"
          :key="item.user_agent"
          class="border-b border-gray-100 p-3 last:border-b-0 dark:border-dark-700"
        >
          <div class="break-all font-mono text-xs leading-5 text-gray-900 dark:text-gray-100">
            {{ item.user_agent }}
          </div>
          <div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-gray-500 dark:text-dark-400">
            <span>{{ t('admin.accounts.upstreamUserAgents.lastSeen') }}: {{ formatDateTime(item.last_seen_at) }}</span>
            <span>{{ t('admin.accounts.upstreamUserAgents.firstSeen') }}: {{ formatDateTime(item.first_seen_at) }}</span>
            <span>{{ t('admin.accounts.upstreamUserAgents.count', { count: item.seen_count }) }}</span>
          </div>
        </div>
      </div>
    </div>

    <template #footer>
      <button type="button" class="btn btn-secondary" @click="emit('close')">
        {{ t('common.close') }}
      </button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { Account } from '@/types'
import type { AccountUpstreamUserAgent } from '@/api/admin/accounts'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { formatDateTime } from '@/utils/format'

const props = defineProps<{
  show: boolean
  account: Account | null
}>()

const emit = defineEmits<{
  close: []
}>()

const { t } = useI18n()
const loading = ref(false)
const error = ref('')
const items = ref<AccountUpstreamUserAgent[]>([])

const dialogTitle = computed(() => t('admin.accounts.upstreamUserAgents.title'))

async function load() {
  if (!props.account) {
    items.value = []
    return
  }

  loading.value = true
  error.value = ''
  try {
    const result = await adminAPI.accounts.getUpstreamUserAgents(props.account.id)
    items.value = result.items ?? []
  } catch (err: any) {
    error.value = err?.message || t('admin.accounts.upstreamUserAgents.loadFailed')
    items.value = []
  } finally {
    loading.value = false
  }
}

watch(
  () => [props.show, props.account?.id] as const,
  ([visible]) => {
    if (visible) {
      void load()
    } else {
      items.value = []
      error.value = ''
      loading.value = false
    }
  },
  { immediate: true }
)
</script>
