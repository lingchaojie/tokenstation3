<template>
  <article class="min-w-0 max-w-full space-y-10 text-gray-700 dark:text-gray-300">
    <header class="min-w-0 space-y-4">
      <div class="flex min-w-0 flex-wrap items-center gap-2">
        <span
          data-testid="endpoint-method"
          class="rounded-md bg-primary-100 px-2.5 py-1 font-mono text-xs font-bold text-primary-800 dark:bg-primary-500/15 dark:text-primary-300"
        >
          {{ endpoint.method }}
        </span>
        <code
          data-testid="endpoint-path"
          class="min-w-0 break-all rounded-md border border-gray-200 bg-gray-50 px-2.5 py-1 font-mono text-sm font-semibold text-gray-900 dark:border-linear-hairline dark:bg-dark-800 dark:text-gray-100"
        >{{ endpoint.path }}</code>
        <span
          class="rounded-full border border-gray-200 px-2.5 py-1 text-xs font-medium uppercase tracking-wide text-gray-500 dark:border-linear-hairline dark:text-gray-400"
        >
          {{ endpoint.protocol }}
        </span>
      </div>
      <div class="space-y-2">
        <h1 class="text-3xl font-semibold tracking-tight text-gray-950 dark:text-white">
          {{ t(endpoint.titleKey) }}
        </h1>
        <p class="max-w-3xl text-base leading-7 text-gray-600 dark:text-gray-300">
          {{ t(endpoint.summaryKey) }}
        </p>
      </div>
    </header>

    <section class="scroll-mt-24 space-y-4" aria-labelledby="overview">
      <h2 id="overview" class="text-xl font-semibold text-gray-950 dark:text-white">
        {{ t('apiDocs.sections.overview') }}
      </h2>
      <dl class="rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-linear-hairline dark:bg-dark-800/70">
        <div class="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <dt class="text-sm font-medium text-gray-500 dark:text-gray-400">
            {{ t('apiDocs.labels.protocol') }}
          </dt>
          <dd class="font-mono text-sm text-gray-900 dark:text-gray-100">
            {{ endpoint.protocol }}
          </dd>
        </div>
      </dl>
    </section>

    <section class="scroll-mt-24 space-y-4" aria-labelledby="authentication">
      <h2 id="authentication" class="text-xl font-semibold text-gray-950 dark:text-white">
        {{ t('apiDocs.sections.authentication') }}
      </h2>
      <div class="min-w-0 rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-linear-hairline dark:bg-dark-800/70">
        <code class="break-all font-mono text-sm text-gray-900 dark:text-gray-100">
          Authorization: Bearer $LINX2_API_KEY
        </code>
      </div>
    </section>

    <section class="scroll-mt-24 space-y-4" aria-labelledby="parameters">
      <h2 id="parameters" class="text-xl font-semibold text-gray-950 dark:text-white">
        {{ t('apiDocs.sections.parameters') }}
      </h2>
      <div class="max-w-full overflow-x-auto rounded-xl border border-gray-200 dark:border-linear-hairline">
        <table class="min-w-full border-collapse text-left text-sm">
          <caption class="sr-only">
            {{ t('apiDocs.sections.parameters') }}
          </caption>
          <thead class="bg-gray-50 text-xs uppercase tracking-wide text-gray-500 dark:bg-dark-800 dark:text-gray-400">
            <tr>
              <th scope="col" class="px-4 py-3 font-semibold">
                {{ t('apiDocs.labels.parameter') }}
              </th>
              <th scope="col" class="px-4 py-3 font-semibold">
                {{ t('apiDocs.labels.required') }}
              </th>
              <th scope="col" class="px-4 py-3 font-semibold">
                {{ t('apiDocs.labels.type') }}
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-200 dark:divide-linear-hairline">
            <tr
              v-for="parameter in endpoint.parameters"
              :key="`${parameter.location}:${parameter.name}`"
              data-testid="endpoint-parameter"
              class="align-top"
            >
              <th scope="row" class="min-w-56 px-4 py-4 font-normal">
                <code class="font-mono font-semibold text-gray-950 dark:text-white">
                  {{ parameter.name }}
                </code>
                <p class="mt-1 max-w-xl font-sans text-sm font-normal leading-6 text-gray-500 dark:text-gray-400">
                  {{ t(parameter.descriptionKey) }}
                </p>
              </th>
              <td class="whitespace-nowrap px-4 py-4">
                <span
                  class="rounded-full px-2 py-1 text-xs font-medium"
                  :class="parameter.required
                    ? 'bg-red-50 text-red-700 dark:bg-red-500/10 dark:text-red-300'
                    : 'bg-gray-100 text-gray-600 dark:bg-white/5 dark:text-gray-300'"
                >
                  {{ t(parameter.required ? 'apiDocs.labels.required' : 'apiDocs.labels.optional') }}
                </span>
              </td>
              <td class="whitespace-nowrap px-4 py-4">
                <code class="font-mono text-xs text-gray-700 dark:text-gray-300">
                  {{ parameter.type }}
                </code>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <section class="scroll-mt-24 space-y-4" aria-labelledby="request">
      <h2 id="request" class="text-xl font-semibold text-gray-950 dark:text-white">
        {{ t('apiDocs.sections.request') }}
      </h2>
      <div class="min-w-0 space-y-4">
        <ApiDocsCodeBlock
          :label="t('apiDocs.labels.curl')"
          language="bash"
          :code="examples.curl"
        />
        <ApiDocsCodeBlock
          v-if="examples.python"
          :label="t('apiDocs.labels.python')"
          language="python"
          :code="examples.python"
        />
      </div>
    </section>

    <section class="scroll-mt-24 space-y-4" aria-labelledby="response">
      <h2 id="response" class="text-xl font-semibold text-gray-950 dark:text-white">
        {{ t('apiDocs.sections.response') }}
      </h2>
      <ApiDocsCodeBlock
        :label="t('apiDocs.labels.successExample')"
        language="json"
        :code="examples.success"
      />
    </section>

    <section
      v-if="endpoint.supportsStreaming"
      class="scroll-mt-24 space-y-4"
      aria-labelledby="streaming"
    >
      <h2 id="streaming" class="text-xl font-semibold text-gray-950 dark:text-white">
        {{ t('apiDocs.sections.streaming') }}
      </h2>
      <ApiDocsCodeBlock
        v-if="examples.stream"
        :label="t('apiDocs.labels.streamExample')"
        language="text"
        :code="examples.stream"
      />
    </section>

    <section class="scroll-mt-24 space-y-4" aria-labelledby="errors">
      <h2 id="errors" class="text-xl font-semibold text-gray-950 dark:text-white">
        {{ t('apiDocs.sections.errors') }}
      </h2>
      <p
        v-if="endpoint.protocol === 'openai'"
        data-testid="gateway-envelope-warning"
        role="note"
        class="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm leading-6 text-amber-900 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200"
      >
        {{ t('apiDocs.errors.gatewayEnvelopeWarning') }}
      </p>
      <ul class="flex flex-wrap gap-2" :aria-label="t('apiDocs.sections.errors')">
        <li v-for="errorCode in endpoint.errorCodes" :key="errorCode">
          <code
            data-testid="endpoint-error"
            class="inline-flex rounded-full border border-red-200 bg-red-50 px-2.5 py-1 font-mono text-xs text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300"
          >{{ errorCode }}</code>
        </li>
      </ul>

      <aside class="rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-linear-hairline dark:bg-dark-800/70">
        <h3 class="text-sm font-semibold text-gray-950 dark:text-white">
          {{ t('apiDocs.sections.troubleshooting') }}
        </h3>
        <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-gray-300">
          {{ t('apiDocs.guides.requestId.headers') }}
        </p>
        <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-gray-300">
          {{ t('apiDocs.guides.requestId.supportChecklist') }}
        </p>
        <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-gray-300">
          {{ t('apiDocs.guides.requestId.redaction') }}
        </p>
      </aside>
    </section>
  </article>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import ApiDocsCodeBlock from './ApiDocsCodeBlock.vue'
import type { ApiEndpointDefinition, ApiEndpointExamples } from './types'

const props = defineProps<{
  endpoint: ApiEndpointDefinition
  examples: ApiEndpointExamples
}>()

const { t } = useI18n()

const headings = [
  { id: 'overview', labelKey: 'apiDocs.sections.overview' },
  { id: 'authentication', labelKey: 'apiDocs.sections.authentication' },
  { id: 'parameters', labelKey: 'apiDocs.sections.parameters' },
  { id: 'request', labelKey: 'apiDocs.sections.request' },
  { id: 'response', labelKey: 'apiDocs.sections.response' },
  { id: 'streaming', labelKey: 'apiDocs.sections.streaming' },
  { id: 'errors', labelKey: 'apiDocs.sections.errors' }
].filter(({ id }) => id !== 'streaming' || props.endpoint.supportsStreaming)

defineExpose({ headings })
</script>
