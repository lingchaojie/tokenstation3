<template>
  <div
    v-if="hasRewards"
    class="text-xs leading-5 text-gray-600 dark:text-dark-300"
    data-testid="reward-balance-breakdown"
  >
    <ul class="space-y-1.5">
      <li
        v-if="hasDailyReward"
        class="flex items-start gap-1.5"
        data-testid="reward-balance-daily"
      >
        <span class="mt-[1px] text-amber-500" aria-hidden="true">•</span>
        <span>
          {{ t('dashboard.rewardBalance.daily', {
            amount: formatCurrency(summary?.daily_check_in.amount),
            expiresAt: formatDateTime(summary?.daily_check_in.expires_at),
          }) }}
        </span>
      </li>
      <li
        v-if="hasAffiliateReward"
        class="flex items-start gap-1.5"
        data-testid="reward-balance-affiliate"
      >
        <span class="mt-[1px] text-violet-500" aria-hidden="true">•</span>
        <span>
          {{ t('dashboard.rewardBalance.affiliate', {
            amount: formatCurrency(summary?.affiliate.amount),
            expiresAt: formatDateTime(summary?.affiliate.earliest_expires_at),
          }) }}
          <button
            type="button"
            class="ml-1 font-medium text-primary-600 underline decoration-primary-300 underline-offset-2 hover:text-primary-500 dark:text-primary-300"
            :aria-label="t('dashboard.rewardBalance.detailsAria')"
            data-testid="reward-credit-details-trigger"
            @click="openDetails"
          >
            {{ t('dashboard.rewardBalance.detailCount', { count: summary?.affiliate.credit_count ?? 0 }) }}
          </button>
        </span>
      </li>
    </ul>

    <Teleport to="body">
      <div
        v-if="detailsOpen"
        class="fixed inset-0 z-[100] flex items-center justify-center bg-black/45 p-4"
        @click.self="closeDetails"
      >
        <section
          ref="dialogRef"
          role="dialog"
          aria-modal="true"
          aria-labelledby="reward-credit-dialog-title"
          tabindex="-1"
          class="max-h-[80vh] w-full max-w-lg overflow-hidden rounded-2xl bg-white text-left shadow-2xl outline-none dark:bg-dark-800"
          @keydown.esc="closeDetails"
        >
          <header class="flex items-center justify-between border-b border-gray-100 px-5 py-4 dark:border-dark-700">
            <h2 id="reward-credit-dialog-title" class="text-base font-semibold text-gray-950 dark:text-white">
              {{ t('dashboard.rewardBalance.dialogTitle') }}
            </h2>
            <button
              type="button"
              class="rounded-lg p-1.5 text-gray-400 hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-dark-700 dark:hover:text-white"
              :aria-label="t('dashboard.rewardBalance.closeAria')"
              data-testid="reward-credit-dialog-close"
              @click="closeDetails"
            >
              <span aria-hidden="true">×</span>
            </button>
          </header>

          <div class="max-h-[60vh] overflow-y-auto p-5">
            <div
              v-if="loading"
              class="py-10 text-center text-sm text-gray-500 dark:text-dark-300"
              data-testid="reward-credit-loading"
            >
              {{ t('dashboard.rewardBalance.loading') }}
            </div>
            <div
              v-else-if="loadError"
              class="rounded-xl border border-red-200 bg-red-50 p-4 text-center dark:border-red-900/60 dark:bg-red-950/30"
              data-testid="reward-credit-load-error"
            >
              <p class="text-sm text-red-700 dark:text-red-300">{{ t('dashboard.rewardBalance.loadError') }}</p>
              <button
                type="button"
                class="mt-3 rounded-lg bg-red-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-red-500"
                data-testid="reward-credit-retry"
                @click="loadPage(page, true)"
              >
                {{ t('common.retry') }}
              </button>
            </div>
            <p
              v-else-if="items.length === 0"
              class="py-10 text-center text-sm text-gray-500 dark:text-dark-300"
              data-testid="reward-credit-empty"
            >
              {{ t('dashboard.rewardBalance.empty') }}
            </p>
            <ul v-else class="space-y-3">
              <li
                v-for="item in items"
                :key="item.id"
                class="rounded-xl border border-gray-100 bg-gray-50/70 p-4 dark:border-dark-700 dark:bg-dark-900/40"
              >
                <div class="flex items-center justify-between gap-3">
                  <span class="font-medium text-gray-900 dark:text-white">
                    {{ roleLabel(item.role_label) }}
                  </span>
                  <span class="font-semibold text-primary-600 dark:text-primary-300">
                    {{ formatCurrency(item.remaining_amount) }}
                  </span>
                </div>
                <p class="mt-1 text-gray-500 dark:text-dark-300">
                  {{ t('dashboard.rewardBalance.expiresAt', { expiresAt: formatDateTime(item.expires_at) }) }}
                </p>
              </li>
            </ul>
          </div>

          <footer
            v-if="!loading && !loadError && pages > 1"
            class="flex items-center justify-between border-t border-gray-100 px-5 py-3 dark:border-dark-700"
          >
            <button
              type="button"
              class="rounded-lg px-3 py-1.5 text-xs font-medium text-gray-600 hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-40 dark:text-dark-300 dark:hover:bg-dark-700"
              :disabled="page <= 1"
              data-testid="reward-credit-previous-page"
              @click="loadPage(page - 1)"
            >
              {{ t('common.previousPage') }}
            </button>
            <span class="text-xs text-gray-500 dark:text-dark-300" data-testid="reward-credit-page-status">
              {{ t('dashboard.rewardBalance.pageStatus', { page, pages }) }}
            </span>
            <button
              type="button"
              class="rounded-lg px-3 py-1.5 text-xs font-medium text-gray-600 hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-40 dark:text-dark-300 dark:hover:bg-dark-700"
              :disabled="page >= pages"
              data-testid="reward-credit-next-page"
              @click="loadPage(page + 1)"
            >
              {{ t('common.nextPage') }}
            </button>
          </footer>
        </section>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { userAPI } from '@/api/user'
import type { RewardBalanceSummary, RewardCreditItem, RewardCreditPage } from '@/types'
import { formatCurrency, formatDateTime } from '@/utils/format'

const props = defineProps<{
  summary?: RewardBalanceSummary | null
}>()

const { t } = useI18n()
const pageSize = 10
const detailsOpen = ref(false)
const dialogRef = ref<HTMLElement | null>(null)
const loading = ref(false)
const loadError = ref(false)
const items = ref<RewardCreditItem[]>([])
const page = ref(1)
const pages = ref(0)
const pageCache = new Map<number, RewardCreditPage>()
let loadingPromise: Promise<void> | null = null

const hasDailyReward = computed(() => Number(props.summary?.daily_check_in.amount ?? 0) > 0)
const hasAffiliateReward = computed(() => Number(props.summary?.affiliate.amount ?? 0) > 0)
const hasRewards = computed(() => hasDailyReward.value || hasAffiliateReward.value)

function applyPage(result: RewardCreditPage) {
  items.value = result.items
  page.value = result.page
  pages.value = result.pages
}

async function loadPage(targetPage: number, force = false): Promise<void> {
  if (targetPage < 1) return
  if (!force) {
    const cached = pageCache.get(targetPage)
    if (cached) {
      applyPage(cached)
      return
    }
  }
  if (loadingPromise) return loadingPromise

  loading.value = true
  loadError.value = false
  loadingPromise = (async () => {
    try {
      const result = await userAPI.getRewardCredits({
        type: 'affiliate',
        status: 'active',
        page: targetPage,
        page_size: pageSize,
      })
      pageCache.set(targetPage, result)
      applyPage(result)
    } catch {
      loadError.value = true
    } finally {
      loading.value = false
      loadingPromise = null
    }
  })()
  return loadingPromise
}

async function openDetails() {
  detailsOpen.value = true
  void loadPage(1)
  await nextTick()
  dialogRef.value?.focus()
}

function closeDetails() {
  detailsOpen.value = false
}

function roleLabel(role: RewardCreditItem['role_label']): string {
  return t(role === 'inviter'
    ? 'dashboard.rewardBalance.inviterRole'
    : 'dashboard.rewardBalance.inviteeRole')
}
</script>
