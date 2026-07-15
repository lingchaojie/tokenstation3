<template>
  <BaseDialog
    :show="show"
    :title="t('keys.useKeyModal.title')"
    width="wide"
    @close="emit('close')"
  >
    <div class="space-y-4">
      <!-- No Group Assigned Warning -->
      <div v-if="!platform" class="flex items-start gap-3 p-4 rounded-lg bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-yellow-800">
        <svg class="w-5 h-5 text-yellow-500 flex-shrink-0 mt-0.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
          <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
        </svg>
        <div>
          <p class="text-sm font-medium text-yellow-800 dark:text-yellow-200">
            {{ t('keys.useKeyModal.noGroupTitle') }}
          </p>
          <p class="text-sm text-yellow-700 dark:text-yellow-300 mt-1">
            {{ t('keys.useKeyModal.noGroupDescription') }}
          </p>
        </div>
      </div>

      <!-- Platform-specific content -->
      <template v-else>
        <!-- Description -->
        <p class="text-sm text-gray-600 dark:text-gray-400">
          {{ platformDescription }}
        </p>

        <!-- Client Tabs -->
        <div v-if="clientTabs.length" class="border-b border-gray-200 dark:border-dark-700">
          <nav class="-mb-px flex flex-wrap gap-x-6 gap-y-1" aria-label="Client">
            <button
              v-for="tab in clientTabs"
              :key="tab.id"
              @click="activeClientTab = tab.id"
              :class="[
                'whitespace-nowrap py-2.5 px-1 border-b-2 font-medium text-sm transition-colors',
                activeClientTab === tab.id
                  ? 'border-primary-500 text-primary-600 dark:text-primary-400'
                  : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300 dark:text-gray-400 dark:hover:text-gray-300'
              ]"
            >
              <span class="flex items-center gap-2">
                <component :is="tab.icon" class="w-4 h-4" />
                {{ tab.label }}
              </span>
            </button>
          </nav>
        </div>

        <!-- OS/Shell Tabs -->
        <div v-if="showShellTabs" class="border-b border-gray-200 dark:border-dark-700">
          <nav class="-mb-px flex space-x-4" aria-label="Tabs">
            <button
              v-for="tab in currentTabs"
              :key="tab.id"
              @click="activeTab = tab.id"
              :class="[
                'whitespace-nowrap py-2.5 px-1 border-b-2 font-medium text-sm transition-colors',
                activeTab === tab.id
                  ? 'border-primary-500 text-primary-600 dark:text-primary-400'
                  : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300 dark:text-gray-400 dark:hover:text-gray-300'
              ]"
            >
              <span class="flex items-center gap-2">
                <component :is="tab.icon" class="w-4 h-4" />
                {{ tab.label }}
              </span>
            </button>
          </nav>
        </div>

        <!-- Code Blocks (Stacked for multi-file platforms) -->
        <div class="space-y-4">
          <div
            v-for="(file, index) in currentFiles"
            :key="index"
            class="relative"
          >
            <!-- File Hint (if exists) -->
            <p v-if="file.hint" class="text-xs text-amber-600 dark:text-amber-400 mb-1.5 flex items-center gap-1">
              <Icon name="exclamationCircle" size="sm" class="flex-shrink-0" />
              {{ file.hint }}
            </p>
            <div class="linx-code-panel overflow-hidden rounded-xl bg-gray-900 dark:bg-linear-surface-1">
              <!-- Code Header -->
              <div class="flex items-center justify-between border-b border-gray-700 bg-gray-800 px-4 py-2 dark:border-linear-hairline dark:bg-linear-surface-2">
                <span class="text-xs text-gray-400 font-mono">{{ file.path }}</span>
                <button
                  @click="copyContent(file.content, index)"
                  class="flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium rounded-lg transition-colors"
                  :class="copiedIndex === index
                    ? 'bg-green-500/20 text-green-400'
                    : 'bg-gray-700 hover:bg-gray-600 text-gray-300 hover:text-white'"
                >
                  <svg v-if="copiedIndex === index" class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                  </svg>
                  <svg v-else class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M15.666 3.888A2.25 2.25 0 0013.5 2.25h-3c-1.03 0-1.9.693-2.166 1.638m7.332 0c.055.194.084.4.084.612v0a.75.75 0 01-.75.75H9a.75.75 0 01-.75-.75v0c0-.212.03-.418.084-.612m7.332 0c.646.049 1.288.11 1.927.184 1.1.128 1.907 1.077 1.907 2.185V19.5a2.25 2.25 0 01-2.25 2.25H6.75A2.25 2.25 0 014.5 19.5V6.257c0-1.108.806-2.057 1.907-2.185a48.208 48.208 0 011.927-.184" />
                  </svg>
                  {{ copiedIndex === index ? t('keys.useKeyModal.copied') : t('keys.useKeyModal.copy') }}
                </button>
              </div>
              <!-- Code Content -->
              <pre class="p-4 text-sm font-mono text-gray-100 overflow-x-auto"><code v-if="file.highlighted" v-html="file.highlighted"></code><code v-else v-text="file.content"></code></pre>
            </div>
          </div>
        </div>

        <!-- Usage Note -->
        <div v-if="showPlatformNote" class="flex items-start gap-3 p-3 rounded-lg bg-blue-50 dark:bg-blue-900/20 border border-blue-100 dark:border-blue-800">
          <Icon name="infoCircle" size="md" class="text-blue-500 flex-shrink-0 mt-0.5" />
          <p class="text-sm text-blue-700 dark:text-blue-300">
            {{ platformNote }}
          </p>
        </div>
      </template>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <button
          @click="emit('close')"
          class="btn btn-secondary"
        >
          {{ t('common.close') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, computed, h, watch, type Component } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'
import type { GroupPlatform } from '@/types'
import { buildClientConfigFiles } from './clientConfigFiles'

interface Props {
  show: boolean
  apiKey: string
  baseUrl: string
  // 'unified' = provider-agnostic key that works with both Anthropic and OpenAI clients.
  platform: GroupPlatform | 'unified' | null
  allowMessagesDispatch?: boolean
}

interface Emits {
  (e: 'close'): void
}

interface TabConfig {
  id: string
  label: string
  icon: Component
}

interface FileConfig {
  path: string
  content: string
  hint?: string  // Optional hint message for this file
  highlighted?: string
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const { t } = useI18n()
const { copyToClipboard: clipboardCopy } = useClipboard()

const copiedIndex = ref<number | null>(null)
const activeTab = ref<string>('unix')
const activeClientTab = ref<string>('claude')

// Reset tabs when platform changes
const defaultClientTab = computed(() => {
  switch (props.platform) {
    case 'openai':
      return 'codex'
    case 'grok':
      return 'grok'
    case 'gemini':
      return 'gemini'
    case 'antigravity':
      return 'claude'
    default:
      return 'claude'
  }
})

watch(() => props.platform, () => {
  activeTab.value = 'unix'
  activeClientTab.value = defaultClientTab.value
}, { immediate: true })

// Reset shell tab when client changes
watch(activeClientTab, () => {
  activeTab.value = 'unix'
})

// Icon components
const AppleIcon = {
  render() {
    return h('svg', {
      fill: 'currentColor',
      viewBox: '0 0 24 24',
      class: 'w-4 h-4'
    }, [
      h('path', { d: 'M18.71 19.5c-.83 1.24-1.71 2.45-3.05 2.47-1.34.03-1.77-.79-3.29-.79-1.53 0-2 .77-3.27.82-1.31.05-2.3-1.32-3.14-2.53C4.25 17 2.94 12.45 4.7 9.39c.87-1.52 2.43-2.48 4.12-2.51 1.28-.02 2.5.87 3.29.87.78 0 2.26-1.07 3.81-.91.65.03 2.47.26 3.64 1.98-.09.06-2.17 1.28-2.15 3.81.03 3.02 2.65 4.03 2.68 4.04-.03.07-.42 1.44-1.38 2.83M13 3.5c.73-.83 1.94-1.46 2.94-1.5.13 1.17-.34 2.35-1.04 3.19-.69.85-1.83 1.51-2.95 1.42-.15-1.15.41-2.35 1.05-3.11z' })
    ])
  }
}

const WindowsIcon = {
  render() {
    return h('svg', {
      fill: 'currentColor',
      viewBox: '0 0 24 24',
      class: 'w-4 h-4'
    }, [
      h('path', { d: 'M3 12V6.75l6-1.32v6.48L3 12zm17-9v8.75l-10 .15V5.21L20 3zM3 13l6 .09v6.81l-6-1.15V13zm7 .25l10 .15V21l-10-1.91v-5.84z' })
    ])
  }
}

// Terminal icon for Claude Code
const TerminalIcon = {
  render() {
    return h('svg', {
      fill: 'none',
      stroke: 'currentColor',
      viewBox: '0 0 24 24',
      'stroke-width': '1.5',
      class: 'w-4 h-4'
    }, [
      h('path', {
        'stroke-linecap': 'round',
        'stroke-linejoin': 'round',
        d: 'm6.75 7.5 3 2.25-3 2.25m4.5 0h3m-9 8.25h13.5A2.25 2.25 0 0 0 21 17.25V6.75A2.25 2.25 0 0 0 18.75 4.5H5.25A2.25 2.25 0 0 0 3 6.75v10.5A2.25 2.25 0 0 0 5.25 20.25Z'
      })
    ])
  }
}

// Sparkle icon for Gemini
const SparkleIcon = {
  render() {
    return h('svg', {
      fill: 'none',
      stroke: 'currentColor',
      viewBox: '0 0 24 24',
      'stroke-width': '1.5',
      class: 'w-4 h-4'
    }, [
      h('path', {
        'stroke-linecap': 'round',
        'stroke-linejoin': 'round',
        d: 'M9.813 15.904 9 18.75l-.813-2.846a4.5 4.5 0 0 0-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 0 0 3.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 0 0 3.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 0 0-3.09 3.09ZM18.259 8.715 18 9.75l-.259-1.035a3.375 3.375 0 0 0-2.455-2.456L14.25 6l1.036-.259a3.375 3.375 0 0 0 2.455-2.456L18 2.25l.259 1.035a3.375 3.375 0 0 0 2.456 2.456L21.75 6l-1.035.259a3.375 3.375 0 0 0-2.456 2.456ZM16.894 20.567 16.5 21.75l-.394-1.183a2.25 2.25 0 0 0-1.423-1.423L13.5 18.75l1.183-.394a2.25 2.25 0 0 0 1.423-1.423l.394-1.183.394 1.183a2.25 2.25 0 0 0 1.423 1.423l1.183.394-1.183.394a2.25 2.25 0 0 0-1.423 1.423Z'
      })
    ])
  }
}

const clientTabs = computed((): TabConfig[] => {
  if (!props.platform) return []
  switch (props.platform) {
    case 'unified':
      return [
        { id: 'claude', label: t('keys.useKeyModal.cliTabs.claudeCode'), icon: TerminalIcon },
        { id: 'codex', label: t('keys.useKeyModal.cliTabs.codexCli'), icon: TerminalIcon },
        { id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: TerminalIcon },
        { id: 'cc_switch', label: t('keys.useKeyModal.cliTabs.ccSwitch'), icon: TerminalIcon },
        { id: 'anthropic-python-sdk', label: `${t('keys.keyTypes.anthropic')} ${t('keys.useKeyModal.cliTabs.anthropicPythonSdk')}`, icon: TerminalIcon },
        { id: 'openai-python-sdk', label: `${t('keys.keyTypes.openai')} ${t('keys.useKeyModal.cliTabs.openaiPythonSdk')}`, icon: TerminalIcon },
        { id: 'openai-imagen2-python-sdk', label: t('keys.useKeyModal.cliTabs.openaiImagen2PythonSdk'), icon: TerminalIcon },
        { id: 'workbuddy', label: t('keys.useKeyModal.cliTabs.workBuddy'), icon: TerminalIcon },
      ]
    case 'openai': {
      const tabs: TabConfig[] = []
      if (props.allowMessagesDispatch) {
        tabs.push({ id: 'claude', label: t('keys.useKeyModal.cliTabs.claudeCode'), icon: TerminalIcon })
      }
      tabs.push(
        { id: 'codex', label: t('keys.useKeyModal.cliTabs.codexCli'), icon: TerminalIcon },
        { id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: TerminalIcon },
        { id: 'cc_switch', label: t('keys.useKeyModal.cliTabs.ccSwitch'), icon: TerminalIcon },
        { id: 'openai-python-sdk', label: t('keys.useKeyModal.cliTabs.openaiPythonSdk'), icon: TerminalIcon },
        { id: 'openai-imagen2-python-sdk', label: t('keys.useKeyModal.cliTabs.openaiImagen2PythonSdk'), icon: TerminalIcon },
        { id: 'workbuddy', label: t('keys.useKeyModal.cliTabs.workBuddy'), icon: TerminalIcon },
      )
      return tabs
    }
    case 'gemini':
      return [
        { id: 'gemini', label: t('keys.useKeyModal.cliTabs.geminiCli'), icon: SparkleIcon },
        { id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: TerminalIcon }
      ]
    case 'antigravity':
      return [
        { id: 'claude', label: t('keys.useKeyModal.cliTabs.claudeCode'), icon: TerminalIcon },
        { id: 'gemini', label: t('keys.useKeyModal.cliTabs.geminiCli'), icon: SparkleIcon },
        { id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: TerminalIcon }
      ]
    case 'grok':
      return [
        { id: 'grok', label: t('keys.useKeyModal.cliTabs.grokCli'), icon: TerminalIcon },
        { id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: TerminalIcon }
      ]
    default:
      return [
        { id: 'claude', label: t('keys.useKeyModal.cliTabs.claudeCode'), icon: TerminalIcon },
        { id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: TerminalIcon },
        { id: 'cc_switch', label: t('keys.useKeyModal.cliTabs.ccSwitch'), icon: TerminalIcon },
        { id: 'anthropic-python-sdk', label: t('keys.useKeyModal.cliTabs.anthropicPythonSdk'), icon: TerminalIcon },
        { id: 'workbuddy', label: t('keys.useKeyModal.cliTabs.workBuddy'), icon: TerminalIcon }
      ]
  }
})

// Shell tabs (3 types for environment variable based configs)
const shellTabs: TabConfig[] = [
  { id: 'unix', label: 'macOS / Linux', icon: AppleIcon },
  { id: 'cmd', label: 'Windows CMD', icon: WindowsIcon },
  { id: 'powershell', label: 'PowerShell', icon: WindowsIcon }
]

// OpenAI tabs (2 OS types)
const openaiTabs: TabConfig[] = [
  { id: 'unix', label: 'macOS / Linux', icon: AppleIcon },
  { id: 'windows', label: 'Windows', icon: WindowsIcon }
]

const pythonSdkTabs = new Set(['anthropic-python-sdk', 'openai-python-sdk', 'openai-imagen2-python-sdk'])

const showShellTabs = computed(() =>
  activeClientTab.value !== 'opencode' &&
  activeClientTab.value !== 'cc_switch' &&
  !pythonSdkTabs.has(activeClientTab.value)
)

const currentTabs = computed(() => {
  if (!showShellTabs.value) return []
  if (activeClientTab.value === 'codex' || activeClientTab.value === 'grok') {
    return openaiTabs
  }
  return shellTabs
})

const platformDescription = computed(() => {
  if (activeClientTab.value === 'opencode') {
    return t('keys.useKeyModal.opencode.description')
  }
  if (activeClientTab.value === 'cc_switch') {
    return t('keys.useKeyModal.ccSwitch.description')
  }
  if (activeClientTab.value === 'workbuddy') {
    return t('keys.useKeyModal.workBuddy.description')
  }
  if (activeClientTab.value === 'codex') {
    return t('keys.useKeyModal.openai.description')
  }
  if (pythonSdkTabs.has(activeClientTab.value)) {
    return t('keys.useKeyModal.pythonSdk.description')
  }
  switch (props.platform) {
    case 'unified':
      return t('keys.useKeyModal.unified.description')
    case 'openai':
      if (activeClientTab.value === 'claude') {
        return t('keys.useKeyModal.description')
      }
      return t('keys.useKeyModal.openai.description')
    case 'gemini':
      return t('keys.useKeyModal.gemini.description')
    case 'antigravity':
      return t('keys.useKeyModal.antigravity.description')
    case 'grok':
      return t('keys.useKeyModal.grok.description')
    default:
      return t('keys.useKeyModal.description')
  }
})

const platformNote = computed(() => {
  if (activeClientTab.value === 'cc_switch') {
    return t('keys.useKeyModal.ccSwitch.note')
  }
  if (activeClientTab.value === 'workbuddy') {
    return t('keys.useKeyModal.workBuddy.note')
  }
  if (activeClientTab.value === 'codex') {
    return activeTab.value === 'windows'
      ? t('keys.useKeyModal.openai.noteWindows')
      : t('keys.useKeyModal.openai.note')
  }
  if (pythonSdkTabs.has(activeClientTab.value)) {
    return t('keys.useKeyModal.pythonSdk.note')
  }
  switch (props.platform) {
    case 'unified':
      return t('keys.useKeyModal.unified.note')
    case 'openai':
      if (activeClientTab.value === 'claude') {
        return t('keys.useKeyModal.note')
      }
      return activeTab.value === 'windows'
        ? t('keys.useKeyModal.openai.noteWindows')
        : t('keys.useKeyModal.openai.note')
    case 'gemini':
      return t('keys.useKeyModal.gemini.note')
    case 'antigravity':
      return activeClientTab.value === 'claude'
        ? t('keys.useKeyModal.antigravity.claudeNote')
        : t('keys.useKeyModal.antigravity.geminiNote')
    case 'grok':
      return activeTab.value === 'windows'
        ? t('keys.useKeyModal.grok.noteWindows')
        : t('keys.useKeyModal.grok.note')
    default:
      return t('keys.useKeyModal.note')
  }
})

const showPlatformNote = computed(() => activeClientTab.value !== 'opencode')

const escapeHtml = (value: string) => value
  .replace(/&/g, '&amp;')
  .replace(/</g, '&lt;')
  .replace(/>/g, '&gt;')
  .replace(/"/g, '&quot;')
  .replace(/'/g, '&#39;')

const wrapToken = (className: string, value: string) =>
  `<span class="${className}">${escapeHtml(value)}</span>`

const keyword = (value: string) => wrapToken('text-emerald-300', value)
const variable = (value: string) => wrapToken('text-sky-200', value)
const operator = (value: string) => wrapToken('text-slate-400', value)
const string = (value: string) => wrapToken('text-amber-200', value)
const comment = (value: string) => wrapToken('text-slate-500', value)

// Syntax highlighting helpers
// Generate file configs based on platform and active tab
const currentFiles = computed((): FileConfig[] => {
  const baseUrl = props.baseUrl || window.location.origin
  const apiKey = props.apiKey
  const baseRoot = baseUrl.replace(/\/v1\/?$/, '').replace(/\/+$/, '')
  const ensureV1 = (value: string) => {
    const trimmed = value.replace(/\/+$/, '')
    return trimmed.endsWith('/v1') ? trimmed : `${trimmed}/v1`
  }
  const ensureChatCompletions = (value: string) => {
    const trimmed = value.replace(/\/+$/, '')
    if (trimmed.endsWith('/chat/completions')) {
      return trimmed
    }
    return `${ensureV1(trimmed)}/chat/completions`
  }
  const apiBase = ensureV1(baseRoot)
  const chatCompletionsUrl = ensureChatCompletions(apiBase)
  const antigravityBase = ensureV1(`${baseRoot}/antigravity`)
  const antigravityGeminiBase = (() => {
    const trimmed = `${baseRoot}/antigravity`.replace(/\/+$/, '')
    return trimmed.endsWith('/v1beta') ? trimmed : `${trimmed}/v1beta`
  })()
  const geminiBase = (() => {
    const trimmed = baseRoot.replace(/\/+$/, '')
    return trimmed.endsWith('/v1beta') ? trimmed : `${trimmed}/v1beta`
  })()

  if ((activeClientTab.value === 'claude' || activeClientTab.value === 'codex') && props.platform) {
    const selectedOS = activeTab.value === 'windows' || activeTab.value === 'cmd' || activeTab.value === 'powershell'
      ? 'windows'
      : 'macos'
    const windowsShell = activeTab.value === 'cmd' ? 'cmd' : 'powershell'
    const sharedBaseUrl = activeClientTab.value === 'claude' && props.platform === 'antigravity'
      ? `${baseRoot}/antigravity`
      : baseUrl

    return buildClientConfigFiles({
      client: activeClientTab.value === 'claude' ? 'claude_code' : 'codex',
      os: selectedOS,
      platform: props.platform,
      apiKey,
      baseUrl: sharedBaseUrl,
      allowMessagesDispatch: props.allowMessagesDispatch,
      windowsShell
    }).map(({ hintKey, ...file }) => hintKey
      ? { ...file, hint: t(hintKey) }
      : file)
  }

  if (activeClientTab.value === 'opencode') {
    switch (props.platform) {
      case 'unified':
        return [
          generateOpenCodeConfig('anthropic', apiBase, apiKey, 'opencode.json (Claude)'),
          generateOpenCodeConfig('openai', apiBase, apiKey, 'opencode.json (OpenAI)')
        ]
      case 'anthropic':
        return [generateOpenCodeConfig('anthropic', apiBase, apiKey)]
      case 'openai':
        return [generateOpenCodeConfig('openai', apiBase, apiKey)]
      case 'gemini':
        return [generateOpenCodeConfig('gemini', geminiBase, apiKey)]
      case 'antigravity':
        return [
          generateOpenCodeConfig('antigravity-claude', antigravityBase, apiKey, 'opencode.json (Claude)'),
          generateOpenCodeConfig('antigravity-gemini', antigravityGeminiBase, apiKey, 'opencode.json (Gemini)')
        ]
      case 'grok':
        return [generateOpenCodeConfig('grok', apiBase, apiKey)]
      default:
        return [generateOpenCodeConfig('openai', apiBase, apiKey)]
    }
  }

  if (activeClientTab.value === 'cc_switch' && props.platform) {
    return buildClientConfigFiles({
      client: 'cc_switch',
      os: 'macos',
      platform: props.platform,
      apiKey,
      baseUrl
    }).map(({ hintKey, ...file }) => hintKey
      ? { ...file, hint: t(hintKey) }
      : file)
  }

  if (activeClientTab.value === 'workbuddy') {
    return [generateWorkBuddyConfigFile(chatCompletionsUrl, apiKey, props.platform)]
  }

  switch (props.platform) {
    case 'unified':
      // Anthropic Python SDK appends /v1/messages itself, so base_url must be bare.
      if (activeClientTab.value === 'anthropic-python-sdk') {
        return [generateAnthropicPythonSdkFile(baseRoot, apiKey)]
      }
      if (activeClientTab.value === 'openai-python-sdk') {
        return [generateOpenAIPythonSdkFile(apiBase, apiKey)]
      }
      if (activeClientTab.value === 'openai-imagen2-python-sdk') {
        return [generateOpenAIImagen2PythonSdkFile(apiBase, apiKey)]
      }
      return []
    case 'openai':
      if (activeClientTab.value === 'openai-python-sdk') {
        return [generateOpenAIPythonSdkFile(apiBase, apiKey)]
      }
      if (activeClientTab.value === 'openai-imagen2-python-sdk') {
        return [generateOpenAIImagen2PythonSdkFile(apiBase, apiKey)]
      }
      return []
    case 'gemini':
      // Gemini CLI appends /v1beta itself; GOOGLE_GEMINI_BASE_URL must be bare.
      return [generateGeminiCliContent(baseRoot, apiKey)]
    case 'antigravity':
      // Antigravity is mounted under /antigravity; Claude Code/Gemini CLI append
      // /v1/messages and /v1beta themselves, so the base must be bare.
      if (activeClientTab.value === 'gemini') {
        return [generateGeminiCliContent(`${baseRoot}/antigravity`, apiKey)]
      }
      return []
    case 'grok':
      return generateGrokFiles(apiBase, apiKey)
    default:
      // Anthropic Python SDK posts to /v1/messages itself; base_url must be bare.
      if (activeClientTab.value === 'anthropic-python-sdk') {
        return [generateAnthropicPythonSdkFile(baseRoot, apiKey)]
      }
      return []
  }
})

interface WorkBuddyModelConfig {
  id: string
  maxInputTokens: number
  maxOutputTokens: number
  supportsImages: boolean
  supportsReasoning: boolean
}

function workBuddyModelsForPlatform(platform: Props['platform']): WorkBuddyModelConfig[] {
  const openAIModels: WorkBuddyModelConfig[] = [
    {
      id: 'gpt-5.5',
      maxInputTokens: 1050000,
      maxOutputTokens: 128000,
      supportsImages: true,
      supportsReasoning: true
    }
  ]
  const claudeModels: WorkBuddyModelConfig[] = [
    {
      id: 'claude-sonnet-5',
      maxInputTokens: 1000000,
      maxOutputTokens: 128000,
      supportsImages: true,
      supportsReasoning: true
    },
    {
      id: 'claude-opus-4-8',
      maxInputTokens: 1000000,
      maxOutputTokens: 128000,
      supportsImages: true,
      supportsReasoning: true
    }
  ]

  if (platform === 'openai') {
    return openAIModels
  }
  if (platform === 'unified') {
    return [...openAIModels, ...claudeModels]
  }
  return claudeModels
}

function generateWorkBuddyConfigFile(
  chatCompletionsUrl: string,
  apiKey: string,
  platform: Props['platform']
): FileConfig {
  const models = workBuddyModelsForPlatform(platform).map((model) => ({
    id: model.id,
    // WorkBuddy integrations can send the displayed model name as `model`, so
    // keep `name` identical to the gateway-supported id.
    name: model.id,
    vendor: 'Custom',
    url: chatCompletionsUrl,
    apiKey,
    maxInputTokens: model.maxInputTokens,
    maxOutputTokens: model.maxOutputTokens,
    supportsToolCall: true,
    supportsImages: model.supportsImages,
    supportsReasoning: model.supportsReasoning
  }))
  const content = JSON.stringify(
    {
      models,
      availableModels: models.map((model) => model.id)
    },
    null,
    2
  )
  const path = activeTab.value === 'unix'
    ? '~/.workbuddy/models.json'
    : '%userprofile%\\.workbuddy\\models.json'

  return {
    path,
    content,
    hint: t('keys.useKeyModal.workBuddy.hint')
  }
}

function generateAnthropicPythonSdkFile(baseUrl: string, apiKey: string): FileConfig {
  return {
    path: 'anthropic_client.py',
    content: `from anthropic import Anthropic

client = Anthropic(
    api_key="${apiKey}",
    base_url="${baseUrl}",
)

with client.messages.stream(
    model="claude-opus-4-8",
    max_tokens=1024,
    messages=[{"role": "user", "content": "Hello, Claude"}],
) as stream:
    for text in stream.text_stream:
        print(text, end="", flush=True)
print()`
  }
}

function generateGeminiCliContent(baseUrl: string, apiKey: string): FileConfig {
  const model = 'gemini-2.0-flash'
  const modelComment = t('keys.useKeyModal.gemini.modelComment')
  let path: string
  let content: string
  let highlighted: string

  switch (activeTab.value) {
    case 'unix':
      path = 'Terminal'
      content = `export GOOGLE_GEMINI_BASE_URL="${baseUrl}"
export GEMINI_API_KEY="${apiKey}"
export GEMINI_MODEL="${model}"  # ${modelComment}`
      highlighted = `${keyword('export')} ${variable('GOOGLE_GEMINI_BASE_URL')}${operator('=')}${string(`"${baseUrl}"`)}
${keyword('export')} ${variable('GEMINI_API_KEY')}${operator('=')}${string(`"${apiKey}"`)}
${keyword('export')} ${variable('GEMINI_MODEL')}${operator('=')}${string(`"${model}"`)}  ${comment(`# ${modelComment}`)}`
      break
    case 'cmd':
      path = 'Command Prompt'
      content = `set GOOGLE_GEMINI_BASE_URL=${baseUrl}
set GEMINI_API_KEY=${apiKey}
set GEMINI_MODEL=${model}`
      highlighted = `${keyword('set')} ${variable('GOOGLE_GEMINI_BASE_URL')}${operator('=')}${string(baseUrl)}
${keyword('set')} ${variable('GEMINI_API_KEY')}${operator('=')}${string(apiKey)}
${keyword('set')} ${variable('GEMINI_MODEL')}${operator('=')}${string(model)}
${comment(`REM ${modelComment}`)}`
      break
    case 'powershell':
      path = 'PowerShell'
      content = `$env:GOOGLE_GEMINI_BASE_URL="${baseUrl}"
$env:GEMINI_API_KEY="${apiKey}"
$env:GEMINI_MODEL="${model}"  # ${modelComment}`
      highlighted = `${keyword('$env:')}${variable('GOOGLE_GEMINI_BASE_URL')}${operator('=')}${string(`"${baseUrl}"`)}
${keyword('$env:')}${variable('GEMINI_API_KEY')}${operator('=')}${string(`"${apiKey}"`)}
${keyword('$env:')}${variable('GEMINI_MODEL')}${operator('=')}${string(`"${model}"`)}  ${comment(`# ${modelComment}`)}`
      break
    default:
      path = 'Terminal'
      content = ''
      highlighted = ''
  }

  return { path, content, highlighted }
}

function generateOpenAIPythonSdkFile(baseUrl: string, apiKey: string): FileConfig {
  return {
    path: 'openai_client.py',
    content: `from openai import OpenAI

client = OpenAI(
    api_key="${apiKey}",
    base_url="${baseUrl}",
)

stream = client.responses.create(
    model="gpt-5.5",
    input="Hello, GPT",
    stream=True,
)

for event in stream:
    if event.type == "response.output_text.delta":
        print(event.delta, end="", flush=True)
print()`
  }
}

function generateOpenAIImagen2PythonSdkFile(baseUrl: string, apiKey: string): FileConfig {
  return {
    path: 'imagen2_client.py',
    content: `from base64 import b64decode
from pathlib import Path

from openai import OpenAI

client = OpenAI(
    api_key="${apiKey}",
    base_url="${baseUrl}",
)

stream = client.images.generate(
    model="gpt-image-2",
    prompt="A fox mascot using an AI gateway",
    size="1024x1024",
    stream=True,
    partial_images=2,
)

for event in stream:
    image_b64 = getattr(event, "b64_json", None)
    if not image_b64:
        continue

    if event.type == "image_generation.partial_image":
        output_path = Path(f"partial_{event.partial_image_index}.png")
    elif event.type == "image_generation.completed":
        output_path = Path("image.png")
    else:
        continue

    output_path.write_bytes(b64decode(image_b64))
    print(f"Wrote {output_path}")`
  }
}

function generateGrokFiles(baseUrl: string, apiKey: string): FileConfig[] {
  const isWindows = activeTab.value === 'windows'
  const configDir = isWindows ? '%userprofile%\\.grok' : '~/.grok'
  const configContent = `[models]
default = "sub2api-grok"
web_search = "sub2api-grok"

[model."sub2api-grok"]
model = "grok-4.5"
base_url = "${baseUrl}"
name = "Grok 4.5 via Sub2API"
description = "Grok 4.5 through a Sub2API Grok group"
api_key = "${apiKey}"
api_backend = "responses"
context_window = 1000000
supports_backend_search = true`

  return [{
    path: `${configDir}/config.toml`,
    content: configContent,
    hint: t('keys.useKeyModal.grok.configTomlHint')
  }]
}
function generateOpenCodeConfig(platform: string, baseUrl: string, apiKey: string, pathLabel?: string): FileConfig {
  const provider: Record<string, any> = {
    [platform]: {
      options: {
        baseURL: baseUrl,
        apiKey
      }
    }
  }
  const reasoningVariants = {
    low: {},
    medium: {},
    high: {},
    xhigh: {}
  }
  const maxReasoningVariants = {
    ...reasoningVariants,
    max: {}
  }
  const openaiModel = (name: string, context: number, output = 128000, variants = reasoningVariants) => ({
    name,
    limit: {
      context,
      output
    },
    options: {
      store: false
    },
    variants
  })
  const openaiModels = {
    'gpt-5.6': openaiModel('GPT-5.6 (Sol)', 1050000, 128000, maxReasoningVariants),
    'gpt-5.6-sol': openaiModel('GPT-5.6 Sol', 1050000, 128000, maxReasoningVariants),
    'gpt-5.6-terra': openaiModel('GPT-5.6 Terra', 1050000, 128000, maxReasoningVariants),
    'gpt-5.6-luna': openaiModel('GPT-5.6 Luna', 1050000, 128000, maxReasoningVariants),
    'gpt-5.5': openaiModel('GPT-5.5', 1050000),
    'gpt-5.4': openaiModel('GPT-5.4', 1050000),
    'gpt-5.4-mini': openaiModel('GPT-5.4 Mini', 400000),
    'gpt-5.3-codex-spark': openaiModel('GPT-5.3 Codex Spark', 128000, 32000),
    'gpt-5.2': openaiModel('GPT-5.2', 400000),
    'codex-mini-latest': {
      name: 'Codex Mini',
      limit: {
        context: 200000,
        output: 100000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {}
      }
    }
  }
  const geminiModels = {
    'gemini-2.0-flash': {
      name: 'Gemini 2.0 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      }
    },
    'gemini-2.5-flash': {
      name: 'Gemini 2.5 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      }
    },
    'gemini-2.5-pro': {
      name: 'Gemini 2.5 Pro',
      limit: {
        context: 2097152,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.5-flash': {
      name: 'Gemini 3.5 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      }
    },
    'gemini-3-flash-preview': {
      name: 'Gemini 3 Flash Preview',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      }
    },
    'gemini-3-pro-preview': {
      name: 'Gemini 3 Pro Preview',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.1-pro-preview': {
      name: 'Gemini 3.1 Pro Preview',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    }
  }

  const antigravityGeminiModels = {
    'gemini-2.5-flash': {
      name: 'Gemini 2.5 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'disable'
        }
      }
    },
    'gemini-2.5-flash-lite': {
      name: 'Gemini 2.5 Flash Lite',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-2.5-flash-thinking': {
      name: 'Gemini 2.5 Flash (Thinking)',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3-flash': {
      name: 'Gemini 3 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.1-pro-low': {
      name: 'Gemini 3.1 Pro Low',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.1-pro-high': {
      name: 'Gemini 3.1 Pro High',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-2.5-flash-image': {
      name: 'Gemini 2.5 Flash Image',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image'],
        output: ['image']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.1-flash-image': {
      name: 'Gemini 3.1 Flash Image',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image'],
        output: ['image']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    }
  }
  const claudeModel = (
    name: string,
    context: number,
    output: number,
    thinking?: { type: 'adaptive' } | { type: 'enabled'; budgetTokens: number }
  ) => ({
    name,
    limit: {
      context,
      output
    },
    modalities: {
      input: ['text', 'image', 'pdf'],
      output: ['text']
    },
    ...(thinking ? { options: { thinking } } : {})
  })
  const claudeAdaptive = (name: string) => claudeModel(name, 1048576, 128000, { type: 'adaptive' })
  const claudeThinking = (name: string, output = 128000) =>
    claudeModel(name, 200000, output, { budgetTokens: 24576, type: 'enabled' })
  const claudeModels = {
    'claude-fable-5': claudeAdaptive('Claude Fable 5'),
    'claude-mythos-5': claudeAdaptive('Claude Mythos 5'),
    'claude-opus-4-8': claudeModel('Claude Opus 4.8', 200000, 128000),
    'claude-opus-4-7': claudeModel('Claude Opus 4.7', 200000, 128000),
    'claude-opus-4-6': claudeModel('Claude Opus 4.6', 200000, 128000),
    'claude-opus-4-5-20251101': claudeModel('Claude Opus 4.5', 200000, 128000),
    'claude-sonnet-4-6': claudeThinking('Claude Sonnet 4.6', 64000),
    'claude-sonnet-4-5-20250929': claudeModel('Claude Sonnet 4.5', 200000, 64000),
    'claude-haiku-4-5-20251001': claudeModel('Claude Haiku 4.5', 200000, 64000),
    'claude-sonnet-4-20250514': claudeModel('Claude Sonnet 4', 200000, 64000),
    'claude-opus-4-20250514': claudeModel('Claude Opus 4', 200000, 128000),
    'claude-opus-4-1-20250805': claudeModel('Claude Opus 4.1', 200000, 128000),
    'claude-3-7-sonnet-20250219': claudeModel('Claude Sonnet 3.7', 200000, 64000),
    'claude-3-5-sonnet-20241022': claudeModel('Claude Sonnet 3.5 20241022', 200000, 8192),
    'claude-3-5-sonnet-20240620': claudeModel('Claude Sonnet 3.5 20240620', 200000, 8192),
    'claude-3-5-haiku-20241022': claudeModel('Claude Haiku 3.5', 200000, 8192)
  }
  const antigravityClaudeModels = {
    'claude-fable-5': claudeAdaptive('Claude Fable 5'),
    'claude-mythos-5': claudeAdaptive('Claude Mythos 5'),
    'claude-opus-4-8': claudeModel('Claude Opus 4.8', 200000, 128000),
    'claude-opus-4-7': claudeModel('Claude Opus 4.7', 200000, 128000),
    'claude-opus-4-6': claudeModel('Claude Opus 4.6', 200000, 128000),
    'claude-opus-4-6-thinking': claudeThinking('Claude Opus 4.6 Thinking'),
    'claude-opus-4-5-thinking': claudeThinking('Claude Opus 4.5 Thinking'),
    'claude-sonnet-4-6': claudeThinking('Claude Sonnet 4.6', 64000),
    'claude-sonnet-4-5': claudeModel('Claude Sonnet 4.5', 200000, 64000),
    'claude-sonnet-4-5-thinking': claudeThinking('Claude Sonnet 4.5 Thinking', 64000)
  }
  const grokModels = {
    'grok-4.5': {
      name: 'Grok 4.5',
      limit: { context: 1000000, output: 128000 }
    },
    'grok-4.3': {
      name: 'Grok 4.3',
      limit: { context: 1000000, output: 128000 }
    },
    'grok-build-0.1': {
      name: 'Grok Build 0.1',
      limit: { context: 256000, output: 128000 }
    },
    'grok-composer-2.5-fast': {
      name: 'Grok Composer 2.5 Fast',
      limit: { context: 500000, output: 128000 }
    }
  }

  if (platform === 'gemini') {
    provider[platform].npm = '@ai-sdk/google'
    provider[platform].models = geminiModels
  } else if (platform === 'anthropic') {
    provider[platform].npm = '@ai-sdk/anthropic'
    provider[platform].models = claudeModels
  } else if (platform === 'antigravity-claude') {
    provider[platform].npm = '@ai-sdk/anthropic'
    provider[platform].name = 'Antigravity (Claude)'
    provider[platform].models = antigravityClaudeModels
  } else if (platform === 'antigravity-gemini') {
    provider[platform].npm = '@ai-sdk/google'
    provider[platform].name = 'Antigravity (Gemini)'
    provider[platform].models = antigravityGeminiModels
  } else if (platform === 'openai') {
    provider[platform].models = openaiModels
  } else if (platform === 'grok') {
    provider[platform].npm = '@ai-sdk/openai'
    provider[platform].name = 'Grok via Sub2API'
    provider[platform].models = grokModels
  }

  const agent =
    platform === 'openai'
      ? {
          build: {
            options: {
              store: false
            }
          },
          plan: {
            options: {
              store: false
            }
          }
        }
      : undefined
  const defaultModelByPlatform: Record<string, string> = {
    anthropic: 'anthropic/claude-fable-5',
    openai: 'openai/gpt-5.5',
    gemini: 'gemini/gemini-2.5-flash',
    'antigravity-claude': 'antigravity-claude/claude-fable-5',
    'antigravity-gemini': 'antigravity-gemini/gemini-2.5-flash'
  }
  const smallModelByPlatform: Record<string, string> = {
    anthropic: 'anthropic/claude-haiku-4-5-20251001',
    openai: 'openai/gpt-5.3-codex-spark',
    gemini: 'gemini/gemini-2.0-flash',
    'antigravity-claude': 'antigravity-claude/claude-sonnet-4-5',
    'antigravity-gemini': 'antigravity-gemini/gemini-2.5-flash-lite'
  }
  const defaultModel = defaultModelByPlatform[platform]
  const smallModel = smallModelByPlatform[platform]

  const content = JSON.stringify(
    {
      $schema: 'https://opencode.ai/config.json',
      ...(defaultModel ? { model: defaultModel } : {}),
      ...(smallModel ? { small_model: smallModel } : {}),
      provider,
      ...(agent ? { agent } : {})
    },
    null,
    2
  )

  return {
    path: pathLabel ?? 'opencode.json',
    content,
    hint: t('keys.useKeyModal.opencode.hint')
  }
}

const copyContent = async (content: string, index: number) => {
  const success = await clipboardCopy(content, t('keys.copied'))
  if (success) {
    copiedIndex.value = index
    setTimeout(() => {
      copiedIndex.value = null
    }, 2000)
  }
}
</script>
