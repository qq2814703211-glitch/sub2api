import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import ModelPricingSyncView from '@/views/admin/ModelPricingSyncView.vue'

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

async function mountReady(config = defaultConfig()) {
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
    ['missing slash', 'deepseek-flash', '1', '4', '0.02', '模型键需为 vendor/model 格式'],
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
    expect(wrapper.find('[data-testid="pricing-row-1"]').text()).toContain('¥1 / 4 / 0.02')
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

    expect(wrapper.find('[data-testid="pricing-row-1"]').text()).toContain('¥9')
  })
})
