<template>
  <Teleport to="body">
    <Transition name="popup-fade">
      <div
        v-if="displayedAnnouncement"
        data-testid="announcement-popup"
        role="dialog"
        aria-modal="true"
        aria-labelledby="announcement-popup-title"
        class="fixed inset-0 z-[120] flex items-center justify-center overflow-y-auto bg-black/60 p-4 backdrop-blur-sm sm:p-6"
      >
        <div
          ref="dialogRef"
          tabindex="-1"
          class="flex max-h-[calc(100vh-2rem)] w-full max-w-[680px] flex-col overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-2xl dark:border-dark-700 dark:bg-dark-800 sm:max-h-[84vh]"
          @click.stop
        >
          <header
            data-testid="announcement-popup-brand"
            class="flex shrink-0 items-center gap-3 border-b border-gray-100 bg-gradient-to-r from-primary-50 via-white to-white px-5 py-4 dark:border-dark-700 dark:from-primary-950/30 dark:via-dark-800 dark:to-dark-800 sm:px-6"
          >
            <div class="flex h-11 w-11 shrink-0 items-center justify-center overflow-hidden rounded-xl border border-primary-100 bg-white shadow-sm dark:border-primary-900/60 dark:bg-dark-700">
              <img
                data-testid="announcement-popup-logo"
                :src="siteLogo"
                :alt="siteName"
                class="h-full w-full object-contain p-1.5"
              />
            </div>
            <div class="min-w-0 flex-1">
              <span class="block truncate text-sm font-semibold text-gray-900 dark:text-white">
                {{ siteName }}
              </span>
              <span class="mt-0.5 flex items-center gap-1.5 text-xs font-medium text-primary-600 dark:text-primary-400">
                <Icon name="bell" size="xs" />
                {{ t('announcements.officialUpdate') }}
              </span>
            </div>
            <button
              data-testid="announcement-popup-close"
              type="button"
              :aria-label="t('common.close')"
              class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 dark:text-gray-500 dark:hover:bg-dark-700 dark:hover:text-gray-200 dark:focus:ring-offset-dark-800"
              @click="handleClose"
            >
              <Icon name="x" size="sm" />
            </button>
          </header>

          <section class="announcement-popup-content min-h-0 flex-1 overflow-y-auto px-5 py-6 sm:px-8 sm:py-7">
            <h2
              id="announcement-popup-title"
              class="text-xl font-semibold leading-tight text-gray-900 dark:text-white sm:text-2xl"
            >
              {{ displayedAnnouncement.title }}
            </h2>
            <time class="mt-2 flex items-center gap-1.5 text-sm text-gray-500 dark:text-gray-400">
              <Icon name="clock" size="sm" />
              {{ formatRelativeWithDateTime(displayedAnnouncement.created_at) }}
            </time>
            <div
              class="markdown-body prose prose-sm mt-6 max-w-none dark:prose-invert"
              v-html="renderedContent"
            ></div>
          </section>

          <footer class="shrink-0 border-t border-gray-100 bg-gray-50/80 px-5 py-4 dark:border-dark-700 dark:bg-dark-900/40 sm:px-6">
            <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
              <div class="flex items-center gap-3 text-sm text-gray-500 dark:text-gray-400">
                <span data-testid="announcement-popup-progress" class="font-medium tabular-nums">
                  {{ position }} / {{ total }}
                </span>
                <span class="flex items-center gap-1" aria-hidden="true">
                  <span
                    v-for="index in total"
                    :key="index"
                    class="h-1.5 rounded-full transition-all"
                    :class="index === position
                      ? 'w-5 bg-primary-500'
                      : index < position
                        ? 'w-1.5 bg-primary-300 dark:bg-primary-700'
                        : 'w-1.5 bg-gray-300 dark:bg-dark-600'"
                  ></span>
                </span>
              </div>
              <div class="flex flex-col-reverse gap-2 sm:flex-row sm:items-center">
                <button
                  v-if="!preview"
                  data-testid="announcement-popup-snooze"
                  type="button"
                  class="btn btn-secondary justify-center"
                  @click="handleClose"
                >
                  {{ t('announcements.later') }}
                </button>
                <button
                  data-testid="announcement-popup-advance"
                  type="button"
                  class="btn btn-primary justify-center"
                  @click="handleAdvance"
                >
                  {{ preview
                    ? t('common.close')
                    : isLast
                      ? t('announcements.acknowledge')
                      : t('announcements.next') }}
                </button>
              </div>
            </div>
          </footer>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import Icon from '@/components/icons/Icon.vue'
import { useAnnouncementStore } from '@/stores/announcements'
import { useAppStore } from '@/stores/app'
import { useDialogFocus } from '@/composables/useDialogFocus'
import { createBodyScrollLock } from '@/utils/bodyScrollLock'
import { formatRelativeWithDateTime } from '@/utils/format'
import { sanitizeUrl } from '@/utils/url'
import type { Announcement, UserAnnouncement } from '@/types'
import '@/styles/announcement-markdown.css'

type PreviewAnnouncement = Pick<Announcement | UserAnnouncement, 'title' | 'content' | 'created_at'>

const props = withDefaults(defineProps<{
  announcement?: PreviewAnnouncement | null
  preview?: boolean
}>(), {
  announcement: null,
  preview: false,
})

const emit = defineEmits<{
  close: []
}>()

const { t } = useI18n()
const announcementStore = useAnnouncementStore()
const appStore = useAppStore()
const bodyScrollLock = createBodyScrollLock()
const dialogRef = ref<HTMLElement | null>(null)
const displayedAnnouncement = computed(() => (
  props.preview ? props.announcement : announcementStore.currentPopup
))
const siteName = computed(() => appStore.siteName || 'LINX2.AI')
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', {
  allowRelative: true,
  allowDataUrl: true,
}) || '/linx2-icon.png')
const position = computed(() => props.preview ? 1 : announcementStore.popupPosition)
const total = computed(() => props.preview ? 1 : announcementStore.popupTotal)
const isLast = computed(() => position.value >= total.value)
const ownsBodyScrollLock = computed(() => props.preview
  ? Boolean(props.announcement)
  : Boolean(announcementStore.currentPopup) || announcementStore.popupTotal > 0)

marked.setOptions({
  breaks: true,
  gfm: true,
})

const renderedContent = computed(() => {
  const content = displayedAnnouncement.value?.content
  if (!content) return ''
  const html = marked.parse(content) as string
  return DOMPurify.sanitize(html)
})

function handleClose() {
  if (props.preview) return emit('close')
  announcementStore.snoozePopupBatch()
}

async function handleAdvance() {
  if (props.preview) return emit('close')
  const saved = await announcementStore.advancePopup()
  if (!saved) appStore.showError(t('announcements.readSaveFailed'))
}

useDialogFocus({
  dialogRef,
  isOpen: () => Boolean(displayedAnnouncement.value),
  onEscape: handleClose,
})

watch(
  ownsBodyScrollLock,
  (ownsLock) => {
    if (ownsLock) {
      bodyScrollLock.acquire()
    } else {
      bodyScrollLock.release()
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  bodyScrollLock.release()
})
</script>

<style scoped>
.popup-fade-enter-active {
  transition: all 0.3s cubic-bezier(0.16, 1, 0.3, 1);
}

.popup-fade-leave-active {
  transition: all 0.2s cubic-bezier(0.4, 0, 1, 1);
}

.popup-fade-enter-from,
.popup-fade-leave-to {
  opacity: 0;
}

.popup-fade-enter-from > div {
  transform: scale(0.96) translateY(-8px);
  opacity: 0;
}

.popup-fade-leave-to > div {
  transform: scale(0.97) translateY(-6px);
  opacity: 0;
}

.announcement-popup-content::-webkit-scrollbar {
  width: 8px;
}

.announcement-popup-content::-webkit-scrollbar-track {
  background: transparent;
}

.announcement-popup-content::-webkit-scrollbar-thumb {
  background: #cbd5e1;
  border-radius: 4px;
}

.dark .announcement-popup-content::-webkit-scrollbar-thumb {
  background: #4b5563;
}
</style>
