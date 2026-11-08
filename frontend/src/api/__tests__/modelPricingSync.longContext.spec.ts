import { describe, expect, it } from 'vitest'

import { normalizeCnyOfficialConfig } from '@/api/admin/modelPricingSync'

/**
 * `settings` 里的 `pricing.cny_official` 可能原样是字符串，也可能已经解成对象；
 * 而且长上下文三兄弟带 `omitempty`，缺省必须保持「缺省」，不能被 `toPrice`
 * 归一成 0 —— 写 0 和不写在后端语义里不等价。
 */
describe('normalizeCnyOfficialConfig 长上下文阶梯字段', () => {
  it('passes the three long-context fields through when they are numbers', () => {
    const config = normalizeCnyOfficialConfig({
      enabled: true,
      manual_overrides: {
        'deepseek/deepseek-flash': {
          input: 1,
          output: 4,
          cache_read: 0.02,
          long_context_threshold: 32000,
          long_context_input_multiplier: 1.5,
          long_context_output_multiplier: 2
        }
      }
    })

    expect(config.manual_overrides['deepseek/deepseek-flash']).toEqual({
      input: 1,
      output: 4,
      cache_read: 0.02,
      long_context_threshold: 32000,
      long_context_input_multiplier: 1.5,
      long_context_output_multiplier: 2
    })
  })

  it('coerces string-typed numbers coming from the settings string payload', () => {
    const config = normalizeCnyOfficialConfig(
      JSON.stringify({
        enabled: true,
        manual_overrides: {
          'zhipuai/glm-4': {
            input: '2',
            output: '8',
            cache_read: '0.05',
            long_context_threshold: '64000',
            long_context_input_multiplier: '1.25',
            long_context_output_multiplier: '3'
          }
        }
      })
    )

    expect(config.manual_overrides['zhipuai/glm-4']).toEqual({
      input: 2,
      output: 8,
      cache_read: 0.05,
      long_context_threshold: 64000,
      long_context_input_multiplier: 1.25,
      long_context_output_multiplier: 3
    })
  })

  it('leaves the long-context keys absent instead of defaulting them to 0', () => {
    const config = normalizeCnyOfficialConfig({
      enabled: true,
      manual_overrides: {
        'moonshotai/kimi-k2': {
          input: 3,
          output: 6,
          cache_read: 0.1,
          // 后端 omitempty：不启用阶梯时这三个键根本不出现。
          long_context_input_multiplier: '',
          long_context_output_multiplier: null
        }
      }
    })

    const entry = config.manual_overrides['moonshotai/kimi-k2']
    expect(entry).toEqual({ input: 3, output: 6, cache_read: 0.1 })
    expect(Object.keys(entry).sort()).toEqual(['cache_read', 'input', 'output'])
  })

  it('keeps an explicit threshold of 0 as a real value', () => {
    const config = normalizeCnyOfficialConfig({
      enabled: true,
      manual_overrides: {
        'alibaba-cn/qwen-max': { input: 1, output: 2, cache_read: 0, long_context_threshold: 0 }
      }
    })

    // 0 是后端认可的「不配置阶梯」，但它确实是页面填过的值，要原样回填。
    expect(config.manual_overrides['alibaba-cn/qwen-max'].long_context_threshold).toBe(0)
  })
})
