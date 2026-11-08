<template>
  <AppLayout>
    <div class="w-full min-w-0 space-y-6 pb-8">
      <!-- 计价口径配置：usd < cny-official < manual，manual 是这张卡片唯一可写的部分。 -->
      <section class="card p-4 sm:p-6" data-testid="pricing-source-card">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div class="min-w-0">
            <h2 class="flex items-center gap-2 text-base font-semibold text-gray-900 dark:text-white">
              <span
                class="inline-flex h-7 w-7 items-center justify-center rounded-lg bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300"
              >
                <Icon name="cog" size="sm" />
              </span>
              {{ t('admin.modelPricingSync.pricingSource.title') }}
            </h2>
            <p class="mt-1.5 text-xs text-gray-500 dark:text-dark-400">
              {{ t('admin.modelPricingSync.pricingSource.description') }}
            </p>
          </div>
          <span
            v-if="dirty"
            class="inline-flex items-center rounded-full bg-amber-50 px-2.5 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-300"
            data-testid="pricing-source-dirty"
          >
            {{ t('admin.modelPricingSync.pricingSource.unsaved') }}
          </span>
        </div>

        <!-- 优先级条：一眼看出 manual 最高。 -->
        <div class="mt-4" data-testid="pricing-priority-strip">
          <div class="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-gray-400 dark:text-dark-500">
            {{ t('admin.modelPricingSync.pricingSource.priorityLabel') }}
          </div>
          <div class="flex flex-col gap-2 sm:flex-row sm:items-stretch">
            <div
              v-for="(tier, index) in priorityTiers"
              :key="tier.key"
              class="flex min-w-0 flex-1 flex-col justify-center rounded-xl border px-3 py-2"
              :class="tier.highest
                ? 'border-primary-300 bg-primary-50 dark:border-primary-700/60 dark:bg-primary-900/20'
                : 'border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-900'"
              :data-testid="`pricing-tier-${tier.key}`"
            >
              <div class="flex items-center gap-1.5">
                <span
                  class="text-sm font-semibold"
                  :class="tier.highest ? 'text-primary-700 dark:text-primary-300' : 'text-gray-700 dark:text-gray-200'"
                >
                  {{ index + 1 }}. {{ tier.label }}
                </span>
                <span
                  v-if="tier.highest"
                  class="inline-flex items-center rounded-full bg-primary-600 px-1.5 py-0.5 text-[10px] font-bold text-white"
                >
                  {{ t('admin.modelPricingSync.pricingSource.highestBadge') }}
                </span>
              </div>
              <span class="mt-0.5 truncate text-[11px] text-gray-500 dark:text-dark-400">{{ tier.note }}</span>
            </div>
          </div>
        </div>

        <!-- 加载 / 错误 -->
        <div
          v-if="configLoading"
          class="mt-5 flex min-h-[120px] items-center justify-center rounded-xl border border-dashed border-gray-200 text-sm text-gray-500 dark:border-dark-700 dark:text-dark-400"
          data-testid="pricing-source-loading"
        >
          {{ t('admin.modelPricingSync.pricingSource.loading') }}
        </div>

        <div
          v-else-if="configError"
          class="mt-5 rounded-xl border border-red-200 bg-red-50 px-4 py-3 dark:border-red-900/50 dark:bg-red-950/30"
          data-testid="pricing-source-load-error"
          role="alert"
        >
          <p class="text-sm font-medium text-red-700 dark:text-red-300">
            {{ routeMissing
              ? t('admin.modelPricingSync.pricingSource.endpointMissing', { path: OPEN_API_PATH })
              : t('admin.modelPricingSync.pricingSource.loadFailed') }}
          </p>
          <p class="mt-1 break-all font-mono text-xs text-red-600 dark:text-red-400">{{ configError }}</p>
          <p v-if="routeMissing" class="mt-2 text-xs text-gray-600 dark:text-gray-300">
            {{ t('admin.modelPricingSync.pricingSource.endpointMissingHint') }}
          </p>
          <button type="button" class="btn btn-secondary btn-sm mt-3" @click="loadConfig">
            {{ t('admin.modelPricingSync.pricingSource.retry') }}
          </button>
        </div>

        <template v-else-if="config">
          <!-- 三档状态区 -->
          <div class="mt-4 divide-y divide-gray-100 rounded-xl border border-gray-200 dark:divide-dark-700 dark:border-dark-700">
            <!-- USD 同步：只读 -->
            <div class="flex flex-wrap items-center justify-between gap-3 px-4 py-3">
              <div class="min-w-0">
                <strong class="text-sm font-medium text-gray-900 dark:text-white">
                  {{ t('admin.modelPricingSync.pricingSource.usdTitle') }}
                </strong>
                <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
                  {{ t('admin.modelPricingSync.pricingSource.usdHint') }}
                </p>
              </div>
              <span
                class="inline-flex items-center gap-1.5 whitespace-nowrap rounded-full px-2.5 py-0.5 text-xs font-semibold"
                :class="usdEnabled
                  ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
                  : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-300'"
                data-testid="pricing-usd-state"
              >
                <span class="h-1.5 w-1.5 rounded-full" :class="usdEnabled ? 'bg-emerald-500' : 'bg-gray-400'"></span>
                {{ usdEnabled
                  ? t('admin.modelPricingSync.pricingSource.usdEnabled')
                  : t('admin.modelPricingSync.pricingSource.usdDisabled') }}
              </span>
            </div>

            <!-- CNY 官方价：开关 + 数据源（只读） -->
            <div class="px-4 py-3">
              <div class="flex flex-wrap items-center justify-between gap-3">
                <div class="min-w-0">
                  <strong class="text-sm font-medium text-gray-900 dark:text-white">
                    {{ t('admin.modelPricingSync.pricingSource.cnyTitle') }}
                  </strong>
                  <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
                    {{ t('admin.modelPricingSync.pricingSource.cnyHint') }}
                  </p>
                </div>
                <Toggle v-model="config.enabled" data-testid="pricing-cny-toggle" />
              </div>
              <div class="mt-2 flex flex-wrap items-baseline gap-2">
                <span class="text-xs text-gray-500 dark:text-dark-400">
                  {{ t('admin.modelPricingSync.pricingSource.sourceUrl') }}
                </span>
                <span class="break-all font-mono text-xs text-gray-700 dark:text-gray-300" data-testid="pricing-source-url">
                  {{ config.source_url }}
                </span>
              </div>
            </div>
          </div>

          <!-- 手动覆盖编辑器 -->
          <div class="mt-4">
            <div class="flex flex-wrap items-center justify-between gap-3">
              <div class="min-w-0">
                <strong class="text-sm font-medium text-gray-900 dark:text-white">
                  {{ t('admin.modelPricingSync.pricingSource.manualTitle') }}
                </strong>
                <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
                  {{ t('admin.modelPricingSync.pricingSource.manualHint') }}
                </p>
              </div>
              <button
                type="button"
                class="btn btn-secondary btn-sm"
                data-testid="pricing-add-row"
                @click="addRow"
              >
                <Icon name="plus" size="sm" />
                {{ t('admin.modelPricingSync.pricingSource.addRow') }}
              </button>
            </div>

            <div
              v-if="overrideRows.length === 0"
              class="mt-3 flex flex-col items-center rounded-xl border border-dashed border-gray-200 px-4 py-8 text-center dark:border-dark-700"
              data-testid="pricing-manual-empty"
            >
              <Icon name="lightbulb" size="lg" class="mb-2 h-8 w-8 text-gray-300 dark:text-dark-600" />
              <p class="text-sm text-gray-500 dark:text-dark-400">
                {{ t('admin.modelPricingSync.pricingSource.manualEmpty') }}
              </p>
            </div>

            <div v-else class="mt-2 divide-y divide-gray-100 dark:divide-dark-700">
              <div
                v-for="row in overrideRows"
                :key="row.id"
                class="grid gap-3 py-3 sm:grid-cols-12"
                :data-testid="`pricing-row-${row.id}`"
              >
                <label class="block sm:col-span-4">
                  <span class="text-[11px] font-medium text-gray-600 dark:text-dark-300">
                    {{ t('admin.modelPricingSync.pricingSource.colModel') }}
                  </span>
                  <input
                    v-model.trim="row.model"
                    type="text"
                    class="input mt-1 w-full font-mono text-sm"
                    :class="{ 'input-error': rowErrors[row.id]?.model }"
                    :placeholder="t('admin.modelPricingSync.pricingSource.colModelPlaceholder')"
                    :data-testid="`pricing-row-model-${row.id}`"
                  />
                  <span
                    v-if="rowErrors[row.id]?.model"
                    class="input-error-text mt-1 block"
                    :data-testid="`pricing-row-model-error-${row.id}`"
                  >
                    {{ rowErrors[row.id]?.model }}
                  </span>
                </label>

                <label
                  v-for="field in PRICE_FIELDS"
                  :key="field"
                  class="block sm:col-span-2"
                >
                  <span class="text-[11px] font-medium text-gray-600 dark:text-dark-300">
                    {{ t(`admin.modelPricingSync.pricingSource.col.${field}`) }}
                  </span>
                  <input
                    v-model.trim="row[field]"
                    type="text"
                    inputmode="decimal"
                    class="input mt-1 w-full font-mono text-sm"
                    :class="{ 'input-error': rowErrors[row.id]?.[field] }"
                    :data-testid="`pricing-row-${field}-${row.id}`"
                  />
                  <span
                    v-if="rowErrors[row.id]?.[field]"
                    class="input-error-text mt-1 block"
                    :data-testid="`pricing-row-${field}-error-${row.id}`"
                  >
                    {{ rowErrors[row.id]?.[field] }}
                  </span>
                </label>

                <div class="flex items-end justify-between gap-2 sm:col-span-2">
                  <span
                    class="text-[11px] leading-tight text-primary-600 dark:text-primary-300"
                    data-testid="pricing-row-priority-note"
                  >
                    {{ t('admin.modelPricingSync.pricingSource.manualPriorityNote') }}
                  </span>
                  <button
                    type="button"
                    class="btn btn-ghost btn-sm text-red-600 hover:text-red-700 dark:text-red-400"
                    :title="t('admin.modelPricingSync.pricingSource.removeRow')"
                    :aria-label="t('admin.modelPricingSync.pricingSource.removeRow')"
                    :data-testid="`pricing-remove-row-${row.id}`"
                    @click="removeRow(row.id)"
                  >
                    <Icon name="trash" size="sm" />
                  </button>
                </div>
              </div>
            </div>
          </div>

          <!-- 国模厂商清单（只读折叠） -->
          <details class="mt-4 rounded-xl border border-gray-200 dark:border-dark-700">
            <summary class="cursor-pointer px-4 py-3 text-sm font-medium text-gray-800 dark:text-gray-200">
              {{ t('admin.modelPricingSync.pricingSource.vendorsTitle') }}
              <span class="ml-1 text-xs font-normal text-gray-500 dark:text-dark-400">
                {{ t('admin.modelPricingSync.pricingSource.vendorsHint', { count: cnVendors.length }) }}
              </span>
            </summary>
            <div class="flex flex-wrap gap-1.5 px-4 pb-4" data-testid="pricing-cn-vendors">
              <code
                v-for="vendor in cnVendors"
                :key="vendor"
                class="rounded-md bg-gray-100 px-1.5 py-0.5 font-mono text-[11px] text-gray-700 dark:bg-dark-700 dark:text-gray-300"
              >
                {{ vendor }}
              </code>
            </div>
          </details>

          <!-- 保存 -->
          <div class="mt-4 flex flex-wrap items-center justify-end gap-3">
            <p
              v-if="saveError"
              class="mr-auto break-all text-xs text-red-600 dark:text-red-400"
              data-testid="pricing-save-error"
              role="alert"
            >
              {{ saveError }}
            </p>
            <button
              type="button"
              class="btn btn-primary"
              data-testid="pricing-save"
              :disabled="saving"
              @click="save"
            >
              <Icon name="check" size="sm" />
              {{ saving ? t('admin.modelPricingSync.pricingSource.saving') : t('admin.modelPricingSync.pricingSource.save') }}
            </button>
          </div>
        </template>
      </section>

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
          <DataTable :columns="columns" :data="historyRows" :loading="loading" row-key="id">
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
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  adminAPI,
  type ModelPricingSyncHistoryRow,
  type ModelPricingSyncStatus,
  type PricingCnyOfficialConfig
} from '@/api/admin'
import { DEFAULT_CN_VENDORS, DEFAULT_CNY_SOURCE_URL } from '@/api/admin/modelPricingSync'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import type { Column } from '@/components/common/types'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const historyRows = ref<ModelPricingSyncHistoryRow[]>([])
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
    historyRows.value = history.items ?? []
    total.value = history.total ?? 0
    status.value = syncStatus
  } finally {
    loading.value = false
  }
}

/** 历史列表数据源；卡片里的手动覆盖行是另一份状态（overrideRows）。 */

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

// ==================== 计价口径配置（pricing.cny_official） ====================

/** 单价列固定三个，顺序即界面顺序。 */
const PRICE_FIELDS = ['input', 'output', 'cache_read'] as const
type PriceField = (typeof PRICE_FIELDS)[number]

/**
 * 编辑行用字符串存价格：`type="number"` 在清空时给空串，直接绑 number 会
 * 把 NaN 写进模型，保存时无从分辨「空」与「0」。
 *
 * `id` 只用于 v-for 的稳定 key 与错误定位 —— 模型键可被用户随便改，不能当 key。
 */
interface OverrideRow {
  id: number
  model: string
  input: string
  output: string
  cache_read: string
}

type RowErrors = { model?: string } & Partial<Record<PriceField, string>>

const configLoading = ref(true)
const configError = ref('')
/** 专用配置端点还没在后端注册（404/405）。界面要如实说明，不能假装是加载失败。 */
const routeMissing = ref(false)
const config = ref<PricingCnyOfficialConfig | null>(null)
const overrideRows = ref<OverrideRow[]>([])
const rowErrors = ref<Record<number, RowErrors>>({})
const saving = ref(false)
const saveError = ref('')
/** 保存成功后的基线快照，用来判断是否有未保存改动。 */
const savedSnapshot = ref('')

let nextRowId = 1

const priorityTiers = computed(() => [
  {
    key: 'usd',
    label: t('admin.modelPricingSync.pricingSource.tierUsd'),
    note: t('admin.modelPricingSync.pricingSource.tierUsdNote'),
    highest: false
  },
  {
    key: 'cny-official',
    label: t('admin.modelPricingSync.pricingSource.tierCny'),
    note: t('admin.modelPricingSync.pricingSource.tierCnyNote'),
    highest: false
  },
  {
    key: 'manual',
    label: t('admin.modelPricingSync.pricingSource.tierManual'),
    note: t('admin.modelPricingSync.pricingSource.tierManualNote'),
    highest: true
  }
])

/** USD 同步是后端既有任务，本卡片只读展示它是否在跑。 */
const usdEnabled = computed(() => status.value?.enabled === true)

/**
 * 国模厂商清单，优先级：
 *   1. 配置接口返回的 `cn_vendors`（后端写入时回传）
 *   2. `status.cny_official.national_vendor` —— 后端白名单的真实来源
 *   3. 内置清单（后端老版本没有该字段时的兜底，仅用于展示）
 *
 * 判定逻辑始终在后端，这里只是把「哪些厂商走 CNY 官方价」摆给运营看。
 */
const cnVendors = computed<string[]>(() => {
  const fromConfig = config.value?.cn_vendors
  if (fromConfig && fromConfig.length > 0) return fromConfig
  const fromStatus = status.value?.cny_official?.national_vendor
  if (fromStatus && fromStatus.length > 0) return fromStatus
  return [...DEFAULT_CN_VENDORS]
})

function makeRow(model = '', price?: { input: number; output: number; cache_read: number }): OverrideRow {
  return {
    id: nextRowId++,
    model,
    input: price ? String(price.input) : '',
    output: price ? String(price.output) : '',
    cache_read: price ? String(price.cache_read) : ''
  }
}

function applyConfig(next: PricingCnyOfficialConfig) {
  config.value = next
  overrideRows.value = Object.entries(next.manual_overrides)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([model, price]) => makeRow(model, price))
  rowErrors.value = {}
  saveError.value = ''
  savedSnapshot.value = snapshot()
}

function addRow() {
  const row = makeRow()
  overrideRows.value = [...overrideRows.value, row]
  // 新行默认聚焦到模型键，省一次点击。
  void nextTickFocus(`[data-testid="pricing-row-model-${row.id}"]`)
}

function nextTickFocus(selector: string) {
  return Promise.resolve().then(() => {
    const el = document.querySelector<HTMLInputElement>(selector)
    el?.focus()
  })
}

function removeRow(id: number) {
  overrideRows.value = overrideRows.value.filter((row) => row.id !== id)
  const next = { ...rowErrors.value }
  delete next[id]
  rowErrors.value = next
}

/** 价格必须是有限非负数；空值单列一条，免得空输入被当成 0 静默落库。 */
function priceError(raw: string): string | undefined {
  const text = raw.trim()
  if (text === '') return t('admin.modelPricingSync.pricingSource.errPriceRequired')
  const parsed = Number(text)
  if (!Number.isFinite(parsed)) return t('admin.modelPricingSync.pricingSource.errPriceInvalid')
  if (parsed < 0) return t('admin.modelPricingSync.pricingSource.errPriceInvalid')
  return undefined
}

/** 校验全部行，返回 id → 错误。纯函数，不改组件状态。 */
function collectErrors(): Record<number, RowErrors> {
  const errors: Record<number, RowErrors> = {}
  const seen = new Map<string, number>()

  for (const row of overrideRows.value) {
    const rowError: RowErrors = {}
    const model = row.model.trim()

    if (model === '') {
      rowError.model = t('admin.modelPricingSync.pricingSource.errModelRequired')
    } else if (!model.includes('/')) {
      // 后端按 `vendor/model` 拆分，缺斜杠会整条被忽略 —— 必须在这里挡住。
      rowError.model = t('admin.modelPricingSync.pricingSource.errModelFormat')
    } else if (seen.has(model)) {
      rowError.model = t('admin.modelPricingSync.pricingSource.errModelDuplicate')
    } else {
      seen.set(model, row.id)
    }

    for (const field of PRICE_FIELDS) {
      const message = priceError(row[field])
      if (message) rowError[field] = message
    }

    if (Object.keys(rowError).length > 0) errors[row.id] = rowError
  }

  return errors
}

/** 把校验结果落到界面。只在保存时调用，不在 computed 里做。 */
function validate(): Record<number, RowErrors> {
  const errors = collectErrors()
  rowErrors.value = errors
  return errors
}

/** 稳定的载荷快照，字段顺序固定，供 dirty 比较与测试断言。 */
function snapshot(): string {
  if (!config.value) return ''
  const overrides: Record<string, { input: number; output: number; cache_read: number }> = {}
  for (const row of overrideRows.value) {
    const model = row.model.trim()
    if (!model) continue
    overrides[model] = {
      input: Number(row.input) || 0,
      output: Number(row.output) || 0,
      cache_read: Number(row.cache_read) || 0
    }
  }
  return JSON.stringify({
    enabled: config.value.enabled,
    source_url: config.value.source_url,
    manual_overrides: overrides
  })
}

const dirty = computed(() => config.value !== null && snapshot() !== savedSnapshot.value)

const OPEN_API_PATH = '/admin/model-pricing-sync/cny-official'

function isRouteMissing(err: unknown): boolean {
  const status = (err as { status?: number } | null)?.status
  // 404/405 只可能是路由没注册：settings 服务本身的故障会走 500 分支。
  return status === 404 || status === 405
}

async function loadConfig() {
  configLoading.value = true
  configError.value = ''
  routeMissing.value = false
  try {
    const next = await adminAPI.modelPricingSync.getCnyOfficialConfig()
    applyConfig(next)
  } catch (err) {
    routeMissing.value = isRouteMissing(err)
    configError.value = routeMissing.value
      // 端点没注册时把路径写进界面：这是并行后端 lane 尚未落地的信号，
      // 报「加载失败」会让人以为是自己配坏了。
      ? t('admin.modelPricingSync.pricingSource.endpointMissing', { path: OPEN_API_PATH })
      : extractApiErrorMessage(err, t('admin.modelPricingSync.pricingSource.loadFailed'))
    config.value = null
    overrideRows.value = []
  } finally {
    configLoading.value = false
  }
}

async function save() {
  if (!config.value || saving.value) return
  saveError.value = ''

  const errors = validate()
  if (Object.keys(errors).length > 0) {
    appStore.showError(t('admin.modelPricingSync.pricingSource.errFixFirst'))
    return
  }

  const manualOverrides: Record<string, { input: number; output: number; cache_read: number }> = {}
  for (const row of overrideRows.value) {
    const model = row.model.trim()
    manualOverrides[model] = {
      input: Number(row.input),
      output: Number(row.output),
      cache_read: Number(row.cache_read)
    }
  }

  saving.value = true
  try {
    const next = await adminAPI.modelPricingSync.updateCnyOfficialConfig({
      enabled: config.value.enabled,
      source_url: config.value.source_url || DEFAULT_CNY_SOURCE_URL,
      manual_overrides: manualOverrides
    })
    applyConfig(next)
    appStore.showSuccess(t('admin.modelPricingSync.pricingSource.saved'))
  } catch (err) {
    // 后端真实报错要留在页面上，不能只弹一个 5 秒就消失的 toast。
    const message = extractApiErrorMessage(err, t('admin.modelPricingSync.pricingSource.saveFailed'))
    saveError.value = message
    appStore.showError(message)
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  void load()
  void loadConfig()
})
</script>
