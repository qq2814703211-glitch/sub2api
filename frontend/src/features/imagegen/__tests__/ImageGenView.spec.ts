/**
 * Mount smoke test for the image-generation plaza.
 *
 * The page is the one place where 73 JSON presets, an IndexedDB-backed history
 * and a Teleported drawer all meet. A type-check cannot catch a template that
 * throws on mount, so this renders the real component with the network stubbed
 * and asserts the interactive parts appear.
 *
 * Render the real Chinese messages using the installed vue-i18n compiler.
 * Assertions use the locale values so translated controls remain interactive.
 */
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { createPinia, setActivePinia } from 'pinia'

import ImageGenView from '@/views/ImageGenView.vue'
import zh from '@/i18n/locales/zh'

// AppLayout pulls in the whole nav tree; a passthrough keeps the test focused
// on the page's own markup.
vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { name: 'AppLayout', template: '<div><slot /></div>' },
}))

function makeI18n() {
  return createI18n({
    legacy: false,
    locale: 'zh',
    fallbackLocale: 'zh',
    messages: { zh },
  })
}

/** Minimal fetch stub: the catalog and model list are the only real calls. */
function stubFetch() {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = String(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url)
    if (url.includes('templates.json')) {
      return new Response(
        JSON.stringify({
          templates: [
            {
              name: '测试模板',
              category: '海报广告',
              prompt: 'a test prompt',
              preview: 'previews/x.webp',
              params: { size: '1024x1024', quality: 'high', count: 2 },
            },
          ],
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      )
    }
    return new Response(JSON.stringify({ data: [] }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  })
}

const mountView = () => mount(ImageGenView, { global: { plugins: [makeI18n()] } })

describe('ImageGenView', () => {
  let originalFetch: typeof globalThis.fetch

  beforeEach(() => {
    setActivePinia(createPinia())
    originalFetch = globalThis.fetch
    globalThis.fetch = stubFetch() as unknown as typeof globalThis.fetch
    // jsdom has no IndexedDB; the history layer tolerates its absence.
    if (!('indexedDB' in globalThis)) {
      Object.defineProperty(globalThis, 'indexedDB', { configurable: true, value: undefined })
    }
  })

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('mounts and renders the generate form', async () => {
    const wrapper = mountView()
    await flushPromises()

    // 生成按钮与密钥卡片是用户最先要用的两样东西。
    const buttons = wrapper.findAll('button').map((node) => node.text())
    expect(buttons.some((label) => label.includes(zh.imagegen.generate))).toBe(true)
    // 密钥改成下拉：页面上不再有手填令牌的密码框。
    expect(wrapper.find('input[type="password"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="generation-key-select"]').exists()).toBe(true)
    expect(wrapper.text()).toContain(zh.imagegen.promptLabel)

    wrapper.unmount()
  })

  it('seeds the default model and size', async () => {
    const wrapper = mountView()
    await flushPromises()

    const values = wrapper.findAll('input').map((node) => (node.element as HTMLInputElement).value)
    expect(values).toContain('gpt-image-2')

    // Size is a grouped <select>, not a text input.
    const sizeSelect = wrapper
      .findAll('select')
      .find((node) =>
        Array.from((node.element as HTMLSelectElement).options).some((o) => o.value === '1024x1024'),
      )
    expect(sizeSelect).toBeTruthy()
    expect((sizeSelect!.element as HTMLSelectElement).value).toBe('1024x1024')

    wrapper.unmount()
  })

  it('expands the retouch workbench with the preset categories', async () => {
    const wrapper = mountView()
    await flushPromises()

    const retouchToggle = wrapper
      .findAll('button')
      .find((node) => node.text().includes(zh.imagegen.retouchTitle))
    expect(retouchToggle).toBeTruthy()
    await retouchToggle!.trigger('click')
    await flushPromises()

    // Category titles come straight from the extracted preset tree, so they are
    // real Chinese strings even under the runtime-only i18n build.
    expect(wrapper.text()).toContain('人像')
    expect(wrapper.text()).toContain('AI色彩')
    expect(wrapper.text()).toContain(zh.imagegen.retouchIntensity)
    expect(wrapper.text()).toContain(zh.imagegen.retouchSubject)

    wrapper.unmount()
  })

  it('shows a preset from the default workbench group', async () => {
    const wrapper = mountView()
    await flushPromises()

    const retouchToggle = wrapper
      .findAll('button')
      .find((node) => node.text().includes(zh.imagegen.retouchTitle))
    await retouchToggle!.trigger('click')
    await flushPromises()

    // 肤色瑕疵 is the default group opened by the source page.
    expect(wrapper.text()).toContain('肤色瑕疵')

    wrapper.unmount()
  })

  it('loads the server template catalog into the drawer', async () => {
    const wrapper = mountView()
    await flushPromises()

    const openButton = wrapper
      .findAll('button')
      .find((node) => node.text().includes(zh.imagegen.templatesOpen))
    expect(openButton).toBeTruthy()
    await openButton!.trigger('click')
    await flushPromises()

    // The drawer Teleports to body, so it lives outside the wrapper element.
    expect(document.body.textContent).toContain(zh.imagegen.templatesTitle)
    expect(document.body.textContent).toContain('测试模板')

    wrapper.unmount()
  })

  it('rejects generation without a token instead of calling the gateway', async () => {
    const wrapper = mountView()
    await flushPromises()

    const fetchStub = globalThis.fetch as unknown as ReturnType<typeof vi.fn>
    const before = fetchStub.mock.calls.length

    const generate = wrapper
      .findAll('button')
      .find((node) => node.text().includes(zh.imagegen.generate))
    await generate!.trigger('click')
    await flushPromises()

    // No /v1/images call may happen without a token.
    const imageCalls = fetchStub.mock.calls.filter((call) => String(call[0]).includes('/v1/images'))
    expect(imageCalls).toHaveLength(0)
    expect(fetchStub.mock.calls.length).toBe(before)

    wrapper.unmount()
  })

  it('把生图密钥与 AI 改写密钥放在同一张卡片里，并分别标注', async () => {
    const wrapper = mountView()
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain(zh.imagegen.generationKeySection)
    expect(text).toContain(zh.imagegen.rewriteKeySection)
    // 同卡：两个分区都在同一个 section.card 里。
    const cards = wrapper.findAll('section.card')
    const keyCard = cards.find((card) => card.text().includes(zh.imagegen.keyCardTitle))
    expect(keyCard).toBeTruthy()
    expect(keyCard!.text()).toContain(zh.imagegen.generationKeySection)
    expect(keyCard!.text()).toContain(zh.imagegen.rewriteKeySection)

    wrapper.unmount()
  })

  it('页面上没有可填地址、没有协议下拉、没有手填令牌', async () => {
    const wrapper = mountView()
    await flushPromises()

    // 1) 没有密码框（旧的手填令牌入口）。
    expect(wrapper.findAll('input[type="password"]')).toHaveLength(0)
    // 2) 没有地址形状的输入提示：key 与地址都不再手填。
    for (const input of wrapper.findAll('input')) {
      expect(String(input.attributes('placeholder') ?? '')).not.toMatch(/https?:\/\//)
      expect(String(input.attributes('autocomplete') ?? '')).not.toBe('off-url')
    }
    // 3) 协议选择器不存在：所有 select 都不带协议值。
    for (const select of wrapper.findAll('select')) {
      const values = Array.from((select.element as HTMLSelectElement).options).map((option) => option.value)
      expect(values).not.toContain('openai-chat')
      expect(values).not.toContain('openai-responses')
      expect(values).not.toContain('anthropic-messages')
    }
    // 4) 密钥卡片内部只有下拉与按钮，没有一个输入框。
    const keyCard = wrapper
      .findAll('section.card')
      .find((card) => card.text().includes(zh.imagegen.keyCardTitle))
    expect(keyCard).toBeTruthy()
    expect(keyCard!.findAll('input, textarea')).toHaveLength(0)
    // 5) 文案里也不再出现「改写接口地址」这种可填项。
    expect(wrapper.text()).not.toContain(zh.imagegen.rewriteEndpointLabel)

    wrapper.unmount()
  })

  it('卡片默认只露两个密钥下拉与校验/详细配置按钮', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-test="generation-key-select"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="rewrite-key-select"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="generation-key-check"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="rewrite-key-check"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="rewrite-advanced-open"]').exists()).toBe(true)
    // 模型与思考强度收在弹窗里。
    expect(wrapper.find('[data-test="rewrite-model-select"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="rewrite-effort-select"]').exists()).toBe(false)

    wrapper.unmount()
  })

  it('点详细配置能打开弹窗，地址是只读的固定值', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="rewrite-advanced-open"]').trigger('click')
    await flushPromises()

    // BaseDialog Teleport 到 body。
    const endpoint = document.querySelector('[data-test="rewrite-endpoint-readonly"]')
    expect(endpoint).toBeTruthy()
    expect(endpoint!.textContent).toContain('https://api.example.com/v1/')
    // 只读文本，不是输入框。
    expect(endpoint!.tagName).toBe('P')

    wrapper.unmount()
  })

  it('改写侧未配置时不影响生图按钮', async () => {
    const wrapper = mountView()
    await flushPromises()

    const fetchStub = globalThis.fetch as unknown as ReturnType<typeof vi.fn>
    const before = fetchStub.mock.calls.length

    // 生成按钮必须可用（不能被改写配置连带禁用）。
    const generate = wrapper
      .findAll('button')
      .find((node) => node.text().includes(zh.imagegen.generate))
    expect((generate!.element as HTMLButtonElement).disabled).toBe(false)
    expect(fetchStub.mock.calls.length).toBe(before)

    wrapper.unmount()
  })
})
