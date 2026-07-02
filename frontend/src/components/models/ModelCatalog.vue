<template>
  <section class="space-y-5" data-testid="model-catalog">
    <header class="linx-panel-strong p-5 sm:p-6">
      <p class="text-xs font-semibold uppercase tracking-[0.18em] text-linear-ink-tertiary">
        {{ t('modelCatalog.kicker') }}
      </p>
      <div class="mt-3 flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
        <div class="max-w-3xl">
          <h2 class="text-2xl font-semibold text-linear-ink sm:text-3xl">
            {{ t('modelCatalog.title') }}
          </h2>
          <p class="mt-2 text-sm leading-6 text-linear-ink-subtle">
            {{ t('modelCatalog.description') }}
          </p>
        </div>
        <dl class="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:min-w-[32rem]">
          <div v-for="stat in stats" :key="stat.label" class="linx-panel px-3 py-2">
            <dt class="text-xs text-linear-ink-tertiary">{{ stat.label }}</dt>
            <dd class="mt-1 text-xl font-semibold text-linear-ink">{{ stat.value }}</dd>
          </div>
        </dl>
      </div>
    </header>

    <div class="linx-panel p-4">
      <div class="space-y-4">
        <label class="block">
          <span class="mb-1 block text-xs font-medium text-linear-ink-muted">{{ t('modelCatalog.searchLabel') }}</span>
          <input
            v-model="query"
            class="input w-full"
            type="search"
            :placeholder="t('modelCatalog.searchPlaceholder')"
            data-testid="model-catalog-search"
          />
        </label>

        <div>
          <span class="mb-2 block text-xs font-medium text-linear-ink-muted">{{ t('modelCatalog.modelFilterLabel') }}</span>
          <div class="flex flex-nowrap gap-2 overflow-x-auto pb-1 sm:flex-wrap sm:overflow-visible" role="listbox" :aria-label="t('modelCatalog.modelFilterLabel')">
            <button
              v-for="option in providerOptions"
              :key="option.value"
              class="inline-flex h-10 shrink-0 items-center gap-2 rounded-full border px-3 text-sm font-medium transition-colors"
              :class="provider === option.value
                ? 'border-linear-ink bg-linear-surface-2 text-linear-ink shadow-sm'
                : 'border-linear-hairline bg-linear-surface-0 text-linear-ink-muted hover:border-linear-ink-muted hover:text-linear-ink'"
              type="button"
              role="option"
              :aria-selected="provider === option.value"
              :aria-pressed="provider === option.value"
              :data-provider="option.value"
              data-testid="model-catalog-provider-chip"
              @click="provider = option.value"
            >
              <span
                v-if="option.value === 'all'"
                class="flex h-5 min-w-5 items-center justify-center rounded-full bg-linear-surface-2 px-1.5 text-[11px] font-semibold"
              >
                {{ t('modelCatalog.allFilterShort') }}
              </span>
              <ModelIcon v-else :model="providerIconModel(option.value)" size="16px" aria-hidden="true" />
              <span v-if="option.value !== 'all'" class="whitespace-nowrap">{{ option.label }}</span>
              <span class="rounded-full bg-linear-surface-1 px-1.5 py-0.5 text-[11px] text-linear-ink-tertiary">
                {{ option.count }}
              </span>
            </button>
          </div>
        </div>

        <div class="grid gap-3 md:grid-cols-[12rem_12rem]">
          <label class="block">
            <span class="mb-1 block text-xs font-medium text-linear-ink-muted">{{ t('modelCatalog.modalityLabel') }}</span>
            <select v-model="modality" class="input w-full" data-testid="model-catalog-modality">
              <option v-for="option in modalityOptions" :key="option.value" :value="option.value">
                {{ option.label }}
              </option>
            </select>
          </label>

          <label class="block">
            <span class="mb-1 block text-xs font-medium text-linear-ink-muted">{{ t('modelCatalog.sortLabel') }}</span>
            <select v-model="sortKey" class="input w-full" data-testid="model-catalog-sort">
              <option v-for="option in sortOptions" :key="option.value" :value="option.value">
                {{ option.label }}
              </option>
            </select>
          </label>
        </div>
      </div>
    </div>

    <div v-if="loading" class="grid gap-4 md:grid-cols-2 xl:grid-cols-3" aria-live="polite">
      <article v-for="index in 3" :key="index" class="linx-panel animate-pulse p-5">
        <div class="h-4 w-24 rounded bg-linear-surface-2" />
        <div class="mt-4 h-6 w-2/3 rounded bg-linear-surface-2" />
        <div class="mt-3 h-4 w-full rounded bg-linear-surface-2" />
        <div class="mt-2 h-4 w-4/5 rounded bg-linear-surface-2" />
        <div class="mt-5 grid gap-2">
          <div class="h-9 rounded bg-linear-surface-2" />
          <div class="h-9 rounded bg-linear-surface-2" />
          <div class="h-9 rounded bg-linear-surface-2" />
        </div>
        <span class="sr-only">{{ t('modelCatalog.loading') }}</span>
      </article>
    </div>

    <div v-else-if="errorMessage" class="linx-panel border-red-200 p-5 dark:border-red-500/30" role="alert">
      <p class="text-sm font-semibold text-linear-ink">{{ errorMessage }}</p>
      <button class="btn btn-secondary mt-4" type="button" data-testid="model-catalog-retry" @click="loadCatalog">
        <Icon name="refresh" size="sm" class="mr-2" />
        {{ t('common.retry') }}
      </button>
    </div>

    <div v-else-if="visibleModels.length === 0" class="linx-panel p-8 text-center">
      <p class="text-base font-semibold text-linear-ink">{{ t('modelCatalog.emptyTitle') }}</p>
      <p class="mt-2 text-sm text-linear-ink-subtle">{{ t('modelCatalog.emptyDescription') }}</p>
    </div>

    <div v-else class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
      <article v-for="model in visibleModels" :key="`${model.provider}:${model.model_name}`" class="linx-panel flex min-w-0 flex-col p-5">
        <div class="flex items-start gap-3">
          <span
            class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border border-linear-hairline bg-linear-surface-1 text-linear-ink"
            aria-hidden="true"
          >
            <ModelIcon :model="model.model_name" size="21px" />
          </span>
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <p class="truncate text-xs font-medium text-linear-ink-muted">{{ model.provider_name }}</p>
            </div>
            <h3 class="mt-3 text-lg font-semibold text-linear-ink">{{ model.display_name }}</h3>
            <p class="mt-1 break-words font-mono text-xs text-linear-ink-tertiary">{{ model.model_name }}</p>
          </div>
        </div>

        <p class="mt-3 text-sm leading-6 text-linear-ink-subtle">{{ model.description }}</p>

        <div class="mt-4 flex flex-wrap gap-2">
          <span
            v-for="item in model.modalities"
            :key="item"
            class="rounded-full border border-linear-hairline bg-linear-surface-1 px-2.5 py-1 text-xs font-medium text-linear-ink-muted"
          >
            {{ modalityLabel(item) }}
          </span>
          <span
            v-for="feature in model.features"
            :key="feature"
            class="rounded-full border border-linear-hairline bg-linear-surface-1 px-2.5 py-1 text-xs text-linear-ink-tertiary"
          >
            {{ feature }}
          </span>
          <span
            v-if="formatContextWindow(model.context_window)"
            class="rounded-full border border-linear-hairline bg-linear-surface-1 px-2.5 py-1 text-xs text-linear-ink-tertiary"
          >
            {{ t('modelCatalog.context') }} {{ formatContextWindow(model.context_window) }}
          </span>
        </div>

        <div class="mt-5 divide-y divide-linear-hairline overflow-hidden rounded-lg border border-linear-hairline">
          <div
            v-for="row in pricingRows(model)"
            :key="row.key"
            class="grid grid-cols-[minmax(0,1fr)_auto] gap-3 px-3 py-2 text-sm"
          >
            <span class="min-w-0 text-linear-ink-muted">{{ row.label }}</span>
            <span class="font-mono text-linear-ink">{{ row.value }}</span>
          </div>
        </div>

        <router-link
          :to="chatRouteForModel(model)"
          class="btn btn-primary mt-5 w-full justify-center"
          :data-provider="model.provider"
          :data-model="model.model_name"
          data-testid="model-catalog-chat-link"
        >
          <Icon name="chat" size="sm" class="mr-2" />
          {{ t('modelCatalog.chatNow') }}
        </router-link>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { chatAPI } from '@/api/chat'
import { getPublicModelCatalog, type PublicModelCatalogModel, type PublicModelCatalogProvider } from '@/api/settings'
import ModelIcon from '@/components/common/ModelIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import { extractApiErrorMessage } from '@/utils/apiError'
import {
  buildModelCatalogProviderOptions,
  filterModelCatalogByWebChatModels,
  filterModelCatalog,
  formatContextWindow,
  formatModelCatalogAmount,
  providerIconModel,
  sortModelCatalog,
  type ModelCatalogSortKey,
} from '@/utils/modelCatalog'

const { t } = useI18n()

const loading = ref(false)
const errorMessage = ref('')
const models = ref<PublicModelCatalogModel[]>([])
const providers = ref<PublicModelCatalogProvider[]>([])
const query = ref('')
const provider = ref('all')
const modality = ref('all')
const sortKey = ref<ModelCatalogSortKey>('default')

const providerOptions = computed(() =>
  buildModelCatalogProviderOptions(models.value).map((option) =>
    option.value === 'all' ? { ...option, label: t('modelCatalog.allProviders') } : option,
  ),
)

const modalityOptions = computed(() => {
  const values = Array.from(new Set(models.value.flatMap((model) => model.modalities))).sort()
  return [
    { value: 'all', label: t('modelCatalog.allModalities') },
    ...values.map((value) => ({ value, label: modalityLabel(value) })),
  ]
})

const sortOptions = computed<Array<{ value: ModelCatalogSortKey; label: string }>>(() => [
  { value: 'default', label: t('modelCatalog.sort.default') },
  { value: 'newest', label: t('modelCatalog.sort.newest') },
  { value: 'provider', label: t('modelCatalog.sort.provider') },
])

const visibleModels = computed(() => {
  const filtered = filterModelCatalog(models.value, {
    query: query.value,
    provider: provider.value,
    modality: modality.value,
  })
  return sortModelCatalog(filtered, sortKey.value)
})

const stats = computed(() => [
  { label: t('modelCatalog.stats.models'), value: models.value.length },
  { label: t('modelCatalog.stats.providers'), value: providers.value.length },
  { label: t('modelCatalog.stats.text'), value: models.value.filter((model) => model.modalities.includes('text')).length },
  { label: t('modelCatalog.stats.image'), value: models.value.filter((model) => model.modalities.includes('image')).length },
])

function modalityLabel(value: string): string {
  const key = `modelCatalog.modality.${value}`
  const label = t(key)
  return label === key ? value : label
}

function pricingRows(model: PublicModelCatalogModel): Array<{ key: string; label: string; value: string }> {
  if (model.price_status !== 'confirmed') {
    return [
      {
        key: 'pending',
        label: model.pricing.note || t('modelCatalog.pending'),
        value: t('modelCatalog.pending'),
      },
    ]
  }

  const rows: Array<{ key: string; label: string; value: string }> = []
  if (model.pricing.input_per_million !== undefined) {
    rows.push({
      key: 'input',
      label: `${t('modelCatalog.pricing.input')} / ${model.pricing.unit}`,
      value: formatModelCatalogAmount(model.pricing.input_per_million),
    })
  }
  if (model.pricing.output_per_million !== undefined) {
    rows.push({
      key: 'output',
      label: `${t('modelCatalog.pricing.output')} / ${model.pricing.unit}`,
      value: formatModelCatalogAmount(model.pricing.output_per_million),
    })
  }
  if (model.pricing.cache_read_per_million !== undefined) {
    rows.push({
      key: 'cache-read',
      label: `${t('modelCatalog.pricing.cacheRead')} / ${model.pricing.unit}`,
      value: formatModelCatalogAmount(model.pricing.cache_read_per_million),
    })
  }
  for (const line of model.pricing.price_lines ?? []) {
    rows.push({
      key: `line:${line.label}`,
      label: line.label,
      value: `${formatModelCatalogAmount(line.amount)} / ${line.unit}`,
    })
  }
  return rows
}

function chatRouteForModel(model: PublicModelCatalogModel) {
  return {
    path: '/chat',
    query: {
      provider: model.provider,
      model: model.model_name,
    },
  }
}

async function loadCatalog() {
  loading.value = true
  errorMessage.value = ''
  try {
    const [catalog, chatModels] = await Promise.all([
      getPublicModelCatalog(),
      chatAPI.listModels(),
    ])
    models.value = filterModelCatalogByWebChatModels(catalog.models, chatModels)
    const providerKeys = new Set(models.value.map((model) => model.provider))
    providers.value = catalog.providers.filter((provider) => providerKeys.has(provider.key))
  } catch (err) {
    errorMessage.value = extractApiErrorMessage(err, t('modelCatalog.loadError'))
    models.value = []
    providers.value = []
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void loadCatalog()
})
</script>
