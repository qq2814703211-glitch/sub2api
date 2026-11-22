<template>
  <div class="min-w-0 rounded-lg border border-gray-200 bg-gray-50 p-3 text-xs text-gray-700 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-300" :data-testid="`pricing-quote-${kind}`">
    <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ text(`quote.${kind}`) }}</h3>
    <p v-if="kind === 'effective' && quote" class="mt-1 text-gray-600 dark:text-gray-300">{{ text('effectivePriceHint') }}</p>
    <p v-if="effectiveMode || revision || manualPolicy" class="mt-1 break-all">
      <span v-if="effectiveMode">{{ text(`quoteMode.${effectiveMode}`) }} </span>
      <code v-if="revision">{{ revision }}</code>
      <span v-if="manualPolicy">{{ text(manualPolicy) }}</span>
    </p>
    <p v-if="!quote" class="mt-2 text-gray-600 dark:text-dark-300">{{ text(`empty.${kind}`) }}</p>
    <template v-else>
      <p class="mt-1">{{ text(`mode.${quote.billing_mode}`) }}</p>
      <p v-if="quote.provider" class="mt-1 break-words">{{ text('provider') }}: {{ quote.provider }}</p>
      <p v-if="quote.source_url" class="mt-1 break-all">{{ t('admin.modelPricingSync.pricingSource.sourceUrl') }}: <code>{{ quote.source_url }}</code></p>
      <p v-if="quote.evidence" class="mt-1 break-all">{{ text('evidence') }}: <code>{{ quote.evidence }}</code></p>
      <p v-if="quote.verified_at && !quote.verified_at.startsWith('0001-01-01')" class="mt-1">{{ text('verifiedAt') }}: <time :datetime="quote.verified_at">{{ quote.verified_at }}</time></p>
      <dl v-for="(group, index) in groups" :key="index" class="mt-2 space-y-1 border-t border-gray-200 pt-2 dark:border-dark-700">
        <div v-if="group.label" class="font-medium">{{ group.label }}</div>
        <div v-for="field in group.fields" :key="field" class="flex flex-wrap justify-between gap-x-2 gap-y-0.5">
          <dt class="min-w-0 break-all">{{ label(field) }}</dt>
          <dd class="break-all font-mono">{{ amount(field, group.rates[field]) }}</dd>
        </div>
      </dl>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { getRateUnit, MANUAL_EXTRA_FIELDS, MANUAL_RATE_KEYS, MANUAL_TOKEN_FIELDS, MANUAL_UNIT_FIELDS, type PriceSyncQuote, type PricingQuoteRates } from '@/api/admin/modelPricingSync'

type Quote = Pick<PriceSyncQuote, 'currency' | 'billing_mode' | 'rates'> & Partial<PriceSyncQuote>
const props = defineProps<{
  kind: 'auto' | 'manual' | 'effective'
  quote?: Quote | null
  effectiveMode?: 'auto' | 'manual'
  manualPolicy?: 'autoRestore' | 'manualPinned'
  manualUnits?: boolean
  revision?: string
}>()
const { t } = useI18n()
const prefix = 'admin.modelPricingSync'
const text = (key: string) => t(`${prefix}.v2.${key}`)
const groups = computed(() => {
  const quote = props.quote
  if (!quote) return []
  const rates = quote.rates ?? {}
  const baseFields = quote.billing_mode === 'token'
    ? MANUAL_TOKEN_FIELDS.map((key) => MANUAL_RATE_KEYS[key])
    : [MANUAL_RATE_KEYS[MANUAL_UNIT_FIELDS[quote.billing_mode]]]
  return [
    { label: '', rates, fields: [...new Set([...baseFields, ...Object.keys(rates)])] },
    ...(quote.tiers ?? []).map((tier) => ({
      label: `${text('contextTier')} ${tier.inclusive ? '≥' : '>'} ${tier.threshold} tokens`,
      rates: tier.rates, fields: Object.keys(tier.rates)
    })),
    ...(quote.windows ?? []).map((window) => ({
      label: [
        `${window.start}–${window.end} (${window.timezone})`,
        window.weekdays?.map((day) => text(`day.${day}`)).join(', '),
        window.exclude_cn_holidays ? text('excludeHolidays') : ''
      ].filter(Boolean).join(' · '),
      rates: window.rates, fields: Object.keys(window.rates)
    }))
  ]
})

function label(field: string) {
  const extra = MANUAL_EXTRA_FIELDS.find(({ key }) => MANUAL_RATE_KEYS[key] === field)
  if (extra && field !== 'output_cost_per_image') return t(`${prefix}.pricingSource.extra.${extra.key}`)
  const key = `${prefix}.fields.${field}`
  const value = t(key)
  return value === key ? field : value
}

function amount(field: string, value: PricingQuoteRates[string] | undefined) {
  if (value === null) return text('unknownRate')
  if (value === undefined) return text('unsetRate')
  const unit = getRateUnit(field)
  const perMillion = !props.manualUnits && (unit === 'token' || unit === 'storage')
  const amount = perMillion ? Number((value * 1e6).toPrecision(15)) : value
  const currency = props.quote?.currency === 'SITE' ? text('siteCurrency') : props.quote?.currency
  return `${amount} ${currency}${unit ? ` / ${text(`unit.${unit}`)}` : ''}`
}
</script>