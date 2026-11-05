import { describe, expect, it } from 'vitest'
import { mediaPricingFixture } from '@/__tests__/fixtures/mediaPricing'
import { MEDIA_PRICING_EXAMPLES, formMediaPricingToAPI, mediaConditionLabels, mediaPriceLabels, mediaPricingToForm, validateMediaPricing } from '../mediaPricing'

const t = (key: string) => key

describe('media pricing wire/form contract', () => {
  it('roundtrips all fields without MTok conversion, aliases or mutation', () => {
    const draft = mediaPricingToForm(mediaPricingFixture)
    expect(validateMediaPricing(draft, t)).toBeNull()
    const value = formMediaPricingToAPI(draft)!
    expect(value).toEqual(mediaPricingFixture)
    expect(value.rules[4].input_price).toBe(0.000002)
    expect(value.rules[0].conditions?.layer).toBe(false)
    expect(value.rules[1].conditions?.min_duration).toBe(0)
    value.rules[0].price = 9
    expect(mediaPricingFixture.rules[0].price).toBe(0.12)
  })

  it.each([undefined, null, '', '  ', 'null'])('keeps legacy pricing when rules are absent: %s', (value) => {
    expect(validateMediaPricing(value, t)).toBeNull()
    expect(formMediaPricingToAPI(value)).toBeNull()
  })

  it.each(Object.entries(MEDIA_PRICING_EXAMPLES))('provides a valid %s example', (_, example) => {
    expect(validateMediaPricing(example, t)).toBeNull()
  })

  it.each([
    ['{', 'json'], ['[]', 'rules'], ['{}', 'rules'], ['{"rules":[]}', 'rules'],
    ['{"rules":[null]}', 'rule'],
    ['{"rules":[{"unit":"minute","price":1}]}', 'unit'],
    ['{"rules":[{"unit":"image"}]}', 'directPrice'],
    ['{"rules":[{"unit":"token"}]}', 'tokenPrice'],
    ['{"rules":[{"unit":"token","price":1,"input_price":0.01}]}', 'unitPrices'],
    ['{"rules":[{"unit":"image","price":1,"output_price":0.01}]}', 'unitPrices'],
    ['{"rules":[{"unit":"image","price":-1}]}', 'price'],
    ['{"rules":[{"unit":"image","price":"1"}]}', 'price'],
    ['{"rules":[{"unit":"image","price":1e400}]}', 'price'],
    ['{"rules":[{"unit":"image","price":1,"conditions":[]}]}', 'conditions'],
    ['{"rules":[{"unit":"image","price":1,"conditions":{"audio":"false"}}]}', 'boolean'],
    ['{"rules":[{"unit":"image","price":1,"conditions":{"resolution":720}}]}', 'string'],
    ['{"rules":[{"unit":"image","price":1,"conditions":{"min_pixels":0.5}}]}', 'number'],
    ['{"rules":[{"unit":"image","price":1,"conditions":{"min_pixels":9007199254740992}}]}', 'number'],
    ['{"rules":[{"unit":"second","price":1,"conditions":{"min_duration":-1}}]}', 'number'],
    ['{"rules":[{"unit":"frame","price":1,"conditions":{"min_fps":30,"max_fps":24}}]}', 'range'],
    ['{"rules":[{"unit":"frame","price":1,"conditions":{"fps":60,"max_fps":24}}]}', 'range'],
    ['{"rules":[{"unit":"image","price":1,"conditions":{"min_pixels":2,"max_pixels":1}}]}', 'range'],
    ['{"rules":[{"unit":"second","price":1,"conditions":{"min_duration":10,"max_duration":5}}]}', 'range'],
    ['{"rules":[{"unit":"image","price":1,"prompt_price":-1}]}', 'price'],
    ['{"rules":[{"unit":"image","price":1,"conditions":{"referenceVideo":true}}]}', 'unknown'],
    ['{"rules":[{"unit":"image","price":1,"promptPrice":1}]}', 'unknown'],
  ])('blocks invalid drafts instead of clearing or submitting old rules: %s', (draft, key) => {
    expect(validateMediaPricing(draft, t)).toBe(`mediaPricing.validation.${key}`)
    expect(() => formMediaPricingToAPI(draft)).toThrow()
  })

  it('allows zero prices and inclusive single-value ranges', () => {
    expect(validateMediaPricing({ rules: [{ unit: 'image', price: 0, conditions: { min_pixels: 0, max_pixels: 0 } }] }, t)).toBeNull()
  })

  it('discloses false, zero and every multimodal price with unambiguous units', () => {
    expect(mediaConditionLabels(mediaPricingFixture.rules[0].conditions, t)).toEqual([
      'mediaPricing.conditions.min_pixels: 0', 'mediaPricing.conditions.max_pixels: 4194304', 'mediaPricing.conditions.layer: mediaPricing.no',
    ])
    expect(mediaPriceLabels(mediaPricingFixture.rules[4], 0.5, t)).toEqual([
      'mediaPricing.prices.input_price 0.000001 / mediaPricing.units.token',
      'mediaPricing.prices.output_price 0.000004 / mediaPricing.units.token',
      'mediaPricing.prices.image_input_price 0 / mediaPricing.units.token',
      'mediaPricing.prices.image_output_price 0.000005 / mediaPricing.units.token',
      'mediaPricing.prices.prompt_price +0.01 / mediaPricing.units.request',
    ])
  })
})
