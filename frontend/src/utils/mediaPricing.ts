import type { MediaPricing, MediaPricingConditions, MediaPricingRule } from '@/types/mediaPricing'

type TranslateFn = (key: string, params?: Record<string, unknown>) => string
export type MediaPricingFormValue = MediaPricing | string | null | undefined

export const MEDIA_PRICING_UNITS = ['image', 'second', 'frame', 'request', 'token'] as const
export const MEDIA_PRICING_TOKEN_PRICES = ['input_price', 'output_price', 'image_input_price', 'image_output_price'] as const
const units = MEDIA_PRICING_UNITS as readonly string[]
const tokenPrices = MEDIA_PRICING_TOKEN_PRICES
const prices = ['price', ...tokenPrices, 'prompt_price'] as const
export const MEDIA_PRICING_STRING_CONDITIONS = ['resolution', 'mode', 'input_type'] as const
export const MEDIA_PRICING_BOOLEAN_CONDITIONS = ['audio', 'reference_video', 'layer'] as const
export const MEDIA_PRICING_NUMERIC_CONDITIONS = ['min_pixels', 'max_pixels', 'min_duration', 'max_duration', 'fps', 'min_fps', 'max_fps'] as const
/** Boundary pairs whose lower bound may not exceed the upper bound. */
export const MEDIA_PRICING_RANGE_PAIRS = [['min_pixels', 'max_pixels'], ['min_duration', 'max_duration'], ['min_fps', 'max_fps']] as const
const stringConditions = MEDIA_PRICING_STRING_CONDITIONS
const booleanConditions = MEDIA_PRICING_BOOLEAN_CONDITIONS
const numericConditions = MEDIA_PRICING_NUMERIC_CONDITIONS
const conditionKeys = [...stringConditions, ...booleanConditions, ...numericConditions]

function isObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

/** Keep the draft in the parent form, including invalid JSON, so save cannot silently use stale rules. */
export function mediaPricingToForm(value: MediaPricing | null | undefined): string {
  return value == null ? '' : JSON.stringify(value, null, 2)
}

function parse(value: MediaPricingFormValue): unknown {
  if (typeof value !== 'string') return value ?? null
  return value.trim() === '' ? null : JSON.parse(value)
}

export function validateMediaPricing(value: MediaPricingFormValue, t: TranslateFn): string | null {
  const error = (key: string, field = 'media_pricing') => t(`mediaPricing.validation.${key}`, { field })
  let parsed: unknown
  try { parsed = parse(value) } catch { return error('json') }
  if (parsed == null) return null
  if (!isObject(parsed) ||
      !Array.isArray(parsed.rules) || parsed.rules.length === 0) return error('rules')
  for (const key of Object.keys(parsed)) {
    if (!['rules', 'extra_input_image_price', 'free_input_images', 'duration_source'].includes(key)) return error('unknown', key)
  }
  const extra = parsed.extra_input_image_price
  if (extra != null && (typeof extra !== 'number' || !Number.isFinite(extra) || extra < 0)) return error('price', 'extra_input_image_price')
  const free = parsed.free_input_images
  if (free != null && (typeof free !== 'number' || !Number.isSafeInteger(free) || free < 0)) return error('inputCount', 'free_input_images')
  if (parsed.duration_source != null && !['', 'output', 'total'].includes(parsed.duration_source as string)) return error('durationSource', 'duration_source')
  for (const [index, rule] of parsed.rules.entries()) {
    const path = `rules[${index}]`
    if (!isObject(rule)) return error('rule', path)
    for (const key of Object.keys(rule)) {
      if (!['name', 'unit', 'conditions', ...prices].includes(key)) return error('unknown', `${path}.${key}`)
    }
    if (rule.name != null && typeof rule.name !== 'string') return error('string', `${path}.name`)
    if (!units.includes(rule.unit as string)) return error('unit', `${path}.unit`)
    if (parsed.duration_source === 'total' && rule.unit !== 'second') return error('durationUnit', `${path}.unit`)
    for (const key of prices) {
      const price = rule[key]
      if (price != null && (typeof price !== 'number' || !Number.isFinite(price) || price < 0)) return error('price', `${path}.${key}`)
    }
    if (rule.unit === 'token') {
      if (rule.price != null) return error('unitPrices', `${path}.price`)
      if (!tokenPrices.some(key => rule[key] != null)) return error('tokenPrice', path)
    } else {
      if (rule.price == null) return error('directPrice', `${path}.price`)
      if (tokenPrices.some(key => rule[key] != null)) return error('unitPrices', path)
    }
    const conditions = rule.conditions
    if (conditions == null) continue
    if (!isObject(conditions)) return error('conditions', `${path}.conditions`)
    for (const key of Object.keys(conditions)) {
      if (!(conditionKeys as readonly string[]).includes(key)) return error('unknown', `${path}.conditions.${key}`)
    }
    for (const key of stringConditions) {
      if (conditions[key] != null && typeof conditions[key] !== 'string') return error('string', `${path}.conditions.${key}`)
    }
    for (const key of booleanConditions) {
      if (conditions[key] != null && typeof conditions[key] !== 'boolean') return error('boolean', `${path}.conditions.${key}`)
    }
    for (const key of numericConditions) {
      const number = conditions[key]
      if (number == null) continue
      if (typeof number !== 'number' || !Number.isFinite(number) || number < 0 ||
          (key.endsWith('pixels') && !Number.isSafeInteger(number))) return error('number', `${path}.conditions.${key}`)
    }
    for (const [minKey, maxKey] of [['min_pixels', 'max_pixels'], ['min_duration', 'max_duration'], ['min_fps', 'max_fps']]) {
      if (conditions[minKey] != null && conditions[maxKey] != null &&
          (conditions[minKey] as number) > (conditions[maxKey] as number)) return error('range', `${path}.conditions.${minKey}/${maxKey}`)
    }
    if (conditions.fps != null &&
        ((conditions.min_fps != null && (conditions.fps as number) < (conditions.min_fps as number)) ||
         (conditions.max_fps != null && (conditions.fps as number) > (conditions.max_fps as number)))) return error('range', `${path}.conditions.fps`)
  }
  return null
}

/** No MTok conversion or currency conversion inside media rules. Invalid drafts must never be dropped. */
export function formMediaPricingToAPI(value: MediaPricingFormValue): MediaPricing | null {
  const error = validateMediaPricing(value, key => key)
  if (error) throw new Error(error)
  const parsed = parse(value)
  return parsed == null ? null : JSON.parse(JSON.stringify(parsed)) as MediaPricing
}

export const MEDIA_PRICING_EXAMPLES: Record<'image' | 'video' | 'referenceVideo' | 'token', MediaPricing> = {
  image: { extra_input_image_price: 0.02, free_input_images: 1, rules: [
    { name: 'pixel / layer', unit: 'image', conditions: { min_pixels: 0, max_pixels: 4194304, layer: false }, price: 0.12, prompt_price: 0.01 },
    { name: 'larger output / layer', unit: 'image', conditions: { min_pixels: 4194305, layer: false }, price: 0.24, prompt_price: 0.01 },
    { name: 'layered output', unit: 'image', conditions: { layer: true }, price: 0.18, prompt_price: 0.01 },
  ] },
  video: { rules: [
    { name: '720p silent', unit: 'second', conditions: { resolution: '720p', mode: 'standard', input_type: 'text', audio: false, reference_video: false, fps: 24, min_duration: 0, max_duration: 10 }, price: 0.2 },
    { name: '1080p audio', unit: 'frame', conditions: { resolution: '1080p', audio: true, reference_video: true, min_fps: 24, max_fps: 60 }, price: 0.01 },
  ] },
  token: { rules: [
    { name: 'multimodal tokens', unit: 'token', conditions: {}, input_price: 0.000002, output_price: 0.000008, image_input_price: 0.000003, image_output_price: 0.00001, prompt_price: 0.01 },
  ] },
  referenceVideo: { duration_source: 'total', extra_input_image_price: 0.2, free_input_images: 5, rules: [
    { name: 'total video seconds', unit: 'second', conditions: { resolution: '2k' }, price: 0.5 },
  ] },
}

/** Request-level charges are shown separately so they cannot look per-output. */
export function mediaRequestLabels(pricing: MediaPricing, multiplier: number, t: TranslateFn): string[] {
  const labels: string[] = []
  if (pricing.extra_input_image_price != null) {
    labels.push(t('mediaPricing.inputSurcharge', {
      count: pricing.free_input_images ?? 0,
      price: String(Number((pricing.extra_input_image_price * multiplier).toPrecision(12))),
    }))
  } else if (pricing.free_input_images != null) {
    labels.push(t('mediaPricing.inputAllowanceOnly', { count: pricing.free_input_images }))
  }
  if (pricing.rules.some(rule => rule.unit === 'second')) {
    labels.push(t(`mediaPricing.durationSources.${pricing.duration_source || 'output'}`))
  }
  return labels
}

/** Human-readable conditions retain explicit false and zero and disclose every configured dimension. */
export function mediaConditionLabels(conditions: MediaPricingConditions | undefined, t: TranslateFn): string[] {
  return Object.entries(conditions || {}).filter(([, value]) => value != null && value !== '').map(([key, value]) =>
    `${t(`mediaPricing.conditions.${key}`)}: ${typeof value === 'boolean' ? t(`mediaPricing.${value ? 'yes' : 'no'}`) : value}`,
  )
}

export function mediaPriceLabels(rule: MediaPricingRule, multiplier: number, t: TranslateFn): string[] {
  const amount = (value: number) => String(Number((value * multiplier).toPrecision(12)))
  const unit = t(`mediaPricing.units.${rule.unit}`)
  const labels: string[] = []
  if (rule.unit !== 'token' && rule.price != null) labels.push(`${amount(rule.price)} / ${unit}`)
  if (rule.unit === 'token') {
    for (const key of tokenPrices) {
      if (rule[key] != null) labels.push(`${t(`mediaPricing.prices.${key}`)} ${amount(rule[key]!)} / ${unit}`)
    }
  }
  if (rule.prompt_price != null) labels.push(`${t('mediaPricing.prices.prompt_price')} +${amount(rule.prompt_price)} / ${t('mediaPricing.units.request')}`)
  return labels
}
