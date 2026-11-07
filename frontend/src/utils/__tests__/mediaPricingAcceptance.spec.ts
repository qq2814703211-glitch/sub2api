// 独立验收：三态布尔与数值0的语义区分（不依赖子代理的测试）
import { describe, expect, it } from 'vitest'
import { validateMediaPricing, formMediaPricingToAPI } from '@/utils/mediaPricing'

const t = (k: string) => k

describe('media_pricing 计费语义独立验收', () => {
  it('未设置 audio 与 audio:false 必须是不同语义', () => {
    const unset = { rules: [{ unit: 'image', price: 1, conditions: {} }] }
    const noAudio = { rules: [{ unit: 'image', price: 1, conditions: { audio: false } }] }

    // 两者都合法
    expect(validateMediaPricing(JSON.stringify(unset), t)).toBeNull()
    expect(validateMediaPricing(JSON.stringify(noAudio), t)).toBeNull()

    const a = formMediaPricingToAPI(JSON.stringify(unset))
    const b = formMediaPricingToAPI(JSON.stringify(noAudio))
    // 关键：conditions 必须不同，audio 键在有 false 时必须存在
    expect(Object.keys(a!.rules[0].conditions || {})).toHaveLength(0)
    expect(b!.rules[0].conditions!.audio).toBe(false)
    expect(JSON.stringify(a)).not.toBe(JSON.stringify(b))
  })

  it('数值 0 是有效值，不能被当成未设置丢掉', () => {
    const v = { rules: [{ unit: 'image', price: 0, conditions: { min_pixels: 0 } }] }
    const out = formMediaPricingToAPI(JSON.stringify(v))
    expect(out!.rules[0].price).toBe(0)
    expect(out!.rules[0].conditions!.min_pixels).toBe(0)
  })

  it('token 单位禁止 price，其它单位禁止 token 价（后端硬约束）', () => {
    // token + price → 必须报错
    expect(validateMediaPricing(JSON.stringify({
      rules: [{ unit: 'token', price: 1 }],
    }), t)).not.toBeNull()
    // image + input_price → 必须报错
    expect(validateMediaPricing(JSON.stringify({
      rules: [{ unit: 'image', price: 1, input_price: 0.1 }],
    }), t)).not.toBeNull()
    // token 但一个价都没有 → 必须报错
    expect(validateMediaPricing(JSON.stringify({
      rules: [{ unit: 'token' }],
    }), t)).not.toBeNull()
    // token 有 input_price → 合法
    expect(validateMediaPricing(JSON.stringify({
      rules: [{ unit: 'token', input_price: 0.000002 }],
    }), t)).toBeNull()
  })

  it('duration_source=total 只允许 second 单位', () => {
    expect(validateMediaPricing(JSON.stringify({
      duration_source: 'total',
      rules: [{ unit: 'image', price: 1 }],
    }), t)).not.toBeNull()
    expect(validateMediaPricing(JSON.stringify({
      duration_source: 'total',
      rules: [{ unit: 'second', price: 1 }],
    }), t)).toBeNull()
  })

  it('区间校验：min > max 必须被拦', () => {
    expect(validateMediaPricing(JSON.stringify({
      rules: [{ unit: 'second', price: 1, conditions: { min_duration: 10, max_duration: 5 } }],
    }), t)).not.toBeNull()
    expect(validateMediaPricing(JSON.stringify({
      rules: [{ unit: 'second', price: 1, conditions: { min_duration: 5, max_duration: 10 } }],
    }), t)).toBeNull()
  })

  it('rules 空数组非法，null 才是关闭', () => {
    expect(validateMediaPricing(JSON.stringify({ rules: [] }), t)).not.toBeNull()
    expect(validateMediaPricing('', t)).toBeNull()
    expect(validateMediaPricing(null, t)).toBeNull()
  })
})
