<template>
  <ApiDocsShell
    :current-page-id="page?.id"
    :headings="headings"
  >
    <ApiEndpointPage
      v-if="endpoint && endpointExamples"
      :key="page?.id"
      ref="endpointPage"
      :endpoint="endpoint"
      :examples="endpointExamples"
    />

    <template v-else-if="page && guide">
      <header class="mb-10 min-w-0 space-y-3">
        <h1
          data-testid="api-docs-guide-title"
          class="text-3xl font-semibold tracking-tight text-gray-950 dark:text-white"
        >
          {{ t(page.titleKey) }}
        </h1>
        <p class="max-w-3xl text-base leading-7 text-gray-600 dark:text-gray-300">
          {{ t(page.summaryKey) }}
        </p>
      </header>
      <ApiGuidePage
        :key="page.id"
        ref="guidePage"
        :definition="guide"
      />
    </template>

    <article
      v-else
      data-testid="api-docs-not-found"
      class="rounded-2xl border border-gray-200 bg-white p-6 text-gray-700 shadow-sm dark:border-linear-hairline dark:bg-linear-elevated dark:text-gray-300 sm:p-8"
    >
      <h1 class="text-2xl font-semibold tracking-tight text-gray-950 dark:text-white">
        {{ t('apiDocs.notFoundTitle') }}
      </h1>
      <p class="mt-3 max-w-2xl leading-7">
        {{ t('apiDocs.notFoundDescription') }}
      </p>
      <div class="mt-6 flex flex-wrap gap-3">
        <RouterLink
          data-testid="api-docs-not-found-home"
          to="/docs"
          class="inline-flex h-10 items-center rounded-lg bg-primary-500 px-4 text-sm font-medium text-white outline-none transition-colors hover:bg-primary-400 focus-visible:ring-2 focus-visible:ring-primary-500/50 motion-reduce:transition-none"
        >
          {{ t('apiDocs.pages.quickstart.title') }}
        </RouterLink>
        <button
          type="button"
          data-testid="api-docs-not-found-search"
          class="inline-flex h-10 items-center rounded-lg border border-gray-200 bg-white px-4 text-sm font-medium text-gray-700 outline-none transition-colors hover:bg-gray-100 focus-visible:ring-2 focus-visible:ring-primary-500/50 motion-reduce:transition-none dark:border-linear-hairline dark:bg-linear-canvas dark:text-linear-ink dark:hover:bg-dark-800"
          @click="openSearch"
        >
          {{ t('apiDocs.search') }}
        </button>
      </div>
    </article>
  </ApiDocsShell>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'

import ApiDocsShell from '@/components/api-docs/ApiDocsShell.vue'
import ApiEndpointPage from '@/components/api-docs/ApiEndpointPage.vue'
import ApiGuidePage from '@/components/api-docs/ApiGuidePage.vue'
import { API_ENDPOINTS, findApiDocsPage } from '@/components/api-docs/catalog'
import { buildEndpointExamples } from '@/components/api-docs/examples'
import { buildGuidePage } from '@/components/api-docs/guideContent'
import { useAppStore } from '@/stores/app'

interface EndpointPageExposed {
  headings: Array<{ id: string; labelKey: string }>
}

interface GuidePageExposed {
  headings: Array<{ id: string; label: string }>
}

const route = useRoute()
const appStore = useAppStore()
const { locale, t } = useI18n()
const endpointPage = ref<EndpointPageExposed | null>(null)
const guidePage = ref<GuidePageExposed | null>(null)

const baseUrl = computed(() => appStore.apiBaseUrl || window.location.origin)
const page = computed(() => findApiDocsPage(route.path))
const endpoint = computed(() =>
  API_ENDPOINTS.find(({ pageId }) => page.value?.id === pageId)
)
const endpointExamples = computed(() =>
  endpoint.value ? buildEndpointExamples(endpoint.value.id, baseUrl.value) : null
)
const guide = computed(() =>
  page.value && page.value.kind !== 'endpoint'
    ? buildGuidePage(page.value.id, baseUrl.value)
    : null
)
const headings = computed(() => {
  locale.value
  if (endpointPage.value) {
    return endpointPage.value.headings.map(({ id, labelKey }) => ({ id, label: t(labelKey) }))
  }
  return guidePage.value?.headings ?? []
})

watch(
  [() => page.value?.titleKey, locale, () => appStore.siteName],
  updateDocumentTitle,
  { immediate: true, flush: 'post' }
)

onMounted(async () => {
  if (!appStore.publicSettingsLoaded) {
    await appStore.fetchPublicSettings()
  }
  updateDocumentTitle()
})

function updateDocumentTitle(): void {
  document.title = `${t(page.value?.titleKey ?? 'apiDocs.title')} - ${appStore.siteName || 'LINX2.AI'}`
}

function openSearch(): void {
  window.dispatchEvent(new KeyboardEvent('keydown', { key: '/', cancelable: true }))
}
</script>
