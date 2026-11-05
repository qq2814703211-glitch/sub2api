import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { describe, expect, it } from 'vitest'
import MediaPricingRules from '../MediaPricingRules.vue'
import { referenceVideoPricingFixture } from '@/__tests__/fixtures/mediaPricing'
import en from '@/i18n/locales/en/mediaPricing'
import zh from '@/i18n/locales/zh/mediaPricing'

describe('request input pricing display', () => {
  it.each(['en', 'zh'])('shows the allowance, scaled surcharge and total seconds in %s', locale => {
    const i18n = createI18n({ legacy: false, locale, messages: { en: { mediaPricing: en }, zh: { mediaPricing: zh } } })
    const wrapper = mount(MediaPricingRules, { props: { pricing: referenceVideoPricingFixture, multiplier: 1.5 }, global: { plugins: [i18n] } })
    const text = wrapper.text()
    expect(text).toContain('5')
    expect(text).toContain('0.3')
    expect(text).toContain('0.75')
    expect(text).toContain(locale === 'en' ? 'output + reference video' : '输出 + 参考视频')
    expect(text).toContain(locale === 'en' ? 'All successful outputs are charged normally' : '成功输出均正常收费')
    expect(wrapper.findAll('[data-testid="media-request-pricing"]')).toHaveLength(2)
    wrapper.unmount()
  })
  it('shows explicit zero input prices without replacing them with defaults', () => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: { mediaPricing: en } } })
    const pricing = { ...referenceVideoPricingFixture, extra_input_image_price: 0, free_input_images: 0 }
    const wrapper = mount(MediaPricingRules, { props: { pricing, multiplier: 2 }, global: { plugins: [i18n] } })
    expect(wrapper.text()).toContain('0 input images included; each extra input image costs 0')
    wrapper.unmount()
  })
})
