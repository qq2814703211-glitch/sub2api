<template>
  <div class="min-w-0 space-y-2 border-t border-gray-200 py-3 text-sm dark:border-dark-700">
    <p class="flex flex-wrap gap-x-2 gap-y-1 text-gray-700 dark:text-gray-200">
      <span class="font-semibold tabular-nums">{{ window.start }}–{{ window.end }}</span>
      <span class="break-all">{{ window.timezone }}</span>
      <span>{{ days }}</span>
      <span v-if="window.end < window.start">{{ t('modelPlaza.windows.overnight') }}</span>
      <span v-if="window.exclude_cn_holidays">{{ t('modelPlaza.windows.excludeHolidays') }}</span>
    </p>
    <p class="text-xs text-gray-600 dark:text-gray-300">{{ t('modelPlaza.table.unitPerMillion') }}</p>
    <dl class="grid grid-cols-1 gap-x-8 gap-y-1.5 sm:grid-cols-2">
      <div
        v-for="entry in entries"
        :key="entry.field"
        :data-component="entry.component"
        class="flex min-w-0 flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5"
      >
        <dt class="min-w-0 break-words text-gray-700 dark:text-gray-200">{{ entry.label }}</dt>
        <dd class="max-w-full break-all font-medium tabular-nums text-gray-900 dark:text-gray-50">{{ amount(entry.value) }}</dd>
      </div>
    </dl>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PlazaPriceWindow } from '@/api/modelPlaza'
import { formatScaled } from '@/utils/pricing'

const props = defineProps<{ window: PlazaPriceWindow; rate: number }>()
const { t, locale } = useI18n()
const componentLabels: Record<string, string> = {
  input_cost_per_token: 'input',
  output_cost_per_token: 'output',
  cache_read_input_token_cost: 'read',
  cache_creation_input_token_cost: 'write',
  cache_creation_input_token_cost_above_1hr: 'write1h',
  cache_creation_input_token_cost_30m: 'write30m',
  cache_creation_input_token_cost_12h: 'write12h',
  input_cost_per_token_priority: 'inputPriority',
  output_cost_per_token_priority: 'outputPriority',
  cache_read_input_token_cost_priority: 'readPriority',
  cache_creation_input_token_cost_priority: 'writePriority',
  input_cost_per_image_token: 'imageInput',
  output_cost_per_image_token: 'imageOutput',
  cache_read_input_image_token_cost: 'imageRead'
}

const days = computed(() => {
  if (!props.window.weekdays?.length) return t('modelPlaza.windows.everyDay')
  const localeTag = locale.value === 'zh' ? 'zh-CN' : locale.value
  const weekday = new Intl.DateTimeFormat(localeTag, { weekday: 'short', timeZone: 'UTC' })
  // 2024-01-07 is a Sunday, matching the server's time.Weekday convention.
  const labels = props.window.weekdays.map(day => weekday.format(Date.UTC(2024, 0, 7 + day)))
  return labels.join(locale.value === 'zh' ? '、' : ', ')
})

const entries = computed(() => {
  // Keep known components in reading order. Preserve future numeric keys instead of hiding a quoted rate.
  const fields = new Set([...Object.keys(componentLabels), ...Object.keys(props.window.rates)])
  return [...fields].flatMap(field => {
    const value = props.window.rates[field]
    if (value == null) return []
    const component = componentLabels[field] ?? field
    return [{ field, component, value, label: componentLabels[field] ? t(`modelPlaza.windows.components.${component}`) : field }]
  })
})

function amount(value: number) {
  return formatScaled(value * props.rate, 1_000_000, 2).replace(/^\$/, '')
}
</script>
