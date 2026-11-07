<template>
  <section class="rounded-lg border border-gray-200 dark:border-dark-700">
    <!-- Collapsed header -->
    <button
      type="button"
      class="flex w-full items-center justify-between gap-2 px-3 py-2 text-left"
      @click="open = !open"
    >
      <span class="flex min-w-0 items-center gap-2 text-xs font-medium text-gray-900 dark:text-white">
        <Icon name="lightbulb" size="sm" class="shrink-0 text-gray-400" />
        <span class="truncate">{{ t('imagegen.rewriteTitle') }}</span>
      </span>
      <span class="flex shrink-0 items-center gap-2">
        <span class="badge" :class="config.enabled ? 'badge-primary' : 'badge-gray'">
          {{ config.enabled ? t('imagegen.rewriteOn') : t('imagegen.rewriteOff') }}
        </span>
        <span v-if="usesGenerationKey" class="badge badge-warning">{{ t('imagegen.rewriteKeyFallbackShort') }}</span>
        <Icon :name="open ? 'chevronUp' : 'chevronDown'" size="sm" class="text-gray-400" />
      </span>
    </button>

    <div v-if="open" class="space-y-3 border-t border-gray-100 px-3 py-3 dark:border-dark-800">
      <!-- Enable -->
      <div class="flex items-start justify-between gap-3">
        <div class="min-w-0">
          <p class="text-xs font-medium text-gray-900 dark:text-white">{{ t('imagegen.rewriteEnable') }}</p>
          <p class="mt-0.5 text-[11px] leading-relaxed text-gray-500 dark:text-dark-400">
            {{ t('imagegen.rewriteEnableHint') }}
          </p>
        </div>
        <Toggle :model-value="config.enabled" @update:model-value="emit('update:enabled', $event)" />
      </div>

      <!-- Dedicated key -->
      <label class="block">
        <span class="input-label">{{ t('imagegen.rewriteKeyLabel') }}</span>
        <div class="flex gap-2">
          <input
            :value="config.apiKey"
            type="password"
            class="input flex-1"
            :placeholder="t('imagegen.rewriteKeyPlaceholder')"
            autocomplete="off"
            @input="emit('update:apiKey', ($event.target as HTMLInputElement).value)"
          />
          <button
            v-if="config.apiKey"
            type="button"
            class="btn btn-secondary btn-sm shrink-0"
            @click="emit('update:apiKey', '')"
          >
            {{ t('imagegen.rewriteKeyClear') }}
          </button>
        </div>
        <span class="input-hint">
          {{ config.apiKey ? t('imagegen.rewriteKeyOwnHint') : t('imagegen.rewriteKeyFallbackHint') }}
        </span>
      </label>

      <!-- Base URL -->
      <label class="block">
        <span class="input-label">{{ t('imagegen.rewriteBaseUrlLabel') }}</span>
        <input
          :value="config.baseUrl"
          class="input font-mono text-xs"
          :placeholder="t('imagegen.rewriteBaseUrlPlaceholder')"
          autocomplete="off"
          spellcheck="false"
          @input="emit('update:baseUrl', ($event.target as HTMLInputElement).value)"
        />
        <span class="input-hint" :class="baseUrlInvalid ? 'text-red-600 dark:text-red-400' : ''">
          {{ baseUrlInvalid ? t('imagegen.rewriteBaseUrlInvalid') : t('imagegen.rewriteBaseUrlHint') }}
        </span>
      </label>

      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <!-- Protocol -->
        <label class="block">
          <span class="input-label">{{ t('imagegen.rewriteProtocolLabel') }}</span>
          <select
            class="input"
            :value="config.protocol"
            @change="emit('update:protocol', ($event.target as HTMLSelectElement).value as RewriteProtocol)"
          >
            <option v-for="item in REWRITE_PROTOCOLS" :key="item" :value="item">
              {{ protocolLabel(item) }}
            </option>
          </select>
          <span class="input-hint">{{ protocolHint }}</span>
        </label>

        <!-- Reasoning effort -->
        <label class="block">
          <span class="input-label">{{ t('imagegen.rewriteEffortLabel') }}</span>
          <select
            class="input"
            :value="config.effort"
            @change="emit('update:effort', ($event.target as HTMLSelectElement).value as RewriteEffort)"
          >
            <option v-for="item in effortOptions" :key="item" :value="item">
              {{ effortLabel(item) }}
            </option>
          </select>
          <span class="input-hint">{{ effortHint }}</span>
        </label>
      </div>

      <!-- Model -->
      <label class="block">
        <span class="input-label">{{ t('imagegen.rewriteModelLabel') }}</span>
        <input
          :value="config.model"
          class="input"
          list="imagegen-rewrite-model-suggestions"
          :placeholder="t('imagegen.rewriteModelPlaceholder')"
          autocomplete="off"
          @input="emit('update:model', ($event.target as HTMLInputElement).value)"
        />
        <datalist id="imagegen-rewrite-model-suggestions">
          <option v-for="name in modelSuggestions" :key="name" :value="name" />
        </datalist>
        <span class="input-hint">{{ t('imagegen.rewriteModelHint') }}</span>
      </label>

      <!-- Effective endpoint preview -->
      <p class="break-all rounded-md bg-gray-50 px-2 py-1.5 font-mono text-[10px] leading-relaxed text-gray-500 dark:bg-dark-800 dark:text-dark-400">
        POST {{ endpointPreview }}
      </p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import Toggle from '@/components/common/Toggle.vue'
import { buildRewriteUrl, rewritePath } from './rewriteApi'
import {
  REWRITE_EFFORTS_BY_PROTOCOL,
  REWRITE_MODEL_SUGGESTIONS,
  REWRITE_PROTOCOLS,
  isValidRewriteBaseUrl,
  type RewriteConfig,
  type RewriteEffort,
  type RewriteProtocol,
} from './rewriteConfig'

const props = defineProps<{
  config: RewriteConfig
  /** Mirrors the composable's computed so the badge matches the real call. */
  usesGenerationKey: boolean
}>()

const emit = defineEmits<{
  (e: 'update:enabled', value: boolean): void
  (e: 'update:apiKey', value: string): void
  (e: 'update:baseUrl', value: string): void
  (e: 'update:protocol', value: RewriteProtocol): void
  (e: 'update:model', value: string): void
  (e: 'update:effort', value: RewriteEffort): void
}>()

const { t } = useI18n()
const open = ref(false)

const effortOptions = computed(() => REWRITE_EFFORTS_BY_PROTOCOL[props.config.protocol])
const modelSuggestions = computed(() => REWRITE_MODEL_SUGGESTIONS[props.config.protocol])
const baseUrlInvalid = computed(() => !isValidRewriteBaseUrl(props.config.baseUrl))

const endpointPreview = computed(() =>
  buildRewriteUrl(props.config.baseUrl, rewritePath(props.config.protocol)),
)

function protocolLabel(protocol: RewriteProtocol): string {
  return t(`imagegen.rewriteProtocol_${protocol.replace(/-/g, '_')}`)
}

function effortLabel(effort: RewriteEffort): string {
  return t(`imagegen.rewriteEffort_${effort}`)
}

const protocolHint = computed(() => t(`imagegen.rewriteProtocolHint_${props.config.protocol.replace(/-/g, '_')}`))

/**
 * Effort means different things per protocol, so the hint names the actual
 * request field instead of describing "thinking" in the abstract.
 */
const effortHint = computed(() => {
  if (props.config.effort === 'unset') return t('imagegen.rewriteEffortHintUnset')
  if (props.config.protocol === 'anthropic-messages') {
    return t('imagegen.rewriteEffortHintAnthropic')
  }
  if (props.config.protocol === 'openai-responses') return t('imagegen.rewriteEffortHintResponses')
  return t('imagegen.rewriteEffortHintChat')
})
</script>
