<template>
  <div class="mt-3 border-t border-gray-200 pt-3 dark:border-dark-600">
    <label class="block text-xs font-medium text-gray-500 dark:text-gray-400">
      {{ t('mediaPricing.editorTitle') }}
      <textarea
        :value="text"
        class="input mt-1 min-h-40 font-mono text-xs"
        rows="8"
        spellcheck="false"
        data-testid="media-pricing-json"
        :aria-invalid="!!error"
        :aria-label="t('mediaPricing.editorTitle')"
        :placeholder="t('mediaPricing.empty')"
        @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
      />
    </label>
    <p class="mt-1 text-xs text-gray-500">{{ t('mediaPricing.editorHint') }}</p>
    <p class="mt-1 text-xs text-gray-500">{{ t('mediaPricing.referenceInputHint') }}</p>
    <p class="mt-1 text-xs text-gray-500">{{ t('mediaPricing.matchHint') }}</p>
    <p v-if="error" role="alert" class="mt-1 text-xs text-red-500">{{ error }}</p>
    <details class="mt-2 text-xs text-gray-500">
      <summary class="cursor-pointer">{{ t('mediaPricing.examples') }}</summary>
      <p class="my-1">{{ t('mediaPricing.exampleHint') }}</p>
      <div v-for="(example, key) in MEDIA_PRICING_EXAMPLES" :key="key" class="mt-2">
        <div class="font-medium">{{ t(`mediaPricing.exampleLabels.${key}`) }}</div>
        <pre class="mt-1 overflow-x-auto whitespace-pre-wrap break-words rounded border border-gray-200 p-2 dark:border-dark-600">{{ JSON.stringify(example, null, 2) }}</pre>
      </div>
    </details>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { MEDIA_PRICING_EXAMPLES, mediaPricingToForm, validateMediaPricing, type MediaPricingFormValue } from '@/utils/mediaPricing'

const props = defineProps<{ modelValue?: MediaPricingFormValue }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { t } = useI18n()
const text = computed(() => typeof props.modelValue === 'string' ? props.modelValue : mediaPricingToForm(props.modelValue))
const error = computed(() => validateMediaPricing(props.modelValue, t))
</script>
