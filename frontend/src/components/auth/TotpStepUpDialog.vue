<template>
  <div v-if="controller.visible.value" class="fixed inset-0 z-[60] overflow-y-auto">
    <div class="flex min-h-full items-center justify-center p-4">
      <div class="fixed inset-0 bg-black/50 transition-opacity" @click="handleCancel"></div>
      <div class="relative w-full max-w-md transform rounded-xl bg-white p-6 shadow-xl transition-all dark:bg-dark-800">
        <div class="mb-6 text-center">
          <h3 class="mt-4 text-xl font-semibold text-gray-900 dark:text-white">{{ t('stepUp.title') }}</h3>
          <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('stepUp.hint') }}</p>
        </div>
        <div class="mb-6">
          <div class="flex justify-center gap-2">
            <input
              v-for="(_, index) in 6"
              :key="index"
              :ref="(el) => setInputRef(el, index)"
              type="text"
              maxlength="1"
              inputmode="numeric"
              autocomplete="off"
              class="h-12 w-10 rounded-lg border border-gray-300 text-center text-lg font-semibold focus:border-primary-500 focus:ring-primary-500 dark:border-dark-600 dark:bg-dark-700"
              :disabled="verifying"
              @input="handleCodeInput($event, index)"
              @keydown="handleKeydown($event, index)"
              @paste="handlePaste"
            />
          </div>
          <div v-if="verifying" class="mt-3 text-center text-sm text-gray-500">{{ t('common.verifying') }}</div>
        </div>
        <button type="button" class="btn btn-secondary w-full" :disabled="verifying" @click="handleCancel">
          {{ t('common.cancel') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'
import { totpAPI } from '@/api'
import type { StepUpController } from '@/composables/useStepUp'

const props = defineProps<{ controller: StepUpController }>()
const { t } = useI18n()
const appStore = useAppStore()
const verifying = ref(false)
const code = ref<string[]>(['', '', '', '', '', ''])
const inputRefs = ref<(HTMLInputElement | null)[]>([])

watch(() => props.controller.visible.value, (open) => {
  if (open) {
    resetInputs()
    nextTick(() => inputRefs.value[0]?.focus())
  }
})

watch(() => code.value.join(''), (value) => {
  if (value.length === 6 && !verifying.value) submit(value)
})

async function submit(otp: string) {
  verifying.value = true
  try {
    await totpAPI.stepUp(otp)
    props.controller.onVerified()
  } catch (err: any) {
    appStore.showError(err?.message || t('stepUp.verifyFailed'))
    resetInputs()
    nextTick(() => inputRefs.value[0]?.focus())
  } finally {
    verifying.value = false
  }
}

function resetInputs() {
  code.value = ['', '', '', '', '', '']
  inputRefs.value.forEach((input) => { if (input) input.value = '' })
}

function handleCancel() {
  if (!verifying.value) props.controller.onCancel()
}

function setInputRef(el: unknown, index: number) {
  inputRefs.value[index] = el as HTMLInputElement | null
}

function handleCodeInput(event: Event, index: number) {
  const input = event.target as HTMLInputElement
  const value = input.value.replace(/[^0-9]/g, '').slice(-1)
  input.value = value
  code.value[index] = value
  if (value && index < 5) nextTick(() => inputRefs.value[index + 1]?.focus())
}

function handleKeydown(event: KeyboardEvent, index: number) {
  if (event.key === 'Backspace' && !(event.target as HTMLInputElement).value && index > 0) {
    event.preventDefault()
    inputRefs.value[index - 1]?.focus()
  }
}

function handlePaste(event: ClipboardEvent) {
  event.preventDefault()
  const digits = (event.clipboardData?.getData('text') || '').replace(/[^0-9]/g, '').slice(0, 6).split('')
  for (let i = 0; i < 6; i++) {
    code.value[i] = digits[i] || ''
    if (inputRefs.value[i]) inputRefs.value[i]!.value = digits[i] || ''
  }
  nextTick(() => inputRefs.value[Math.min(digits.length, 5)]?.focus())
}
</script>
