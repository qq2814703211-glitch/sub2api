<template>
  <section class="card p-4" data-testid="video-billing">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h3 class="font-semibold text-gray-900 dark:text-gray-100">{{ t('videoBilling.title') }}</h3>
      <div class="flex flex-wrap items-center gap-2">
        <label class="text-sm"><input type="checkbox" :checked="enabled" :disabled="loading || saving || !workerLoaded" @change="toggleWorker" /> {{ t('videoBilling.worker') }}</label>
        <select v-model="state" class="input w-auto" @change="reset">
          <option value="">{{ t('videoBilling.all') }}</option>
          <option v-for="value in states" :key="value" :value="value">{{ t(`videoBilling.states.${value}`) }}</option>
        </select>
        <button class="btn btn-secondary" :disabled="loading" @click="reset">{{ t('videoBilling.refresh') }}</button>
      </div>
    </div>
    <p class="mt-2 text-xs text-gray-500">{{ t('videoBilling.help') }}</p>
    <p v-if="error" role="alert" class="mt-3 text-sm text-red-600">{{ error }}</p>
    <p v-if="loading" role="status" class="mt-3 text-sm">{{ t('videoBilling.loading') }}</p>
    <p v-else-if="!items.length && !error" class="mt-3 text-sm text-gray-500">{{ t('videoBilling.empty') }}</p>
    <div v-if="items.length" class="mt-3 overflow-x-auto">
      <table class="w-full text-left text-sm">
        <thead><tr><th>{{ t('videoBilling.task') }}</th><th>{{ t('videoBilling.owner') }}</th><th>{{ t('videoBilling.state') }}</th><th>{{ t('videoBilling.amount') }}</th><th>{{ t('videoBilling.updated') }}</th><th>{{ t('videoBilling.action') }}</th></tr></thead>
        <tbody>
          <tr v-for="job in items" :key="job.id" class="border-t border-gray-100 dark:border-dark-700">
            <td class="py-2"><div>{{ job.provider }} · {{ job.model }}</div><div class="break-all font-mono text-xs">{{ job.task_id || job.id }}</div></td>
            <td>{{ job.user_id }} / {{ job.api_key_id }} / {{ job.account_id }}</td>
            <td><div>{{ t(`videoBilling.states.${job.state}`) }}</div><div class="text-xs text-gray-500">{{ job.provider_status }} · {{ job.attempts }}</div><div class="text-xs text-red-600">{{ job.last_error }}</div></td>
            <td>{{ job.actual_cost == null ? '—' : `$${job.actual_cost.toFixed(8)}` }}</td>
            <td class="whitespace-nowrap">{{ new Date(job.updated_at).toLocaleString() }}</td>
            <td><button v-if="canRetry(job)" class="btn btn-secondary" :disabled="saving" @click="retry(job.id)">{{ t('videoBilling.retry') }}</button></td>
          </tr>
        </tbody>
      </table>
      <button v-if="hasMore" class="btn btn-secondary mt-3" :disabled="loading" @click="load(true)">{{ t('videoBilling.more') }}</button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { videoBillingAPI, type VideoBillingJob } from '@/api/admin/videoBilling'

const { t } = useI18n()
const states = ['creating', 'submitted', 'ready', 'billed', 'done', 'failed', 'expired', 'reconciliation']
const state = ref('')
const items = ref<VideoBillingJob[]>([])
const enabled = ref(true)
const workerLoaded = ref(false)
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const hasMore = ref(false)
let controller: AbortController | undefined
let mutation: AbortController | undefined
let generation = 0
let disposed = false

function canRetry(job: VideoBillingJob) {
  return !!job.task_id && ['submitted', 'ready', 'billed', 'reconciliation'].includes(job.state)
}
async function load(more = false) {
  controller?.abort()
  controller = new AbortController()
  const current = ++generation
  const signal = controller.signal
  loading.value = true
  error.value = ''
  try {
    const [jobs, worker] = await Promise.all([
      videoBillingAPI.list(state.value, more ? items.value.at(-1)?.id ?? '' : '', signal),
      videoBillingAPI.worker(signal)
    ])
    if (disposed || signal.aborted || current !== generation) return
    items.value = more ? [...items.value, ...jobs] : jobs
    enabled.value = worker
    workerLoaded.value = true
    hasMore.value = jobs.length === 50
  } catch {
    if (!disposed && !signal.aborted && current === generation) error.value = t('videoBilling.loadError')
  } finally {
    if (!disposed && current === generation) loading.value = false
  }
}
function reset() { items.value = []; void load() }
async function retry(id: string) {
  mutation?.abort()
  mutation = new AbortController()
  const signal = mutation.signal
  saving.value = true
  try {
    await videoBillingAPI.retry(id, signal)
    if (!disposed && !signal.aborted) await load()
  } catch { if (!disposed && !signal.aborted) error.value = t('videoBilling.saveError') }
  finally { if (!disposed && !signal.aborted) saving.value = false }
}
async function toggleWorker() {
  mutation?.abort()
  mutation = new AbortController()
  const signal = mutation.signal
  saving.value = true
  try {
    await videoBillingAPI.setWorker(!enabled.value, signal)
    if (!disposed && !signal.aborted) enabled.value = !enabled.value
  } catch { if (!disposed && !signal.aborted) error.value = t('videoBilling.saveError') }
  finally { if (!disposed && !signal.aborted) saving.value = false }
}
onMounted(() => { void load() })
onBeforeUnmount(() => { disposed = true; generation++; controller?.abort(); mutation?.abort() })
</script>
