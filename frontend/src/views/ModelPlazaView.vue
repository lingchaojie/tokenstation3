<template>
  <!-- 后台内嵌形态:?embedded=1 且已登录,套完整后台布局 -->
  <AppLayout v-if="isEmbedded">
    <ModelPlazaContent :response="data" :loading="loading" :error="loadFailed" embedded />
  </AppLayout>

  <!-- 独立形态:自带导航条(logo/站名 + 登录/回后台) -->
  <div v-else class="min-h-screen bg-gray-50 dark:bg-dark-950">
    <PlazaNavBar />
    <main class="mx-auto max-w-7xl px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
      <ModelPlazaContent :response="data" :loading="loading" :error="loadFailed" />
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import PlazaNavBar from '@/components/modelPlaza/PlazaNavBar.vue'
import ModelPlazaContent from '@/components/modelPlaza/ModelPlazaContent.vue'
import { getModelPlaza, type ModelPlazaResponse } from '@/api/modelPlaza'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const appStore = useAppStore()
const authStore = useAuthStore()
const { locale } = useI18n()

// embedded=1 但未登录(如转发的链接)自动降级为独立形态。
const isEmbedded = computed(() => route.query.embedded === '1' && authStore.isAuthenticated)

const data = ref<ModelPlazaResponse | null>(null)
const loading = ref(true)
const loadFailed = ref(false)
let requestSequence = 0

async function loadModelPlaza() {
  const sequence = ++requestSequence
  loading.value = data.value === null
  loadFailed.value = false
  try {
    const response = await getModelPlaza()
    if (sequence === requestSequence) data.value = response
  } catch {
    if (sequence === requestSequence && data.value === null) loadFailed.value = true
  } finally {
    if (sequence === requestSequence) loading.value = false
  }
}

onMounted(() => {
  // 独立形态导航条需要站点名和 Logo。启动时已有配置会直接命中缓存。
  void appStore.fetchPublicSettings()
  void loadModelPlaza()
})

watch(locale, () => { void loadModelPlaza() })
onBeforeUnmount(() => { requestSequence++ })
</script>
