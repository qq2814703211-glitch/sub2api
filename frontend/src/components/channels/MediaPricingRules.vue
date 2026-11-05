<template>
  <div v-if="pricing?.rules?.length" data-testid="media-pricing-rules" class="space-y-2 text-xs">
    <p class="font-medium">{{ t('mediaPricing.displayTitle') }}</p>
    <p class="text-gray-500 dark:text-gray-400">{{ t('mediaPricing.displayHint') }}</p>
    <p v-if="rateOptions && rateOptions.length > 1" class="text-gray-500 dark:text-gray-400">{{ t('mediaPricing.endpointRateHint') }}</p>
    <div v-for="(option, optionIndex) in applicableRates" :key="optionIndex" class="space-y-2" data-testid="media-rate-option">
      <p v-if="option.label" class="font-medium">{{ option.label }} · {{ Number(option.multiplier.toPrecision(12)) }}x</p>
      <p v-for="label in mediaRequestLabels(pricing, option.multiplier, t)" :key="label" data-testid="media-request-pricing" class="break-words">{{ label }}</p>
      <div v-for="(rule, index) in pricing.rules" :key="index" class="border-t border-gray-200 pt-2 dark:border-dark-600">
        <div class="font-medium">{{ rule.name || t('mediaPricing.unnamedRule', { index: index + 1 }) }}</div>
        <div class="flex flex-wrap gap-x-2 text-gray-500 dark:text-gray-400">
          <span v-for="(condition, conditionIndex) in mediaConditionLabels(rule.conditions, t)" :key="conditionIndex" class="break-words">{{ condition }}</span>
          <span v-if="!mediaConditionLabels(rule.conditions, t).length">{{ t('mediaPricing.allConditions') }}</span>
        </div>
        <div v-for="(label, priceIndex) in mediaPriceLabels(rule, option.multiplier, t)" :key="priceIndex" class="break-words font-mono">{{ label }}</div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MediaPricing } from '@/types/mediaPricing'
import { mediaConditionLabels, mediaPriceLabels, mediaRequestLabels } from '@/utils/mediaPricing'
const props = withDefaults(defineProps<{
  pricing?: MediaPricing | null
  multiplier?: number
  rateOptions?: { label: string; multiplier: number }[]
}>(), { multiplier: 1 })
const applicableRates = computed(() => props.rateOptions?.length ? props.rateOptions : [{ label: '', multiplier: props.multiplier }])
const { t } = useI18n()
</script>
