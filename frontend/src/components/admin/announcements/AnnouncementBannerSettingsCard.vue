<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import { useAppStore } from '@/stores'
import type { AnnouncementBanner } from '@/types'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const appStore = useAppStore()

const banners = ref<AnnouncementBanner[]>([])
const intervalSeconds = ref(3)
const loading = ref(true)
const saving = ref(false)
const previewIndex = ref(0)

function addBanner() {
  if (banners.value.length < 20) {
    banners.value.push({ id: '', text_zh: '', text_en: '' })
  }
}

function removeBanner(index: number) {
  banners.value.splice(index, 1)
  previewIndex.value = Math.min(previewIndex.value, Math.max(0, banners.value.length - 1))
}

function moveBanner(index: number, direction: -1 | 1) {
  const target = index + direction
  if (target < 0 || target >= banners.value.length) return

  const [item] = banners.value.splice(index, 1)
  banners.value.splice(target, 0, item)
  previewIndex.value = target
}

function validate(): string | null {
  if (!Number.isFinite(intervalSeconds.value) || intervalSeconds.value < 1 || intervalSeconds.value > 60) {
    return t('admin.settings.announcementBanners.validationInterval')
  }
  if (banners.value.some((item) => !item.text_zh.trim() && !item.text_en.trim())) {
    return t('admin.settings.announcementBanners.validationTextRequired')
  }
  if (banners.value.some((item) => item.text_zh.length > 200 || item.text_en.length > 200)) {
    return t('admin.settings.announcementBanners.validationTextLength')
  }
  return null
}

async function load() {
  loading.value = true
  try {
    const settings = await adminAPI.settings.getSettings()
    banners.value = (settings.announcement_banners ?? []).map((item) => ({ ...item }))
    intervalSeconds.value = Math.min(60, Math.max(1,
      Math.round((settings.announcement_banner_interval_ms || 3000) / 1000)))
  } catch {
    appStore.showError(t('admin.settings.announcementBanners.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function refreshLiveSettings(): Promise<boolean> {
  for (let attempt = 0; attempt < 2; attempt += 1) {
    try {
      if (await appStore.fetchPublicSettings(true)) return true
    } catch {
      // The store normally resolves null; keep the retry boundary defensive for compatible callers.
    }
  }
  return false
}

async function save() {
  if (saving.value) return

  const error = validate()
  if (error) return appStore.showError(error)

  saving.value = true
  try {
    try {
      const updated = await adminAPI.settings.updateSettings({
        announcement_banners: banners.value.map((item) => ({ ...item })),
        announcement_banner_interval_ms: Math.round(intervalSeconds.value * 1000),
      })
      banners.value = (updated.announcement_banners ?? banners.value).map((item) => ({ ...item }))
    } catch {
      appStore.showError(t('admin.settings.announcementBanners.saveFailed'))
      return
    }

    if (await refreshLiveSettings()) {
      appStore.showSuccess(t('admin.settings.announcementBanners.saved'))
    } else {
      appStore.showWarning(t('admin.settings.announcementBanners.liveRefreshFailed'))
    }
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<template>
  <section class="card" data-testid="announcement-banner-settings-card">
    <header class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2>{{ t('admin.settings.announcementBanners.title') }}</h2>
      <p>{{ t('admin.settings.announcementBanners.description') }}</p>
    </header>

    <div class="space-y-4 p-6">
      <div>
        <label class="input-label" for="announcement-banner-interval">
          {{ t('admin.settings.announcementBanners.intervalSeconds') }}
        </label>
        <input
          id="announcement-banner-interval"
          v-model.number="intervalSeconds"
          data-testid="announcement-banner-interval"
          type="number"
          min="1"
          max="60"
          class="input w-32"
        >
      </div>

      <div
        v-for="(item, index) in banners"
        :key="item.id || `new-${index}`"
        class="rounded-xl border border-gray-200 p-4 dark:border-dark-700"
      >
        <div class="mb-3 flex items-center justify-between gap-2">
          <span class="text-sm font-medium text-gray-900 dark:text-white">
            {{ t('admin.settings.announcementBanners.itemLabel', { n: index + 1 }) }}
          </span>
          <div class="flex items-center gap-1">
            <button
              v-if="index > 0"
              type="button"
              class="btn btn-sm btn-secondary"
              :data-testid="`announcement-banner-up-${index}`"
              :title="t('admin.settings.announcementBanners.moveUp')"
              @click="moveBanner(index, -1)"
            >
              <Icon name="arrowUp" size="sm" />
            </button>
            <button
              v-if="index < banners.length - 1"
              type="button"
              class="btn btn-sm btn-secondary"
              :data-testid="`announcement-banner-down-${index}`"
              :title="t('admin.settings.announcementBanners.moveDown')"
              @click="moveBanner(index, 1)"
            >
              <Icon name="arrowDown" size="sm" />
            </button>
            <button
              type="button"
              class="btn btn-sm btn-secondary text-red-600 dark:text-red-400"
              :data-testid="`announcement-banner-remove-${index}`"
              :title="t('admin.settings.announcementBanners.remove')"
              @click="removeBanner(index)"
            >
              <Icon name="trash" size="sm" />
            </button>
          </div>
        </div>

        <div class="grid gap-3 md:grid-cols-2">
          <div>
            <label class="input-label" :for="`announcement-banner-zh-${index}`">
              {{ t('admin.settings.announcementBanners.textZh') }}
            </label>
            <input
              :id="`announcement-banner-zh-${index}`"
              v-model="item.text_zh"
              :data-testid="`announcement-banner-zh-${index}`"
              maxlength="200"
              class="input"
            >
          </div>
          <div>
            <label class="input-label" :for="`announcement-banner-en-${index}`">
              {{ t('admin.settings.announcementBanners.textEn') }}
            </label>
            <input
              :id="`announcement-banner-en-${index}`"
              v-model="item.text_en"
              :data-testid="`announcement-banner-en-${index}`"
              maxlength="200"
              class="input"
            >
          </div>
        </div>
      </div>

      <button
        data-testid="announcement-banner-add"
        type="button"
        class="btn btn-secondary"
        :disabled="banners.length >= 20"
        @click="addBanner"
      >
        <Icon name="plus" size="sm" class="mr-1.5" />
        {{ t('admin.settings.announcementBanners.add') }}
      </button>

      <div v-if="banners[previewIndex]" class="space-y-2">
        <p class="text-sm font-medium text-gray-900 dark:text-white">
          {{ t('admin.settings.announcementBanners.preview') }}
        </p>
        <div data-testid="announcement-banner-preview" class="rounded-lg bg-gray-950 p-3 text-white">
          {{ banners[previewIndex].text_zh || banners[previewIndex].text_en }}
        </div>
      </div>

      <button
        data-testid="announcement-banner-save"
        type="button"
        class="btn btn-primary"
        :disabled="loading || saving"
        @click="save"
      >
        {{ saving ? t('common.saving') : t('common.save') }}
      </button>
    </div>
  </section>
</template>
