<template>
  <AppLayout>
    <TablePageLayout>
      <!-- Filters -->
      <template #filters>
        <div class="card p-4 sm:p-6">
          <div class="flex flex-wrap items-end justify-between gap-4">
            <div class="flex flex-1 flex-wrap items-end gap-4">
              <div class="w-full sm:w-auto sm:min-w-[240px]">
                <label class="input-label">{{ t('admin.modelPricingSync.filters.model') }}</label>
                <div class="relative">
                  <Icon
                    name="search"
                    size="md"
                    class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-500"
                  />
                  <input
                    v-model.trim="filters.model"
                    type="text"
                    class="input pl-10"
                    :placeholder="t('admin.modelPricingSync.filters.modelPlaceholder')"
                    @keyup.enter="search"
                  />
                </div>
              </div>

              <div class="w-full sm:w-auto sm:min-w-[200px]">
                <label class="input-label">{{ t('admin.modelPricingSync.filters.field') }}</label>
                <Select v-model="filters.field" :options="fieldOptions" @change="search" />
              </div>

              <div class="w-full sm:w-auto sm:min-w-[180px]">
                <label class="input-label">{{ t('admin.modelPricingSync.filters.source') }}</label>
                <Select v-model="filters.source" :options="sourceOptions" @change="search" />
              </div>
            </div>

            <div class="flex w-full flex-wrap items-center justify-end gap-3 sm:w-auto">
              <button type="button" class="btn btn-primary" :disabled="loading" @click="search">
                {{ t('common.search') }}
              </button>
              <button type="button" class="btn btn-secondary" :disabled="loading" @click="resetFilters">
                {{ t('common.reset') }}
              </button>
            </div>
          </div>

          <!-- Status strip -->
          <div
            class="mt-4 flex flex-wrap items-center gap-x-5 gap-y-2 rounded-xl bg-gray-50 px-4 py-3 text-xs text-gray-600 dark:bg-dark-900 dark:text-gray-300"
          >
            <span class="inline-flex items-center gap-1.5 font-medium">
              <span
                class="h-1.5 w-1.5 rounded-full"
                :class="status?.enabled ? 'bg-emerald-500' : 'bg-gray-400 dark:bg-dark-600'"
              ></span>
              {{ status?.enabled ? t('admin.modelPricingSync.status.enabled') : t('admin.modelPricingSync.status.disabled') }}
            </span>
            <span>
              {{ t('admin.modelPricingSync.status.lastRun') }}:
              <span class="font-mono">{{ status?.last_run ? formatTime(status.last_run) : t('admin.modelPricingSync.status.never') }}</span>
            </span>
            <span v-if="status?.interval_seconds">
              {{ t('admin.modelPricingSync.status.interval') }}: {{ formatInterval(status.interval_seconds) }}
            </span>
            <span v-if="typeof status?.last_applied === 'number' && status.last_applied > 0">
              {{ t('admin.modelPricingSync.status.lastApplied') }}:
              {{ t('admin.modelPricingSync.status.changes', { count: status.last_applied }) }}
            </span>
            <span v-if="status?.override_file" class="break-all font-mono text-gray-500 dark:text-gray-400">
              {{ t('admin.modelPricingSync.status.overrideFile') }}: {{ status.override_file }}
            </span>
            <span v-if="status?.last_error" class="break-all font-mono text-red-600 dark:text-red-400">
              {{ t('admin.modelPricingSync.status.lastError') }}: {{ status.last_error }}
            </span>
          </div>
          <p v-if="status && !status.enabled" class="mt-2 text-xs text-amber-600 dark:text-amber-400">
            {{ t('admin.modelPricingSync.disabledHint') }}
          </p>
        </div>
      </template>

      <!-- Table -->
      <template #table>
        <DataTable :columns="columns" :data="rows" :loading="loading" row-key="id">
          <template #cell-applied_at="{ value }">
            <span class="whitespace-nowrap text-gray-600 dark:text-gray-300">{{ formatTime(value) }}</span>
          </template>

          <template #cell-model="{ value }">
            <span class="break-all font-mono text-sm text-gray-800 dark:text-gray-200">{{ value }}</span>
          </template>

          <template #cell-field="{ value }">
            <span class="whitespace-nowrap text-gray-600 dark:text-gray-300">
              {{ fieldLabel(value) }}
            </span>
            <div class="mt-0.5 break-all font-mono text-xs text-gray-500">{{ value }}</div>
          </template>

          <template #cell-change="{ row }">
            <span class="inline-flex items-center gap-2 whitespace-nowrap font-mono text-sm">
              <span class="text-gray-500 dark:text-gray-400">{{ formatPrice(row.old_value) }}</span>
              <Icon name="arrowRight" size="sm" class="text-gray-400 dark:text-dark-500" />
              <span class="font-semibold text-gray-900 dark:text-white">{{ formatPrice(row.new_value) }}</span>
            </span>
          </template>

          <template #cell-reason="{ value }">
            <span class="text-gray-600 dark:text-gray-300">{{ value || '—' }}</span>
          </template>

          <template #cell-source="{ value }">
            <span
              class="inline-flex items-center rounded-full bg-primary-50 px-2.5 py-0.5 text-xs font-semibold text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
            >
              {{ value || '—' }}
            </span>
          </template>

          <template #empty>
            <div class="flex flex-col items-center py-8">
              <Icon name="chart" size="xl" class="mb-4 h-12 w-12 text-gray-300 dark:text-dark-600" />
              <p class="text-sm font-medium text-gray-500 dark:text-gray-400">
                {{ t('admin.modelPricingSync.empty') }}
              </p>
            </div>
          </template>
        </DataTable>
      </template>

      <!-- Pagination -->
      <template #pagination>
        <Pagination
          v-if="total > 0"
          :total="total"
          :page="page"
          :page-size="pageSize"
          @update:page="onPageChange"
          @update:pageSize="onPageSizeChange"
        />
      </template>
    </TablePageLayout>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI, type ModelPricingSyncHistoryRow, type ModelPricingSyncStatus } from '@/api/admin'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import type { Column } from '@/components/common/types'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()

const loading = ref(false)
const rows = ref<ModelPricingSyncHistoryRow[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const status = ref<ModelPricingSyncStatus | null>(null)

const filters = reactive({
  model: '',
  field: '',
  source: ''
})

// 与后端 modelPricingSyncFields 保持同一组字段名与顺序。
const SYNC_FIELDS = [
  'input_cost_per_token',
  'output_cost_per_token',
  'cache_read_input_token_cost',
  'cache_creation_input_token_cost'
] as const

const columns = computed<Column[]>(() => [
  { key: 'applied_at', label: t('admin.modelPricingSync.columns.appliedAt') },
  { key: 'model', label: t('admin.modelPricingSync.columns.model') },
  { key: 'field', label: t('admin.modelPricingSync.columns.field') },
  { key: 'change', label: t('admin.modelPricingSync.columns.change') },
  { key: 'reason', label: t('admin.modelPricingSync.columns.reason') },
  { key: 'source', label: t('admin.modelPricingSync.columns.source') }
])

const fieldOptions = computed(() => [
  { value: '', label: t('admin.modelPricingSync.filters.allFields') },
  ...SYNC_FIELDS.map((field) => ({ value: field, label: fieldLabel(field) }))
])

const sourceOptions = computed(() => [
  { value: '', label: t('admin.modelPricingSync.filters.allSources') },
  { value: 'models.dev', label: 'models.dev' }
])

function fieldLabel(field: string): string {
  const key = `admin.modelPricingSync.fields.${field}`
  const label = t(key)
  // vue-i18n 找不到键时回退成键名本身，此时直接展示原始字段名更有用。
  return label === key ? field : label
}

// 覆盖层按 USD / token 存储，页面按 USD / 1M tokens 展示（与官方价目表一致）。
function formatPrice(value: number): string {
  if (!value) return '0'
  const perMillion = value * 1e6
  const digits = perMillion < 1 ? 4 : perMillion < 100 ? 3 : 2
  return `$${perMillion.toFixed(digits)}`
}

function formatTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

function formatInterval(seconds: number): string {
  if (seconds % 3600 === 0) return `${seconds / 3600}h`
  if (seconds % 60 === 0) return `${seconds / 60}m`
  return `${seconds}s`
}

async function load() {
  loading.value = true
  try {
    const [history, syncStatus] = await Promise.all([
      adminAPI.modelPricingSync.listHistory({
        page: page.value,
        page_size: pageSize.value,
        model: filters.model || undefined,
        field: filters.field || undefined,
        source: filters.source || undefined
      }),
      adminAPI.modelPricingSync.getStatus()
    ])
    rows.value = history.items ?? []
    total.value = history.total ?? 0
    status.value = syncStatus
  } finally {
    loading.value = false
  }
}

function search() {
  page.value = 1
  void load()
}

function resetFilters() {
  filters.model = ''
  filters.field = ''
  filters.source = ''
  page.value = 1
  void load()
}

function onPageChange(next: number) {
  page.value = next
  void load()
}

function onPageSizeChange(next: number) {
  pageSize.value = next
  page.value = 1
  void load()
}

onMounted(load)
</script>
