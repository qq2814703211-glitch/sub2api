import { describe, expect, it } from 'vitest'
import { formMediaPricingToAPI, mediaPricingToForm, validateMediaPricing } from '../mediaPricing'

const t = (key: string) => key
const wire = '{"extra_input_image_price":0.2,"free_input_images":5,"duration_source":"total","rules":[{"unit":"second","price":0.5}]}'

describe('reference input media pricing', () => {
  it('roundtrips request-level image allowance and total duration', () => {
    expect(validateMediaPricing(wire, t)).toBeNull()
    expect(JSON.parse(mediaPricingToForm(formMediaPricingToAPI(wire)))).toEqual(JSON.parse(wire))
  })
  it.each([
    ['"extra_input_image_price":-1', 'price'],
    ['"extra_input_image_price":"0.2"', 'price'],
    ['"extra_input_image_price":1e400', 'price'],
    ['"free_input_images":-1', 'inputCount'],
    ['"free_input_images":0.5', 'inputCount'],
    ['"free_input_images":"5"', 'inputCount'],
    ['"free_input_images":9007199254740992', 'inputCount'],
    ['"duration_source":"input"', 'durationSource'],
    ['"duration_source":true', 'durationSource'],
  ])('rejects invalid request configuration %s', (fields, key) => {
    const draft = `{${fields},"rules":[{"unit":"second","price":0.5}]}`
    expect(validateMediaPricing(draft, t)).toBe(`mediaPricing.validation.${key}`)
    expect(() => formMediaPricingToAPI(draft)).toThrow()
  })
  it('rejects total duration on units without seconds', () => {
    expect(validateMediaPricing('{"duration_source":"total","rules":[{"unit":"image","price":0.2}]}', t)).toBe('mediaPricing.validation.durationUnit')
  })
  it.each(['null', '0'])('preserves explicit nullable and zero fields: %s', value => {
    const wire = `{"extra_input_image_price":${value},"free_input_images":${value},"duration_source":"output","rules":[{"unit":"image","price":0.3}]}`
    expect(formMediaPricingToAPI(wire)).toEqual(JSON.parse(wire))
  })
})
