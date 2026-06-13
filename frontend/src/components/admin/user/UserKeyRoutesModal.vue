<template>
  <BaseDialog
    :show="show"
    :title="t('admin.users.keyRoutes.title')"
    width="narrow"
    @close="emit('close')"
  >
    <div v-if="user" class="space-y-5">
      <div class="rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800/60">
        <p class="font-medium text-gray-900 dark:text-white">{{ user.email }}</p>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
          {{ t('admin.users.keyRoutes.description') }}
        </p>
      </div>

      <div v-if="loading" class="py-8 text-center text-sm text-gray-500 dark:text-gray-400">
        {{ t('common.loading') }}
      </div>

      <div v-else class="space-y-4">
        <div>
          <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.users.keyRoutes.anthropicLabel') }}
          </label>
          <Select
            v-model="anthropicGroupId"
            :options="anthropicOptions"
            :placeholder="t('admin.users.keyRoutes.useGlobalDefault')"
          />
        </div>

        <div>
          <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.users.keyRoutes.openaiLabel') }}
          </label>
          <Select
            v-model="openaiGroupId"
            :options="openaiOptions"
            :placeholder="t('admin.users.keyRoutes.useGlobalDefault')"
          />
        </div>

        <p class="text-xs leading-5 text-gray-500 dark:text-gray-400">
          {{ t('admin.users.keyRoutes.hint') }}
        </p>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary px-5" @click="emit('close')">
          {{ t('common.cancel') }}
        </button>
        <button
          type="button"
          class="btn btn-primary px-6"
          :disabled="loading || submitting"
          @click="handleSave"
        >
          {{ submitting ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { AdminGroup, AdminUser } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'

const props = defineProps<{
  show: boolean
  user: AdminUser | null
  groups: AdminGroup[]
}>()

const emit = defineEmits(['close', 'success'])
const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const submitting = ref(false)
const anthropicGroupId = ref<number | null>(null)
const openaiGroupId = ref<number | null>(null)

const activeProviderGroups = computed(() =>
  props.groups.filter((group) => group.status === 'active')
)

const anthropicOptions = computed(() => [
  { value: null, label: t('admin.users.keyRoutes.useGlobalDefault') },
  ...activeProviderGroups.value
    .filter((group) => group.platform === 'anthropic')
    .map((group) => ({ value: group.id, label: group.name })),
])

const openaiOptions = computed(() => [
  { value: null, label: t('admin.users.keyRoutes.useGlobalDefault') },
  ...activeProviderGroups.value
    .filter((group) => group.platform === 'openai')
    .map((group) => ({ value: group.id, label: group.name })),
])

watch(
  () => props.show,
  (show) => {
    if (show && props.user) {
      void loadRoutes()
    }
  }
)

async function loadRoutes() {
  if (!props.user) return
  loading.value = true
  try {
    const routes = await adminAPI.users.getAPIKeyRoutes(props.user.id)
    anthropicGroupId.value = routes.anthropic?.group_id ?? null
    openaiGroupId.value = routes.openai?.group_id ?? null
  } catch (error: any) {
    appStore.showError(error?.response?.data?.detail || t('admin.users.keyRoutes.loadFailed'))
    anthropicGroupId.value = null
    openaiGroupId.value = null
  } finally {
    loading.value = false
  }
}

async function handleSave() {
  if (!props.user) return
  submitting.value = true
  try {
    await adminAPI.users.updateAPIKeyRoutes(props.user.id, {
      anthropic_group_id: anthropicGroupId.value ?? null,
      openai_group_id: openaiGroupId.value ?? null,
    })
    appStore.showSuccess(t('admin.users.keyRoutes.updateSuccess'))
    emit('success')
    emit('close')
  } catch (error: any) {
    appStore.showError(error?.response?.data?.detail || t('admin.users.keyRoutes.updateFailed'))
  } finally {
    submitting.value = false
  }
}
</script>
