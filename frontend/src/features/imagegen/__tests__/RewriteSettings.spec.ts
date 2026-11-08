/**
 * 密钥卡片（生图 + AI 改写）的验收断言。
 *
 * 用户对界面形态有明确要求，这三条必须由测试守住，不能只靠代码走查：
 *   1. 页面上没有可填的 baseURL —— 地址写死，只有只读展示；
 *   2. 没有协议下拉 —— 协议由密钥所属分组自动判定，只有只读说明；
 *   3. 没有手填 key 的输入框 —— 两个密钥都从下拉里选。
 *
 * 另外还钉住返工后必须能用的四件事：校验令牌真的打一次请求、详细配置弹窗
 * 能开能存能回读、模型列表跟着所选密钥变、思考强度按模型能力过滤或给全量七档。
 */
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'

// 密钥详情走 axios（XHR），在 jsdom 里会真的发网络请求并挂住；这里直接替换模块。
// 协议判定只依赖 group.platform，所以给个最小桩即可。
vi.mock('@/api/keys', () => ({
  keysAPI: {
    list: vi.fn(async () => ({ items: [], total: 0, page: 1, page_size: 100, pages: 0 })),
    getById: vi.fn(async (id: number) => ({
      id,
      key: 'sk-detail',
      name: 'key-detail',
      group: { platform: id === 2 ? 'anthropic' : 'openai' },
    })),
  },
}))

import RewriteSettings from '../RewriteSettings.vue'
import zh from '@/i18n/locales/zh'
import {
  DEFAULT_REWRITE_CONFIG,
  REWRITE_API_BASE_URL,
  effortOptionsForModel,
  type RewriteConfig,
  type RewriteModelOption,
} from '../rewriteConfig'
import type { ApiKey } from '@/types'

/**
 * 等到「点击 → 校验 → 回填」整条异步链走完。
 *
 * 校验里包含 fetch + response.json() + 密钥详情三处 await，单次 flushPromises
 * 只能推进一层微任务，所以这里多推几轮并把宏任务也放过去。
 */
async function settle(): Promise<void> {
  for (let i = 0; i < 5; i += 1) {
    await flushPromises()
    await new Promise((resolve) => setTimeout(resolve, 0))
  }
}

function makeI18n() {
  return createI18n({ legacy: false, locale: 'zh', fallbackLocale: 'zh', messages: { zh } })
}

function apiKey(overrides: Partial<ApiKey> = {}): ApiKey {
  return {
    id: 1,
    key: 'sk-secret-plaintext',
    name: 'key-a',
    group_id: 3,
    group: { id: 3, name: 'OpenAI 分组', platform: 'openai' } as never,
    ...overrides,
  } as ApiKey
}

const KEYS: ApiKey[] = [
  apiKey({ id: 1, name: 'key-a', key: 'sk-a', group: { id: 3, name: 'OpenAI 分组', platform: 'openai' } as never }),
  apiKey({ id: 2, name: 'key-b', key: 'sk-b', group: { id: 4, name: 'Claude 分组', platform: 'anthropic' } as never }),
]

function mountCard(props: Record<string, unknown> = {}) {
  return mount(RewriteSettings, {
    global: { plugins: [makeI18n()] },
    props: {
      config: { ...DEFAULT_REWRITE_CONFIG, keyId: 1 },
      keys: KEYS,
      generationKeyId: 1,
      modelOptions: [{ id: 'gpt-5.5' }, { id: 'gpt-6-astra' }] as RewriteModelOption[],
      generationKeyStatus: 'unknown',
      checkingGenerationKey: false,
      modelsLoading: false,
      usesGenerationKey: false,
      ...props,
    },
  })
}

/** 页面上所有可编辑的文本类控件（input/textarea），下拉与按钮不算。 */
function textInputs(wrapper: ReturnType<typeof mountCard>) {
  return wrapper.findAll('input, textarea')
}

describe('硬约束：界面不得出现地址框 / 协议下拉 / 手填 key', () => {
  it('没有任何可编辑的文本输入框（key 与地址都改成只读或下拉）', () => {
    const wrapper = mountCard()
    expect(textInputs(wrapper)).toHaveLength(0)
    wrapper.unmount()
  })

  it('没有 type=password 的输入框 —— 密钥不再手填', () => {
    const wrapper = mountCard()
    expect(wrapper.findAll('input[type="password"]')).toHaveLength(0)
    wrapper.unmount()
  })

  it('没有任何 input 元素的 placeholder 是地址形状', () => {
    const wrapper = mountCard()
    for (const node of textInputs(wrapper)) {
      expect(String(node.attributes('placeholder') ?? '')).not.toMatch(/https?:\/\//)
    }
    // 整个组件的 HTML 里也不出现可编辑地址：地址只以只读文本出现在弹窗中。
    expect(wrapper.html()).not.toContain('<input')
    wrapper.unmount()
  })

  it('协议没有下拉：所有 select 都不带协议选项', () => {
    const wrapper = mountCard()
    const selects = wrapper.findAll('select')
    // 卡片默认只有生图密钥、改写密钥两个 select。
    expect(selects).toHaveLength(2)
    for (const select of selects) {
      const values = Array.from((select.element as HTMLSelectElement).options).map((option) => option.value)
      expect(values).not.toContain('openai-chat')
      expect(values).not.toContain('openai-responses')
      expect(values).not.toContain('anthropic-messages')
    }
    wrapper.unmount()
  })

  it('协议只以只读说明出现，并且带出写死的地址', async () => {
    const wrapper = mountCard()
    const note = wrapper.find('[data-test="rewrite-protocol-readonly"]')
    expect(note.exists()).toBe(true)
    // 文案说明「自动识别」，并展示固定地址。
    expect(note.text()).toContain('自动识别')
    expect(note.text()).toContain(REWRITE_API_BASE_URL)
    expect(REWRITE_API_BASE_URL).toBe('https://api.example.com')
    wrapper.unmount()
  })

  it('协议说明随密钥所属分组变化（Claude 分组 → Messages）', async () => {
    const wrapper = mountCard({ config: { ...DEFAULT_REWRITE_CONFIG, keyId: 2 } })
    const note = wrapper.find('[data-test="rewrite-protocol-readonly"]')
    expect(note.text()).toContain('Anthropic Messages')
    wrapper.unmount()
  })
})

describe('同一张卡片内的两个密钥分区', () => {
  it('「生图密钥」与「AI 改写密钥」两个标题都在同一张卡片', () => {
    const wrapper = mountCard()
    const text = wrapper.text()
    expect(text).toContain(zh.imagegen.generationKeySection)
    expect(text).toContain(zh.imagegen.rewriteKeySection)
    // 同一张卡片：section.card 只有一层，两个分区都在里面。
    expect(wrapper.findAll('section.card')).toHaveLength(1)
    wrapper.unmount()
  })

  it('卡片默认只露两个密钥下拉、说明、校验按钮、详细配置按钮', () => {
    const wrapper = mountCard()
    expect(wrapper.find('[data-test="generation-key-select"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="rewrite-key-select"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="generation-key-check"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="rewrite-key-check"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="rewrite-advanced-open"]').exists()).toBe(true)
    // 模型与思考强度默认不在卡片里，只在弹窗里。
    expect(wrapper.find('[data-test="rewrite-model-select"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="rewrite-effort-select"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('两个下拉都只显示密钥名与分组名，不泄露完整 key', () => {
    const wrapper = mountCard()
    const html = wrapper.html()
    expect(html).not.toContain('sk-a')
    expect(html).not.toContain('sk-b')
    expect(html).not.toContain('sk-secret-plaintext')
    expect(wrapper.text()).toContain('key-a')
    expect(wrapper.text()).toContain('OpenAI 分组')
    wrapper.unmount()
  })

  it('选另一个密钥会发出 update:keyId', async () => {
    const wrapper = mountCard()
    await wrapper.find('[data-test="rewrite-key-select"]').setValue('2')
    expect(wrapper.emitted('update:keyId')?.[0]).toEqual([2])
    wrapper.unmount()
  })

  it('选「请选择密钥」清空选择时发出 null', async () => {
    const wrapper = mountCard()
    await wrapper.find('[data-test="rewrite-key-select"]').setValue('')
    expect(wrapper.emitted('update:keyId')?.[0]).toEqual([null])
    wrapper.unmount()
  })

  it('旧版手填令牌会提示仍可用，但不给编辑入口', () => {
    const wrapper = mountCard({ config: { ...DEFAULT_REWRITE_CONFIG, keyId: null, legacyApiKey: 'sk-old' } })
    expect(wrapper.text()).toContain(zh.imagegen.rewriteLegacyKeyNotice)
    expect(textInputs(wrapper)).toHaveLength(0)
    wrapper.unmount()
  })
})

describe('详细配置弹窗：能开、能存、能回读', () => {
  it('点「详细配置」后弹窗打开，模型与思考强度出现', async () => {
    const wrapper = mountCard()
    await wrapper.find('[data-test="rewrite-advanced-open"]').trigger('click')
    await flushPromises()

    // BaseDialog Teleport 到 body，内容在 document 里而不在 wrapper 内。
    expect(document.body.textContent).toContain(zh.imagegen.rewriteAdvancedTitle)
    expect(document.querySelector('[data-test="rewrite-model-select"]')).toBeTruthy()
    expect(document.querySelector('[data-test="rewrite-effort-select"]')).toBeTruthy()
    wrapper.unmount()
  })

  it('弹窗里的地址是只读文本，不是输入框', async () => {
    const wrapper = mountCard()
    await wrapper.find('[data-test="rewrite-advanced-open"]').trigger('click')
    await flushPromises()

    const endpoint = document.querySelector('[data-test="rewrite-endpoint-readonly"]')
    expect(endpoint).toBeTruthy()
    expect(endpoint!.textContent).toContain(`${REWRITE_API_BASE_URL}/v1/chat/completions`)
    // 只读：不是 input/textarea。
    expect(endpoint!.tagName).not.toBe('INPUT')
    expect(endpoint!.tagName).not.toBe('TEXTAREA')
    wrapper.unmount()
  })

  it('改模型 / 改思考强度都通过事件回传，好让父级落盘', async () => {
    const wrapper = mountCard()
    await wrapper.find('[data-test="rewrite-advanced-open"]').trigger('click')
    await flushPromises()

    const modelSelect = document.querySelector('[data-test="rewrite-model-select"]') as HTMLSelectElement
    modelSelect.value = 'gpt-6-astra'
    modelSelect.dispatchEvent(new Event('change'))
    await flushPromises()
    expect(wrapper.emitted('update:model')?.[0]).toEqual(['gpt-6-astra'])

    const effortSelect = document.querySelector('[data-test="rewrite-effort-select"]') as HTMLSelectElement
    effortSelect.value = 'high'
    effortSelect.dispatchEvent(new Event('change'))
    await flushPromises()
    expect(wrapper.emitted('update:effort')?.[0]).toEqual(['high'])

    wrapper.unmount()
  })

  it('已存的配置能回读：弹窗里显示的正是当前配置', async () => {
    const wrapper = mountCard({
      config: { ...DEFAULT_REWRITE_CONFIG, keyId: 1, model: 'gpt-6-astra', effort: 'high' } as RewriteConfig,
    })
    await wrapper.find('[data-test="rewrite-advanced-open"]').trigger('click')
    await flushPromises()

    const modelSelect = document.querySelector('[data-test="rewrite-model-select"]') as HTMLSelectElement
    expect(modelSelect.value).toBe('gpt-6-astra')
    const effortSelect = document.querySelector('[data-test="rewrite-effort-select"]') as HTMLSelectElement
    expect(effortSelect.value).toBe('high')
    wrapper.unmount()
  })
})

describe('模型列表随所选密钥变化', () => {
  it('下拉只列出传进来的模型候选', async () => {
    const wrapper = mountCard({ modelOptions: [{ id: 'gpt-5.5' }, { id: 'gpt-6-astra' }] })
    await wrapper.find('[data-test="rewrite-advanced-open"]').trigger('click')
    await flushPromises()

    const select = document.querySelector('[data-test="rewrite-model-select"]') as HTMLSelectElement
    expect(Array.from(select.options).map((option) => option.value)).toEqual(['gpt-5.5', 'gpt-6-astra'])
    wrapper.unmount()
  })

  it('换一把密钥后候选列表跟着换（父级重新解析后传入）', async () => {
    const wrapper = mountCard({ modelOptions: [{ id: 'gpt-5.5' }] })
    await wrapper.find('[data-test="rewrite-advanced-open"]').trigger('click')
    await flushPromises()
    let select = document.querySelector('[data-test="rewrite-model-select"]') as HTMLSelectElement
    expect(Array.from(select.options).map((option) => option.value)).toEqual(['gpt-5.5'])

    // 模拟选中 Anthropic 分组的密钥后，父级拿到的是完全不同的模型集合。
    await wrapper.setProps({
      config: { ...DEFAULT_REWRITE_CONFIG, keyId: 2 },
      modelOptions: [{ id: 'claude-haiku-5-5' }, { id: 'claude-sonnet-5-5' }],
    })
    await flushPromises()
    select = document.querySelector('[data-test="rewrite-model-select"]') as HTMLSelectElement
    expect(Array.from(select.options).map((option) => option.value)).toEqual([
      'claude-haiku-5-5',
      'claude-sonnet-5-5',
    ])
    wrapper.unmount()
  })

  it('没选密钥时下拉禁用并给出说明', async () => {
    const wrapper = mountCard({ config: { ...DEFAULT_REWRITE_CONFIG, keyId: null }, modelOptions: [] })
    await wrapper.find('[data-test="rewrite-advanced-open"]').trigger('click')
    await flushPromises()

    const select = document.querySelector('[data-test="rewrite-model-select"]') as HTMLSelectElement
    expect(select.disabled).toBe(true)
    expect(document.body.textContent).toContain(zh.imagegen.rewriteModelNeedKey)
    wrapper.unmount()
  })

  it('加载中 / 该分组无模型时都有对应提示', async () => {
    const loading = mountCard({ modelsLoading: true })
    await loading.find('[data-test="rewrite-advanced-open"]').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain(zh.imagegen.rewriteModelLoading)
    loading.unmount()

    const empty = mountCard({ modelOptions: [] })
    await empty.find('[data-test="rewrite-advanced-open"]').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain(zh.imagegen.rewriteModelNone)
    empty.unmount()
  })
})

describe('思考强度按模型能力过滤或给全量七档', () => {
  it('模型声明了档位清单时只给这些档位', async () => {
    const wrapper = mountCard({
      modelOptions: [
        {
          id: 'grok-4.7',
          supportsReasoningEffort: true,
          reasoningEfforts: [{ value: 'low' }, { value: 'medium' }, { value: 'high', default: true }],
        },
      ],
      config: { ...DEFAULT_REWRITE_CONFIG, keyId: 1, model: 'grok-4.7' },
    })
    await wrapper.find('[data-test="rewrite-advanced-open"]').trigger('click')
    await flushPromises()

    const select = document.querySelector('[data-test="rewrite-effort-select"]') as HTMLSelectElement
    const values = Array.from(select.options).map((option) => option.value)
    expect(values).toEqual(['unset', 'off', 'low', 'medium', 'high'])
    expect(values).not.toContain('max')
    wrapper.unmount()
  })

  it('拿不到能力元数据时给全量七档（含 off 与 xhigh）', async () => {
    const wrapper = mountCard({
      modelOptions: [{ id: 'plain-model' }],
      config: { ...DEFAULT_REWRITE_CONFIG, keyId: 1, model: 'plain-model' },
    })
    await wrapper.find('[data-test="rewrite-advanced-open"]').trigger('click')
    await flushPromises()

    const select = document.querySelector('[data-test="rewrite-effort-select"]') as HTMLSelectElement
    const values = Array.from(select.options).map((option) => option.value)
    for (const level of ['off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max']) {
      expect(values).toContain(level)
    }
    // unset（自动）单独在最前面。
    expect(values[0]).toBe('unset')
    expect(values).toHaveLength(8)
    wrapper.unmount()
  })

  it('明确不支持思考的模型只给 off', async () => {
    const wrapper = mountCard({
      modelOptions: [{ id: 'plain-model', supportsReasoningEffort: false }],
      config: { ...DEFAULT_REWRITE_CONFIG, keyId: 1, model: 'plain-model' },
    })
    await wrapper.find('[data-test="rewrite-advanced-open"]').trigger('click')
    await flushPromises()

    const select = document.querySelector('[data-test="rewrite-effort-select"]') as HTMLSelectElement
    expect(Array.from(select.options).map((option) => option.value)).toEqual(['unset', 'off'])
    wrapper.unmount()
  })

  it('与纯函数给出的档位完全一致（组件不自作主张）', async () => {
    const meta: RewriteModelOption = {
      id: 'x',
      supportsReasoningEffort: true,
      reasoningEfforts: [{ value: 'minimal' }, { value: 'medium' }],
    }
    const wrapper = mountCard({
      modelOptions: [meta],
      config: { ...DEFAULT_REWRITE_CONFIG, keyId: 1, model: 'x' },
    })
    await wrapper.find('[data-test="rewrite-advanced-open"]').trigger('click')
    await flushPromises()

    const select = document.querySelector('[data-test="rewrite-effort-select"]') as HTMLSelectElement
    expect(Array.from(select.options).map((option) => option.value)).toEqual([
      'unset',
      ...effortOptionsForModel(meta, 'openai-chat'),
    ])
    wrapper.unmount()
  })
})

describe('校验令牌真的打一次请求', () => {
  let originalFetch: typeof globalThis.fetch

  beforeEach(() => {
    originalFetch = globalThis.fetch
  })

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('点 AI 改写密钥的校验按钮会打 GET /v1/models 并显示结果', async () => {
    const spy = vi.fn(async (_input: RequestInfo | URL) =>
      new Response(JSON.stringify({ data: [{ id: 'gpt-5.5' }, { id: 'gpt-6-astra' }] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    globalThis.fetch = spy as unknown as typeof globalThis.fetch

    const wrapper = mountCard()
    await wrapper.find('[data-test="rewrite-key-check"]').trigger('click')
    await settle()

    expect(spy).toHaveBeenCalledTimes(1)
    const [url, init] = spy.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe(`${REWRITE_API_BASE_URL}/v1/models`)
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer sk-a')

    // 结果回显：可用 + 模型数量。
    expect(wrapper.text()).toContain('2')
    expect(wrapper.text()).toContain(zh.imagegen.keyValid)
    wrapper.unmount()
  })

  it('令牌不可用时显示失败而不是静默', async () => {
    globalThis.fetch = vi.fn(
      async (_input: RequestInfo | URL) =>
        new Response(JSON.stringify({ error: { message: 'invalid key' } }), { status: 401 }),
    ) as unknown as typeof globalThis.fetch

    const wrapper = mountCard()
    await wrapper.find('[data-test="rewrite-key-check"]').trigger('click')
    await settle()

    expect(wrapper.text()).toContain(zh.imagegen.keyInvalid)
    expect(wrapper.text()).toContain('令牌不可用')
    wrapper.unmount()
  })

  it('没选密钥时按钮禁用，也不会发请求', async () => {
    const spy = vi.fn()
    globalThis.fetch = spy as unknown as typeof globalThis.fetch

    const wrapper = mountCard({ config: { ...DEFAULT_REWRITE_CONFIG, keyId: null } })
    const button = wrapper.find('[data-test="rewrite-key-check"]')
    expect((button.element as HTMLButtonElement).disabled).toBe(true)
    expect(spy).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('生图密钥的校验按钮走父级（同一个 /v1/models 判据）', async () => {
    const wrapper = mountCard()
    await wrapper.find('[data-test="generation-key-check"]').trigger('click')
    expect(wrapper.emitted('check-generation-key')).toBeTruthy()
    wrapper.unmount()
  })
})
