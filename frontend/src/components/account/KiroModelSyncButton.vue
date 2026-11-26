<template>
  <button
    type="button"
    data-testid="kiro-sync-upstream-models"
    :disabled="syncing || (!accountId && !credentials)"
    class="btn btn-secondary mt-3 w-full"
    @click="syncModels"
  >
    {{ t(syncing ? 'admin.accounts.syncUpstreamModelsLoading' : 'admin.accounts.syncUpstreamModels') }}
  </button>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { accountsAPI, type SyncUpstreamPreviewParams } from '@/api/admin/accounts'
import { appendKiroSyncedMappings, type ModelMappingEntry } from '@/composables/useModelWhitelist'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{
  modelValue: ModelMappingEntry[]
  accountId?: number
  credentials?: SyncUpstreamPreviewParams
  direct?: boolean
}>()
const emit = defineEmits<{
  'update:modelValue': [value: ModelMappingEntry[]]
}>()

const { t } = useI18n()
const appStore = useAppStore()
const syncing = ref(false)
let disposed = false
onBeforeUnmount(() => { disposed = true })

async function syncModels() {
  const { accountId, credentials, direct } = props
  if (syncing.value || (!accountId && !credentials)) return
  const isCurrent = () => !disposed && props.accountId === accountId &&
    props.credentials === credentials && props.direct === direct

  syncing.value = true
  try {
    const result = accountId
      ? await accountsAPI.syncUpstreamModels(accountId)
      : await accountsAPI.syncUpstreamModelsPreview(credentials!)

    if (!isCurrent()) return
    if (result.models.length === 0) {
      appStore.showInfo(t('admin.accounts.syncUpstreamModelsEmpty'))
      return
    }
    const mappings = appendKiroSyncedMappings(props.modelValue, result.models, result.metadata, direct)
    const added = mappings.length - props.modelValue.length
    const total = result.models.length
    if (added > 0) {
      emit('update:modelValue', mappings)
      appStore.showSuccess(t('admin.accounts.syncUpstreamModelsSuccess', { count: added, total }))
    } else {
      appStore.showInfo(t('admin.accounts.syncUpstreamModelsNoChanges', { count: total }))
    }
  } catch (error) {
    if (isCurrent()) {
      const message = extractApiErrorMessage(error, t('admin.accounts.syncUpstreamModelsFailed'))
      appStore.showError(t('admin.accounts.syncUpstreamModelsError', { message }))
    }
  } finally {
    syncing.value = false
  }
}
</script>
