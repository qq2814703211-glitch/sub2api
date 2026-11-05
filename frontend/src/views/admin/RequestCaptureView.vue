<template>
 <AppLayout>
  <div class="space-y-5">
   <div class="flex flex-wrap items-center justify-between gap-3">
    <div><h1 class="text-2xl font-semibold">{{ t('admin.requestCapture.title') }}</h1><p class="text-sm text-gray-500">{{ t('admin.requestCapture.description') }}</p></div>
    <button class="btn btn-secondary" :disabled="loading" @click="refresh">{{ t('admin.requestCapture.refresh') }}</button>
   </div>
   <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-red-700 dark:bg-red-950 dark:text-red-300">{{ error }}</p>
   <p v-if="!configLoaded" role="status">{{ t('common.loading') }}</p>
   <template v-else>
    <p class="text-sm text-gray-500">{{ t('admin.requestCapture.instanceScope') }} <code>{{ config.instance_id || t('admin.requestCapture.storageNotConfigured') }}</code></p>
    <p class="rounded-lg bg-amber-50 p-3 text-sm text-amber-900 dark:bg-amber-950 dark:text-amber-200">{{ t('admin.requestCapture.privacy') }}</p>
    <form class="card flex flex-wrap items-end gap-4 p-4" data-testid="capture-config" @submit.prevent="saveSettings">
     <label class="flex items-center gap-2"><input v-model="config.enabled" data-testid="capture-enabled" type="checkbox" :disabled="busy || !config.storage_ready">{{ t('admin.requestCapture.enabled') }}</label>
     <label>{{ t('admin.requestCapture.quota') }}<input v-model.number="config.quota_mib" class="input mt-1" type="number" min="1" required></label>
     <label>{{ t('admin.requestCapture.retentionDays') }}<input v-model.number="config.retention_days" class="input mt-1" type="number" min="1" max="30" required></label>
     <button class="btn btn-primary" data-testid="save-settings" :disabled="busy || !config.storage_ready">{{ t('common.save') }}</button>
     <p class="w-full text-xs text-gray-500">{{ t('admin.requestCapture.limitsHint') }}</p>
    </form>
    <div v-if="stats" class="flex flex-wrap gap-5 text-sm">
     <span>{{ t('admin.requestCapture.used') }}: {{ bytes(stats.used_bytes) }}</span><span>{{ t('admin.requestCapture.active') }}: {{ stats.active_requests }}</span>
     <span>{{ t('admin.requestCapture.buffer') }}: {{ bytes(stats.buffer_bytes) }}</span><span>{{ t('admin.requestCapture.admissionSkipped') }}: {{ stats.admission_skipped }}</span>
     <strong v-if="stats.storage_error" class="text-red-600">{{ t('admin.requestCapture.storageError') }}</strong>
    </div>
    <div class="flex justify-end"><button class="btn btn-primary" data-testid="open-create" :disabled="busy || !config.enabled || !config.storage_ready" @click="createOpen = true">{{ t('admin.requestCapture.create') }}</button></div>
    <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-700">
     <table class="w-full text-left text-sm"><thead class="bg-gray-50 dark:bg-dark-800"><tr><th class="p-3">{{ t('admin.requestCapture.target') }}</th><th>{{ t('admin.requestCapture.status') }}</th><th>{{ t('admin.requestCapture.records') }}</th><th>{{ t('admin.requestCapture.size') }}</th><th>{{ t('admin.requestCapture.actions') }}</th></tr></thead>
      <tbody><tr v-for="task in tasks" :key="task.id" class="border-t border-gray-100 dark:border-dark-700">
       <td class="p-3"><button class="text-primary-600 underline" :data-testid="'select-' + task.id" @click="selectTask(task)">{{ t('admin.requestCapture.' + task.target_type) }} #{{ task.target_id }} {{ task.target_name }}</button><p class="text-xs text-gray-500">{{ task.expires_at }}</p></td>
       <td>{{ task.status }}<p class="text-xs">{{ task.reason }}</p></td><td>{{ task.requests }}<p class="text-xs">{{ t('admin.requestCapture.incomplete') }}: {{ task.partial }} / {{ task.skipped }}</p></td><td>{{ bytes(task.bytes) }}</td>
       <td><div class="flex flex-wrap gap-2 p-2"><button v-if="task.status === 'running'" class="btn btn-secondary" :disabled="busy" @click="stop(task)">{{ t('admin.requestCapture.stop') }}</button><button v-else class="btn btn-secondary" :disabled="busy || !streamExport" @click="download(task.id)">{{ t('admin.requestCapture.export') }}</button><button class="btn btn-secondary text-red-600" :disabled="busy" @click="remove(task)">{{ t('admin.requestCapture.delete') }}</button></div></td>
      </tr><tr v-if="!tasks.length"><td colspan="5" class="p-5 text-center text-gray-500">{{ loading ? t('common.loading') : t('admin.requestCapture.empty') }}</td></tr></tbody>
     </table>
    </div>
    <div class="flex items-center justify-end gap-3"><button class="btn btn-secondary" :disabled="loading || taskPage <= 1" @click="changePage(-1)">{{ t('admin.requestCapture.previous') }}</button><span>{{ taskPage }}</span><button class="btn btn-secondary" :disabled="loading || !taskMore" @click="changePage(1)">{{ t('admin.requestCapture.next') }}</button></div>
    <p v-if="!streamExport" class="text-xs text-gray-500">{{ t('admin.requestCapture.exportBrowser') }}</p>
    <section v-if="selected" class="space-y-3">
     <h2 class="text-lg font-semibold">{{ t('admin.requestCapture.records') }} — {{ selected.target_name }}</h2>
     <p class="text-sm text-gray-500">{{ t('admin.requestCapture.errorsOnly') }}</p>
     <form class="flex gap-2" @submit.prevent="recordPage = 1; loadRecords()"><input v-model.trim="requestID" class="input max-w-lg" placeholder="Request / Client / Session / Thread / Upstream ID" aria-label="Request ID"><button class="btn btn-secondary" :disabled="recordsLoading">{{ t('admin.requestCapture.filter') }}</button></form>
     <p v-if="recordsLoading" role="status">{{ t('common.loading') }}</p><p v-else-if="!records.length">{{ t('admin.requestCapture.noRecords') }}</p>
     <button v-for="record in records" :key="record.id" class="card flex w-full flex-wrap justify-between gap-2 p-3 text-left" :data-testid="'record-' + record.id" @click="viewRecord(record)"><span>{{ record.request_id || record.id }} · {{ record.model }} · {{ record.protocol }} · {{ record.turn || 0 }}</span><span>HTTP {{ record.status }} · {{ record.error_code || t('admin.requestCapture.containsErrors') }} · {{ bytes(record.bytes) }}</span></button>
     <div class="flex justify-end gap-3"><button class="btn btn-secondary" :disabled="recordsLoading || recordPage <= 1" @click="recordPage--; loadRecords()">{{ t('admin.requestCapture.previous') }}</button><span>{{ recordPage }}</span><button class="btn btn-secondary" :disabled="recordsLoading || !recordMore" @click="recordPage++; loadRecords()">{{ t('admin.requestCapture.next') }}</button></div>
    </section>
    <section v-if="detail" class="space-y-3">
     <h2 class="text-lg font-semibold">{{ t('admin.requestCapture.detail') }} — {{ detail.request_id }}</h2>
     <p v-if="detail.partial" class="text-amber-600">{{ t('admin.requestCapture.partial') }}: {{ detail.reason }}</p>
     <details><summary>{{ t('admin.requestCapture.metadata') }}</summary><pre class="max-h-64 overflow-auto whitespace-pre-wrap break-all p-3 text-xs">{{ metadata }}</pre></details>
     <button class="btn btn-secondary" data-testid="export-record" :disabled="busy || !streamExport" @click="download(detail.task_id, detail.id)">{{ t('admin.requestCapture.export') }}</button>
     <div class="flex flex-wrap gap-2"><button v-for="value in stages" :key="value" class="btn btn-secondary" :aria-pressed="stage === value" @click="selectStage(value)">{{ t('admin.requestCapture.' + value) }}</button></div>
     <select v-if="parts.length" class="input" :value="part?.name" :aria-label="t('admin.requestCapture.segment')" @change="selectPart(($event.target as HTMLSelectElement).value)"><option v-for="value in parts" :key="value.name" :value="value.name">{{ value.name }} / {{ t('admin.requestCapture.attempt') }} {{ value.attempt }} / {{ t('admin.requestCapture.turn') }} {{ value.turn }}</option></select>
     <p v-else>{{ t('admin.requestCapture.noPart') }}</p><p v-if="part?.omitted">{{ t('admin.requestCapture.omitted') }}: {{ part.omitted }}</p>
     <div v-if="part" class="space-y-2"><button class="btn btn-primary" data-testid="read-content" :disabled="contentLoading" @click="readContent(0)">{{ t('admin.requestCapture.readContent') }}</button><button v-if="contentMore" class="btn btn-secondary" :disabled="contentLoading" @click="readContent(nextOffset)">{{ t('admin.requestCapture.nextSegment') }}</button><pre v-if="content" data-testid="capture-content" class="max-h-96 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-gray-50 p-4 text-xs dark:bg-dark-800">{{ content }}</pre></div>
    </section>
   </template>
  </div>
  <BaseDialog :show="createOpen" :title="t('admin.requestCapture.create')" @close="createOpen = false">
   <form id="capture-create" class="space-y-4" @submit.prevent="create">
    <label class="block">{{ t('admin.requestCapture.targetType') }}<select v-model="targetType" class="input"><option v-for="kind in kinds" :key="kind" :value="kind">{{ t('admin.requestCapture.' + kind) }}</option></select></label>
    <label class="block">{{ t('admin.requestCapture.target') }} ID<input v-model.number="targetID" data-testid="target-id" class="input" type="number" min="1" required></label>
    <p class="text-xs text-gray-500">{{ t('admin.requestCapture.targetHint') }}</p>
    <label class="block">{{ t('admin.requestCapture.duration') }}<input v-model.number="duration" class="input" type="number" min="1" max="1440" required></label>
    <label class="flex gap-2"><input v-model="saveMedia" type="checkbox">{{ t('admin.requestCapture.saveMedia') }}</label>
    <button class="btn btn-primary" data-testid="start-task" :disabled="busy || !validTask">{{ t('admin.requestCapture.start') }}</button>
   </form>
  </BaseDialog>
  <TotpStepUpDialog :controller="stepUp" />
 </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import * as api from '@/api/admin/requestCaptures'
import type { CaptureTask, CaptureRecord, CapturePart, CaptureStats, CaptureTarget } from '@/api/admin/requestCaptures'
const { t } = useI18n()
const stepUp = useStepUp()
const config = ref<api.CaptureConfig>({ enabled: false, quota_mib: 1024, retention_days: 7 })
const configLoaded = ref(false), loading = ref(false), busy = ref(false), error = ref('')
const tasks = ref<CaptureTask[]>([]), stats = ref<CaptureStats>(), taskPage = ref(1), taskMore = ref(false)
const selected = ref<CaptureTask>(), records = ref<CaptureRecord[]>([]), recordPage = ref(1), recordMore = ref(false), recordsLoading = ref(false), requestID = ref('')
const detail = ref<CaptureRecord>(), part = ref<CapturePart>(), content = ref(''), contentMore = ref(false), nextOffset = ref(0), contentLoading = ref(false)
const stages = ['client_request', 'upstream_request', 'upstream_response', 'client_response'], stage = ref(stages[0])
const kinds: CaptureTarget[] = ['user', 'group', 'account']
const targetType = ref<CaptureTarget>('user'), targetID = ref(0), duration = ref(10), saveMedia = ref(false), createOpen = ref(false)
const streamExport = api.canStreamExport()
const validTask = computed(() => Number.isInteger(targetID.value) && targetID.value > 0 && Number.isInteger(duration.value) && duration.value >= 1 && duration.value <= 1440)
const parts = computed(() => (detail.value?.parts || []).filter(item => item.stage === stage.value))
const metadata = computed(() => JSON.stringify(detail.value, null, 2))
let disposed = false, listVersion = 0, recordVersion = 0, detailVersion = 0, contentVersion = 0
let listAbort: AbortController | undefined, recordAbort: AbortController | undefined, detailAbort: AbortController | undefined, contentAbort: AbortController | undefined
const exportAbort = new AbortController()
let poll: ReturnType<typeof setInterval> | undefined
function bytes(value: number) { return (value / 1048576).toFixed(2) + ' MiB' }
function report(err: unknown) {
 const e = err as { name?: string; code?: string; message?: string }
 if (!disposed && !isStepUpCancelled(err) && e.name !== 'AbortError' && e.code !== 'ERR_CANCELED') error.value = e.message || t('admin.requestCapture.failed')
}
async function refresh() {
 const version = ++listVersion; listAbort?.abort(); const controller = listAbort = new AbortController(); loading.value = true
 try { const result = await api.listTasks(taskPage.value, controller.signal); if (disposed || version !== listVersion) return; tasks.value = result.items; stats.value = result.stats; taskMore.value = result.has_more; if (selected.value) selected.value = tasks.value.find(task => task.id === selected.value?.id) || selected.value }
 catch (err) { if (version === listVersion) report(err) } finally { if (version === listVersion) loading.value = false }
}
async function action(fn: () => Promise<void>) { if (busy.value || disposed) return; busy.value = true; error.value = ''; try { await fn(); if (!disposed) await refresh() } catch (err) { report(err) } finally { busy.value = false } }
async function saveSettings() { await action(async () => { const value = await api.saveConfig(config.value); if (!disposed) config.value = value }) }
async function create() { if (!validTask.value) return; await action(async () => { const task = await api.createTask({ target_type: targetType.value, target_id: targetID.value, duration_minutes: duration.value, save_media: saveMedia.value }); if (disposed) return; createOpen.value = false; taskPage.value = 1; selectTask(task) }) }
async function stop(task: CaptureTask) { await action(() => api.stopTask(task.id)) }
async function remove(task: CaptureTask) { if (!window.confirm(t('admin.requestCapture.deleteConfirm'))) return; await action(async () => { await api.deleteTask(task.id); if (selected.value?.id === task.id) { invalidateDetail(); selected.value = undefined; records.value = [] } }) }
function changePage(delta: number) { taskPage.value += delta; void refresh() }
function invalidateDetail() { detailVersion++; contentVersion++; detailAbort?.abort(); contentAbort?.abort(); stepUp.onCancel(); detail.value = undefined; part.value = undefined; content.value = ''; contentMore.value = false; contentLoading.value = false }
function selectTask(task: CaptureTask) { invalidateDetail(); selected.value = task; recordPage.value = 1; records.value = []; void loadRecords() }
async function loadRecords() {
 if (!selected.value) return; const task = selected.value.id, version = ++recordVersion; recordAbort?.abort(); const controller = recordAbort = new AbortController(); recordsLoading.value = true
 try { const result = await api.listRecords(task, recordPage.value, requestID.value, true, controller.signal); if (disposed || version !== recordVersion || selected.value?.id !== task) return; records.value = result.items; recordMore.value = result.has_more } catch (err) { if (version === recordVersion) report(err) } finally { if (version === recordVersion) recordsLoading.value = false }
}
async function viewRecord(record: CaptureRecord) {
 invalidateDetail(); const version = ++detailVersion, controller = detailAbort = new AbortController()
 try { const result = await api.getRecord(record.task_id, record.id, controller.signal); if (disposed || version !== detailVersion) return; detail.value = result; selectStage(stages[0]) } catch (err) { if (version === detailVersion) report(err) }
}
function clearContent() { contentVersion++; contentAbort?.abort(); stepUp.onCancel(); content.value = ''; contentMore.value = false; contentLoading.value = false }
function selectStage(value: string) { clearContent(); stage.value = value; part.value = parts.value[0] }
function selectPart(name: string) { clearContent(); part.value = parts.value.find(item => item.name === name) }
async function readContent(offset: number) {
 if (!detail.value || !part.value || contentLoading.value) return
 const version = ++contentVersion, task = detail.value.task_id, record = detail.value.id, name = part.value.name; contentAbort?.abort(); const controller = contentAbort = new AbortController(); contentLoading.value = true
 try { const result = await stepUp.run(() => api.getContent(task, record, name, offset, controller.signal)); if (disposed || version !== contentVersion) return; content.value = result.text; contentMore.value = result.has_more; nextOffset.value = result.next_offset } catch (err) { if (version === contentVersion) report(err) } finally { if (version === contentVersion) contentLoading.value = false }
}
async function download(task: string, record?: string) { await action(() => api.exportCapture(task, record, () => stepUp.run(api.authorizeSensitiveRead), exportAbort.signal)) }
onMounted(async () => {
 try { const value = await api.getConfig(); if (disposed) return; config.value = value; configLoaded.value = true; if (value.storage_ready) await refresh() } catch (err) { report(err) }
 if (!disposed) poll = setInterval(() => { if (!loading.value && config.value.enabled && config.value.storage_ready) { void refresh(); if (selected.value) void loadRecords() } }, 3000)
})
onUnmounted(() => { disposed = true; listVersion++; recordVersion++; invalidateDetail(); listAbort?.abort(); recordAbort?.abort(); exportAbort.abort(); clearInterval(poll) })
</script>
