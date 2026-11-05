<template>
  <BaseDialog
    :show="show"
    :title="t('keys.usageDialog.title')"
    width="wide"
    @close="emit('close')"
  >
    <div class="space-y-4" data-testid="key-usage-dialog">
      <!-- Key identity and period presets -->
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="min-w-0">
          <p
            class="truncate text-sm font-medium text-gray-900 dark:text-white"
            data-testid="key-usage-key-name"
          >
            {{ apiKey?.name || '-' }}
          </p>
          <p class="mt-0.5 font-mono text-xs text-gray-500 dark:text-gray-400">
            {{ maskedKey }}
          </p>
        </div>
        <div
          class="flex flex-wrap items-center gap-1.5"
          role="group"
          :aria-label="t('keys.usageDialog.period')"
        >
          <button
            v-for="item in presets"
            :key="item.key"
            type="button"
            :data-testid="`key-usage-preset-${item.key}`"
            :aria-pressed="preset === item.key"
            :class="[
              'rounded-lg px-2.5 py-1 text-xs font-medium transition-colors',
              preset === item.key
                ? 'bg-primary-600 text-white'
                : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600'
            ]"
            @click="selectPreset(item.key)"
          >
            {{ item.label }}
          </button>
        </div>
      </div>

      <p class="text-xs text-gray-500 dark:text-gray-400" data-testid="key-usage-range">
        {{ rangeText }}
      </p>

      <!-- Loading -->
      <div v-if="loading" class="space-y-2" data-testid="key-usage-loading">
        <div class="h-16 animate-pulse rounded-lg bg-gray-100 dark:bg-dark-700"></div>
        <div class="h-16 animate-pulse rounded-lg bg-gray-100 dark:bg-dark-700"></div>
      </div>

      <!-- Error -->
      <div
        v-else-if="error"
        class="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-600 dark:border-red-900/50 dark:bg-red-900/20 dark:text-red-400"
        data-testid="key-usage-error"
      >
        <p>{{ t('keys.usageDialog.failed') }}</p>
        <button type="button" class="mt-2 font-medium underline" @click="loadUsage">
          {{ t('keys.usageDialog.retry') }}
        </button>
      </div>

      <template v-else-if="data">
        <!-- Totals -->
        <div class="grid gap-3 sm:grid-cols-2">
          <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-700">
            <div class="text-[11px] font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
              {{ t('keys.usageDialog.today') }}
            </div>
            <div
              class="mt-1 font-mono text-xl font-semibold tabular-nums text-gray-900 dark:text-white"
              data-testid="key-usage-today-cost"
            >
              {{ todayCost ?? '—' }}
            </div>
            <p
              v-if="!todayPoint"
              class="mt-1 text-xs text-gray-500 dark:text-gray-400"
              data-testid="key-usage-today-empty"
            >
              {{ t('keys.usageDialog.noTodayRecord') }}
            </p>
            <div v-else class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ formatNumber(todayPoint?.requests) }} {{ t('keys.usageDialog.requests') }} ·
              {{ formatNumber(todayPoint?.total_tokens) }} {{ t('keys.usageDialog.tokens') }}
            </div>
          </div>
          <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-700">
            <div class="text-[11px] font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
              {{ t('keys.usageDialog.rangeTotal') }}
            </div>
            <div
              class="mt-1 font-mono text-xl font-semibold tabular-nums text-gray-900 dark:text-white"
              data-testid="key-usage-range-cost"
            >
              {{ formatUsd(totals.actual_cost) }}
            </div>
            <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ formatNumber(totals.requests) }} {{ t('keys.usageDialog.requests') }} ·
              {{ formatNumber(totals.total_tokens) }} {{ t('keys.usageDialog.tokens') }}
            </div>
          </div>
        </div>

        <!-- Daily breakdown -->
        <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-700">
          <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
            <thead class="bg-gray-50 dark:bg-dark-900/40">
              <tr>
                <th class="px-3 py-2 text-left text-[11px] font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
                  {{ t('keys.usageDialog.date') }}
                </th>
                <th class="px-3 py-2 text-right text-[11px] font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
                  {{ t('keys.usageDialog.requests') }}
                </th>
                <th class="px-3 py-2 text-right text-[11px] font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
                  {{ t('keys.usageDialog.tokens') }}
                </th>
                <th class="px-3 py-2 text-right text-[11px] font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
                  {{ t('keys.usageDialog.actualCost') }}
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-for="row in dailyRows" :key="row.date" data-testid="key-usage-row">
                <td class="px-3 py-2 font-mono tabular-nums text-gray-900 dark:text-gray-100">
                  {{ row.date }}
                </td>
                <td class="px-3 py-2 text-right font-mono tabular-nums text-gray-700 dark:text-gray-300">
                  {{ formatNumber(row.requests) }}
                </td>
                <td class="px-3 py-2 text-right font-mono tabular-nums text-gray-700 dark:text-gray-300">
                  {{ formatNumber(row.total_tokens) }}
                </td>
                <td class="px-3 py-2 text-right font-mono font-medium tabular-nums text-gray-900 dark:text-white">
                  {{ formatUsd(row.actual_cost) }}
                </td>
              </tr>
            </tbody>
          </table>
          <div
            v-if="dailyRows.length === 0"
            class="px-3 py-6 text-center text-sm text-gray-500 dark:text-gray-400"
            data-testid="key-usage-empty"
          >
            {{ t('keys.usageDialog.empty') }}
          </div>
        </div>
      </template>
    </div>
    <template #footer>
      <div class="flex justify-end">
        <button type="button" class="btn btn-secondary" @click="emit('close')">
          {{ t('keys.usageDialog.close') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { usageAPI } from '@/api'
import type { ApiKeyDailyUsagePoint, ApiKeyDailyUsageResponse } from '@/api/usage'
import type { ApiKey } from '@/types'
import { maskApiKey } from '@/utils/maskApiKey'

const props = defineProps<{
  show: boolean
  apiKey: ApiKey | null
}>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()

type PresetKey = 'today' | '7d' | '30d'
const PRESET_DAYS: Record<PresetKey, number> = { today: 1, '7d': 7, '30d': 30 }

const presets = computed<{ key: PresetKey; label: string }[]>(() => [
  { key: 'today', label: t('keys.usageDialog.presetToday') },
  { key: '7d', label: t('keys.usageDialog.preset7d') },
  { key: '30d', label: t('keys.usageDialog.preset30d') }
])

const preset = ref<PresetKey>('today')
const data = ref<ApiKeyDailyUsageResponse | null>(null)
const loading = ref(false)
const error = ref(false)
let requestVersion = 0
let controller: AbortController | null = null

const timezone = computed(() => Intl.DateTimeFormat().resolvedOptions().timeZone || 'Asia/Shanghai')
const maskedKey = computed(() => maskApiKey(props.apiKey?.key || ''))

const dailyRows = computed<ApiKeyDailyUsagePoint[]>(() =>
  [...(data.value?.items ?? [])].sort((a, b) => b.date.localeCompare(a.date))
)

const totals = computed(() =>
  (data.value?.items ?? []).reduce(
    (sum, row) => ({
      requests: sum.requests + (row.requests ?? 0),
      total_tokens: sum.total_tokens + (row.total_tokens ?? 0),
      actual_cost: sum.actual_cost + (row.actual_cost ?? 0)
    }),
    { requests: 0, total_tokens: 0, actual_cost: 0 }
  )
)

// The API returns end_date as the last day of the requested window, i.e. today
// in the requested timezone. Using it keeps the "today" card aligned with the
// range instead of the browser's possibly different local date.
const todayPoint = computed(
  () => (data.value?.items ?? []).find((row) => row.date === data.value?.end_date) ?? null
)

// No row for today is not the same as a real zero charge. Only a present row with
// an actual_cost of 0 may render "$0.0000"; a missing row renders a dash instead.
const todayCost = computed(() =>
  todayPoint.value?.actual_cost == null ? null : formatUsd(todayPoint.value.actual_cost)
)

const rangeText = computed(() =>
  t('keys.usageDialog.range', {
    start: data.value?.start_date || '-',
    end: data.value?.end_date || '-',
    timezone: timezone.value
  })
)

function formatNumber(value?: number | null) {
  return Number(value ?? 0).toLocaleString('en-US')
}

function formatUsd(value?: number | null) {
  return `$${Number(value ?? 0).toFixed(4)}`
}

function cancelPending() {
  requestVersion += 1
  controller?.abort()
  controller = null
}

function selectPreset(next: PresetKey) {
  preset.value = next
  void loadUsage()
}

async function loadUsage() {
  if (!props.apiKey) return
  cancelPending()
  const version = requestVersion
  const request = new AbortController()
  controller = request
  loading.value = true
  error.value = false
  data.value = null
  try {
    const result = await usageAPI.getMyApiKeyDailyUsage(
      props.apiKey.id,
      PRESET_DAYS[preset.value],
      { timezone: timezone.value, signal: request.signal }
    )
    if (version !== requestVersion) return
    data.value = result
  } catch {
    if (version !== requestVersion || request.signal.aborted) return
    error.value = true
  } finally {
    if (version === requestVersion) {
      loading.value = false
      if (controller === request) controller = null
    }
  }
}

watch(
  () => [props.show, props.apiKey?.id ?? null] as const,
  ([show, apiKeyId]) => {
    if (!show || apiKeyId == null) {
      cancelPending()
      loading.value = false
      error.value = false
      data.value = null
      return
    }
    preset.value = 'today'
    void loadUsage()
  },
  { immediate: true }
)

onBeforeUnmount(cancelPending)
</script>
