import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import PricingEntryCard from '../PricingEntryCard.vue'
import type { PricingFormEntry } from '../types'

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))

function entry(): PricingFormEntry {
  return {
    models: [], billing_mode: 'image', input_price: null, output_price: null,
    cache_write_price: null, cache_read_price: null, image_input_price: null,
    image_output_price: null, per_request_price: null, intervals: [],
    time_pricing: { timezone: 'Asia/Shanghai', weekdays_only: false, periods: [] },
  }
}

const rules = { rules: [{ name: 'layer', unit: 'image', conditions: { layer: false, max_pixels: 4194304 }, price: 0.12, prompt_price: 0.01 }] }

describe('PricingEntryCard media pricing', () => {
  it.each(['token', 'per_request', 'image', 'video'] as const)('keeps media rules editable in %s mode and validates the current draft', async (billingMode) => {
    const initial = { ...entry(), billing_mode: billingMode, media_pricing: rules }
    const wrapper = mount(PricingEntryCard, { props: { entry: initial }, global: { stubs: { Select: true, ModelTagInput: true, Icon: true } } })
    try {
      const editor = () => wrapper.get<HTMLTextAreaElement>('[data-testid="media-pricing-json"]')
      expect(JSON.parse(editor().element.value)).toEqual(rules)
      await editor().setValue('{')
      const draft = wrapper.emitted('update')!.at(-1)![0] as PricingFormEntry
      await wrapper.setProps({ entry: draft })
      expect(editor().attributes('aria-invalid')).toBe('true')
      expect(wrapper.get('[role="alert"]').text()).toBe('mediaPricing.validation.json')
      await editor().setValue('null')
      await wrapper.setProps({ entry: wrapper.emitted('update')!.at(-1)![0] as PricingFormEntry })
      expect(editor().attributes('aria-invalid')).toBe('false')
      expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    } finally { wrapper.unmount() }
  })


  it('lets an image card edit complete media rules without changing scalar prices', async () => {
    const wrapper = mount(PricingEntryCard, { props: { entry: entry() }, global: { stubs: { Select: true, ModelTagInput: true, Icon: true } } })
    expect(wrapper.find('textarea[data-testid="media-pricing-json"]').exists()).toBe(true)
    await wrapper.get('textarea[data-testid="media-pricing-json"]').setValue(JSON.stringify(rules))
    expect(wrapper.emitted('update')?.at(-1)?.[0]).toMatchObject({ media_pricing: JSON.stringify(rules), per_request_price: null })
    wrapper.unmount()
  })
})
