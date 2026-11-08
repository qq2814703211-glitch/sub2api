/**
 * AI 改写的下拉数据源：模型列表、思考强度元数据、密钥解析。
 *
 * 这几条断言对应返工后用户能看到的每一个「选项」从哪来：
 *   - 模型列表随所选密钥变化（打的是该密钥的 /v1/models）；
 *   - 思考强度按模型元数据收窄，取不到就是全量七档；
 *   - 界面只拿到密钥名与分组名，拿不到完整 key 用于渲染。
 */
import { describe, expect, it, vi, afterEach } from 'vitest'

// `api/keys.ts` 走 axios（XHR），stub fetch 打不到它；这里按仓库既有写法
// （composables/__tests__/useBatchImageAccess.retry.spec.ts）把模块整体替换掉。
// 工厂函数被 hoist 到文件顶部，所以这里只放字面量，不引用文件后面的 fixture。
const listMock = vi.fn(async () => ({ items: [] as unknown[], total: 0, page: 1, page_size: 100, pages: 0 }))
const getByIdMock = vi.fn(async (): Promise<unknown> => ({ id: 1, key: 'sk-abc', name: 'key-a' }))
vi.mock('@/api/keys', () => ({
  keysAPI: {
    list: (...args: unknown[]) => listMock(...(args as [])),
    getById: (...args: unknown[]) => getByIdMock(...(args as [])),
  },
}))

import {
  REWRITE_MODELS_PATH,
  fetchRewriteModels,
  isTextRewriteModel,
  listOwnKeys,
  parseRewriteModels,
  resolveKeyContext,
  rewriteGatewayUrl,
  verifyRewriteToken,
} from '../rewriteCatalog'
import { REWRITE_API_BASE_URL, effortOptionsForModel } from '../rewriteConfig'
import type { ApiKey } from '@/types'

function apiKey(overrides: Partial<ApiKey> = {}): ApiKey {
  return {
    id: 1,
    user_id: 1,
    key: 'sk-abc',
    name: 'key-a',
    group_id: 3,
    status: 'active',
    ip_whitelist: [],
    ip_blacklist: [],
    last_used_at: null,
    last_used_ip: null,
    quota: 0,
    quota_used: 0,
    expires_at: null,
    created_at: '',
    updated_at: '',
    current_concurrency: 0,
    rate_limit_5h: 0,
    rate_limit_1d: 0,
    rate_limit_7d: 0,
    usage_5h: 0,
    usage_1d: 0,
    usage_7d: 0,
    window_5h_start: null,
    window_1d_start: null,
    window_7d_start: null,
    reset_5h_at: null,
    reset_1d_at: null,
    reset_7d_at: null,
    ...overrides,
  } as ApiKey
}

describe('模型列表解析', () => {
  it('读 camelCase 的思考强度元数据（/v1/models 的真实形状）', () => {
    const models = parseRewriteModels({
      object: 'list',
      data: [
        {
          id: 'grok-4.7',
          supportsReasoningEffort: true,
          reasoningEffort: 'high',
          reasoningEfforts: [
            { value: 'low', label: 'Low' },
            { value: 'medium', label: 'Medium' },
            { value: 'high', label: 'High', default: true },
            { value: 'xhigh', label: 'xHigh' },
          ],
        },
      ],
    })
    expect(models).toHaveLength(1)
    expect(models[0].id).toBe('grok-4.7')
    expect(models[0].supportsReasoningEffort).toBe(true)
    expect(models[0].reasoningEffort).toBe('high')
    expect(models[0].reasoningEfforts?.map((item) => item.value)).toEqual(['low', 'medium', 'high', 'xhigh'])
    // 声明了清单 → 只给这些档位（外加 off）。
    expect(effortOptionsForModel(models[0], 'openai-chat')).toEqual(['off', 'low', 'medium', 'high', 'xhigh'])
  })

  it('也容忍 snake_case 变体', () => {
    const models = parseRewriteModels({ data: [{ id: 'm', supports_reasoning_effort: true }] })
    expect(models[0].supportsReasoningEffort).toBe(true)
  })

  it('不支持思考的模型 → 只给 off', () => {
    const models = parseRewriteModels({ data: [{ id: 'plain', supportsReasoningEffort: false }] })
    expect(effortOptionsForModel(models[0], 'openai-chat')).toEqual(['off'])
  })

  it('没有任何能力字段 → 全量七档', () => {
    const models = parseRewriteModels({ data: [{ id: 'mystery-model' }] })
    expect(models[0].supportsReasoningEffort).toBeUndefined()
    expect(effortOptionsForModel(models[0], 'openai-chat')).toEqual([
      'off',
      'minimal',
      'low',
      'medium',
      'high',
      'xhigh',
      'max',
    ])
  })

  it('剔掉图片/视频/嵌入模型，避免选中必然 400 的模型', () => {
    const models = parseRewriteModels({
      data: [
        { id: 'gpt-5.5' },
        { id: 'gpt-image-2' },
        { id: 'dall-e-3' },
        { id: 'flux-pro' },
        { id: 'stable-diffusion-3' },
        { id: 'sora-video' },
        { id: 'text-embedding-3' },
        { id: 'whisper-1' },
      ],
    })
    expect(models.map((item) => item.id)).toEqual(['gpt-5.5'])
    expect(isTextRewriteModel('gpt-image-2')).toBe(false)
    expect(isTextRewriteModel('claude-opus-5-5')).toBe(true)
  })

  it('去重、排序、容忍缺 id 与重复项', () => {
    const models = parseRewriteModels({
      data: [{ id: 'b' }, { id: 'a' }, { id: 'b' }, { id: '' }, { model: 'c' }, null, 'x'],
    })
    expect(models.map((item) => item.id)).toEqual(['a', 'b', 'c'])
  })

  it('形状异常一律返回空数组', () => {
    for (const payload of [null, undefined, 'oops', 7, {}, { data: 'x' }, { data: null }]) {
      expect(parseRewriteModels(payload)).toEqual([])
    }
  })
})

describe('模型列表请求', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('打的是站内网关固定地址下的 /v1/models', () => {
    expect(rewriteGatewayUrl(REWRITE_MODELS_PATH)).toBe(`${REWRITE_API_BASE_URL}/v1/models`)
    expect(REWRITE_API_BASE_URL).toBe('https://api.example.com')
  })

  it('用选中密钥的 Bearer 头请求', async () => {
    const spy = vi.fn(async () =>
      new Response(JSON.stringify({ data: [{ id: 'gpt-5.5' }] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    globalThis.fetch = spy as unknown as typeof globalThis.fetch
    const models = await fetchRewriteModels('sk-picked', new AbortController().signal)
    expect(models.map((item) => item.id)).toEqual(['gpt-5.5'])
    const [url, init] = spy.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe(`${REWRITE_API_BASE_URL}/v1/models`)
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer sk-picked')
  })

  it('空密钥不发请求', async () => {
    const spy = vi.fn()
    globalThis.fetch = spy as unknown as typeof globalThis.fetch
    expect(await fetchRewriteModels('   ', new AbortController().signal)).toEqual([])
    expect(spy).not.toHaveBeenCalled()
  })

  it('网络异常或非 2xx 时返回空数组而不是抛错', async () => {
    globalThis.fetch = vi.fn(async () => {
      throw new Error('boom')
    }) as unknown as typeof globalThis.fetch
    expect(await fetchRewriteModels('sk-x', new AbortController().signal)).toEqual([])

    globalThis.fetch = vi.fn(async () => new Response('nope', { status: 500 })) as unknown as typeof globalThis.fetch
    expect(await fetchRewriteModels('sk-x', new AbortController().signal)).toEqual([])
  })
})

describe('校验令牌真的打一次请求', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    getByIdMock.mockReset()
    getByIdMock.mockResolvedValue(apiKey({ id: 9, group: { platform: 'anthropic' } as never }))
  })

  it('成功时给出模型数量与自动判定的协议', async () => {
    getByIdMock.mockResolvedValue(apiKey({ id: 9, group: { platform: 'anthropic' } as never }))
    const spy = vi.fn(async (_input: RequestInfo | URL) =>
      new Response(JSON.stringify({ data: [{ id: 'claude-haiku-5-5' }, { id: 'claude-sonnet-5-5' }] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    globalThis.fetch = spy as unknown as typeof globalThis.fetch

    const result = await verifyRewriteToken(9, 'sk-rewrite', new AbortController().signal)
    expect(result.ok).toBe(true)
    expect(result.modelCount).toBe(2)
    expect(result.message).toContain('2')
    expect(result.protocol).toBe('anthropic-messages')
    // 至少打过一次 /v1/models —— 这就是「真打一次请求」。
    expect(spy.mock.calls.some((call) => String(call[0]).includes('/v1/models'))).toBe(true)
    // 协议来自密钥详情里的 group.platform，证明用的就是自己的密钥接口。
    expect(getByIdMock).toHaveBeenCalledWith(9)
  })

  it('模型列表为空时判失败并说明原因', async () => {
    const spy = vi.fn(async () =>
      new Response(JSON.stringify({ error: { message: 'invalid api key' } }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    globalThis.fetch = spy as unknown as typeof globalThis.fetch

    const result = await verifyRewriteToken(1, 'sk-bad', new AbortController().signal)
    expect(result.ok).toBe(false)
    expect(result.modelCount).toBe(0)
    expect(result.message).toContain('令牌不可用')
    expect(spy).toHaveBeenCalled()
  })

  it('没选密钥时不打请求', async () => {
    const spy = vi.fn()
    globalThis.fetch = spy as unknown as typeof globalThis.fetch
    const result = await verifyRewriteToken(null, '', new AbortController().signal)
    expect(result.ok).toBe(false)
    expect(result.message).toContain('请先选择')
    expect(spy).not.toHaveBeenCalled()
  })
})

describe('密钥上下文解析', () => {
  const keys = [
    apiKey({ id: 1, key: 'sk-one', name: 'one', group: { platform: 'openai', name: 'OpenAI 分组' } as never }),
    apiKey({ id: 2, key: 'sk-two', name: 'two', group: { platform: 'anthropic', name: 'Claude 分组' } as never }),
    apiKey({ id: 3, key: '  sk-three  ', name: 'three', group: undefined }),
  ]

  it('按 id 取到明文与分组平台', () => {
    expect(resolveKeyContext(keys, 1)).toEqual({ key: 'sk-one', platform: 'openai' })
    expect(resolveKeyContext(keys, 2)).toEqual({ key: 'sk-two', platform: 'anthropic' })
    // 没有分组时平台为空 → 协议回落 chat。
    expect(resolveKeyContext(keys, 3)).toEqual({ key: 'sk-three', platform: '' })
  })

  it('未选或 id 不在列表里时返回空上下文', () => {
    expect(resolveKeyContext(keys, null)).toEqual({ key: '', platform: '' })
    expect(resolveKeyContext(keys, 999)).toEqual({ key: '', platform: '' })
    expect(resolveKeyContext([], 1)).toEqual({ key: '', platform: '' })
  })
})

describe('「我的密钥」列表', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('复用 keys 列表接口并取回 items', async () => {
    listMock.mockResolvedValueOnce({
      items: [apiKey({ id: 5 })],
      total: 1,
      page: 1,
      page_size: 100,
      pages: 1,
    })
    const keys = await listOwnKeys()
    expect(keys.map((item) => item.id)).toEqual([5])
    // 第一页、一次尽量多取，页面才有完整的下拉候选。
    expect(listMock).toHaveBeenCalledWith(1, 100, undefined, { signal: undefined })
  })

  it('接口失败时返回空数组，页面仍能渲染', async () => {
    listMock.mockRejectedValueOnce(new Error('offline'))
    expect(await listOwnKeys()).toEqual([])
  })
})
