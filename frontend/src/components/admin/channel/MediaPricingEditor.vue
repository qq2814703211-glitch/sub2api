<template>
  <div class="mt-3 border-t border-gray-200 pt-3 dark:border-dark-600" data-testid="media-pricing-editor">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <label class="block text-xs font-medium text-gray-500 dark:text-gray-400">
        {{ t('mediaPricing.visual.title') }}
      </label>
      <div class="flex items-center gap-3">
        <button
          v-if="!isEmptyState"
          type="button"
          data-testid="media-pricing-clear"
          class="text-xs text-gray-500 hover:text-red-500"
          @click="clearPricing"
        >
          {{ t('mediaPricing.visual.clear') }}
        </button>
        <button
          type="button"
          data-testid="media-pricing-add-rule"
          class="text-xs text-primary-700 hover:text-primary-600"
          @click="addRule"
        >
          + {{ t('mediaPricing.visual.addRule') }}
        </button>
      </div>
    </div>
    <p class="mt-1 text-xs text-gray-500">{{ t('mediaPricing.editorHint') }}</p>

    <!-- Parse or shape failure: the draft text is kept verbatim, visual editing pauses. -->
    <p
      v-if="unrenderable"
      class="mt-2 rounded border border-amber-300 bg-amber-50 p-2 text-xs text-amber-700 dark:border-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
      data-testid="media-pricing-unrenderable"
    >
      {{ t(unrenderable === 'json' ? 'mediaPricing.visual.jsonBroken' : 'mediaPricing.visual.shapeBroken') }}
    </p>

    <p v-else-if="isEmptyState" class="mt-2 text-xs text-gray-500" data-testid="media-pricing-empty">
      {{ t('mediaPricing.empty') }}
    </p>

    <template v-else-if="model">
      <!-- Request-level charges and the duration source. -->
      <div class="mt-3 rounded border border-gray-200 p-2 dark:border-dark-600">
        <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('mediaPricing.visual.requestFields') }}</p>
        <div class="mt-1 grid gap-2 sm:grid-cols-3">
          <label class="text-xs text-gray-500">
            {{ t('mediaPricing.visual.extraInputImagePrice') }}
            <input
              type="text"
              inputmode="decimal"
              class="input mt-0.5 text-sm"
              data-testid="media-pricing-extra-input-image-price"
              :value="numberText('top:extra_input_image_price', model.extra_input_image_price)"
              @input="onTopNumber($event, 'extra_input_image_price')"
            />
            <span v-if="isInvalid('top:extra_input_image_price')" class="mt-0.5 block text-red-500">
              {{ t('mediaPricing.visual.invalidNumber') }}
            </span>
          </label>
          <label class="text-xs text-gray-500">
            {{ t('mediaPricing.visual.freeInputImages') }}
            <input
              type="text"
              inputmode="numeric"
              class="input mt-0.5 text-sm"
              data-testid="media-pricing-free-input-images"
              :value="numberText('top:free_input_images', model.free_input_images)"
              @input="onTopNumber($event, 'free_input_images')"
            />
          </label>
          <label class="text-xs text-gray-500">
            {{ t('mediaPricing.visual.durationSource') }}
            <select
              class="input mt-0.5 text-sm"
              data-testid="media-pricing-duration-source"
              :value="model.duration_source ?? ''"
              @change="onDurationSource"
            >
              <option value="">{{ t('mediaPricing.visual.durationDefault') }}</option>
              <option value="output">{{ t('mediaPricing.visual.durationOutput') }}</option>
              <option value="total">{{ t('mediaPricing.visual.durationTotal') }}</option>
              <option v-if="model.duration_source && !['output', 'total'].includes(model.duration_source)" :value="model.duration_source">
                {{ model.duration_source }}
              </option>
            </select>
          </label>
        </div>
        <p
          v-if="model.duration_source === 'total'"
          class="mt-1 text-xs text-amber-600 dark:text-amber-400"
          data-testid="media-pricing-duration-total-hint"
        >
          {{ t('mediaPricing.visual.durationTotalHint') }}
        </p>
        <p class="mt-1 text-xs text-gray-500">{{ t('mediaPricing.referenceInputHint') }}</p>
      </div>

      <!-- One collapsible card per rule. -->
      <p v-if="model.rules.length === 0" class="mt-2 rounded border border-dashed border-gray-300 p-3 text-center text-xs text-gray-500 dark:border-dark-500" data-testid="media-pricing-no-rules">
        {{ t('mediaPricing.visual.noRules') }}
      </p>
      <div class="mt-3 space-y-2">
        <div
          v-for="(rule, index) in model.rules"
          :key="ruleIds[index]"
          class="rounded border border-gray-200 bg-white dark:border-dark-500 dark:bg-dark-700"
          :data-testid="`media-pricing-rule-${index}`"
          :data-media-unit="rule.unit"
        >
          <div class="flex flex-wrap items-center gap-2 p-2">
            <button
              type="button"
              class="rounded p-0.5 text-gray-500 hover:text-gray-700"
              :data-testid="`media-pricing-rule-${index}-toggle`"
              :aria-expanded="!!openRules[index]"
              :aria-label="t('mediaPricing.visual.toggle')"
              @click="toggleRule(index)"
            >
              <Icon :name="openRules[index] ? 'chevronDown' : 'chevronRight'" size="sm" />
            </button>
            <span class="text-xs font-medium text-gray-700 dark:text-gray-200">
              {{ rule.name || t('mediaPricing.unnamedRule', { index: index + 1 }) }}
            </span>
            <span class="rounded-full bg-primary-100 px-2 py-0.5 text-[11px] font-medium text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">
              {{ t(`mediaPricing.units.${rule.unit || 'image'}`) }}
            </span>
            <span class="text-xs text-gray-500" :data-testid="`media-pricing-rule-${index}-summary`">
              {{ mediaPriceLabels(rule, 1, t).join(' · ') }}
            </span>
            <span v-if="mediaConditionLabels(rule.conditions, t).length" class="min-w-0 truncate text-xs text-gray-500">
              {{ mediaConditionLabels(rule.conditions, t).join(' · ') }}
            </span>
            <span v-else class="text-xs italic text-gray-500">{{ t('mediaPricing.visual.noConditions') }}</span>
            <div class="ml-auto flex items-center gap-1">
              <button
                type="button"
                class="rounded p-0.5 text-gray-500 hover:text-gray-700 disabled:opacity-30"
                :data-testid="`media-pricing-rule-${index}-up`"
                :aria-label="t('mediaPricing.visual.moveUp')"
                :disabled="index === 0"
                @click="moveRule(index, -1)"
              >
                <Icon name="arrowUp" size="sm" />
              </button>
              <button
                type="button"
                class="rounded p-0.5 text-gray-500 hover:text-gray-700 disabled:opacity-30"
                :data-testid="`media-pricing-rule-${index}-down`"
                :aria-label="t('mediaPricing.visual.moveDown')"
                :disabled="index === model.rules.length - 1"
                @click="moveRule(index, 1)"
              >
                <Icon name="arrowDown" size="sm" />
              </button>
              <button
                type="button"
                class="rounded p-0.5"
                :class="confirmingLastRule ? 'text-red-600 dark:text-red-400' : 'text-gray-500 hover:text-red-500'"
                :data-testid="`media-pricing-rule-${index}-remove`"
                :aria-label="t('mediaPricing.visual.removeRule')"
                @click="removeRule(index)"
              >
                <Icon name="trash" size="sm" />
              </button>
            </div>
          </div>

          <p
            v-if="confirmingLastRule && model.rules.length === 1"
            class="mx-2 mb-1 rounded bg-red-50 p-1.5 text-xs text-red-600 dark:bg-red-900/20 dark:text-red-400"
            role="status"
            data-testid="media-pricing-remove-last-hint"
          >
            {{ t('mediaPricing.visual.removeLastConfirm') }}
          </p>

          <div v-show="openRules[index]" class="border-t border-gray-200 p-2 dark:border-dark-600">
            <div class="grid gap-2 sm:grid-cols-2">
              <label class="text-xs text-gray-500">
                {{ t('mediaPricing.visual.ruleName') }}
                <input
                  type="text"
                  class="input mt-0.5 text-sm"
                  :data-testid="`media-pricing-rule-${index}-name`"
                  :placeholder="t('mediaPricing.visual.ruleNamePlaceholder')"
                  :value="rule.name ?? ''"
                  @input="onRuleName($event, index)"
                />
              </label>
              <label class="text-xs text-gray-500">
                {{ t('mediaPricing.visual.unit') }}
                <select
                  class="input mt-0.5 text-sm"
                  :data-testid="`media-pricing-rule-${index}-unit`"
                  :value="rule.unit ?? ''"
                  @change="onUnit($event, index)"
                >
                  <option v-if="!MEDIA_PRICING_UNITS.includes(rule.unit)" :value="rule.unit ?? ''" disabled>
                    {{ rule.unit || t('mediaPricing.visual.unitUnset') }}
                  </option>
                  <option v-for="unit in MEDIA_PRICING_UNITS" :key="unit" :value="unit">
                    {{ t(`mediaPricing.units.${unit}`) }} ({{ unit }})
                  </option>
                </select>
              </label>
            </div>
            <p class="mt-1 text-xs text-gray-500">{{ t('mediaPricing.visual.unitChangeHint') }}</p>

            <!-- Prices: token units use the four token fields, every other unit uses price. -->
            <div class="mt-2">
              <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('mediaPricing.visual.prices') }}</p>
              <div class="mt-1 grid gap-2 sm:grid-cols-3 lg:grid-cols-4">
                <template v-if="rule.unit === 'token'">
                  <label v-for="key in MEDIA_PRICING_TOKEN_PRICES" :key="key" class="text-xs text-gray-500">
                    {{ t(`mediaPricing.prices.${key}`) }}
                    <input
                      type="text"
                      inputmode="decimal"
                      class="input mt-0.5 text-sm"
                      :data-testid="`media-pricing-rule-${index}-${key}`"
                      :value="numberText(`${ruleIds[index]}:${key}`, rule[key])"
                      @input="onRuleNumber($event, index, key)"
                    />
                    <span v-if="isInvalid(`${ruleIds[index]}:${key}`)" class="mt-0.5 block text-red-500">
                      {{ t('mediaPricing.visual.invalidNumber') }}
                    </span>
                  </label>
                </template>
                <label v-else class="text-xs text-gray-500">
                  {{ t('mediaPricing.visual.priceLabel', { unit: t(`mediaPricing.units.${rule.unit || 'image'}`) }) }}
                  <span class="text-red-500">*</span>
                  <input
                    type="text"
                    inputmode="decimal"
                    class="input mt-0.5 text-sm"
                    :data-testid="`media-pricing-rule-${index}-price`"
                    :value="numberText(`${ruleIds[index]}:price`, rule.price)"
                    @input="onRuleNumber($event, index, 'price')"
                  />
                  <span v-if="isInvalid(`${ruleIds[index]}:price`)" class="mt-0.5 block text-red-500">
                    {{ t('mediaPricing.visual.invalidNumber') }}
                  </span>
                </label>
                <label class="text-xs text-gray-500">
                  {{ t('mediaPricing.prices.prompt_price') }}
                  <input
                    type="text"
                    inputmode="decimal"
                    class="input mt-0.5 text-sm"
                    :data-testid="`media-pricing-rule-${index}-prompt_price`"
                    :value="numberText(`${ruleIds[index]}:prompt_price`, rule.prompt_price)"
                    @input="onRuleNumber($event, index, 'prompt_price')"
                  />
                  <span v-if="isInvalid(`${ruleIds[index]}:prompt_price`)" class="mt-0.5 block text-red-500">
                    {{ t('mediaPricing.visual.invalidNumber') }}
                  </span>
                </label>
              </div>
              <p v-if="rule.unit === 'token' && !hasTokenPrice(rule)" class="mt-1 text-xs text-red-500">
                {{ t('mediaPricing.visual.tokenPriceRequired') }}
              </p>
              <p v-if="rule.unit !== 'token' && rule.price == null" class="mt-1 text-xs text-red-500">
                {{ t('mediaPricing.visual.priceRequired') }}
              </p>
            </div>

            <!-- Conditions, grouped so every dimension stays reachable. -->
            <div class="mt-2">
              <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                {{ t('mediaPricing.visual.conditions') }}
                <span class="font-normal">({{ conditionCount(rule) }})</span>
              </p>

              <details class="mt-1" open>
                <summary class="cursor-pointer text-xs text-gray-500">{{ t('mediaPricing.visual.stringConditions') }}</summary>
                <div class="mt-1 grid gap-2 sm:grid-cols-3">
                  <label v-for="key in MEDIA_PRICING_STRING_CONDITIONS" :key="key" class="text-xs text-gray-500">
                    {{ t(`mediaPricing.conditions.${key}`) }}
                    <input
                      type="text"
                      class="input mt-0.5 text-sm"
                      :data-testid="`media-pricing-rule-${index}-cond-${key}`"
                      :value="rule.conditions?.[key] ?? ''"
                      :placeholder="t('mediaPricing.visual.unset')"
                      @input="onStringCondition($event, index, key)"
                    />
                  </label>
                </div>
              </details>

              <details class="mt-1" open>
                <summary class="cursor-pointer text-xs text-gray-500">{{ t('mediaPricing.visual.booleanConditions') }}</summary>
                <div class="mt-1 grid gap-2 sm:grid-cols-3">
                  <label v-for="key in MEDIA_PRICING_BOOLEAN_CONDITIONS" :key="key" class="text-xs text-gray-500">
                    {{ t(`mediaPricing.conditions.${key}`) }}
                    <select
                      class="input mt-0.5 text-sm"
                      :data-testid="`media-pricing-rule-${index}-cond-${key}`"
                      :value="booleanValue(rule, key)"
                      @change="onBooleanCondition($event, index, key)"
                    >
                      <option value="">{{ t('mediaPricing.visual.unset') }}</option>
                      <option value="true">{{ t('mediaPricing.yes') }}</option>
                      <option value="false">{{ t('mediaPricing.no') }}</option>
                    </select>
                  </label>
                </div>
              </details>

              <details class="mt-1" open>
                <summary class="cursor-pointer text-xs text-gray-500">{{ t('mediaPricing.visual.numericConditions') }}</summary>
                <div class="mt-1 grid gap-2 sm:grid-cols-4">
                  <label v-for="key in MEDIA_PRICING_NUMERIC_CONDITIONS" :key="key" class="text-xs text-gray-500">
                    {{ t(`mediaPricing.conditions.${key}`) }}
                    <input
                      type="text"
                      inputmode="decimal"
                      class="input mt-0.5 text-sm"
                      :data-testid="`media-pricing-rule-${index}-cond-${key}`"
                      :value="numberText(`${ruleIds[index]}:cond:${key}`, rule.conditions?.[key])"
                      :placeholder="t('mediaPricing.visual.unset')"
                      @input="onNumericCondition($event, index, key)"
                    />
                    <span v-if="isInvalid(`${ruleIds[index]}:cond:${key}`)" class="mt-0.5 block text-red-500">
                      {{ t('mediaPricing.visual.invalidNumber') }}
                    </span>
                  </label>
                </div>
                <p
                  v-for="warning in rangeWarnings(index)"
                  :key="warning"
                  class="mt-1 text-xs text-red-500"
                >
                  {{ warning }}
                </p>
              </details>
            </div>
          </div>
        </div>
      </div>
      <p class="mt-1 text-xs text-gray-500">{{ t('mediaPricing.matchHint') }}</p>
    </template>

    <p v-if="error" role="alert" class="mt-1 text-xs text-red-500" data-testid="media-pricing-validation">{{ error }}</p>

    <!-- Escape hatch: full JSON freedom, two-way with the visual editor. -->
    <details class="mt-2 text-xs text-gray-500" :open="unrenderable !== null">
      <summary class="cursor-pointer">{{ t('mediaPricing.visual.advanced') }}</summary>
      <p class="my-1">{{ t('mediaPricing.visual.advancedHint') }}</p>
      <textarea
        :value="rawText"
        class="input mt-1 min-h-40 w-full font-mono text-xs"
        rows="8"
        spellcheck="false"
        data-testid="media-pricing-json"
        :aria-invalid="!!error"
        :aria-label="t('mediaPricing.editorTitle')"
        :placeholder="t('mediaPricing.empty')"
        @input="onJsonInput"
      />
    </details>

    <details class="mt-2 text-xs text-gray-500">
      <summary class="cursor-pointer">{{ t('mediaPricing.examples') }}</summary>
      <p class="my-1">{{ t('mediaPricing.exampleHint') }}</p>
      <div v-for="(example, key) in MEDIA_PRICING_EXAMPLES" :key="key" class="mt-2">
        <div class="flex items-center justify-between gap-2">
          <span class="font-medium">{{ t(`mediaPricing.exampleLabels.${key}`) }}</span>
          <button
            type="button"
            class="text-xs text-primary-700 hover:text-primary-600"
            :data-testid="`media-pricing-example-${key}`"
            @click="loadExample(example)"
          >
            {{ t('mediaPricing.visual.loadExample') }}
          </button>
        </div>
        <pre class="mt-1 overflow-x-auto whitespace-pre-wrap break-words rounded border border-gray-200 p-2 dark:border-dark-600">{{ JSON.stringify(example, null, 2) }}</pre>
      </div>
    </details>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { MediaPricing, MediaPricingConditions, MediaPricingRule, MediaPricingUnit } from '@/types/mediaPricing'
import {
  MEDIA_PRICING_BOOLEAN_CONDITIONS,
  MEDIA_PRICING_EXAMPLES,
  MEDIA_PRICING_NUMERIC_CONDITIONS,
  MEDIA_PRICING_RANGE_PAIRS,
  MEDIA_PRICING_STRING_CONDITIONS,
  MEDIA_PRICING_TOKEN_PRICES,
  MEDIA_PRICING_UNITS,
  mediaConditionLabels,
  mediaPriceLabels,
  mediaPricingToForm,
  validateMediaPricing,
  type MediaPricingFormValue,
} from '@/utils/mediaPricing'

const props = defineProps<{ modelValue?: MediaPricingFormValue }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { t } = useI18n()

type NumericConditionKey = (typeof MEDIA_PRICING_NUMERIC_CONDITIONS)[number]
type StringConditionKey = (typeof MEDIA_PRICING_STRING_CONDITIONS)[number]
type BooleanConditionKey = (typeof MEDIA_PRICING_BOOLEAN_CONDITIONS)[number]
type TokenPriceKey = (typeof MEDIA_PRICING_TOKEN_PRICES)[number]
type RuleNumberKey = TokenPriceKey | 'price' | 'prompt_price'

/**
 * The draft is the single source of truth so invalid JSON survives; the visual
 * model only exists when the draft can be rendered without losing anything.
 */
type DraftState =
  | { kind: 'empty' }
  | { kind: 'unrenderable'; reason: 'json' | 'shape' }
  | { kind: 'object'; value: MediaPricing }

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function readDraft(text: string): DraftState {
  const trimmed = text.trim()
  if (trimmed === '' || trimmed === 'null') return { kind: 'empty' }
  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch {
    return { kind: 'unrenderable', reason: 'json' }
  }
  if (parsed === null) return { kind: 'empty' }
  if (!isPlainObject(parsed)) return { kind: 'unrenderable', reason: 'shape' }
  const rules = parsed.rules
  if (rules !== undefined && (!Array.isArray(rules) || rules.some(rule => !isPlainObject(rule)))) {
    return { kind: 'unrenderable', reason: 'shape' }
  }
  // A non-object conditions value cannot be edited dimension by dimension without
  // discarding what the operator wrote; keep it in the JSON escape hatch instead.
  if (Array.isArray(rules) && rules.some(rule => {
    const conditions = (rule as Record<string, unknown>).conditions
    return conditions !== undefined && conditions !== null && !isPlainObject(conditions)
  })) {
    return { kind: 'unrenderable', reason: 'shape' }
  }
  // Deep clone so visual edits never mutate the value the parent handed in.
  const value = JSON.parse(JSON.stringify(parsed)) as MediaPricing
  if (!Array.isArray(value.rules)) value.rules = []
  return { kind: 'object', value: normalize(value) }
}

const PRICE_KEYS = ['price', 'prompt_price', ...MEDIA_PRICING_TOKEN_PRICES] as const

/**
 * Explicit nulls are unset for validation purposes, and the visual model spells
 * "unset" as an absent key. Dropping them here keeps the emitted JSON free of
 * nulls that the editor never asked for; every real value, including false and
 * 0, is preserved. Unknown keys are left alone so nothing is silently lost.
 */
function normalize(value: MediaPricing): MediaPricing {
  const drop = (target: Record<string, unknown>, key: string) => {
    if (target[key] === null) delete target[key]
  }
  for (const key of ['extra_input_image_price', 'free_input_images', 'duration_source']) drop(value as unknown as Record<string, unknown>, key)
  for (const rule of value.rules as unknown as Record<string, unknown>[]) {
    if (!isPlainObject(rule)) continue
    for (const key of PRICE_KEYS) drop(rule, key)
    if (rule.name === null) drop(rule, 'name')
    const conditions = rule.conditions
    if (!isPlainObject(conditions)) {
      drop(rule, 'conditions')
      continue
    }
    for (const key of [...MEDIA_PRICING_STRING_CONDITIONS, ...MEDIA_PRICING_BOOLEAN_CONDITIONS, ...MEDIA_PRICING_NUMERIC_CONDITIONS]) drop(conditions, key)
  }
  return value
}

const state = ref<DraftState>({ kind: 'empty' })
const ruleIds = ref<number[]>([])
const openRules = ref<boolean[]>([])
/** In-flight text for numeric inputs, so "0." and "1.20" are not rewritten mid-typing. */
const numberDrafts = ref<Record<string, string>>({})
const invalidDrafts = ref<Record<string, boolean>>({})
const confirmingLastRule = ref(false)
let nextRuleId = 1
let lastEmitted: string | null = null

const rawText = computed(() => typeof props.modelValue === 'string' ? props.modelValue : mediaPricingToForm(props.modelValue))
const error = computed(() => validateMediaPricing(props.modelValue, t))
const model = computed(() => state.value.kind === 'object' ? state.value.value : null)
const isEmptyState = computed(() => state.value.kind === 'empty')
const unrenderable = computed(() => state.value.kind === 'unrenderable' ? state.value.reason : null)

function applyState(next: DraftState) {
  state.value = next
  numberDrafts.value = {}
  invalidDrafts.value = {}
  if (next.kind !== 'object') {
    ruleIds.value = []
    openRules.value = []
    return
  }
  // Keep the existing ids while the rule count is unchanged so open/typing state stays attached.
  if (ruleIds.value.length === next.value.rules.length) return
  ruleIds.value = next.value.rules.map(() => nextRuleId++)
  openRules.value = next.value.rules.map((_, index) => index === 0)
}

watch(rawText, (text) => {
  if (text === lastEmitted) return
  applyState(readDraft(text))
}, { immediate: true })

function commit(next: MediaPricing) {
  const text = mediaPricingToForm(next)
  lastEmitted = text
  state.value = { kind: 'object', value: next }
  emit('update:modelValue', text)
}

function cloneModel(): MediaPricing {
  const current = model.value
  const base = current ? JSON.parse(JSON.stringify(current)) as MediaPricing : { rules: [] } as MediaPricing
  if (!Array.isArray(base.rules)) base.rules = []
  return base
}

function clearKey(target: object, key: string) {
  delete (target as Record<string, unknown>)[key]
}

function dropDrafts(predicate: (key: string) => boolean) {
  const drafts = { ...numberDrafts.value }
  for (const key of Object.keys(drafts)) if (predicate(key)) delete drafts[key]
  numberDrafts.value = drafts
  const invalid = { ...invalidDrafts.value }
  for (const key of Object.keys(invalid)) if (predicate(key)) delete invalid[key]
  invalidDrafts.value = invalid
}

function numberText(draftKey: string, value: unknown): string {
  const draft = numberDrafts.value[draftKey]
  if (draft !== undefined) return draft
  return value == null ? '' : String(value)
}

function isInvalid(draftKey: string): boolean {
  return !!invalidDrafts.value[draftKey]
}

/**
 * Only a complete, non-negative number is committed. Anything else is rejected
 * outright (never coerced) so a visible input can never disagree with the stored value.
 */
type NumberRead = { kind: 'unset' } | { kind: 'invalid' } | { kind: 'value'; value: number }

const DECIMAL_PATTERN = /^\+?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/
const INTEGER_PATTERN = /^\+?\d+$/

function readNumber(text: string, integer = false): NumberRead {
  const trimmed = text.trim()
  if (trimmed === '') return { kind: 'unset' }
  if (!(integer ? INTEGER_PATTERN : DECIMAL_PATTERN).test(trimmed)) return { kind: 'invalid' }
  const value = Number(trimmed)
  if (!Number.isFinite(value) || value < 0) return { kind: 'invalid' }
  if (integer && !Number.isSafeInteger(value)) return { kind: 'invalid' }
  return { kind: 'value', value }
}

function rejectNumber(input: HTMLInputElement, draftKey: string, committed: unknown) {
  dropDrafts(key => key === draftKey)
  invalidDrafts.value = { ...invalidDrafts.value, [draftKey]: true }
  input.value = committed == null ? '' : String(committed)
}

function acceptDraft(draftKey: string, text: string) {
  numberDrafts.value = { ...numberDrafts.value, [draftKey]: text }
  if (!invalidDrafts.value[draftKey]) return
  const invalid = { ...invalidDrafts.value }
  delete invalid[draftKey]
  invalidDrafts.value = invalid
}

function onTopNumber(event: Event, key: 'extra_input_image_price' | 'free_input_images') {
  const input = event.target as HTMLInputElement
  const draftKey = `top:${key}`
  const read = readNumber(input.value, key === 'free_input_images')
  const committed = key === 'extra_input_image_price' ? model.value?.extra_input_image_price : model.value?.free_input_images
  if (read.kind === 'invalid') {
    rejectNumber(input, draftKey, committed)
    return
  }
  acceptDraft(draftKey, input.value)
  const base = cloneModel()
  if (read.kind === 'unset') clearKey(base, key)
  else (base as unknown as Record<string, unknown>)[key] = read.value
  commit(base)
}

function onDurationSource(event: Event) {
  const raw = (event.target as HTMLSelectElement).value
  const base = cloneModel()
  if (raw === 'output' || raw === 'total') base.duration_source = raw
  else clearKey(base, 'duration_source')
  commit(base)
}

function mutateRule(index: number, mutate: (rule: MediaPricingRule) => void) {
  const base = cloneModel()
  const rule = base.rules[index]
  if (!rule) return
  mutate(rule)
  commit(base)
}

function mutateConditions(index: number, mutate: (conditions: MediaPricingConditions) => void) {
  mutateRule(index, (rule) => {
    // An explicitly present conditions object is kept even when it ends up empty:
    // {} and an absent key mean the same thing, so the editor does not rewrite it.
    const existed = isPlainObject(rule.conditions)
    const conditions: MediaPricingConditions = existed ? { ...rule.conditions } : {}
    mutate(conditions)
    if (existed || Object.keys(conditions).length > 0) rule.conditions = conditions
    else clearKey(rule, 'conditions')
  })
}

function onRuleName(event: Event, index: number) {
  const text = (event.target as HTMLInputElement).value
  mutateRule(index, (rule) => {
    if (text.trim() === '') clearKey(rule, 'name')
    else rule.name = text
  })
}

function onRuleNumber(event: Event, index: number, key: RuleNumberKey) {
  const input = event.target as HTMLInputElement
  const draftKey = `${ruleIds.value[index]}:${key}`
  const read = readNumber(input.value)
  const committed = model.value?.rules[index]?.[key]
  if (read.kind === 'invalid') {
    rejectNumber(input, draftKey, committed)
    return
  }
  numberDrafts.value = { ...numberDrafts.value, [draftKey]: input.value }
  if (invalidDrafts.value[draftKey]) {
    const invalid = { ...invalidDrafts.value }
    delete invalid[draftKey]
    invalidDrafts.value = invalid
  }
  mutateRule(index, (rule) => {
    if (read.kind === 'unset') clearKey(rule, key)
    else (rule as unknown as Record<string, unknown>)[key] = read.value
  })
}

function onStringCondition(event: Event, index: number, key: StringConditionKey) {
  const text = (event.target as HTMLInputElement).value
  mutateConditions(index, (conditions) => {
    const trimmed = text.trim()
    if (trimmed === '') clearKey(conditions, key)
    else conditions[key] = trimmed
  })
}

function onBooleanCondition(event: Event, index: number, key: BooleanConditionKey) {
  const raw = (event.target as HTMLSelectElement).value
  mutateConditions(index, (conditions) => {
    // Unset, true and false are three different states; false must survive.
    if (raw === '') clearKey(conditions, key)
    else conditions[key] = raw === 'true'
  })
}

function onNumericCondition(event: Event, index: number, key: NumericConditionKey) {
  const input = event.target as HTMLInputElement
  const draftKey = `${ruleIds.value[index]}:cond:${key}`
  const integer = key.endsWith('pixels')
  const read = readNumber(input.value, integer)
  const committed = model.value?.rules[index]?.conditions?.[key]
  if (read.kind === 'invalid') {
    rejectNumber(input, draftKey, committed)
    return
  }
  numberDrafts.value = { ...numberDrafts.value, [draftKey]: input.value }
  if (invalidDrafts.value[draftKey]) {
    const invalid = { ...invalidDrafts.value }
    delete invalid[draftKey]
    invalidDrafts.value = invalid
  }
  mutateConditions(index, (conditions) => {
    if (read.kind === 'unset') clearKey(conditions, key)
    else conditions[key] = read.value
  })
}

function onUnit(event: Event, index: number) {
  const unit = (event.target as HTMLSelectElement).value as MediaPricingUnit
  const ruleId = ruleIds.value[index]
  mutateRule(index, (rule) => {
    rule.unit = unit
    // price and the token prices are mutually exclusive; keep the config valid.
    if (unit === 'token') clearKey(rule, 'price')
    else for (const key of MEDIA_PRICING_TOKEN_PRICES) clearKey(rule, key)
  })
  dropDrafts(key => key.startsWith(`${ruleId}:`) && !key.startsWith(`${ruleId}:cond:`))
}

function addRule() {
  confirmingLastRule.value = false
  const base = cloneModel()
  base.rules.push({ unit: 'image' })
  ruleIds.value = [...ruleIds.value, nextRuleId++]
  openRules.value = [...openRules.value, true]
  commit(base)
}

function removeRule(index: number) {
  const base = cloneModel()
  if (!base.rules[index]) return
  // Removing the last rule disables media pricing entirely, so it takes a second click.
  if (base.rules.length === 1) {
    if (!confirmingLastRule.value) {
      confirmingLastRule.value = true
      return
    }
    clearPricing()
    return
  }
  confirmingLastRule.value = false
  const removedId = ruleIds.value[index]
  base.rules.splice(index, 1)
  ruleIds.value = ruleIds.value.filter((_, position) => position !== index)
  openRules.value = openRules.value.filter((_, position) => position !== index)
  dropDrafts(key => key.startsWith(`${removedId}:`))
  commit(base)
}

function moveRule(index: number, delta: number) {
  const target = index + delta
  const base = cloneModel()
  if (target < 0 || target >= base.rules.length) return
  const [rule] = base.rules.splice(index, 1)
  base.rules.splice(target, 0, rule)
  const ids = [...ruleIds.value]
  const [id] = ids.splice(index, 1)
  ids.splice(target, 0, id)
  ruleIds.value = ids
  const open = [...openRules.value]
  const [flag] = open.splice(index, 1)
  open.splice(target, 0, flag)
  openRules.value = open
  commit(base)
}

function toggleRule(index: number) {
  // Any other interaction clears the destructive-confirm state.
  confirmingLastRule.value = false
  const open = [...openRules.value]
  open[index] = !open[index]
  openRules.value = open
}

function clearPricing() {
  confirmingLastRule.value = false
  lastEmitted = ''
  applyState({ kind: 'empty' })
  emit('update:modelValue', '')
}

function onJsonInput(event: Event) {
  const text = (event.target as HTMLTextAreaElement).value
  // The raw draft always wins here: invalid text is kept, never dropped.
  lastEmitted = text
  applyState(readDraft(text))
  emit('update:modelValue', text)
}

function loadExample(example: MediaPricing) {
  const value = JSON.parse(JSON.stringify(example)) as MediaPricing
  const text = mediaPricingToForm(value)
  lastEmitted = text
  applyState({ kind: 'object', value })
  emit('update:modelValue', text)
}

function booleanValue(rule: MediaPricingRule, key: BooleanConditionKey): string {
  const value = rule.conditions ? rule.conditions[key] : undefined
  if (value === true) return 'true'
  if (value === false) return 'false'
  return ''
}

function hasTokenPrice(rule: MediaPricingRule): boolean {
  return MEDIA_PRICING_TOKEN_PRICES.some(key => rule[key] != null)
}

function conditionCount(rule: MediaPricingRule): number {
  const conditions = rule.conditions
  if (!conditions) return 0
  return Object.values(conditions).filter(value => value != null && value !== '').length
}

function rangeWarnings(index: number): string[] {
  const conditions = model.value?.rules[index]?.conditions
  if (!conditions) return []
  const warnings: string[] = []
  for (const [minKey, maxKey] of MEDIA_PRICING_RANGE_PAIRS) {
    const min = conditions[minKey]
    const max = conditions[maxKey]
    if (typeof min === 'number' && typeof max === 'number' && min > max) {
      warnings.push(t('mediaPricing.visual.rangeMinExceedsMax', {
        min: t(`mediaPricing.conditions.${minKey}`),
        max: t(`mediaPricing.conditions.${maxKey}`),
      }))
    }
  }
  const fps = conditions.fps
  if (typeof fps === 'number' &&
      ((typeof conditions.min_fps === 'number' && fps < conditions.min_fps) ||
       (typeof conditions.max_fps === 'number' && fps > conditions.max_fps))) {
    warnings.push(t('mediaPricing.visual.fpsOutsideRange'))
  }
  return warnings
}
</script>
