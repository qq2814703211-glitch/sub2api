/**
 * AI 改写：配置兼容、协议自适应、请求构造与响应解析。
 *
 * 0.1.2 返工后改写配置换了形态（密钥改成选、协议自适应、地址写死），
 * 但三协议各自的 URL / 鉴权头 / body 形状 / 取文本位置一个都没变——这一组
 * 用例继续把这些逐条钉死，同时新增返工后必须成立的三条硬约束：
 *   1. 地址恒为 https://api.example.com，用户输入不参与（也不存在该输入）；
 *   2. 协议由分组平台判出来，配置里没有 protocol 字段可写；
 *   3. 思考强度按模型能力收窄，拿不到元数据时给全量七档。
 *
 * 字段路径与真实线上格式对齐，依据是本仓自己的
 * `backend/internal/pkg/apicompat/types.go` 与
 * `backend/internal/service/channel_monitor_checker.go`。Responses 的 fixture
 * 故意把 reasoning 项放在最前面，证明解析器是遍历数组而不是取 output[0]。
 */
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'

import {
  ANTHROPIC_VERSION,
  buildRewriteInstruction,
  buildRewriteUrl,
  buildRewriteWireRequest,
  parseRewriteResponse,
  requestRewrite,
  rewritePath,
} from '../rewriteApi'
import {
  ANTHROPIC_REPLY_HEADROOM_TOKENS,
  ANTHROPIC_THINKING_BUDGETS,
  DEFAULT_REWRITE_CONFIG,
  DEFAULT_REWRITE_MODEL,
  REWRITE_API_BASE_URL,
  REWRITE_CONFIG_STORAGE_KEY,
  REWRITE_EFFORT_LEVELS,
  anthropicMaxTokens,
  anthropicThinkingBudget,
  coerceEffortForProtocol,
  defaultEffortForModel,
  detectRewriteProtocol,
  effortOptionsForModel,
  normalizeRewriteConfig,
  resolveRewriteRequest,
  type RewriteConfig,
  type RewriteContext,
} from '../rewriteConfig'

function config(overrides: Partial<RewriteConfig> = {}): RewriteConfig {
  return { ...DEFAULT_REWRITE_CONFIG, keyId: 7, ...overrides }
}

/** 默认上下文：选中的改写密钥 + 一把生图密钥 + OpenAI 分组。 */
function context(overrides: Partial<RewriteContext> = {}): RewriteContext {
  return { rewriteKey: 'sk-rewrite', generationKey: 'sk-image', platform: 'openai', ...overrides }
}

const resolved = (overrides: Partial<RewriteConfig> = {}, ctx: Partial<RewriteContext> = {}) =>
  resolveRewriteRequest(config(overrides), context(ctx))

function mustResolve(overrides: Partial<RewriteConfig> = {}, ctx: Partial<RewriteContext> = {}) {
  const result = resolved(overrides, ctx)
  if (!result.ok) throw new Error(`expected a resolved request, got ${result.reason}`)
  return result.request
}

describe('返工硬约束 —— 地址写死', () => {
  it('无论配置与上下文如何，地址恒为站内网关', () => {
    expect(mustResolve().baseUrl).toBe(REWRITE_API_BASE_URL)
    expect(REWRITE_API_BASE_URL).toBe('https://api.example.com')
    // 三个协议都拼到同一个固定根上。
    for (const platform of ['openai', 'anthropic', 'composite']) {
      expect(mustResolve({}, { platform }).baseUrl).toBe('https://api.example.com')
    }
  })

  it('忽略 URL 构造函数传来的任何地址，只认常量', () => {
    // 旧版允许用户填地址；现在参数被忽略，脏值不能影响拼接结果。
    for (const dirty of ['https://api.openai.com/v1', 'not-a-url', '   ', 'http://evil.example']) {
      expect(buildRewriteUrl(dirty, '/chat/completions')).toBe(
        'https://api.example.com/v1/chat/completions',
      )
    }
  })

  it('写死的地址本身是合法 URL，且不会被拼出双斜杠', () => {
    expect(() => new URL(REWRITE_API_BASE_URL)).not.toThrow()
    expect(buildRewriteUrl(REWRITE_API_BASE_URL, '/responses')).toBe(
      'https://api.example.com/v1/responses',
    )
  })

  it('存量里带脏 baseUrl 的配置不再导致「地址非法」', () => {
    // 这正是返工前会卡死的输入：地址写错 → bad-base-url → 改写不可用。
    const legacy = normalizeRewriteConfig({
      enabled: true,
      apiKey: 'sk-legacy',
      baseUrl: '://broken',
      protocol: 'openai-chat',
      model: 'gpt-5.5',
      effort: 'medium',
    })
    const result = resolveRewriteRequest(legacy, context({ rewriteKey: '' }))
    expect(result.ok).toBe(true)
    if (result.ok) {
      expect(result.request.baseUrl).toBe(REWRITE_API_BASE_URL)
      // 旧手填令牌被降级成兜底令牌，能力不丢。
      expect(result.request.apiKey).toBe('sk-legacy')
    }
  })
})

describe('返工硬约束 —— 协议自适应', () => {
  it('Anthropic 网关平台走 Messages 协议', () => {
    expect(detectRewriteProtocol('anthropic', 'claude-opus-5-5')).toBe('anthropic-messages')
    expect(detectRewriteProtocol('typesafe', 'any-model')).toBe('anthropic-messages')
    expect(detectRewriteProtocol('Antigravity', 'claude-sonnet-5-5')).toBe('anthropic-messages')
  })

  it('OpenAI 网关平台（含国产与 grok）走 Chat Completions', () => {
    for (const platform of ['openai', 'grok', 'kimi', 'zhipu', 'deepseek', 'minimax', 'gemini', 'opencode-go']) {
      expect(detectRewriteProtocol(platform, 'some-model')).toBe('openai-chat')
    }
  })

  it('composite 分组按模型名区分；拿不准回落 chat', () => {
    expect(detectRewriteProtocol('composite', 'claude-haiku-5-5')).toBe('anthropic-messages')
    expect(detectRewriteProtocol('composite', 'anthropic/claude-sonnet-5-5')).toBe('anthropic-messages')
    expect(detectRewriteProtocol('composite', 'gpt-5.5')).toBe('openai-chat')
    expect(detectRewriteProtocol('composite', '')).toBe('openai-chat')
  })

  it('平台缺失或未知时回落 openai-chat，不会抛错', () => {
    for (const platform of ['', '   ', 'unknown-platform', undefined as unknown as string]) {
      expect(detectRewriteProtocol(platform, 'gpt-5.5')).toBe('openai-chat')
    }
  })

  it('解析结果里的协议由上下文决定，配置里没有可写的 protocol 字段', () => {
    expect(mustResolve({}, { platform: 'anthropic' }).protocol).toBe('anthropic-messages')
    expect(mustResolve({}, { platform: 'openai' }).protocol).toBe('openai-chat')
    // 关键：RewriteConfig 类型里根本没有 protocol。
    expect('protocol' in DEFAULT_REWRITE_CONFIG).toBe(false)
    expect('baseUrl' in DEFAULT_REWRITE_CONFIG).toBe(false)
  })
})

describe('返工硬约束 —— 思考强度按模型能力收窄', () => {
  it('模型声明了精确档位时只给这些档位（并补上 off）', () => {
    const options = effortOptionsForModel(
      { supportsReasoningEffort: true, reasoningEfforts: [{ value: 'low' }, { value: 'medium' }, { value: 'high', default: true }] },
      'openai-chat',
    )
    expect(options).toEqual(['off', 'low', 'medium', 'high'])
  })

  it('支持思考但没给清单时给全量七档', () => {
    const options = effortOptionsForModel({ supportsReasoningEffort: true }, 'openai-chat')
    expect(options).toEqual([...REWRITE_EFFORT_LEVELS])
    expect(options).toHaveLength(7)
  })

  it('字段缺失（非 Grok 平台拿不到元数据）时同样给全量七档', () => {
    expect(effortOptionsForModel(null, 'openai-chat')).toEqual([...REWRITE_EFFORT_LEVELS])
    expect(effortOptionsForModel(undefined, 'anthropic-messages')).toEqual([...REWRITE_EFFORT_LEVELS])
    expect(effortOptionsForModel({}, 'openai-chat')).toEqual([...REWRITE_EFFORT_LEVELS])
  })

  it('明确不支持思考时只给 off', () => {
    expect(effortOptionsForModel({ supportsReasoningEffort: false }, 'openai-chat')).toEqual(['off'])
  })

  it('七档就是用户点名的那七个值', () => {
    expect([...REWRITE_EFFORT_LEVELS]).toEqual(['off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'])
  })

  it('模型的默认档位能被读出来做提示', () => {
    expect(defaultEffortForModel({ reasoningEfforts: [{ value: 'high', default: true }] })).toBe('high')
    expect(defaultEffortForModel({ reasoningEffort: 'medium' })).toBe('medium')
    expect(defaultEffortForModel({ supportsReasoningEffort: true })).toBeNull()
    expect(defaultEffortForModel({ reasoningEffort: 'auto' })).toBeNull()
  })

  it('超出协议表达能力的档位向下收窄而不是丢弃', () => {
    expect(coerceEffortForProtocol('max', 'openai-chat')).toBe('max')
    expect(coerceEffortForProtocol('bogus', 'openai-chat')).toBe('unset')
  })
})

describe('配置兼容 —— 旧 profile 平滑过渡', () => {
  it('全新用户拿到默认配置：没选密钥、默认模型、自动档', () => {
    expect(normalizeRewriteConfig(null)).toEqual(DEFAULT_REWRITE_CONFIG)
    expect(DEFAULT_REWRITE_CONFIG.keyId).toBeNull()
    expect(DEFAULT_REWRITE_CONFIG.legacyApiKey).toBe('')
    expect(DEFAULT_REWRITE_CONFIG.model).toBe(DEFAULT_REWRITE_MODEL)
    expect(DEFAULT_REWRITE_CONFIG.effort).toBe('unset')
  })

  it('旧版 apiKey 迁移成只读 legacyApiKey，baseUrl/protocol 被丢弃', () => {
    const migrated = normalizeRewriteConfig({
      enabled: true,
      apiKey: ' sk-old ',
      baseUrl: 'https://api.openai.com/v1',
      protocol: 'anthropic-messages',
      model: 'gpt-5.5',
      effort: 'high',
    })
    expect(migrated.legacyApiKey).toBe('sk-old')
    expect(migrated.model).toBe('gpt-5.5')
    expect(migrated.effort).toBe('high')
    // 这三个字段已经不在配置形状里了。
    expect('apiKey' in migrated).toBe(false)
    expect('baseUrl' in migrated).toBe(false)
    expect('protocol' in migrated).toBe(false)
    expect('enabled' in migrated).toBe(false)
  })

  it('新版存下来的 keyId 能回读，非法 id 归零', () => {
    expect(normalizeRewriteConfig({ keyId: 12 }).keyId).toBe(12)
    expect(normalizeRewriteConfig({ keyId: '12' }).keyId).toBe(12)
    expect(normalizeRewriteConfig({ keyId: 'abc' }).keyId).toBeNull()
    expect(normalizeRewriteConfig({ keyId: -3 }).keyId).toBeNull()
    expect(normalizeRewriteConfig({ keyId: 0 }).keyId).toBeNull()
  })

  it('裸模型名字符串仍当作旧 profile', () => {
    const migrated = normalizeRewriteConfig('gpt-5.5')
    expect(migrated.model).toBe('gpt-5.5')
    expect(migrated.keyId).toBeNull()
  })

  it('legacyModel 只在新模型为空时生效', () => {
    expect(normalizeRewriteConfig(null, 'legacy-model').model).toBe('legacy-model')
    expect(normalizeRewriteConfig({ model: 'new-model' }, 'legacy-model').model).toBe('new-model')
    expect(normalizeRewriteConfig({ model: '   ' }, 'legacy-model').model).toBe('legacy-model')
  })

  it('字段逐个兜底：一个坏值不带走其余字段', () => {
    const repaired = normalizeRewriteConfig({
      keyId: {} as unknown as number,
      model: '  claude-haiku-5-5  ',
      effort: 'ultra' as never,
    })
    expect(repaired.keyId).toBeNull()
    expect(repaired.model).toBe('claude-haiku-5-5')
    expect(repaired.effort).toBe('unset')
  })

  it('非对象且非字符串的负载一律给默认值', () => {
    for (const raw of [undefined, 0, true, [], ['x']]) {
      expect(normalizeRewriteConfig(raw)).toEqual(DEFAULT_REWRITE_CONFIG)
    }
  })
})

describe('密钥解析', () => {
  it('优先用选中的改写密钥，并标记不使用生图密钥', () => {
    const request = mustResolve()
    expect(request.apiKey).toBe('sk-rewrite')
    expect(request.usesGenerationKey).toBe(false)
  })

  it('没选改写密钥时回落到生图密钥并标记出来', () => {
    const request = mustResolve({ keyId: null }, { rewriteKey: '' })
    expect(request.apiKey).toBe('sk-image')
    expect(request.usesGenerationKey).toBe(true)
  })

  it('旧版手填令牌排在生图密钥之前，且不算「用生图密钥」', () => {
    const request = mustResolve({ keyId: null, legacyApiKey: 'sk-old' }, { rewriteKey: '' })
    expect(request.apiKey).toBe('sk-old')
    expect(request.usesGenerationKey).toBe(false)
  })

  it('两把密钥都没有时明确失败，且不碰生图链路', () => {
    const result = resolved({ keyId: null, legacyApiKey: '' }, { rewriteKey: '', generationKey: '' })
    expect(result.ok).toBe(false)
    if (!result.ok) expect(result.reason).toBe('missing-key')
  })

  it('解析结果里永远不会有空模型', () => {
    expect(mustResolve({ model: '   ' }).model).toBe(DEFAULT_REWRITE_MODEL)
  })

  it('UI 徽标与解析器对「谁付钱」的判定一致', () => {
    const cases: Array<[RewriteConfig, RewriteContext]> = [
      [config({ keyId: null, legacyApiKey: '' }), context({ rewriteKey: '' })],
      [config({ keyId: null, legacyApiKey: 'sk-old' }), context({ rewriteKey: '' })],
      [config({ keyId: 7 }), context()],
    ]
    for (const [cfg, ctx] of cases) {
      const result = resolveRewriteRequest(cfg, ctx)
      expect(result.ok).toBe(true)
      if (!result.ok) continue
      // 与 composable 里 rewriteUsesGenerationKey 的同一条规则。
      const badgeSaysFallback = !cfg.keyId && !cfg.legacyApiKey.trim()
      expect(result.request.usesGenerationKey).toBe(badgeSaysFallback)
    }
  })
})

describe('URL 与路径', () => {
  it('每个协议映射到各自的固定路径', () => {
    expect(rewritePath('openai-chat')).toBe('/chat/completions')
    expect(rewritePath('openai-responses')).toBe('/responses')
    expect(rewritePath('anthropic-messages')).toBe('/messages')
  })

  it('三个协议都拼在写死的根上', () => {
    for (const protocol of ['openai-chat', 'openai-responses', 'anthropic-messages'] as const) {
      expect(buildRewriteUrl('', rewritePath(protocol))).toBe(
        `https://api.example.com/v1${rewritePath(protocol)}`,
      )
    }
  })
})

describe('三协议请求构造', () => {
  const instruction = buildRewriteInstruction('a cat', 'make it neon')

  it('openai-chat: Bearer 鉴权、messages 数组、reasoning_effort', () => {
    const wire = buildRewriteWireRequest(
      mustResolve({ model: 'gpt-6-astra', effort: 'medium' }),
      'a cat',
      'make it neon',
    )
    expect(wire.url).toBe('https://api.example.com/v1/chat/completions')
    expect(wire.headers.Authorization).toBe('Bearer sk-rewrite')
    expect(wire.body.model).toBe('gpt-6-astra')
    expect(wire.body.messages).toEqual([{ role: 'user', content: instruction }])
    expect(wire.body.reasoning_effort).toBe('medium')
    expect(wire.body.reasoning).toBeUndefined()
    expect(wire.body.thinking).toBeUndefined()
  })

  it('openai-chat: unset 时完全不发 reasoning_effort', () => {
    const wire = buildRewriteWireRequest(mustResolve({ effort: 'unset' }), 'a cat', 'make it neon')
    expect('reasoning_effort' in wire.body).toBe(false)
    // 旧版就带的 temperature 保留。
    expect(wire.body.temperature).toBe(0.2)
  })

  it('openai-chat: off 映射成 none，xhigh 直传', () => {
    const off = buildRewriteWireRequest(mustResolve({ effort: 'off' }), 'a cat', 'x')
    expect(off.body.reasoning_effort).toBe('none')
    const xhigh = buildRewriteWireRequest(mustResolve({ effort: 'xhigh' }), 'a cat', 'x')
    expect(xhigh.body.reasoning_effort).toBe('xhigh')
  })

  it('openai-responses: Bearer 鉴权、input 字符串、reasoning.effort、store=false', () => {
    const wire = buildRewriteWireRequest(
      mustResolve({}, { platform: 'openai', rewriteKey: 'sk-rewrite' }),
      'a cat',
      'make it neon',
    )
    // 平台判定是 openai → chat；强制走 responses 需要显式构造 request。
    const responses = buildRewriteWireRequest(
      {
        ...mustResolve({ model: 'gpt-5.5', effort: 'high' }),
        protocol: 'openai-responses',
      },
      'a cat',
      'make it neon',
    )
    expect(responses.url).toBe('https://api.example.com/v1/responses')
    expect(responses.headers.Authorization).toBe('Bearer sk-rewrite')
    expect(responses.body.model).toBe('gpt-5.5')
    expect(responses.body.input).toBe(instruction)
    expect(responses.body.messages).toBeUndefined()
    expect(responses.body.reasoning).toEqual({ effort: 'high' })
    expect(responses.body.store).toBe(false)
    // 默认平台仍然是 chat。
    expect(wire.body.input).toBeUndefined()
  })

  it('openai-responses: off 也发 reasoning.effort = none', () => {
    const wire = buildRewriteWireRequest(
      { ...mustResolve({ effort: 'off' }), protocol: 'openai-responses' },
      'a cat',
      'x',
    )
    expect(wire.body.reasoning).toEqual({ effort: 'none' })
  })

  it('anthropic-messages: x-api-key + anthropic-version，thinking budget', () => {
    const wire = buildRewriteWireRequest(
      mustResolve({ model: 'claude-haiku-5-5', effort: 'low' }, { platform: 'anthropic' }),
      'a cat',
      'make it neon',
    )
    expect(wire.url).toBe('https://api.example.com/v1/messages')
    expect(wire.headers['x-api-key']).toBe('sk-rewrite')
    expect(wire.headers['anthropic-version']).toBe(ANTHROPIC_VERSION)
    // Anthropic 上不该出现 Bearer。
    expect(wire.headers.Authorization).toBeUndefined()
    expect(wire.body.messages).toEqual([{ role: 'user', content: instruction }])
    expect(wire.body.thinking).toEqual({ type: 'enabled', budget_tokens: ANTHROPIC_THINKING_BUDGETS.low })
    expect(wire.body.max_tokens).toBeGreaterThan(ANTHROPIC_THINKING_BUDGETS.low)
    // 扩展思考不接受自定义 temperature。
    expect('temperature' in wire.body).toBe(false)
  })

  it('anthropic-messages: unset 与 off 都不发 thinking 块', () => {
    for (const effort of ['unset', 'off'] as const) {
      const wire = buildRewriteWireRequest(
        mustResolve({ effort }, { platform: 'anthropic' }),
        'a cat',
        'x',
      )
      expect('thinking' in wire.body).toBe(false)
      expect(wire.body.temperature).toBe(0.2)
      expect(wire.body.max_tokens).toBe(ANTHROPIC_REPLY_HEADROOM_TOKENS)
    }
  })

  it('anthropic budget 全部 ≥ 1024，且 max_tokens 严格大于 budget', () => {
    expect(anthropicThinkingBudget('unset')).toBeNull()
    expect(anthropicThinkingBudget('off')).toBeNull()
    for (const effort of ['minimal', 'low', 'medium', 'high', 'xhigh', 'max'] as const) {
      const budget = anthropicThinkingBudget(effort)!
      expect(budget).toBe(ANTHROPIC_THINKING_BUDGETS[effort])
      expect(budget).toBeGreaterThanOrEqual(1024)
      expect(anthropicMaxTokens(effort)).toBeGreaterThan(budget)
    }
    expect(anthropicMaxTokens('unset')).toBe(ANTHROPIC_REPLY_HEADROOM_TOKENS)
  })
})

describe('三协议响应解析', () => {
  it('openai-chat: 取 choices[0].message.content', () => {
    const text = parseRewriteResponse('openai-chat', {
      id: 'chatcmpl-1',
      object: 'chat.completion',
      choices: [{ index: 0, message: { role: 'assistant', content: '  neon cat  ' }, finish_reason: 'stop' }],
    })
    expect(text.trim()).toBe('neon cat')
  })

  it('openai-chat: 拼接 content parts，并容忍老式 choices[0].text', () => {
    expect(
      parseRewriteResponse('openai-chat', {
        choices: [{ message: { content: [{ type: 'text', text: 'neon ' }, { type: 'text', text: 'cat' }] } }],
      }),
    ).toBe('neon cat')
    expect(parseRewriteResponse('openai-chat', { choices: [{ text: 'legacy text' }] })).toBe('legacy text')
  })

  it('openai-chat: 没有答案时返回空串', () => {
    expect(parseRewriteResponse('openai-chat', { choices: [] })).toBe('')
    expect(parseRewriteResponse('openai-chat', {})).toBe('')
  })

  it('openai-responses: 遍历 output，跳过开头那条 reasoning', () => {
    const text = parseRewriteResponse('openai-responses', {
      id: 'resp_1',
      object: 'response',
      status: 'completed',
      output: [
        { type: 'reasoning', id: 'rs_1', summary: [] },
        {
          type: 'message',
          role: 'assistant',
          status: 'completed',
          content: [
            { type: 'output_text', text: 'neon cat', annotations: [] },
            { type: 'refusal', refusal: '' },
          ],
        },
      ],
    })
    expect(text).toBe('neon cat')
  })

  it('openai-responses: 优先用 output_text 便捷字段', () => {
    expect(parseRewriteResponse('openai-responses', { output_text: 'neon cat', output: [] })).toBe('neon cat')
  })

  it('openai-responses: 拼接多个 message 的 output_text', () => {
    const text = parseRewriteResponse('openai-responses', {
      output: [
        { type: 'message', content: [{ type: 'output_text', text: 'neon ' }] },
        { type: 'message', content: [{ type: 'output_text', text: 'cat' }] },
      ],
    })
    expect(text).toBe('neon cat')
  })

  it('anthropic-messages: 取 text 块、忽略 thinking', () => {
    const text = parseRewriteResponse('anthropic-messages', {
      id: 'msg_1',
      type: 'message',
      role: 'assistant',
      content: [
        { type: 'thinking', thinking: 'ponder…', signature: 'sig' },
        { type: 'text', text: 'neon cat' },
      ],
    })
    expect(text).toBe('neon cat')
  })

  it('anthropic-messages: 只有 tool 块时没有答案', () => {
    expect(
      parseRewriteResponse('anthropic-messages', {
        content: [{ type: 'tool_use', id: 't1', name: 'x', input: {} }],
      }),
    ).toBe('')
  })

  it('垃圾负载不抛异常', () => {
    for (const protocol of ['openai-chat', 'openai-responses', 'anthropic-messages'] as const) {
      expect(parseRewriteResponse(protocol, null)).toBe('')
      expect(parseRewriteResponse(protocol, 'oops')).toBe('')
      expect(parseRewriteResponse(protocol, { choices: 'x', output: 7, content: null })).toBe('')
    }
  })
})

describe('requestRewrite 端到端（fetch 打桩）', () => {
  let originalFetch: typeof globalThis.fetch

  beforeEach(() => {
    originalFetch = globalThis.fetch
  })

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  function stubFetch(payload: unknown, init: { status?: number } = {}) {
    const spy = vi.fn(
      async () =>
        new Response(JSON.stringify(payload), {
          status: init.status ?? 200,
          headers: { 'Content-Type': 'application/json' },
        }),
    )
    globalThis.fetch = spy as unknown as typeof globalThis.fetch
    return spy
  }

  it('用选中的改写密钥打固定地址，并返回解析后的提示词', async () => {
    const spy = stubFetch({ choices: [{ message: { content: ' rewrote it ' } }] })
    const result = await requestRewrite({
      config: config(),
      context: context(),
      original: 'a cat',
      instruction: 'neon',
      signal: new AbortController().signal,
    })

    expect(result.prompt).toBe('rewrote it')
    expect(result.usesGenerationKey).toBe(false)
    expect(result.protocol).toBe('openai-chat')

    const [url, init] = spy.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('https://api.example.com/v1/chat/completions')
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer sk-rewrite')
  })

  it('有改写密钥时绝不拿生图密钥付钱', async () => {
    const spy = stubFetch({ content: [{ type: 'text', text: 'ok' }] })
    await requestRewrite({
      config: config(),
      context: context({ platform: 'anthropic', rewriteKey: 'sk-own' }),
      original: 'a cat',
      instruction: 'neon',
      signal: new AbortController().signal,
    })
    const [url, init] = spy.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('https://api.example.com/v1/messages')
    expect((init.headers as Record<string, string>)['x-api-key']).toBe('sk-own')
    expect(JSON.stringify(init.headers)).not.toContain('sk-image')
  })

  it('没选改写密钥时走生图密钥', async () => {
    const spy = stubFetch({ choices: [{ message: { content: 'ok' } }] })
    const result = await requestRewrite({
      config: config({ keyId: null }),
      context: context({ rewriteKey: '' }),
      original: 'a cat',
      instruction: 'neon',
      signal: new AbortController().signal,
    })
    expect(result.usesGenerationKey).toBe(true)
    const [, init] = spy.mock.calls[0] as unknown as [string, RequestInit]
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer sk-image')
  })

  it('上游报错时带上原文与状态码', async () => {
    stubFetch({ error: { type: 'invalid_request_error', message: 'model not found' } }, { status: 404 })
    await expect(
      requestRewrite({
        config: config(),
        context: context(),
        original: 'a cat',
        instruction: 'neon',
        signal: new AbortController().signal,
      }),
    ).rejects.toMatchObject({ status: 404, message: expect.stringContaining('model not found') })
  })

  it('响应里没有文本时报错', async () => {
    stubFetch({ choices: [{ message: { content: '   ' } }] })
    await expect(
      requestRewrite({
        config: config(),
        context: context(),
        original: 'a cat',
        instruction: 'neon',
        signal: new AbortController().signal,
      }),
    ).rejects.toThrow('AI 未返回新提示词')
  })

  it('一个密钥都没有时不发请求', async () => {
    const spy = stubFetch({})
    await expect(
      requestRewrite({
        config: config({ keyId: null, legacyApiKey: '' }),
        context: context({ rewriteKey: '', generationKey: '' }),
        original: 'a cat',
        instruction: 'neon',
        signal: new AbortController().signal,
      }),
    ).rejects.toThrow()
    expect(spy).not.toHaveBeenCalled()
  })
})

describe('存储键约定', () => {
  it('沿用原键，不与生图令牌的键冲突', () => {
    expect(REWRITE_CONFIG_STORAGE_KEY).toBe('ig_rewriteConfig')
    expect(REWRITE_CONFIG_STORAGE_KEY).not.toBe('ig_apiKey')
  })
})
