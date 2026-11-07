import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import MediaPricingEditor from '../MediaPricingEditor.vue'
import { MEDIA_PRICING_EXAMPLES, formMediaPricingToAPI, mediaPricingToForm, validateMediaPricing } from '@/utils/mediaPricing'
import { mediaPricingFixture, referenceVideoPricingFixture } from '@/__tests__/fixtures/mediaPricing'
import type { MediaPricing } from '@/types/mediaPricing'

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))

const t = (key: string) => key

/** Feed every emitted draft back into props, exactly like PricingEntryCard does. */
async function mountEditor(value: MediaPricing | string | null) {
  const wrapper = mount(MediaPricingEditor, { props: { modelValue: value } })
  const sync = async () => {
    const emitted = wrapper.emitted('update:modelValue')
    if (!emitted?.length) return
    await wrapper.setProps({ modelValue: emitted.at(-1)![0] as string })
  }
  const last = () => wrapper.emitted('update:modelValue')!.at(-1)![0] as string
  const lastValue = () => formMediaPricingToAPI(last())
  /** Raw draft, for intermediate states that are not yet a valid submission. */
  const lastJson = <T = Record<string, unknown>>() => JSON.parse(last()) as T
  const type = async (testid: string, text: string) => {
    await wrapper.get(`[data-testid="${testid}"]`).setValue(text)
    await sync()
  }
  const select = async (testid: string, option: string) => {
    await wrapper.get(`[data-testid="${testid}"]`).setValue(option)
    await sync()
  }
  return { wrapper, sync, last, lastValue, lastJson, type, select, emitted: () => wrapper.emitted('update:modelValue') ?? [] }
}

describe('media pricing visual editor equivalence', () => {
  it.each([
    ['fixture', mediaPricingFixture],
    ['reference video', referenceVideoPricingFixture],
    ...Object.entries(MEDIA_PRICING_EXAMPLES),
  ])('roundtrips %s through the visual model without changing the wire value', async (_label, fixture) => {
    const { wrapper, lastValue, last, select } = await mountEditor(fixture as MediaPricing)
    try {
      // Re-selecting the current unit rebuilds the whole document from the visual
      // model and re-emits it, which is what every real edit does.
      const units = Array.from(wrapper.findAll<HTMLSelectElement>('[data-testid$="-unit"]'))
      expect(units).toHaveLength((fixture as MediaPricing).rules.length)
      for (const [index, unit] of ((fixture as MediaPricing).rules).entries()) {
        await select(`media-pricing-rule-${index}-unit`, unit.unit)
      }
      expect(validateMediaPricing(last(), t)).toBeNull()
      expect(lastValue()).toEqual(fixture)
    } finally { wrapper.unmount() }
  })

  it('keeps every rule field reachable and never renders null for an unset field', async () => {
    const draft = '{"rules":[{"unit":"image","price":null,"conditions":{"audio":null,"max_pixels":null,"resolution":null}}]}'
    const { wrapper, last, lastJson, select } = await mountEditor(draft)
    try {
      await select('media-pricing-rule-0-unit', 'image')
      // Explicit nulls are the same state as "unset", so the emitted draft has none.
      expect(last()).not.toContain('null')
      expect(lastJson()).toEqual({ rules: [{ unit: 'image', conditions: {} }] })
    } finally { wrapper.unmount() }
  })

  it('preserves explicit false, explicit zero and declaration order', async () => {
    const { wrapper, lastValue, select } = await mountEditor(mediaPricingFixture)
    try {
      await select('media-pricing-rule-0-unit', 'image')
      const value = lastValue()!
      expect(value.rules[0].conditions).toEqual({ min_pixels: 0, max_pixels: 4194304, layer: false })
      expect(value.rules[2].conditions).toEqual({ audio: true, reference_video: true, layer: true })
      expect(value.rules[2].price).toBe(0)
      expect(value.rules[4].image_input_price).toBe(0)
      expect(value.rules.map(rule => rule.name)).toEqual(mediaPricingFixture.rules.map(rule => rule.name))
      expect(value.extra_input_image_price).toBe(0.02)
      expect(value.free_input_images).toBe(1)
      expect(value.duration_source).toBe('output')
    } finally { wrapper.unmount() }
  })

  it('shows every declared condition for a rule so nothing is silently dropped', async () => {
    const { wrapper } = await mountEditor(mediaPricingFixture)
    try {
      await wrapper.get('[data-testid="media-pricing-rule-1-toggle"]').trigger('click')
      const expected = wrapper.get('[data-testid="media-pricing-rule-1-cond-resolution"]')
      expect((expected.element as HTMLInputElement).value).toBe('720p')
      expect((wrapper.get('[data-testid="media-pricing-rule-1-cond-mode"]').element as HTMLInputElement).value).toBe('standard')
      expect((wrapper.get('[data-testid="media-pricing-rule-1-cond-input_type"]').element as HTMLInputElement).value).toBe('text')
      expect((wrapper.get('[data-testid="media-pricing-rule-1-cond-audio"]').element as HTMLSelectElement).value).toBe('false')
      expect((wrapper.get('[data-testid="media-pricing-rule-1-cond-reference_video"]').element as HTMLSelectElement).value).toBe('false')
      expect((wrapper.get('[data-testid="media-pricing-rule-1-cond-layer"]').element as HTMLSelectElement).value).toBe('')
      expect((wrapper.get('[data-testid="media-pricing-rule-1-cond-min_duration"]').element as HTMLInputElement).value).toBe('0')
      expect((wrapper.get('[data-testid="media-pricing-rule-1-cond-max_duration"]').element as HTMLInputElement).value).toBe('10')
      expect((wrapper.get('[data-testid="media-pricing-rule-1-cond-fps"]').element as HTMLInputElement).value).toBe('24')
      expect((wrapper.get('[data-testid="media-pricing-rule-1-cond-min_fps"]').element as HTMLInputElement).value).toBe('24')
      expect((wrapper.get('[data-testid="media-pricing-rule-1-cond-max_fps"]').element as HTMLInputElement).value).toBe('60')
      expect((wrapper.get('[data-testid="media-pricing-rule-0-cond-min_pixels"]').element as HTMLInputElement).value).toBe('0')
      expect((wrapper.get('[data-testid="media-pricing-rule-0-cond-max_pixels"]').element as HTMLInputElement).value).toBe('4194304')
      expect(wrapper.get('[data-testid="media-pricing-duration-source"]').element as HTMLSelectElement).toHaveProperty('value', 'output')
      expect((wrapper.get('[data-testid="media-pricing-extra-input-image-price"]').element as HTMLInputElement).value).toBe('0.02')
      expect((wrapper.get('[data-testid="media-pricing-free-input-images"]').element as HTMLInputElement).value).toBe('1')
    } finally { wrapper.unmount() }
  })
})

/**
 * Hand-written wire JSON covering all 13 conditions, all 6 price fields and all
 * three top-level fields. Written verbatim as an operator would type it, never
 * produced by the editor, so equivalence is measured against an independent source.
 */
const handWritten = {
  rules: [
    {
      name: 'everything image', unit: 'image', price: 0.12, prompt_price: 0.01,
      conditions: {
        resolution: '4k', mode: 'standard', input_type: 'text',
        audio: false, reference_video: true, layer: false,
        min_pixels: 0, max_pixels: 4194304, min_duration: 0, max_duration: 10,
        fps: 24, min_fps: 24, max_fps: 60,
      },
    },
    {
      name: 'everything token', unit: 'token',
      input_price: 0.000002, output_price: 0.000008,
      image_input_price: 0, image_output_price: 0.00001, prompt_price: 0.02,
      conditions: {},
    },
  ],
  extra_input_image_price: 0.02,
  free_input_images: 1,
  duration_source: 'output',
} as const

describe('media pricing hand-written equivalence', () => {
  it('survives edits on every dimension and still equals the hand-written JSON', async () => {
    const { wrapper, last, lastValue, emitted } = await mountEditor(JSON.stringify(handWritten))
    try {
      // Walk every control the operator can reach and re-commit its current value.
      const write = async (testid: string, value: string) => {
        await wrapper.get(`[data-testid="${testid}"]`).setValue(value)
        await wrapper.setProps({ modelValue: wrapper.emitted('update:modelValue')!.at(-1)![0] as string })
        // The control shows exactly what the operator typed; nothing is rewritten.
        expect((wrapper.get(`[data-testid="${testid}"]`).element as HTMLInputElement).value).toBe(value)
      }
      const conditions: Record<string, string | number | boolean> = handWritten.rules[0].conditions
      for (const key of ['resolution', 'mode', 'input_type']) await write(`media-pricing-rule-0-cond-${key}`, String(conditions[key]))
      for (const key of ['audio', 'reference_video', 'layer']) await write(`media-pricing-rule-0-cond-${key}`, String(conditions[key]))
      for (const key of ['min_pixels', 'max_pixels', 'min_duration', 'max_duration', 'fps', 'min_fps', 'max_fps']) {
        await write(`media-pricing-rule-0-cond-${key}`, String(conditions[key]))
      }
      await write('media-pricing-rule-0-name', 'everything image')
      await write('media-pricing-rule-0-price', '0.12')
      await write('media-pricing-rule-0-prompt_price', '0.01')
      await write('media-pricing-rule-1-input_price', '0.000002')
      await write('media-pricing-rule-1-output_price', '0.000008')
      await write('media-pricing-rule-1-image_input_price', '0')
      await write('media-pricing-rule-1-image_output_price', '0.00001')
      await write('media-pricing-rule-1-prompt_price', '0.02')
      await write('media-pricing-extra-input-image-price', '0.02')
      await write('media-pricing-free-input-images', '1')
      await write('media-pricing-duration-source', 'output')

      expect(validateMediaPricing(last(), t)).toBeNull()
      expect(lastValue()).toEqual(handWritten)
      expect(JSON.parse(last())).toEqual(handWritten)
      // 15 condition controls + 6 price controls + 3 top-level + 2 selects were exercised.
      expect(emitted().length).toBeGreaterThan(20)
    } finally { wrapper.unmount() }
  })
})

describe('media pricing tri-state boolean conditions', () => {
  it.each([
    ['unset', '', undefined],
    ['yes', 'true', true],
    ['no', 'false', false],
  ] as const)('%s writes %s and only that state', async (_label, option, expected) => {
    const { wrapper, lastValue, select } = await mountEditor({ rules: [{ unit: 'image', price: 0.1, conditions: {} }] })
    try {
      await select('media-pricing-rule-0-cond-audio', option)
      const conditions = lastValue()!.rules[0].conditions ?? {}
      expect(conditions.audio).toBe(expected)
      expect('audio' in conditions).toBe(expected !== undefined)
      // The other two booleans stay unset.
      expect('reference_video' in conditions).toBe(false)
      expect('layer' in conditions).toBe(false)
    } finally { wrapper.unmount() }
  })

  it('distinguishes an empty conditions object from an explicit false', async () => {
    const absent = await mountEditor({ rules: [{ unit: 'image', price: 0.1, conditions: {} }] })
    const explicit = await mountEditor({ rules: [{ unit: 'image', price: 0.1, conditions: { audio: false } }] })
    try {
      // Selecting the same "no" value the rule already has keeps false, and a rule
      // with only unset booleans keeps its empty conditions object untouched.
      await absent.select('media-pricing-rule-0-unit', 'image')
      await explicit.select('media-pricing-rule-0-unit', 'image')
      expect(absent.lastValue()!.rules[0].conditions).toEqual({})
      expect(explicit.lastValue()!.rules[0].conditions).toEqual({ audio: false })
      expect(absent.lastValue()).not.toEqual(explicit.lastValue())
    } finally {
      absent.wrapper.unmount()
      explicit.wrapper.unmount()
    }
  })
})

describe('media pricing unit price fields are mutually exclusive', () => {
  it('drops the token prices when a token rule becomes a per-unit rule', async () => {
    const { wrapper, lastJson, select } = await mountEditor({ rules: [{ unit: 'token', input_price: 0.000002, output_price: 0.000008 }] })
    try {
      await select('media-pricing-rule-0-unit', 'second')
      const rule = lastJson<{ rules: Array<Record<string, unknown>> }>().rules[0]
      expect(rule.unit).toBe('second')
      expect('input_price' in rule).toBe(false)
      expect('output_price' in rule).toBe(false)
      expect(wrapper.find('[data-testid="media-pricing-rule-0-price"]').exists()).toBe(true)
      expect(wrapper.find('[data-testid="media-pricing-rule-0-input_price"]').exists()).toBe(false)
      // Without a price the draft is blocked rather than silently saved.
      expect(wrapper.get('[data-testid="media-pricing-validation"]').text()).toBe('mediaPricing.validation.directPrice')
      await expect(async () => formMediaPricingToAPI(wrapper.emitted('update:modelValue')!.at(-1)![0] as string)).rejects.toThrow()
    } finally { wrapper.unmount() }
  })

  it('drops the per-unit price and demands a token price when a rule becomes token', async () => {
    const { wrapper, lastJson, select } = await mountEditor({ rules: [{ unit: 'image', price: 0.12, prompt_price: 0.01 }] })
    try {
      await select('media-pricing-rule-0-unit', 'token')
      const rule = lastJson<{ rules: Array<Record<string, unknown>> }>().rules[0]
      expect(rule.unit).toBe('token')
      expect('price' in rule).toBe(false)
      expect(rule.prompt_price).toBe(0.01)
      for (const key of ['input_price', 'output_price', 'image_input_price', 'image_output_price']) {
        expect(wrapper.find(`[data-testid="media-pricing-rule-0-${key}"]`).exists()).toBe(true)
      }
      expect(wrapper.find('[data-testid="media-pricing-rule-0-price"]').exists()).toBe(false)
      expect(wrapper.get('[data-testid="media-pricing-validation"]').text()).toBe('mediaPricing.validation.tokenPrice')
    } finally { wrapper.unmount() }
  })

  it('writes the four token prices and keeps zero', async () => {
    const { wrapper, lastValue, type } = await mountEditor({ rules: [{ unit: 'token', input_price: 0 }] })
    try {
      await type('media-pricing-rule-0-output_price', '0.000008')
      await type('media-pricing-rule-0-image_input_price', '0')
      await type('media-pricing-rule-0-image_output_price', '0.00001')
      expect(lastValue()).toEqual({
        rules: [{ unit: 'token', input_price: 0, output_price: 0.000008, image_input_price: 0, image_output_price: 0.00001 }],
      })
    } finally { wrapper.unmount() }
  })
})

describe('media pricing rejects values it cannot represent', () => {
  it.each([
    ['negative price', 'media-pricing-rule-0-price', '-1'],
    ['text price', 'media-pricing-rule-0-price', 'abc'],
    ['fractional pixels', 'media-pricing-rule-0-cond-min_pixels', '1.5'],
    ['negative duration', 'media-pricing-rule-0-cond-max_duration', '-0.5'],
  ])('refuses %s instead of coercing it', async (_label, testid, text) => {
    const { wrapper, type, emitted } = await mountEditor({ rules: [{ unit: 'image', price: 0.1, conditions: { min_pixels: 0, max_duration: 5 } }] })
    try {
      const committed = (wrapper.get(`[data-testid="${testid}"]`).element as HTMLInputElement).value
      await type(testid, text)
      expect((wrapper.get(`[data-testid="${testid}"]`).element as HTMLInputElement).value).toBe(committed)
      expect(emitted()).toHaveLength(0)
      expect(wrapper.find('[class*="text-red-500"]').exists()).toBe(true)
      expect(validateMediaPricing(wrapper.props('modelValue'), t)).toBeNull()
    } finally { wrapper.unmount() }
  })

  it('accepts zero as a real value everywhere', async () => {
    const { wrapper, type, lastValue } = await mountEditor({ rules: [{ unit: 'image', price: 0.1, conditions: { min_pixels: 1 } }] })
    try {
      await type('media-pricing-rule-0-price', '0')
      await type('media-pricing-rule-0-cond-min_pixels', '0')
      await type('media-pricing-extra-input-image-price', '0')
      await type('media-pricing-free-input-images', '0')
      expect(lastValue()).toEqual({ extra_input_image_price: 0, free_input_images: 0, rules: [{ unit: 'image', price: 0, conditions: { min_pixels: 0 } }] })
      expect(wrapper.find('[data-testid="media-pricing-validation"]').exists()).toBe(false)
    } finally { wrapper.unmount() }
  })

  it('flags a min/max inversion while leaving the value the operator typed', async () => {
    const { wrapper, type } = await mountEditor({ rules: [{ unit: 'frame', price: 0.01, conditions: { min_fps: 24, max_fps: 60 } }] })
    try {
      await type('media-pricing-rule-0-cond-max_fps', '12')
      expect(wrapper.text()).toContain('mediaPricing.visual.rangeMinExceedsMax')
      expect(wrapper.get('[data-testid="media-pricing-validation"]').text()).toBe('mediaPricing.validation.range')
    } finally { wrapper.unmount() }
  })

  it('flags an exact FPS outside its bounds', async () => {
    const { wrapper, type } = await mountEditor({ rules: [{ unit: 'frame', price: 0.01, conditions: { fps: 24, max_fps: 60 } }] })
    try {
      await type('media-pricing-rule-0-cond-fps', '90')
      expect(wrapper.text()).toContain('mediaPricing.visual.fpsOutsideRange')
      expect(wrapper.get('[data-testid="media-pricing-validation"]').text()).toBe('mediaPricing.validation.range')
    } finally { wrapper.unmount() }
  })

  it('blocks saving an empty rules array by clearing instead of writing {}', async () => {
    const { wrapper, last, emitted } = await mountEditor({ rules: [{ unit: 'image', price: 0.1 }] })
    try {
      // Removing the last rule is irreversible for this model, so it confirms first.
      await wrapper.get('[data-testid="media-pricing-rule-0-remove"]').trigger('click')
      expect(emitted()).toHaveLength(0)
      expect(wrapper.get('[data-testid="media-pricing-remove-last-hint"]').text()).toBe('mediaPricing.visual.removeLastConfirm')
      await wrapper.get('[data-testid="media-pricing-rule-0-remove"]').trigger('click')
      expect(last()).toBe('')
      expect(emitted()).toHaveLength(1)
      expect(wrapper.find('[data-testid="media-pricing-empty"]').exists()).toBe(true)
      expect(validateMediaPricing(last(), t)).toBeNull()
      expect(formMediaPricingToAPI(last())).toBeNull()
    } finally { wrapper.unmount() }
  })
})

describe('media pricing duration source', () => {
  it('writes total only for second rules and warns about the restriction', async () => {
    const { wrapper, lastValue, select } = await mountEditor(referenceVideoPricingFixture)
    try {
      await select('media-pricing-duration-source', 'total')
      expect(lastValue()!.duration_source).toBe('total')
      expect(wrapper.get('[data-testid="media-pricing-duration-total-hint"]').text()).toBe('mediaPricing.visual.durationTotalHint')
      expect(validateMediaPricing(wrapper.props('modelValue'), t)).toBeNull()
    } finally { wrapper.unmount() }
  })

  it('rejects total combined with a non-second rule', async () => {
    const { wrapper, select } = await mountEditor({ rules: [{ unit: 'image', price: 0.12 }] })
    try {
      await select('media-pricing-duration-source', 'total')
      expect(wrapper.get('[data-testid="media-pricing-validation"]').text()).toBe('mediaPricing.validation.durationUnit')
      await expect(async () => formMediaPricingToAPI(wrapper.emitted('update:modelValue')!.at(-1)![0] as string)).rejects.toThrow()
    } finally { wrapper.unmount() }
  })

  it('removes the key when the default source is selected again', async () => {
    const { wrapper, lastValue, last, select } = await mountEditor(referenceVideoPricingFixture)
    try {
      await select('media-pricing-duration-source', '')
      expect(lastValue()!.duration_source).toBeUndefined()
      expect(last()).not.toContain('duration_source')
    } finally { wrapper.unmount() }
  })
})

describe('media pricing JSON escape hatch', () => {
  it('keeps an unparsable draft verbatim and pauses visual editing', async () => {
    const { wrapper, last, emitted, sync } = await mountEditor(mediaPricingFixture)
    try {
      await wrapper.get('[data-testid="media-pricing-json"]').setValue('{ "rules": [')
      await sync()
      expect(last()).toBe('{ "rules": [')
      expect(emitted()).toHaveLength(1)
      expect(wrapper.get('[data-testid="media-pricing-unrenderable"]').text()).toBe('mediaPricing.visual.jsonBroken')
      expect(wrapper.get('[data-testid="media-pricing-validation"]').text()).toBe('mediaPricing.validation.json')
      expect(wrapper.find('[data-testid="media-pricing-rule-0"]').exists()).toBe(false)
    } finally { wrapper.unmount() }
  })

  it('keeps a structurally unrenderable draft instead of dropping rules', async () => {
    const { wrapper, last } = await mountEditor(mediaPricingFixture)
    try {
      await wrapper.get('[data-testid="media-pricing-json"]').setValue('{"rules":{"a":1}}')
      expect(last()).toBe('{"rules":{"a":1}}')
      expect(wrapper.get('[data-testid="media-pricing-unrenderable"]').text()).toBe('mediaPricing.visual.shapeBroken')
    } finally { wrapper.unmount() }
  })

  it('syncs valid JSON back into the visual controls', async () => {
    const { wrapper } = await mountEditor(mediaPricingFixture)
    try {
      await wrapper.get('[data-testid="media-pricing-json"]').setValue(JSON.stringify(MEDIA_PRICING_EXAMPLES.token))
      expect(wrapper.findAll('[data-testid$="-unit"]')).toHaveLength(1)
      expect((wrapper.get('[data-testid="media-pricing-rule-0-unit"]').element as HTMLSelectElement).value).toBe('token')
      expect((wrapper.get('[data-testid="media-pricing-rule-0-input_price"]').element as HTMLInputElement).value).toBe('0.000002')
      expect(wrapper.find('[data-testid="media-pricing-rule-0-price"]').exists()).toBe(false)
    } finally { wrapper.unmount() }
  })

  it('writes visual edits back into the JSON text', async () => {
    const { wrapper, type } = await mountEditor({ rules: [{ unit: 'image', price: 0.1 }] })
    try {
      await type('media-pricing-rule-0-name', 'layered')
      expect(JSON.parse((wrapper.get('[data-testid="media-pricing-json"]').element as HTMLTextAreaElement).value))
        .toEqual({ rules: [{ unit: 'image', price: 0.1, name: 'layered' }] })
    } finally { wrapper.unmount() }
  })

  it('never drops keys it cannot render, and reports them as unknown', async () => {
    const { wrapper, lastJson } = await mountEditor('{"rules":[{"unit":"image","price":0.1,"future_field":7}],"future_top":1}')
    try {
      await wrapper.get('[data-testid="media-pricing-rule-0-price"]').setValue('0.1')
      // Unrepresentable keys survive the visual round trip instead of being stripped.
      expect(lastJson()).toEqual({ rules: [{ unit: 'image', price: 0.1, future_field: 7 }], future_top: 1 })
      expect(wrapper.get('[data-testid="media-pricing-validation"]').text()).toBe('mediaPricing.validation.unknown')
    } finally { wrapper.unmount() }
  })

  it('loads an example into both views', async () => {
    const { wrapper, lastValue } = await mountEditor(null)
    try {
      expect(wrapper.find('[data-testid="media-pricing-empty"]').exists()).toBe(true)
      await wrapper.get('[data-testid="media-pricing-example-video"]').trigger('click')
      expect(lastValue()).toEqual(MEDIA_PRICING_EXAMPLES.video)
      expect(wrapper.findAll('[data-testid$="-unit"]')).toHaveLength(2)
    } finally { wrapper.unmount() }
  })
})

describe('media pricing rule list operations', () => {
  it('adds, reorders and removes rules without touching the others', async () => {
    const { wrapper, lastJson } = await mountEditor({ rules: [{ name: 'a', unit: 'image', price: 0.1 }, { name: 'b', unit: 'frame', price: 0.2 }, { name: 'c', unit: 'request', price: 0.3 }] })
    type Rules = { rules: Array<{ name?: string; unit: string; price?: number }> }
    try {
      await wrapper.get('[data-testid="media-pricing-rule-2-up"]').trigger('click')
      expect(lastJson<Rules>().rules.map(rule => rule.name)).toEqual(['a', 'c', 'b'])
      await wrapper.get('[data-testid="media-pricing-rule-0-down"]').trigger('click')
      expect(lastJson<Rules>().rules.map(rule => rule.name)).toEqual(['c', 'a', 'b'])
      await wrapper.get('[data-testid="media-pricing-rule-1-remove"]').trigger('click')
      expect(lastJson<Rules>().rules.map(rule => rule.name)).toEqual(['c', 'b'])
      expect(lastJson<Rules>().rules.map(rule => rule.price)).toEqual([0.3, 0.2])
      const before = lastJson<Rules>().rules
      await wrapper.get('[data-testid="media-pricing-add-rule"]').trigger('click')
      const added = lastJson<Rules>().rules
      expect(added).toHaveLength(3)
      expect(added.slice(0, 2)).toEqual(before)
      // A brand-new rule is deliberately incomplete, so the draft blocks saving
      // until its price is filled in rather than silently shipping an unpriced rule.
      expect(added[2]).toEqual({ unit: 'image' })
      expect(validateMediaPricing(lastJson(), t)).toBe('mediaPricing.validation.directPrice')
      await wrapper.get('[data-testid="media-pricing-rule-2-remove"]').trigger('click')
      expect(validateMediaPricing(lastJson(), t)).toBeNull()
    } finally { wrapper.unmount() }
  })

  it.each(['image', 'second', 'frame', 'request'] as const)('prices a %s rule through the per-unit price only', async (unit) => {
    const { wrapper, lastValue, type } = await mountEditor({ rules: [{ unit: 'image', price: 0.1 }] })
    try {
      await wrapper.get('[data-testid="media-pricing-rule-0-unit"]').setValue(unit)
      await wrapper.setProps({ modelValue: wrapper.emitted('update:modelValue')!.at(-1)![0] as string })
      await type('media-pricing-rule-0-price', '0.25')
      expect(lastValue()).toEqual({ rules: [{ unit, price: 0.25 }] })
      expect(validateMediaPricing(wrapper.props('modelValue'), t)).toBeNull()
    } finally { wrapper.unmount() }
  })
})

describe('media pricing legacy draft handling', () => {
  it.each([undefined, null, '', '  ', 'null'])('treats %s as disabled and offers to add a rule', async (value) => {
    const { wrapper } = await mountEditor(value as string | null)
    try {
      expect(wrapper.find('[data-testid="media-pricing-empty"]').exists()).toBe(true)
      expect(wrapper.find('[data-testid="media-pricing-validation"]').exists()).toBe(false)
      await wrapper.get('[data-testid="media-pricing-add-rule"]').trigger('click')
      const draft = wrapper.emitted('update:modelValue')!.at(-1)![0] as string
      expect(JSON.parse(draft)).toEqual({ rules: [{ unit: 'image' }] })
      // A fresh rule has no price yet: validation reports it rather than letting it save.
      expect(validateMediaPricing(draft, t)).toBe('mediaPricing.validation.directPrice')
    } finally { wrapper.unmount() }
  })

  it('accepts an object modelValue and never mutates it', async () => {
    const source = structuredClone(mediaPricingFixture)
    const { wrapper, type } = await mountEditor(source)
    try {
      await type('media-pricing-free-input-images', '9')
      expect(source.free_input_images).toBe(1)
      expect(mediaPricingToForm(source)).toBe(mediaPricingToForm(mediaPricingFixture))
    } finally { wrapper.unmount() }
  })
})
