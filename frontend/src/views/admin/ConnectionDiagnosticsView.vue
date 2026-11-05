<template>
  <AppLayout>
    <main class="mx-auto w-full max-w-screen-2xl space-y-5 px-4 py-6 sm:px-6">
      <header class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 pb-4 dark:border-dark-700">
        <div>
          <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('admin.connectionDiagnostics.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500">{{ t('admin.connectionDiagnostics.retention', { days: retentionDays }) }}</p>
        </div>
        <label class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
          <input v-model="enabled" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" :disabled="saving" @change="saveEnabled" />
          {{ t('admin.connectionDiagnostics.enabled') }}
        </label>
      </header>

      <form class="flex flex-wrap items-end gap-3" @submit.prevent="search">
        <label class="min-w-40 flex-1 text-sm">{{ t('admin.connectionDiagnostics.state') }}
          <select v-model="filters.state" class="input mt-1">
            <option value="all">{{ t('admin.connectionDiagnostics.allStates') }}</option>
            <option value="abnormal">{{ t('admin.connectionDiagnostics.abnormal') }}</option>
            <option v-for="state in states" :key="state" :value="state">{{ state }}</option>
          </select>
        </label>
        <label class="min-w-40 flex-1 text-sm">{{ t('admin.connectionDiagnostics.source') }}
          <select v-model="filters.source" class="input mt-1"><option value="all">all</option><option value="application">application</option><option value="nginx">nginx</option><option value="caddy">caddy</option></select>
        </label>
        <label class="min-w-48 flex-1 text-sm">{{ t('admin.connectionDiagnostics.requestId') }}
          <input v-model.trim="filters.request_id" class="input mt-1" maxlength="128" />
        </label>
        <label class="min-w-40 flex-1 text-sm">{{ t('admin.connectionDiagnostics.model') }}
          <input v-model.trim="filters.model" class="input mt-1" maxlength="256" />
        </label>
        <button class="btn btn-primary" type="submit" :disabled="loading">{{ t('common.search') }}</button>
      </form>

      <p v-if="error" role="alert" class="border-l-2 border-red-500 pl-3 text-sm text-red-700 dark:text-red-300">{{ error }}</p>
      <div v-if="loading && rows.length === 0" class="py-12 text-center text-sm text-gray-500">{{ t('common.loading') }}</div>
      <div v-else-if="!loading && rows.length === 0" class="py-12 text-center text-sm text-gray-500">{{ t('admin.connectionDiagnostics.empty') }}</div>
      <div v-else class="overflow-x-auto border-y border-gray-200 dark:border-dark-700">
        <table class="w-full min-w-[980px] text-left text-sm">
          <thead class="text-xs uppercase text-gray-500"><tr><th class="px-3 py-3">{{ t('admin.connectionDiagnostics.time') }}</th><th class="px-3 py-3">{{ t('admin.connectionDiagnostics.result') }}</th><th class="px-3 py-3">{{ t('admin.connectionDiagnostics.route') }}</th><th class="px-3 py-3">{{ t('admin.connectionDiagnostics.model') }}</th><th class="px-3 py-3">{{ t('admin.connectionDiagnostics.latency') }}</th><th class="px-3 py-3">{{ t('admin.connectionDiagnostics.identity') }}</th><th class="px-3 py-3">{{ t('admin.connectionDiagnostics.detail') }}</th></tr></thead>
          <tbody>
            <template v-for="row in rows" :key="row.trace_id">
              <tr class="border-t border-gray-100 align-top dark:border-dark-800">
                <td class="whitespace-nowrap px-3 py-3">{{ formatTime(row.started_at) }}</td>
                <td class="px-3 py-3"><span class="font-medium" :class="row.state === 'completed' ? 'text-emerald-700 dark:text-emerald-400' : 'text-amber-700 dark:text-amber-400'">{{ row.state }}</span><span class="block text-xs text-gray-500">HTTP {{ row.detail.status || '—' }} · {{ row.source }}</span></td>
                <td class="max-w-64 px-3 py-3"><span class="font-mono">{{ row.method }} {{ row.path }}</span><span class="block truncate text-xs text-gray-500">{{ row.host }} · {{ row.http_version }}</span></td>
                <td class="max-w-52 truncate px-3 py-3">{{ row.model || '—' }}</td>
                <td class="whitespace-nowrap px-3 py-3">{{ row.detail.total_ms.toFixed(1) }} ms<span v-if="row.detail.terminal" class="block text-xs text-gray-500">{{ row.detail.terminal }}<span v-if="row.detail.outcome"> / {{ row.detail.outcome }}</span></span></td>
                <td class="px-3 py-3">{{ row.user_name || row.user_email || (row.user_id ? `#${row.user_id}` : '—') }}</td>
                <td class="px-3 py-3"><button type="button" class="btn btn-secondary px-2 py-1" :aria-expanded="expanded === row.trace_id" @click="expanded = expanded === row.trace_id ? '' : row.trace_id">{{ t('admin.connectionDiagnostics.toggleDetail') }}</button></td>
              </tr>
              <tr v-if="expanded === row.trace_id" class="border-t border-gray-100 bg-gray-50 dark:border-dark-800 dark:bg-dark-900">
                <td colspan="7" class="px-3 py-4"><dl class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4"><div><dt class="text-xs text-gray-500">Trace ID</dt><dd class="break-all font-mono">{{ row.trace_id }}</dd></div><div><dt class="text-xs text-gray-500">Request ID</dt><dd class="break-all font-mono">{{ row.request_id || '—' }}</dd></div><div><dt class="text-xs text-gray-500">{{ t('admin.connectionDiagnostics.network') }}</dt><dd>{{ row.network_error || row.upstream_status || '—' }}</dd></div><div><dt class="text-xs text-gray-500">{{ t('admin.connectionDiagnostics.flags') }}</dt><dd>{{ [row.detail.canceled && 'canceled', row.detail.client_disconnect && 'client_disconnect', row.detail.downstream_error && 'downstream_write_failed'].filter(Boolean).join(', ') || '—' }}</dd></div></dl><pre class="mt-4 max-h-72 overflow-auto border-t border-gray-200 pt-3 text-xs dark:border-dark-700">{{ JSON.stringify({ events: row.detail.events, spans: row.detail.spans, attempts: row.detail.attempts }, null, 2) }}</pre></td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>
      <footer class="flex items-center justify-between text-sm text-gray-500"><span>{{ t('admin.connectionDiagnostics.page', { page }) }}</span><div class="flex gap-2"><button class="btn btn-secondary" :disabled="page <= 1 || loading" @click="load(page - 1)">{{ t('common.previous') }}</button><button class="btn btn-secondary" :disabled="!hasMore || loading" @click="load(page + 1)">{{ t('common.next') }}</button></div></footer>
    </main>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import connectionDiagnosticsAPI, { type ConnectionTrace } from '@/api/admin/connectionDiagnostics'

const { t } = useI18n()
const states = ['edge_failed', 'edge_completed', 'downstream_write_failed', 'client_disconnected', 'failed', 'incomplete', 'completed', 'canceled', 'unknown']
const filters = reactive({ state: 'abnormal', source: 'all', request_id: '', model: '' })
const rows = ref<ConnectionTrace[]>([])
const enabled = ref(false)
const retentionDays = ref(30)
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const page = ref(1)
const hasMore = ref(false)
const expanded = ref('')
let controller: AbortController | undefined
let generation = 0

function formatTime(value: string) { return new Date(value).toLocaleString() }
async function load(targetPage = 1) {
  controller?.abort()
  controller = new AbortController()
  const current = ++generation
  loading.value = true
  error.value = ''
  try {
    const result = await connectionDiagnosticsAPI.list({ ...filters, page: targetPage, page_size: 20 })
    if (current !== generation) return
    rows.value = result.items
    page.value = result.page
    hasMore.value = result.has_more
    retentionDays.value = result.retention_days
    expanded.value = ''
  } catch (e) {
    if (current === generation && !(e instanceof Error && e.name === 'CanceledError')) error.value = e instanceof Error ? e.message : String(e)
  } finally {
    if (current === generation) loading.value = false
  }
}
function search() { void load(1) }
async function saveEnabled() {
  saving.value = true
  error.value = ''
  try { const result = await connectionDiagnosticsAPI.setEnabled(enabled.value); enabled.value = result.enabled }
  catch (e) { error.value = e instanceof Error ? e.message : String(e); enabled.value = !enabled.value }
  finally { saving.value = false }
}
onMounted(async () => {
  try { const result = await connectionDiagnosticsAPI.settings(); enabled.value = result.enabled; retentionDays.value = result.retention_days }
  catch (e) { error.value = e instanceof Error ? e.message : String(e) }
  await load()
})
onUnmounted(() => { generation++; controller?.abort() })
</script>
