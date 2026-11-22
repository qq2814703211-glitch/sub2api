import { defineComponent } from 'vue'
import { readFileSync, writeFileSync } from 'node:fs'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import ModelPricingSyncView from '@/views/admin/ModelPricingSyncView.vue'
import { apiClient } from '@/api/client'
import { getCnyOfficialConfig as getConfig, normalizeCnyOfficialConfig, updateCnyOfficialConfig as putConfig, type PricingCnyOfficialConfig } from '@/api/admin/modelPricingSync'

/**
 * /admin/model-pricing-sync 的「计价口径配置」卡片。
 *
 * 这张卡片是三档口径里 manual 的唯一入口，锁四件事：
 *   1. 三档优先级的展示顺序与 manual 的「最高」标记（usd < cny-official < manual）；
 *   2. manual_overrides 可增行、可删行，行内容进入保存载荷；
 *   3. 保存载荷严格是 { enabled, source_url, manual_overrides } 三项；
 *   4. 校验拦截：模型键空 / 缺斜杠 / 重复 / 价格空 / 价格为负都不许发请求。
 *
 * 文案断言走真实 zh 语言包（不 mock t），这样键名写错会真的失败。
 */

const {
  listHistory,
  getStatus,
  getCnyOfficialConfig,
  updateCnyOfficialConfig,
  showError,
  showSuccess
} = vi.hoisted(() => ({
  listHistory: vi.fn(),
  getStatus: vi.fn(),
  getCnyOfficialConfig: vi.fn(),
  updateCnyOfficialConfig: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    modelPricingSync: {
      listHistory,
      getStatus,
      getCnyOfficialConfig,
      updateCnyOfficialConfig
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess })
}))

// 只替换 useI18n，语言包本体保持真实实现。包体在工厂内部动态取，
// 因为 vi.mock 会被提升到文件顶部，顶层的 import 那时还没初始化。
// 语言包是按 `admin` 命名空间嵌套导出的，根对象要按同样结构拼。
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  const zh = (await import('@/i18n/locales/zh')).default as Record<string, unknown>
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        const nest = key.split('.').reduce<unknown>((acc, part) => {
          if (acc && typeof acc === 'object') return (acc as Record<string, unknown>)[part]
          return undefined
        }, zh)
        if (typeof nest !== 'string') return key
        if (!params) return nest
        return Object.entries(params).reduce(
          (text, [name, replacement]) => text.replaceAll(`{${name}}`, String(replacement)),
          nest
        )
      }
    })
  }
})

const AppLayoutStub = defineComponent({ template: '<main><slot /></main>' })
const TablePageLayoutStub = defineComponent({
  template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>'
})

const OVERRIDES_TESTID = 'pricing-source-card'

function mountView() {
  return mount(ModelPricingSyncView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        // 历史表格与本卡片无关，用只占位的空组件顶掉，避免 DataTable 内部逻辑干扰。
        DataTable: defineComponent({ template: '<div data-testid="history-table" />' }),
        Pagination: true,
        Icon: defineComponent({ template: '<i />' })
      }
    }
  })
}

function defaultConfig(
  overrides: Record<
    string,
    {
      input: number
      output: number
      cache_read: number
      long_context_threshold?: number
      long_context_input_multiplier?: number
      long_context_output_multiplier?: number
    }
  > = {}
) {
  return {
    enabled: true,
    source_url: 'https://raw.githubusercontent.com/basellm/llm-metadata/main/dist/api/all.json',
    manual_overrides: overrides
  }
}

/** 取新加那一行的 id 后缀：行 id 是自增的，测试里不能写死。 */
function rowSuffix(wrapper: Awaited<ReturnType<typeof mountReady>>, index = 0): string {
  const rows = wrapper.findAll('[data-testid^="pricing-row-model-"]')
  return rows[index].attributes('data-testid')!.replace('pricing-row-model-', '')
}

/** 填一行的三档基础价，长上下文字段按需再填。 */
async function fillPrice(
  wrapper: Awaited<ReturnType<typeof mountReady>>,
  suffix: string,
  model = 'deepseek/deepseek-flash'
) {
  await wrapper.find(`[data-testid="pricing-row-model-${suffix}"]`).setValue(model)
  await wrapper.find(`[data-testid="pricing-row-input-${suffix}"]`).setValue('1')
  await wrapper.find(`[data-testid="pricing-row-output-${suffix}"]`).setValue('4')
  await wrapper.find(`[data-testid="pricing-row-cache_read-${suffix}"]`).setValue('0.02')
}

async function mountReady(config: Omit<PricingCnyOfficialConfig, 'source_url'> & { source_url?: string } = defaultConfig()) {
  getCnyOfficialConfig.mockResolvedValue(config)
  const wrapper = mountView()
  await flushPromises()
  return wrapper
}

describe('ModelPricingSyncView 计价口径配置卡片', () => {
  beforeEach(() => {
    listHistory.mockReset().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 })
    getStatus.mockReset().mockResolvedValue({ enabled: true, interval_seconds: 21600 })
    getCnyOfficialConfig.mockReset()
    updateCnyOfficialConfig.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
  })

  it('renders the card with the three tiers in ascending priority order', async () => {
    const wrapper = await mountReady()

    const card = wrapper.find(`[data-testid="${OVERRIDES_TESTID}"]`)
    expect(card.exists()).toBe(true)
    expect(card.text()).toContain('计价口径配置')

    const tiers = wrapper.findAll('[data-testid^="pricing-tier-"]')
    expect(tiers.map((tier) => tier.attributes('data-testid'))).toEqual([
      'pricing-tier-usd',
      'pricing-tier-cny-official',
      'pricing-tier-manual'
    ])

    // 只有 manual 带「最高」，而且它是视觉上突出的那一档。
    const manual = wrapper.find('[data-testid="pricing-tier-manual"]')
    expect(manual.text()).toContain('最高')
    expect(manual.classes()).toContain('border-primary-300')
    expect(wrapper.find('[data-testid="pricing-tier-usd"]').text()).not.toContain('最高')
    expect(wrapper.find('[data-testid="pricing-tier-cny-official"]').text()).not.toContain('最高')

    wrapper.unmount()
  })

  it('shows the USD sync state from the status endpoint and the CNY source URL', async () => {
    const wrapper = await mountReady()

    expect(wrapper.find('[data-testid="pricing-usd-state"]').text()).toContain('已启用')
    expect(wrapper.find('[data-testid="pricing-source-url"]').text()).toBe(
      'https://raw.githubusercontent.com/basellm/llm-metadata/main/dist/api/all.json'
    )

    wrapper.unmount()
  })

  it('shows USD as disabled when the sync status endpoint reports it off', async () => {
    getStatus.mockResolvedValue({ enabled: false })
    const wrapper = await mountReady()

    expect(wrapper.find('[data-testid="pricing-usd-state"]').text()).toContain('未启用')

    wrapper.unmount()
  })

  it('lists all 18 Chinese vendors in the read-only fold', async () => {
    const wrapper = await mountReady()

    const vendors = wrapper.find('[data-testid="pricing-cn-vendors"]')
    expect(vendors.exists()).toBe(true)
    expect(vendors.findAll('code')).toHaveLength(18)
    expect(vendors.text()).toContain('alibaba-cn')
    expect(vendors.text()).toContain('zhipuai-coding-plan')

    wrapper.unmount()
  })

  it('prefers the vendor list from status.cny_official.national_vendor', async () => {
    // 后端 Status() 的 national_vendor 是白名单真实来源，优先于内置兜底清单。
    getStatus.mockResolvedValue({
      enabled: true,
      interval_seconds: 21600,
      cny_official: {
        enabled: true,
        source_url: 'https://raw.githubusercontent.com/basellm/llm-metadata/main/dist/api/all.json',
        national_vendor: ['alibaba-cn', 'volcengine', 'zhipuai']
      }
    })
    const wrapper = await mountReady()

    const vendors = wrapper.find('[data-testid="pricing-cn-vendors"]')
    expect(vendors.findAll('code')).toHaveLength(3)
    expect(vendors.text()).toContain('volcengine')
    expect(vendors.text()).not.toContain('moonshotai-cn')

    wrapper.unmount()
  })

  it('prefers the backend-provided vendor list over the built-in fallback', async () => {
    const wrapper = await mountReady({
      ...defaultConfig(),
      cn_vendors: ['alibaba-cn', 'moonshotai-cn'],
      // 后端返回的清单即使比内置清单短，也以后端为准。
    } as unknown as ReturnType<typeof defaultConfig>)

    const vendors = wrapper.find('[data-testid="pricing-cn-vendors"]')
    expect(vendors.findAll('code')).toHaveLength(2)
    expect(vendors.text()).toContain('moonshotai-cn')
    expect(vendors.text()).not.toContain('zhipuai')

    wrapper.unmount()
  })

  it('renders existing manual overrides as editable rows', async () => {
    const wrapper = await mountReady(
      defaultConfig({ 'deepseek/deepseek-flash': { input: 1, output: 4, cache_read: 0.02 } })
    )

    expect(wrapper.findAll('[data-testid^="pricing-row-model-"]')).toHaveLength(1)
    expect(
      (wrapper.find('[data-testid^="pricing-row-model-"]').element as HTMLInputElement).value
    ).toBe('deepseek/deepseek-flash')

    // 每行右侧都标注「此项优先级最高，同步不会覆盖」。
    expect(wrapper.find('[data-testid="pricing-row-priority-note"]').text()).toContain(
      '此项优先级最高，同步不会覆盖'
    )

    wrapper.unmount()
  })

  it('shows an empty state when there are no manual overrides', async () => {
    const wrapper = await mountReady()

    expect(wrapper.find('[data-testid="pricing-manual-empty"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-testid^="pricing-row-model-"]')).toHaveLength(0)

    wrapper.unmount()
  })

  it('adds and removes rows', async () => {
    const wrapper = await mountReady()
    expect(wrapper.findAll('[data-testid^="pricing-row-model-"]')).toHaveLength(0)

    await wrapper.find('[data-testid="pricing-add-row"]').trigger('click')
    await wrapper.find('[data-testid="pricing-add-row"]').trigger('click')
    expect(wrapper.findAll('[data-testid^="pricing-row-model-"]')).toHaveLength(2)
    expect(wrapper.find('[data-testid="pricing-manual-empty"]').exists()).toBe(false)

    // 删掉第一行，剩下的行保持可编辑。
    const removeButtons = wrapper.findAll('[data-testid^="pricing-remove-row-"]')
    await removeButtons[0].trigger('click')
    expect(wrapper.findAll('[data-testid^="pricing-row-model-"]')).toHaveLength(1)

    wrapper.unmount()
  })

  it('saves the exact { enabled, source_url, manual_overrides } payload', async () => {
    updateCnyOfficialConfig.mockImplementation(async (payload: unknown) => ({
      ...(payload as object),
      cn_vendors: ['alibaba-cn']
    }))
    const wrapper = await mountReady()

    await wrapper.find('[data-testid="pricing-add-row"]').trigger('click')
    const rowSuffix = wrapper.find('[data-testid^="pricing-row-model-"]').attributes('data-testid')!.replace(
      'pricing-row-model-',
      ''
    )

    await wrapper.find(`[data-testid="pricing-row-model-${rowSuffix}"]`).setValue('deepseek/deepseek-flash')
    await wrapper.find(`[data-testid="pricing-row-input-${rowSuffix}"]`).setValue('1')
    await wrapper.find(`[data-testid="pricing-row-output-${rowSuffix}"]`).setValue('4')
    await wrapper.find(`[data-testid="pricing-row-cache_read-${rowSuffix}"]`).setValue('0.02')

    await wrapper.find('[data-testid="pricing-save"]').trigger('click')
    await flushPromises()

    expect(updateCnyOfficialConfig).toHaveBeenCalledTimes(1)
    expect(updateCnyOfficialConfig).toHaveBeenCalledWith({
      enabled: true,
      source_url: 'https://raw.githubusercontent.com/basellm/llm-metadata/main/dist/api/all.json',
      manual_overrides: {
        'deepseek/deepseek-flash': { input: 1, output: 4, cache_read: 0.02 }
      }
    })
    // 只提交可写字段，后端算出来的 cn_vendors 不能被回写。
    expect(Object.keys(updateCnyOfficialConfig.mock.calls[0][0] as object).sort()).toEqual([
      'enabled',
      'manual_overrides',
      'source_url'
    ])
    expect(showSuccess).toHaveBeenCalledWith('计价口径已保存')

    wrapper.unmount()
  })

  it('sends the toggle state and drops deleted rows from the payload', async () => {
    updateCnyOfficialConfig.mockImplementation(async (payload: unknown) => payload as never)
    const wrapper = await mountReady(
      defaultConfig({
        'deepseek/deepseek-flash': { input: 1, output: 4, cache_read: 0.02 },
        'zhipuai/glm-4': { input: 2, output: 8, cache_read: 0.05 }
      })
    )

    // 关掉 CNY 官方价开关。
    await wrapper.find('[data-testid="pricing-cny-toggle"]').trigger('click')

    // 删掉 glm-4 那行（行序按模型键排序，zhipuai 在 deepseek 之后）。
    const removeButtons = wrapper.findAll('[data-testid^="pricing-remove-row-"]')
    await removeButtons[1].trigger('click')

    await wrapper.find('[data-testid="pricing-save"]').trigger('click')
    await flushPromises()

    expect(updateCnyOfficialConfig).toHaveBeenCalledWith({
      enabled: false,
      source_url: 'https://raw.githubusercontent.com/basellm/llm-metadata/main/dist/api/all.json',
      manual_overrides: {
        'deepseek/deepseek-flash': { input: 1, output: 4, cache_read: 0.02 }
      }
    })

    wrapper.unmount()
  })

  it.each([
    ['empty model key', '', '1', '4', '0.02', '模型键不能为空'],
    ['negative price', 'deepseek/deepseek-flash', '-1', '4', '0.02', '需为非负数'],
    ['empty price', 'deepseek/deepseek-flash', '', '4', '0.02', '必填']
  ])('blocks the save request for %s', async (_case, model, input, output, cacheRead, expectedError) => {
    const wrapper = await mountReady()

    await wrapper.find('[data-testid="pricing-add-row"]').trigger('click')
    const suffix = wrapper.find('[data-testid^="pricing-row-model-"]').attributes('data-testid')!.replace(
      'pricing-row-model-',
      ''
    )

    await wrapper.find(`[data-testid="pricing-row-model-${suffix}"]`).setValue(model)
    await wrapper.find(`[data-testid="pricing-row-input-${suffix}"]`).setValue(input)
    await wrapper.find(`[data-testid="pricing-row-output-${suffix}"]`).setValue(output)
    await wrapper.find(`[data-testid="pricing-row-cache_read-${suffix}"]`).setValue(cacheRead)

    await wrapper.find('[data-testid="pricing-save"]').trigger('click')
    await flushPromises()

    expect(updateCnyOfficialConfig).not.toHaveBeenCalled()
    // 校验失败就把错误摊在行上，而不是只弹一次 toast。
    expect(wrapper.text()).toContain(expectedError)

    wrapper.unmount()
  })

  it('裸模型名可以直接保存，不再要求含斜杠', async () => {
    const wrapper = await mountReady()

    await wrapper.find('[data-testid="pricing-add-row"]').trigger('click')
    const suffix = wrapper
      .find('[data-testid^="pricing-row-model-"]')
      .attributes('data-testid')!
      .replace('pricing-row-model-', '')

    await wrapper.find(`[data-testid="pricing-row-model-${suffix}"]`).setValue('deepseek-v4.1-flash-1017')
    await wrapper.find(`[data-testid="pricing-row-input-${suffix}"]`).setValue('1')
    await wrapper.find(`[data-testid="pricing-row-output-${suffix}"]`).setValue('4')
    await wrapper.find(`[data-testid="pricing-row-cache_read-${suffix}"]`).setValue('0.02')

    await wrapper.find('[data-testid="pricing-save"]').trigger('click')
    await flushPromises()

    expect(updateCnyOfficialConfig).toHaveBeenCalledTimes(1)
    const payload = updateCnyOfficialConfig.mock.calls[0][0]
    expect(Object.keys(payload.manual_overrides)).toEqual(['deepseek-v4.1-flash-1017'])
    expect(wrapper.text()).not.toContain('vendor/model')

    wrapper.unmount()
  })

  it('带斜杠的真实模型名原样提交，不被剥前缀', async () => {
    const wrapper = await mountReady()

    await wrapper.find('[data-testid="pricing-add-row"]').trigger('click')
    const suffix = wrapper
      .find('[data-testid^="pricing-row-model-"]')
      .attributes('data-testid')!
      .replace('pricing-row-model-', '')

    await wrapper.find(`[data-testid="pricing-row-model-${suffix}"]`).setValue('siliconflow/deepseek-v3.2')
    await wrapper.find(`[data-testid="pricing-row-input-${suffix}"]`).setValue('55')
    await wrapper.find(`[data-testid="pricing-row-output-${suffix}"]`).setValue('55')
    await wrapper.find(`[data-testid="pricing-row-cache_read-${suffix}"]`).setValue('0.02')

    await wrapper.find('[data-testid="pricing-save"]').trigger('click')
    await flushPromises()

    const payload = updateCnyOfficialConfig.mock.calls[0][0]
    expect(Object.keys(payload.manual_overrides)).toEqual(['siliconflow/deepseek-v3.2'])

    wrapper.unmount()
  })

  it('rejects duplicate model keys', async () => {
    const wrapper = await mountReady()

    for (let i = 0; i < 2; i += 1) {
      await wrapper.find('[data-testid="pricing-add-row"]').trigger('click')
    }
    const suffixes = wrapper
      .findAll('[data-testid^="pricing-row-model-"]')
      .map((el) => el.attributes('data-testid')!.replace('pricing-row-model-', ''))

    for (const suffix of suffixes) {
      await wrapper.find(`[data-testid="pricing-row-model-${suffix}"]`).setValue('deepseek/deepseek-flash')
      await wrapper.find(`[data-testid="pricing-row-input-${suffix}"]`).setValue('1')
      await wrapper.find(`[data-testid="pricing-row-output-${suffix}"]`).setValue('4')
      await wrapper.find(`[data-testid="pricing-row-cache_read-${suffix}"]`).setValue('0.02')
    }

    await wrapper.find('[data-testid="pricing-save"]').trigger('click')
    await flushPromises()

    expect(updateCnyOfficialConfig).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('模型键重复')

    wrapper.unmount()
  })

  it('surfaces the real backend error text when the save fails', async () => {
    updateCnyOfficialConfig.mockRejectedValue({
      status: 400,
      code: 'INVALID_PRICING_OVERRIDE',
      message: 'manual_overrides["deepseek/deepseek-flash"].input must be >= 0'
    })
    const wrapper = await mountReady()

    await wrapper.find('[data-testid="pricing-add-row"]').trigger('click')
    const suffix = wrapper.find('[data-testid^="pricing-row-model-"]').attributes('data-testid')!.replace(
      'pricing-row-model-',
      ''
    )
    await wrapper.find(`[data-testid="pricing-row-model-${suffix}"]`).setValue('deepseek/deepseek-flash')
    await wrapper.find(`[data-testid="pricing-row-input-${suffix}"]`).setValue('1')
    await wrapper.find(`[data-testid="pricing-row-output-${suffix}"]`).setValue('4')
    await wrapper.find(`[data-testid="pricing-row-cache_read-${suffix}"]`).setValue('0.02')

    await wrapper.find('[data-testid="pricing-save"]').trigger('click')
    await flushPromises()

    // 后端原文必须留在页面上，不能只靠 5 秒消失的 toast。
    expect(wrapper.find('[data-testid="pricing-save-error"]').text()).toBe(
      'manual_overrides["deepseek/deepseek-flash"].input must be >= 0'
    )
    expect(showError).toHaveBeenCalledWith(
      'manual_overrides["deepseek/deepseek-flash"].input must be >= 0'
    )

    wrapper.unmount()
  })

  it('explains a missing backend endpoint instead of reporting a generic load failure', async () => {
    getCnyOfficialConfig.mockRejectedValueOnce({ status: 404, message: '404 page not found' })
    const wrapper = mountView()
    await flushPromises()

    const errorBox = wrapper.find('[data-testid="pricing-source-load-error"]')
    expect(errorBox.text()).toContain('/admin/model-pricing-sync/cny-official')
    expect(errorBox.text()).toContain('尚未注册')
    // 不写「加载失败」，避免让人以为是自己配坏了。
    expect(errorBox.text()).not.toContain('加载口径配置失败')

    wrapper.unmount()
  })

  it('renders a load error with the backend message and can retry', async () => {
    getCnyOfficialConfig.mockRejectedValueOnce({
      status: 500,
      message: 'settings store unavailable'
    })
    const wrapper = mountView()
    await flushPromises()

    const errorBox = wrapper.find('[data-testid="pricing-source-load-error"]')
    expect(errorBox.exists()).toBe(true)
    expect(errorBox.text()).toContain('settings store unavailable')

    // 重试走同一个读取接口，成功后卡片正常渲染。
    getCnyOfficialConfig.mockResolvedValueOnce(defaultConfig())
    await errorBox.find('button').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="pricing-source-load-error"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="pricing-save"]').exists()).toBe(true)

    wrapper.unmount()
  })

  it('renders the long-context tier inputs on every row', async () => {
    const wrapper = await mountReady(defaultConfig({ 'deepseek/deepseek-flash': { input: 1, output: 4, cache_read: 0.02 } }))
    const suffix = rowSuffix(wrapper)

    // 三个新输入框默认留空 —— 空 = 不启用阶梯。
    for (const field of [
      'long_context_threshold',
      'long_context_input_multiplier',
      'long_context_output_multiplier'
    ]) {
      const input = wrapper.find(`[data-testid="pricing-row-${field}-${suffix}"]`)
      expect(input.exists()).toBe(true)
      expect((input.element as HTMLInputElement).value).toBe('')
    }

    expect(wrapper.find(`[data-testid="pricing-row-long-context-${suffix}"]`).text()).toContain(
      '长上下文阶梯（留空不启用）'
    )

    wrapper.unmount()
  })

  it('omits the long-context keys entirely when the inputs are left blank', async () => {
    updateCnyOfficialConfig.mockImplementation(async (payload: unknown) => payload as never)
    const wrapper = await mountReady()

    await wrapper.find('[data-testid="pricing-add-row"]').trigger('click')
    await fillPrice(wrapper, rowSuffix(wrapper))

    await wrapper.find('[data-testid="pricing-save"]').trigger('click')
    await flushPromises()

    const payload = updateCnyOfficialConfig.mock.calls[0][0] as {
      manual_overrides: Record<string, Record<string, unknown>>
    }
    // 留空不能写成 0：后端带 omitempty，省略才是「不启用阶梯」。
    expect(Object.keys(payload.manual_overrides['deepseek/deepseek-flash']).sort()).toEqual([
      'cache_read',
      'input',
      'output'
    ])
    expect(payload.manual_overrides['deepseek/deepseek-flash']).not.toHaveProperty(
      'long_context_threshold'
    )

    wrapper.unmount()
  })

  it('submits all three long-context fields as numbers when filled in', async () => {
    updateCnyOfficialConfig.mockImplementation(async (payload: unknown) => payload as never)
    const wrapper = await mountReady()

    await wrapper.find('[data-testid="pricing-add-row"]').trigger('click')
    const suffix = rowSuffix(wrapper)
    await fillPrice(wrapper, suffix)
    await wrapper.find(`[data-testid="pricing-row-long_context_threshold-${suffix}"]`).setValue('32000')
    await wrapper
      .find(`[data-testid="pricing-row-long_context_input_multiplier-${suffix}"]`)
      .setValue('1.5')
    await wrapper
      .find(`[data-testid="pricing-row-long_context_output_multiplier-${suffix}"]`)
      .setValue('2')

    await wrapper.find('[data-testid="pricing-save"]').trigger('click')
    await flushPromises()

    expect(updateCnyOfficialConfig).toHaveBeenCalledWith({
      enabled: true,
      source_url: 'https://raw.githubusercontent.com/basellm/llm-metadata/main/dist/api/all.json',
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

    wrapper.unmount()
  })

  it('blocks the save when a multiplier is set without a threshold', async () => {
    const wrapper = await mountReady()

    await wrapper.find('[data-testid="pricing-add-row"]').trigger('click')
    const suffix = rowSuffix(wrapper)
    await fillPrice(wrapper, suffix)
    // 只填倍率、不填阈值：阶梯永远不会生效，必须拦。
    await wrapper
      .find(`[data-testid="pricing-row-long_context_input_multiplier-${suffix}"]`)
      .setValue('1.5')

    await wrapper.find('[data-testid="pricing-save"]').trigger('click')
    await flushPromises()

    expect(updateCnyOfficialConfig).not.toHaveBeenCalled()
    expect(
      wrapper.find(`[data-testid="pricing-row-long_context_threshold-error-${suffix}"]`).text()
    ).toContain('填了倍率必须同时填阈值')

    wrapper.unmount()
  })

  it('blocks the save when a multiplier is set with a threshold of 0', async () => {
    const wrapper = await mountReady()

    await wrapper.find('[data-testid="pricing-add-row"]').trigger('click')
    const suffix = rowSuffix(wrapper)
    await fillPrice(wrapper, suffix)
    // 阈值 0 等价于不启用，配倍率同样无意义。
    await wrapper.find(`[data-testid="pricing-row-long_context_threshold-${suffix}"]`).setValue('0')
    await wrapper
      .find(`[data-testid="pricing-row-long_context_output_multiplier-${suffix}"]`)
      .setValue('2')

    await wrapper.find('[data-testid="pricing-save"]').trigger('click')
    await flushPromises()

    expect(updateCnyOfficialConfig).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('填了倍率必须同时填阈值')

    wrapper.unmount()
  })

  it('accepts a threshold of 0 when both multipliers are left blank', async () => {
    updateCnyOfficialConfig.mockImplementation(async (payload: unknown) => payload as never)
    const wrapper = await mountReady()

    await wrapper.find('[data-testid="pricing-add-row"]').trigger('click')
    const suffix = rowSuffix(wrapper)
    await fillPrice(wrapper, suffix)
    await wrapper.find(`[data-testid="pricing-row-long_context_threshold-${suffix}"]`).setValue('0')

    await wrapper.find('[data-testid="pricing-save"]').trigger('click')
    await flushPromises()

    // 阈值 0 + 倍率留空 = 不启用阶梯，合法。提交的阈值是显式的 0。
    expect(updateCnyOfficialConfig).toHaveBeenCalledTimes(1)
    const payload = updateCnyOfficialConfig.mock.calls[0][0] as {
      manual_overrides: Record<string, Record<string, unknown>>
    }
    expect(payload.manual_overrides['deepseek/deepseek-flash'].long_context_threshold).toBe(0)

    wrapper.unmount()
  })

  it.each([
    ['a non-integer threshold', '32000.5', '', '', '需为不小于 0 的整数', 'long_context_threshold'],
    ['a negative threshold', '-1', '', '', '需为不小于 0 的整数', 'long_context_threshold'],
    ['an input multiplier below 1', '32000', '0.5', '', '需为不小于 1 的数字', 'long_context_input_multiplier'],
    ['an output multiplier below 1', '32000', '', '0.9', '需为不小于 1 的数字', 'long_context_output_multiplier']
  ])(
    'blocks the save for %s',
    async (_case, threshold, inputMultiplier, outputMultiplier, expectedError, field) => {
      const wrapper = await mountReady()

      await wrapper.find('[data-testid="pricing-add-row"]').trigger('click')
      const suffix = rowSuffix(wrapper)
      await fillPrice(wrapper, suffix)
      await wrapper.find(`[data-testid="pricing-row-long_context_threshold-${suffix}"]`).setValue(threshold)
      if (inputMultiplier) {
        await wrapper
          .find(`[data-testid="pricing-row-long_context_input_multiplier-${suffix}"]`)
          .setValue(inputMultiplier)
      }
      if (outputMultiplier) {
        await wrapper
          .find(`[data-testid="pricing-row-long_context_output_multiplier-${suffix}"]`)
          .setValue(outputMultiplier)
      }

      await wrapper.find('[data-testid="pricing-save"]').trigger('click')
      await flushPromises()

      expect(updateCnyOfficialConfig).not.toHaveBeenCalled()
      expect(wrapper.find(`[data-testid="pricing-row-${field}-error-${suffix}"]`).text()).toContain(
        expectedError
      )

      wrapper.unmount()
    }
  )

  it('backfills the three long-context fields returned by the backend', async () => {
    const wrapper = await mountReady(
      defaultConfig({
        'deepseek/deepseek-flash': {
          input: 1,
          output: 4,
          cache_read: 0.02,
          long_context_threshold: 32000,
          long_context_input_multiplier: 1.5,
          long_context_output_multiplier: 2
        },
        // 没有阶梯的模型回填成空串，不能变成 "0" / "undefined"。
        'zhipuai/glm-4': { input: 2, output: 8, cache_read: 0.05 }
      })
    )

    const suffixes = wrapper
      .findAll('[data-testid^="pricing-row-model-"]')
      .map((el) => el.attributes('data-testid')!.replace('pricing-row-model-', ''))

    // 行按模型键排序：deepseek 在前。
    const deepseek = suffixes[0]
    expect(
      (wrapper.find(`[data-testid="pricing-row-long_context_threshold-${deepseek}"]`).element as HTMLInputElement)
        .value
    ).toBe('32000')
    expect(
      (
        wrapper.find(`[data-testid="pricing-row-long_context_input_multiplier-${deepseek}"]`)
          .element as HTMLInputElement
      ).value
    ).toBe('1.5')
    expect(
      (
        wrapper.find(`[data-testid="pricing-row-long_context_output_multiplier-${deepseek}"]`)
          .element as HTMLInputElement
      ).value
    ).toBe('2')

    const glm = suffixes[1]
    expect(
      (wrapper.find(`[data-testid="pricing-row-long_context_threshold-${glm}"]`).element as HTMLInputElement)
        .value
    ).toBe('')

    wrapper.unmount()
  })

  it('keeps a backfilled long-context tier out of the dirty state until it is edited', async () => {
    const wrapper = await mountReady(
      defaultConfig({
        'deepseek/deepseek-flash': {
          input: 1,
          output: 4,
          cache_read: 0.02,
          long_context_threshold: 32000,
          long_context_input_multiplier: 1.5,
          long_context_output_multiplier: 2
        }
      })
    )

    // 回填后的快照必须与载荷一致，否则一进页面就显示「有未保存改动」。
    expect(wrapper.find('[data-testid="pricing-source-dirty"]').exists()).toBe(false)

    await wrapper
      .find(`[data-testid="pricing-row-long_context_threshold-${rowSuffix(wrapper)}"]`)
      .setValue('64000')
    expect(wrapper.find('[data-testid="pricing-source-dirty"]').exists()).toBe(true)

    wrapper.unmount()
  })
})

describe('manual 行折叠', () => {
  it('已有的行默认收起，点一下展开', async () => {
    const wrapper = await mountReady({
      enabled: true,
      manual_overrides: {
        'deepseek/deepseek-flash': { input: 1, output: 4, cache_read: 0.02 }
      }
    })
    const row = wrapper.find('[data-testid="pricing-row-1"]')
    expect(row.attributes('data-expanded')).toBe('false')

    await wrapper.find('[data-testid="pricing-row-toggle-1"]').trigger('click')
    expect(wrapper.find('[data-testid="pricing-row-1"]').attributes('data-expanded')).toBe('true')
  })

  it('收起态显示价格摘要，不用展开就能看到价格', async () => {
    const wrapper = await mountReady({
      enabled: true,
      manual_overrides: {
        'deepseek/deepseek-flash': { input: 1, output: 4, cache_read: 0.02 }
      }
    })
    expect(wrapper.find('[data-testid="pricing-row-1"]').text()).toContain('1 / 4 / 0.02 / — · 站内单位 / 百万 tokens')
  })

  it('配了阶梯的行在收起态亮出标记', async () => {
    const wrapper = await mountReady({
      enabled: true,
      manual_overrides: {
        'deepseek/deepseek-flash': {
          input: 1, output: 4,
          long_context_threshold: 32000,
          long_context_input_multiplier: 1.5
        }
      }
    })
    expect(wrapper.find('[data-testid="pricing-row-lc-badge-1"]').exists()).toBe(true)
  })

  it('只填阈值不填倍率不亮标记（等于没配阶梯）', async () => {
    const wrapper = await mountReady({
      enabled: true,
      manual_overrides: {
        'deepseek/deepseek-flash': { input: 1, output: 4, long_context_threshold: 32000 }
      }
    })
    expect(wrapper.find('[data-testid="pricing-row-lc-badge-1"]').exists()).toBe(false)
  })

  it('新增的行直接展开，省掉一次点击', async () => {
    const wrapper = await mountReady({ enabled: true, manual_overrides: {} })
    await wrapper.find('[data-testid="pricing-add-row"]').trigger('click')
    await flushPromises()
    const row = wrapper.find('[data-testid="pricing-row-1"]')
    expect(row.attributes('data-expanded')).toBe('true')
  })

  it('全部展开 / 全部收起作用于所有行', async () => {
    const wrapper = await mountReady({
      enabled: true,
      manual_overrides: {
        'deepseek/deepseek-flash': { input: 1, output: 4 },
        'zhipuai/glm-4.6': { input: 0.6, output: 2.2 }
      }
    })
    expect(wrapper.find('[data-testid="pricing-row-1"]').attributes('data-expanded')).toBe('false')
    expect(wrapper.find('[data-testid="pricing-row-2"]').attributes('data-expanded')).toBe('false')

    await wrapper.find('[data-testid="pricing-expand-all"]').trigger('click')
    expect(wrapper.find('[data-testid="pricing-row-1"]').attributes('data-expanded')).toBe('true')
    expect(wrapper.find('[data-testid="pricing-row-2"]').attributes('data-expanded')).toBe('true')

    await wrapper.find('[data-testid="pricing-collapse-all"]').trigger('click')
    expect(wrapper.find('[data-testid="pricing-row-1"]').attributes('data-expanded')).toBe('false')
    expect(wrapper.find('[data-testid="pricing-row-2"]').attributes('data-expanded')).toBe('false')
  })

  it('折叠不丢编辑：展开改了值再收起，值仍在', async () => {
    const wrapper = await mountReady({
      enabled: true,
      manual_overrides: {
        'deepseek/deepseek-flash': { input: 1, output: 4 }
      }
    })
    await wrapper.find('[data-testid="pricing-row-toggle-1"]').trigger('click')
    await wrapper.find('[data-testid="pricing-row-input-1"]').setValue('9')
    await wrapper.find('[data-testid="pricing-row-toggle-1"]').trigger('click')

    expect(wrapper.find('[data-testid="pricing-row-1"]').text()).toContain('9 / 4')
  })
})

describe('manual 其他计费项（添加目录 / 删除）', () => {
  beforeEach(() => {
    listHistory.mockReset().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 })
    getStatus.mockReset().mockResolvedValue({ enabled: true, interval_seconds: 21600 })
    getCnyOfficialConfig.mockReset()
    updateCnyOfficialConfig.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
  })

  /** 建一行并返回该行的 id 后缀。 */
  async function addRow(wrapper: Awaited<ReturnType<typeof mountReady>>) {
    await wrapper.find('[data-testid="pricing-add-row"]').trigger('click')
    return wrapper
      .find('[data-testid^="pricing-row-model-"]')
      .attributes('data-testid')!
      .replace('pricing-row-model-', '')
  }

  it('默认展示输入、输出、缓存读与四种缓存写', async () => {
    const wrapper = await mountReady()
    const id = await addRow(wrapper)

    expect(wrapper.find(`[data-testid="pricing-row-input-${id}"]`).exists()).toBe(true)
    expect(wrapper.find(`[data-testid="pricing-row-output-${id}"]`).exists()).toBe(true)
    expect(wrapper.find(`[data-testid="pricing-row-cache_read-${id}"]`).exists()).toBe(true)
    expect(wrapper.find(`[data-testid="pricing-row-cache_write-${id}"]`).exists()).toBe(true)
    expect(wrapper.find(`[data-testid="pricing-row-cache_write_1h-${id}"]`).exists()).toBe(true)
    expect(wrapper.find(`[data-testid="pricing-row-cache_write_30m-${id}"]`).exists()).toBe(true)
    expect(wrapper.find(`[data-testid="pricing-row-cache_write_12h-${id}"]`).exists()).toBe(true)

    // 其他计费项默认一个都不渲染。
    expect(wrapper.find(`[data-testid="pricing-row-extras-list-${id}"]`).exists()).toBe(false)
  })

  it('点「添加计费项」展开目录，按分组列出全部可选计费项', async () => {
    const wrapper = await mountReady()
    const id = await addRow(wrapper)

    await wrapper.find(`[data-testid="pricing-row-add-extra-${id}"]`).trigger('click')

    const menu = wrapper.find(`[data-testid="pricing-row-extra-menu-${id}"]`)
    expect(menu.exists()).toBe(true)
    expect(menu.findAll('button')).toHaveLength(9)
    for (const key of [
      'input_priority',
      'output_priority',
      'cache_read_priority',
      'cache_write_priority',
      'image_input',
      'image_cache_read',
      'image_output_token',
      'image_output_per_image',
      'cache_storage_per_hour'
    ]) {
      expect(
        wrapper.find(`[data-testid="pricing-row-extra-option-${key}-${id}"]`).exists(),
        `目录里应有 ${key}`
      ).toBe(true)
    }
  })

  it('从目录选中后该计费项出现输入框，目录里置灰不可重复添加', async () => {
    const wrapper = await mountReady()
    const id = await addRow(wrapper)

    await wrapper.find(`[data-testid="pricing-row-add-extra-${id}"]`).trigger('click')
    await wrapper
      .find(`[data-testid="pricing-row-extra-option-image_output_per_image-${id}"]`)
      .trigger('click')

    expect(wrapper.find(`[data-testid="pricing-row-extra-input-image_output_per_image-${id}"]`).exists()).toBe(true)
    // 添加后目录自动关闭。
    expect(wrapper.find(`[data-testid="pricing-row-extra-menu-${id}"]`).exists()).toBe(false)

    // 再开目录，该项已置灰。
    await wrapper.find(`[data-testid="pricing-row-add-extra-${id}"]`).trigger('click')
    const option = wrapper.find(`[data-testid="pricing-row-extra-option-image_output_per_image-${id}"]`)
    expect(option.attributes('disabled')).toBeDefined()
  })

  it('已添加的计费项可以删除，删除后输入框消失', async () => {
    const wrapper = await mountReady()
    const id = await addRow(wrapper)

    await wrapper.find(`[data-testid="pricing-row-add-extra-${id}"]`).trigger('click')
    await wrapper.find(`[data-testid="pricing-row-extra-option-cache_storage_per_hour-${id}"]`).trigger('click')
    expect(wrapper.find(`[data-testid="pricing-row-extra-input-cache_storage_per_hour-${id}"]`).exists()).toBe(true)

    await wrapper.find(`[data-testid="pricing-row-remove-extra-cache_storage_per_hour-${id}"]`).trigger('click')

    expect(wrapper.find(`[data-testid="pricing-row-extra-input-cache_storage_per_hour-${id}"]`).exists()).toBe(false)
    // 整组都空了就整块不渲染。
    expect(wrapper.find(`[data-testid="pricing-row-extras-list-${id}"]`).exists()).toBe(false)
  })

  it('已添加的计费项随保存载荷一起提交', async () => {
    updateCnyOfficialConfig.mockImplementation(async (payload: unknown) => ({
      ...(payload as object),
      cn_vendors: ['zhipuai']
    }))
    const wrapper = await mountReady()
    const id = await addRow(wrapper)

    await wrapper.find(`[data-testid="pricing-row-model-${id}"]`).setValue('glm-4.6')
    await wrapper.find(`[data-testid="pricing-row-input-${id}"]`).setValue('0.6')
    await wrapper.find(`[data-testid="pricing-row-output-${id}"]`).setValue('2.2')
    await wrapper.find(`[data-testid="pricing-row-cache_read-${id}"]`).setValue('0.11')
    await wrapper.find(`[data-testid="pricing-row-cache_write-${id}"]`).setValue('0.11')

    await wrapper.find(`[data-testid="pricing-row-add-extra-${id}"]`).trigger('click')
    await wrapper
      .find(`[data-testid="pricing-row-extra-option-image_output_per_image-${id}"]`)
      .trigger('click')
    await wrapper
      .find(`[data-testid="pricing-row-extra-input-image_output_per_image-${id}"]`)
      .setValue('0.5')

    await wrapper.find('[data-testid="pricing-save"]').trigger('click')
    await flushPromises()

    expect(updateCnyOfficialConfig).toHaveBeenCalledWith({
      enabled: true,
      source_url: 'https://raw.githubusercontent.com/basellm/llm-metadata/main/dist/api/all.json',
      manual_overrides: {
        'glm-4.6': {
          input: 0.6,
          output: 2.2,
          cache_read: 0.11,
          cache_write: 0.11,
          image_output_per_image: 0.5
        }
      }
    })
  })

  it('删除已添加的计费项后，该键从载荷里消失', async () => {
    updateCnyOfficialConfig.mockImplementation(async (payload: unknown) => ({
      ...(payload as object),
      cn_vendors: ['zhipuai']
    }))
    // 后端回传的行自带一个可选计费项。
    const wrapper = await mountReady({
      enabled: true,
      manual_overrides: {
        'glm-4.6': {
          input: 0.6,
          output: 2.2,
          cache_read: 0.11,
          cache_write: 0.11,
          image_output_per_image: 0.5
        }
      }
    })
    await wrapper.find('[data-testid="pricing-row-toggle-1"]').trigger('click')

    // 回填：该计费项确实出现在界面上。
    expect(wrapper.find('[data-testid="pricing-row-extra-input-image_output_per_image-1"]').exists()).toBe(true)

    await wrapper.find('[data-testid="pricing-row-remove-extra-image_output_per_image-1"]').trigger('click')
    await wrapper.find('[data-testid="pricing-save"]').trigger('click')
    await flushPromises()

    const payload = updateCnyOfficialConfig.mock.calls[0][0] as {
      manual_overrides: Record<string, Record<string, number>>
    }
    expect(payload.manual_overrides['glm-4.6']).not.toHaveProperty('image_output_per_image')
    expect(payload.manual_overrides['glm-4.6'].input).toBe(0.6)
  })

  it('回填后端返回的可选计费项，只渲染已配置的项', async () => {
    const wrapper = await mountReady({
      enabled: true,
      manual_overrides: {
        'glm-4.6': {
          input: 0.6,
          output: 2.2,
          cache_read: 0.11,
          cache_write: 0.11,
          cache_write_1h: 0.22,
          cache_storage_per_hour: 4.5
        }
      }
    })
    await wrapper.find('[data-testid="pricing-row-toggle-1"]').trigger('click')

    expect(
      (wrapper.find('[data-testid="pricing-row-cache_write_1h-1"]').element as HTMLInputElement).value
    ).toBe('0.22')
    expect(
      (wrapper.find('[data-testid="pricing-row-extra-input-cache_storage_per_hour-1"]').element as HTMLInputElement)
        .value
    ).toBe('4.5')
    // 没配的项不出现。
    expect(wrapper.find('[data-testid="pricing-row-extra-input-image_input-1"]').exists()).toBe(false)
  })

  it('已添加的计费项填了非法值时拦截保存', async () => {
    const wrapper = await mountReady()
    const id = await addRow(wrapper)

    await wrapper.find(`[data-testid="pricing-row-model-${id}"]`).setValue('glm-4.6')
    await wrapper.find(`[data-testid="pricing-row-input-${id}"]`).setValue('0.6')
    await wrapper.find(`[data-testid="pricing-row-output-${id}"]`).setValue('2.2')
    await wrapper.find(`[data-testid="pricing-row-cache_read-${id}"]`).setValue('0.11')

    await wrapper.find(`[data-testid="pricing-row-add-extra-${id}"]`).trigger('click')
    await wrapper.find(`[data-testid="pricing-row-extra-option-input_priority-${id}"]`).trigger('click')
    await wrapper.find(`[data-testid="pricing-row-extra-input-input_priority-${id}"]`).setValue('-1')

    await wrapper.find('[data-testid="pricing-save"]').trigger('click')
    await flushPromises()

    expect(updateCnyOfficialConfig).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('请先修正标红的行')
  })

  it('空着的计费项不算错、也不进载荷', async () => {
    updateCnyOfficialConfig.mockImplementation(async (payload: unknown) => ({
      ...(payload as object),
      cn_vendors: ['zhipuai']
    }))
    const wrapper = await mountReady()
    const id = await addRow(wrapper)

    await wrapper.find(`[data-testid="pricing-row-model-${id}"]`).setValue('glm-4.6')
    await wrapper.find(`[data-testid="pricing-row-input-${id}"]`).setValue('0.6')
    await wrapper.find(`[data-testid="pricing-row-output-${id}"]`).setValue('2.2')
    await wrapper.find(`[data-testid="pricing-row-cache_read-${id}"]`).setValue('0.11')

    // 添加了但一个数字都不填。
    await wrapper.find(`[data-testid="pricing-row-add-extra-${id}"]`).trigger('click')
    await wrapper.find(`[data-testid="pricing-row-extra-option-input_priority-${id}"]`).trigger('click')

    await wrapper.find('[data-testid="pricing-save"]').trigger('click')
    await flushPromises()

    expect(updateCnyOfficialConfig).toHaveBeenCalledTimes(1)
    const payload = updateCnyOfficialConfig.mock.calls[0][0] as {
      manual_overrides: Record<string, Record<string, number>>
    }
    expect(payload.manual_overrides['glm-4.6']).not.toHaveProperty('input_priority')
  })
})

describe('Pricing V2 contract', () => {
  const byId = (name: string) => `[data-testid="${name}"]`
  const model = 'vendor/exact-model'
  const persisted = { ...defaultConfig({ [model]: { input: 1, output: 4, cache_read: 0 } }), revision: 'r1', active_revision: 'r1' }

  beforeEach(() => {
    listHistory.mockReset().mockResolvedValue({ items: [], total: 0 })
    getStatus.mockReset().mockResolvedValue({ enabled: true })
    getCnyOfficialConfig.mockReset()
    updateCnyOfficialConfig.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
  })

  function recordDom(label: string, wrapper: ReturnType<typeof mountView>, selectors: string[]) {
    if (!process.env.PRICING_UI_DOM) return
    const nodes = selectors.map((selector) => wrapper.get(selector).element)
    const values = nodes.flatMap((node) => Array.from(node.querySelectorAll<HTMLInputElement | HTMLSelectElement>('input, select'), (control) => ({
      id: control.dataset.testid,
      value: control instanceof HTMLInputElement && control.type === 'checkbox' ? control.checked : control.value
    })))
    console.info(`DOM ${label}\n${nodes.map((node) => node.outerHTML).join('\n')}\nControl values: ${JSON.stringify(values)}`)
  }

  it('sends the editable fields and revision to the existing API route', async () => {
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: persisted })
    const put = vi.spyOn(apiClient, 'put').mockImplementation(async (_path, payload) => ({ data: { ...payload, revision: 'r2', active_revision: 'r1' } }))
    try {
      const config = await getConfig()
      expect(get).toHaveBeenCalledWith('/admin/model-pricing-sync/cny-official')
      const manual = { [model]: { billing_mode: 'per_image' as const, image_output_per_image: 0, auto_restore: false } }
      const next = await putConfig({ ...config, manual_overrides: manual })
      expect(put).toHaveBeenCalledWith('/admin/model-pricing-sync/cny-official', { ...defaultConfig(), revision: 'r1', manual_overrides: manual })
      expect(next).toMatchObject({ revision: 'r2', active_revision: 'r1', manual_overrides: manual })
    } finally {
      get.mockRestore()
      put.mockRestore()
    }
  })

  it.each([
    ['per_request', 'per_request_price', '站内单位 / 次', '0'],
    ['per_second', 'per_second_price', '站内单位 / 秒', '0.375'],
    ['per_image', 'image_output_per_image', '站内单位 / 张', '2.125']
  ])('creates, saves and reopens a %s price without token inputs', async (mode, key, unit, price) => {
    updateCnyOfficialConfig.mockImplementation(async (payload) => normalizeCnyOfficialConfig({ ...payload, revision: 'r2', active_revision: 'r2' }))
    const wrapper = await mountReady(persisted)
    await wrapper.get(byId('pricing-add-row')).trigger('click')
    const suffix = rowSuffix(wrapper, 1)
    await wrapper.get(byId(`pricing-row-model-${suffix}`)).setValue('new-model')
    const select = wrapper.get(byId(`pricing-row-mode-${suffix}`))
    expect(select.findAll('option').map((opt) => opt.attributes('value'))).toEqual(['token', 'per_request', 'per_second', 'per_image'])
    await select.setValue(mode)
    expect(wrapper.find(byId(`pricing-row-input-${suffix}`)).exists()).toBe(false)
    expect(wrapper.get(byId(`pricing-row-unit-${suffix}`)).text()).toBe(unit)
    await wrapper.get(byId(`pricing-row-${key}-${suffix}`)).setValue(price)
    await wrapper.get(byId(`pricing-row-auto-restore-${suffix}`)).setValue(true)
    await wrapper.get(byId('pricing-save')).trigger('click')
    await flushPromises()
    expect(updateCnyOfficialConfig).toHaveBeenCalledWith({
      ...defaultConfig(), revision: 'r1', manual_overrides: { ...persisted.manual_overrides, 'new-model': { billing_mode: mode, [key]: Number(price), auto_restore: true } }
    })
    const reopened = rowSuffix(wrapper)
    await wrapper.get(byId(`pricing-row-toggle-${reopened}`)).trigger('click')
    expect(wrapper.get<HTMLSelectElement>(byId(`pricing-row-mode-${reopened}`)).element.value).toBe(mode)
    expect(wrapper.get<HTMLInputElement>(byId(`pricing-row-${key}-${reopened}`)).element.value).toBe(price)
    expect(wrapper.get<HTMLInputElement>(byId(`pricing-row-auto-restore-${reopened}`)).element.checked).toBe(true)
    recordDom(mode, wrapper, [`fieldset:has(${byId(`pricing-row-mode-${reopened}`)})`])
    wrapper.unmount()
  })

  it('edits four TTLs independently and preserves zero, null and absent prices across the API decoder', async () => {
    const config = normalizeCnyOfficialConfig({ ...persisted, manual_overrides: { [model]: {
      input: 1, output: 4, cache_read: 0, cache_write: 0, cache_write_1h: null, cache_write_12h: 12
    } } })
    expect(config).toMatchObject({ revision: 'r1', active_revision: 'r1', manual_overrides: { [model]: {
      cache_write: 0, cache_write_1h: null, cache_write_12h: 12
    } } })
    expect(config.manual_overrides[model]).not.toHaveProperty('cache_write_30m')
    updateCnyOfficialConfig.mockResolvedValue(config)
    const wrapper = await mountReady(config)
    const suffix = rowSuffix(wrapper)
    await wrapper.get(byId(`pricing-row-toggle-${suffix}`)).trigger('click')
    const fields = ['cache_write', 'cache_write_1h', 'cache_write_30m', 'cache_write_12h']
    expect(fields.map((field) => wrapper.get<HTMLInputElement>(byId(`pricing-row-${field}-${suffix}`)).element.value)).toEqual(['0', '', '', '12'])
    expect(wrapper.get(byId(`pricing-row-cache_write_1h-${suffix}`)).attributes('placeholder')).not.toBe(wrapper.get(byId(`pricing-row-cache_write_30m-${suffix}`)).attributes('placeholder'))
    recordDom('token TTLs', wrapper, [`fieldset:has(${byId(`pricing-row-mode-${suffix}`)})`])
    await wrapper.get(byId(`pricing-row-cache_write_30m-${suffix}`)).setValue('0.3')
    await wrapper.get(byId('pricing-save')).trigger('click')
    expect(updateCnyOfficialConfig.mock.calls[0][0].manual_overrides[model]).toEqual({
      input: 1, output: 4, cache_read: 0, cache_write: 0, cache_write_1h: null, cache_write_30m: 0.3, cache_write_12h: 12
    })
    wrapper.unmount()
  })

  it('compares stored manual, verified auto and confirmed effective prices without conflating them', async () => {
    const quote = { model, provider: 'vendor', currency: 'USD', billing_mode: 'per_second', rates: { cost_per_second: 0.125 }, evidence: 'snapshot-abc', source_url: 'https://example.com/official', verified_at: '2026-11-22T00:00:00Z' }
    getStatus.mockResolvedValue({ enabled: true, pricing_v2: { active_revision: 'r0', models: {
      [model]: { mode: 'auto', auto: quote, effective: { ...quote, rates: { cost_per_second: 0 } }, issue: 'missing_required_rate: cost_per_request' }
    } } })
    const wrapper = await mountReady({ ...persisted, active_revision: 'r0' })
    expect(wrapper.get(byId('pricing-revisions')).text()).toContain('r1')
    expect(wrapper.get(byId('pricing-activation-pending')).text()).toContain('尚未生效')
    expect(wrapper.get(byId('pricing-quote-auto')).text()).toContain('0.125 USD / 秒')
    expect(wrapper.get(byId('pricing-quote-auto')).text()).toContain('snapshot-abc')
    expect(wrapper.get(byId('pricing-quote-auto')).text()).toContain('https://example.com/official')
    expect(wrapper.get(byId('pricing-quote-manual')).text()).toContain('4 站内单位 / 百万 tokens')
    expect(wrapper.get(byId('pricing-quote-effective')).text()).toContain('0 USD / 秒')
    expect(wrapper.get(byId('pricing-model-issue')).text()).toContain('missing_required_rate: cost_per_request')
    recordDom('stored but not active', wrapper, ['pricing-revisions', 'pricing-activation-pending', 'pricing-quote-auto', 'pricing-quote-manual', 'pricing-quote-effective', 'pricing-model-issue'].map(byId))
    wrapper.unmount()
  })

  it('shows a dated original-currency quote with its absolute window conditions', async () => {
    const quote = {
      model, provider: 'fixture vendor', currency: 'CNY', billing_mode: 'token',
      rates: { input_cost_per_token: 2e-6, output_cost_per_token: 3e-6 },
      source_url: 'https://example.com/tariff', evidence: 'fixture-sheet', verified_at: '2026-11-21T05:20:00Z',
      windows: [{ start: '09:00', end: '13:00', timezone: 'Asia/Shanghai', weekdays: [1, 3, 5], exclude_cn_holidays: true,
        rates: { input_cost_per_token: 9e-6, cache_creation_input_token_cost_30m: 0, cache_creation_input_token_cost_12h: 13e-6 } }]
    }
    getStatus.mockResolvedValue({ enabled: true, pricing_v2: { models: { [model]: { mode: 'auto', auto: quote, effective: quote } } } })
    const wrapper = await mountReady(persisted)
    const card = wrapper.get(byId('pricing-quote-auto'))
    expect(card.get('time').attributes('datetime')).toBe(quote.verified_at)
    const window = card.findAll('dl').at(-1)!
    for (const value of ['09:00–13:00 (Asia/Shanghai)', '周一', '周三', '周五', '9 CNY / 百万 tokens', '0 CNY / 百万 tokens', '13 CNY / 百万 tokens']) {
      expect(window.text()).toContain(value)
    }
    expect(window.text()).toMatch(/节假日/)
    expect(wrapper.get(byId('pricing-quote-effective')).text()).toContain('不代表实际采购成本')
    wrapper.unmount()
  })

  const exportPath = process.env.PLAZA_ADMIN_PAYLOAD_OUT
  const responsePath = process.env.PLAZA_ADMIN_READBACK
  it.runIf(!!exportPath || !!responsePath)('bridges edited controls to the disposable HTTP publication test', async () => {
    if (responsePath) {
      const { data } = JSON.parse(readFileSync(responsePath, 'utf8'))
      const reopened = await mountReady(normalizeCnyOfficialConfig(data))
      const row = reopened.findAll<HTMLInputElement>('input[data-testid^="pricing-row-model-"]').find(input => input.element.value === 'fixture-manual')!
      const suffix = row.attributes('data-testid')!.replace('pricing-row-model-', '')
      await reopened.get(byId(`pricing-row-toggle-${suffix}`)).trigger('click')
      for (const [key, value] of Object.entries({ input: '2', output: '11', cache_write: '4', cache_write_1h: '', cache_write_30m: '0', cache_write_12h: '13' })) {
        expect(reopened.get<HTMLInputElement>(byId(`pricing-row-${key}-${suffix}`)).element.value).toBe(value)
      }
      reopened.unmount()
      return
    }
    updateCnyOfficialConfig.mockResolvedValue(persisted)
    const wrapper = await mountReady({ ...defaultConfig(), revision: 'r1' })
    for (const [name, mode, fields] of [
      ['fixture-manual', 'token', { input: '2', output: '11', cache_read: '0.5', cache_write: '4', cache_write_30m: '0', cache_write_12h: '13' }],
      ['fixture-request', 'per_request', { per_request_price: '0' }],
      ['fixture-second', 'per_second', { per_second_price: '0.375' }],
      ['fixture-image', 'per_image', { image_output_per_image: '2.125' }]
    ] as const) {
      await wrapper.get(byId('pricing-add-row')).trigger('click')
      const suffix = rowSuffix(wrapper, wrapper.findAll('[data-testid^="pricing-row-model-"]').length - 1)
      await wrapper.get(byId(`pricing-row-model-${suffix}`)).setValue(name)
      await wrapper.get(byId(`pricing-row-mode-${suffix}`)).setValue(mode)
      for (const [field, value] of Object.entries(fields)) await wrapper.get(byId(`pricing-row-${field}-${suffix}`)).setValue(value)
    }
    await wrapper.get(byId('pricing-save')).trigger('click')
    expect(updateCnyOfficialConfig).toHaveBeenCalledTimes(1)
    writeFileSync(exportPath!, JSON.stringify(updateCnyOfficialConfig.mock.calls[0][0]), { mode: 0o600 })
    wrapper.unmount()
  })

  it('keeps manual on failed restore and removes only that model on a revisioned retry', async () => {
    const other = { input: 8, output: 9, cache_read: 0 }
    const config = { ...persisted, manual_overrides: { ...persisted.manual_overrides, 'another/model': other } }
    updateCnyOfficialConfig.mockRejectedValueOnce({ status: 409, message: '价格修订已变更，请重新读取后保存: no trusted price: missing cache_write_12h' })
      .mockImplementationOnce(async (payload) => ({ ...payload, revision: 'r2', active_revision: 'r2' }))
    const wrapper = await mountReady(config)
    const restore = wrapper.get(byId(`pricing-restore-row-${rowSuffix(wrapper, 1)}`))
    await restore.trigger('click')
    await flushPromises()
    expect(wrapper.get(byId('pricing-save-error')).text()).toContain('missing cache_write_12h')
    expect(wrapper.findAll('input[data-testid^="pricing-row-model-"]')).toHaveLength(2)
    recordDom('restore refused', wrapper, [byId('pricing-save-error')])
    await restore.trigger('click')
    await flushPromises()
    expect(updateCnyOfficialConfig.mock.calls[1][0]).toEqual({ ...defaultConfig({ 'another/model': other }), revision: 'r1' })
    expect(wrapper.findAll('input[data-testid^="pricing-row-model-"]')).toHaveLength(1)
    wrapper.unmount()
  })

  it('will not restore a saved override while another draft is unsaved', async () => {
    const wrapper = await mountReady(persisted)
    await wrapper.get(byId('pricing-add-row')).trigger('click')
    const restore = wrapper.get(byId('pricing-restore-row-1'))
    expect(restore.attributes('disabled')).toBeDefined()
    await restore.trigger('click')
    expect(updateCnyOfficialConfig).not.toHaveBeenCalled()
    await wrapper.get(byId('pricing-discard-draft')).trigger('click')
    expect(wrapper.findAll('input[data-testid^="pricing-row-model-"]')).toHaveLength(1)
    expect(wrapper.get('[data-testid^="pricing-restore-row-"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('keeps the draft on conflict and requires explicit reload', async () => {
    updateCnyOfficialConfig.mockRejectedValueOnce({ status: 409, message: '价格修订已变更，请重新读取后保存' })
    const wrapper = await mountReady(persisted)
    const suffix = rowSuffix(wrapper)
    await wrapper.get(byId(`pricing-row-output-${suffix}`)).setValue('9')
    await wrapper.get(byId('pricing-save')).trigger('click')
    await flushPromises()
    expect(wrapper.get(byId('pricing-revision-conflict')).text()).toContain('请重新读取后保存')
    expect(wrapper.get<HTMLInputElement>(byId(`pricing-row-output-${suffix}`)).element.value).toBe('9')
    expect(wrapper.get(byId('pricing-save')).attributes('disabled')).toBeDefined()
    recordDom('revision conflict', wrapper, [byId('pricing-revision-conflict')])
    getCnyOfficialConfig.mockResolvedValue({ ...persisted, revision: 'r2' })
    await wrapper.get(byId('pricing-conflict-reload')).trigger('click')
    await flushPromises()
    expect(wrapper.get(byId('pricing-revisions')).text()).toContain('r2')
    expect(updateCnyOfficialConfig).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})
