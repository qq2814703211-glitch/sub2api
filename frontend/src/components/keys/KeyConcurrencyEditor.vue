<template>
  <form class="mt-3 flex flex-wrap items-center gap-2" @submit.prevent="save">
    <label :for="`concurrency-${apiKey.id}`" class="text-xs text-gray-500">{{ t('keys.concurrencyLimit') }}</label>
    <input
      :id="`concurrency-${apiKey.id}`" v-model.number="limit" class="input w-24"
      type="number" min="0" max="2147483647" step="1" required :disabled="saving"
    />
    <button class="btn btn-secondary" type="submit" :disabled="saving">
      {{ t('keys.saveConcurrency') }}{{ saving ? '…' : '' }}
    </button>
    <span class="text-xs text-gray-500">
      {{ t('keys.currentConcurrency') }}: {{ apiKey.current_concurrency ?? 0 }} / {{ apiKey.concurrency_limit || '∞' }} ·
      {{ apiKey.queue_stats_available ? `${t('keys.waiting')}: ${apiKey.waiting_count}` : t('keys.queueUnavailable') }}
    </span>
    <p v-if="error" role="alert" class="w-full text-xs text-red-600">{{ error }}</p>
    <p class="w-full text-xs text-gray-500">{{ t('keys.concurrencyHint') }}</p>
  </form>
</template>

<script setup lang="ts">
import { ref, watch, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { updateConcurrency } from '@/api/admin/apiKeys'
import type { ApiKey } from '@/types'

const props = defineProps<{ apiKey: ApiKey }>()
const emit = defineEmits<{ updated: [key: ApiKey] }>()
const { t } = useI18n()
const limit = ref(0)
const saving = ref(false)
const error = ref('')
let version = 0
let controller: AbortController | undefined

watch(() => [props.apiKey.id, props.apiKey.concurrency_limit], () => {
  version++
  controller?.abort()
  limit.value = props.apiKey.concurrency_limit ?? 0
  saving.value = false
  error.value = ''
}, { immediate: true })

onBeforeUnmount(() => {
  version++
  controller?.abort()
})

async function save() {
  if (!Number.isSafeInteger(limit.value) || limit.value < 0 || limit.value > 2147483647) {
    error.value = t('keys.concurrencyInvalid')
    return
  }
  const current = ++version
  controller?.abort()
  controller = new AbortController()
  saving.value = true
  error.value = ''
  try {
    const result = await updateConcurrency(props.apiKey.id, limit.value, controller.signal)
    if (current === version) emit('updated', result.api_key)
  } catch (e) {
    if (current === version) error.value = e instanceof Error ? e.message : t('keys.failedToSave')
  } finally {
    if (current === version) saving.value = false
  }
}
</script>
