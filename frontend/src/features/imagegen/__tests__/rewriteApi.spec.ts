/**
 * AI rewrite: config compatibility, protocol dispatch and response parsing.
 *
 * The three protocols differ in URL, auth header, body shape and — most
 * fragile — where the answer text lives in the response. These specs pin all
 * four per protocol, plus the "old profile still works" contract that the
 * collapsed single-key build left behind.
 *
 * Field paths are asserted against fixtures shaped like the real wire formats
 * pinned by `backend/internal/pkg/apicompat/types.go`; the OpenAI Responses
 * fixtures deliberately lead with a `reasoning` item to prove the parser walks
 * the array instead of indexing `output[0]`.
 */
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'

import {
  ANTHROPIC_BROWSER_HEADER,
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
  REWRITE_CONFIG_STORAGE_KEY,
  anthropicMaxTokens,
  anthropicThinkingBudget,
  coerceEffortForProtocol,
  normalizeRewriteConfig,
  resolveRewriteRequest,
  type RewriteConfig,
} from '../rewriteConfig'

function config(overrides: Partial<RewriteConfig> = {}): RewriteConfig {
  return { ...DEFAULT_REWRITE_CONFIG, enabled: true, apiKey: 'sk-rewrite', ...overrides }
}

const resolved = (overrides: Partial<RewriteConfig> = {}, generationKey = 'sk-image') =>
  resolveRewriteRequest(config(overrides), generationKey)

function mustResolve(overrides: Partial<RewriteConfig> = {}, generationKey = 'sk-image') {
  const result = resolved(overrides, generationKey)
  if (!result.ok) throw new Error(`expected a resolved request, got ${result.reason}`)
  return result.request
}

describe('rewrite config — backward compatibility', () => {
  it('falls back to the collapsed default when nothing is stored', () => {
    expect(normalizeRewriteConfig(null)).toEqual(DEFAULT_REWRITE_CONFIG)
    expect(DEFAULT_REWRITE_CONFIG.enabled).toBe(false)
    expect(DEFAULT_REWRITE_CONFIG.model).toBe(DEFAULT_REWRITE_MODEL)
    expect(DEFAULT_REWRITE_CONFIG.effort).toBe('unset')
  })

  it('treats a bare model string as a pre-split profile', () => {
    // A pre-split profile could only ever have carried a model name.
    const migrated = normalizeRewriteConfig('gpt-5.5')
    expect(migrated.model).toBe('gpt-5.5')
    // A bare string carries no key, so it must not enable the split path.
    expect(migrated.enabled).toBe(false)
    expect(migrated.apiKey).toBe('')
  })

  it('seeds from the legacy model key when the new config is absent', () => {
    expect(normalizeRewriteConfig(null, 'legacy-model').model).toBe('legacy-model')
    // The legacy seed is weaker than anything the user typed into the new shape.
    expect(normalizeRewriteConfig({ model: 'new-model' }, 'legacy-model').model).toBe('new-model')
    // …but still wins over the hard-coded default when the new model is blank.
    expect(normalizeRewriteConfig({ model: '   ' }, 'legacy-model').model).toBe('legacy-model')
  })

  it('keeps every valid field when individual fields are corrupt', () => {
    const repaired = normalizeRewriteConfig({
      enabled: 'yes' as unknown as boolean,
      apiKey: 42 as unknown as string,
      baseUrl: ' https://api.example.com/v1/// ',
      protocol: 'grpc' as never,
      model: '  claude-haiku-5-5  ',
      effort: 'ultra' as never,
    })
    expect(repaired.enabled).toBe(false) // only literal true enables
    expect(repaired.apiKey).toBe('')
    expect(repaired.baseUrl).toBe('https://api.example.com/v1')
    expect(repaired.protocol).toBe('openai-chat') // unknown protocol → default
    expect(repaired.model).toBe('claude-haiku-5-5')
    expect(repaired.effort).toBe('unset')
  })

  it('ignores non-object payloads that are not strings', () => {
    for (const raw of [undefined, 0, true, [], ['x']]) {
      expect(normalizeRewriteConfig(raw)).toEqual(DEFAULT_REWRITE_CONFIG)
    }
  })
})

describe('rewrite config — effort/protocol interaction', () => {
  it('steps max down to high on chat completions instead of dropping it', () => {
    expect(coerceEffortForProtocol('max', 'openai-chat')).toBe('high')
    expect(coerceEffortForProtocol('max', 'openai-responses')).toBe('max')
    expect(coerceEffortForProtocol('max', 'anthropic-messages')).toBe('max')
  })

  it('passes through levels the protocol supports', () => {
    expect(coerceEffortForProtocol('medium', 'openai-chat')).toBe('medium')
    expect(coerceEffortForProtocol('unset', 'anthropic-messages')).toBe('unset')
  })

  it('maps anthropic effort onto legal thinking budgets', () => {
    expect(anthropicThinkingBudget('unset')).toBeNull()
    for (const effort of ['minimal', 'low', 'medium', 'high', 'max'] as const) {
      const budget = anthropicThinkingBudget(effort)!
      expect(budget).toBe(ANTHROPIC_THINKING_BUDGETS[effort])
      // Anthropic rejects budget_tokens < 1024.
      expect(budget).toBeGreaterThanOrEqual(1024)
      // The reply must have room above the thinking budget or the call 400s.
      expect(anthropicMaxTokens(effort)).toBeGreaterThan(budget)
    }
    expect(anthropicMaxTokens('unset')).toBe(ANTHROPIC_REPLY_HEADROOM_TOKENS)
  })
})

describe('rewrite config — key resolution', () => {
  it('uses the dedicated key when the split config is on', () => {
    const request = mustResolve({ apiKey: 'sk-rewrite' })
    expect(request.apiKey).toBe('sk-rewrite')
    expect(request.usesGenerationKey).toBe(false)
  })

  it('falls back to the image key when the split config is off', () => {
    const request = mustResolve({ enabled: false, apiKey: '' }, 'sk-image')
    expect(request.apiKey).toBe('sk-image')
    expect(request.usesGenerationKey).toBe(true)
    // The fallback never honours a custom base URL or protocol — those are
    // opt-in features of the split path only.
    expect(request.baseUrl).toBe('')
    expect(request.protocol).toBe('openai-chat')
    expect(request.effort).toBe('unset')
  })

  it('falls back to the image key when the split config is on but keyless', () => {
    const request = mustResolve({ enabled: true, apiKey: '   ' }, 'sk-image')
    expect(request.apiKey).toBe('sk-image')
    expect(request.usesGenerationKey).toBe(true)
    // …while still honouring the other split settings.
    expect(request.protocol).toBe('openai-chat')
  })

  it('fails when neither key exists, without touching the image path', () => {
    const result = resolved({ enabled: true, apiKey: '' }, '')
    expect(result.ok).toBe(false)
    if (!result.ok) expect(result.reason).toBe('missing-key')
  })

  it('fails on a malformed base URL rather than throwing inside fetch', () => {
    const result = resolved({ baseUrl: 'api.example.com' })
    expect(result.ok).toBe(false)
    if (!result.ok) expect(result.reason).toBe('bad-base-url')
  })

  it('reports the same "uses image key" answer the UI badge derives', () => {
    // The settings card computes this from the config alone; the resolver is
    // the authority. If these ever disagree the badge lies about who pays.
    const cases: Array<[RewriteConfig, string]> = [
      [{ ...DEFAULT_REWRITE_CONFIG, enabled: false }, 'sk-image'],
      [{ ...DEFAULT_REWRITE_CONFIG, enabled: true, apiKey: '' }, 'sk-image'],
      [{ ...DEFAULT_REWRITE_CONFIG, enabled: true, apiKey: 'sk-own' }, 'sk-image'],
    ]
    for (const [cfg, generationKey] of cases) {
      const request = mustResolve(cfg, generationKey)
      // Mirrors the composable's computed exactly.
      const badgeSaysFallback = !cfg.enabled || !cfg.apiKey.trim()
      expect(request.usesGenerationKey).toBe(badgeSaysFallback)
    }
  })

  it('never resolves to an empty model — a cleared field reverts to the default', () => {
    // Blanking the model must not produce a request with `model: ''`; the
    // normaliser substitutes the default so the call still works.
    expect(mustResolve({ model: '   ' }).model).toBe(DEFAULT_REWRITE_MODEL)
    expect(resolveRewriteRequest({ ...DEFAULT_REWRITE_CONFIG, model: '' }, 'sk-image')).toMatchObject({
      ok: true,
      request: { model: DEFAULT_REWRITE_MODEL },
    })
  })
})

describe('rewrite URL construction', () => {
  it('targets this gateway when no base URL is set', () => {
    for (const protocol of ['openai-chat', 'openai-responses', 'anthropic-messages'] as const) {
      expect(buildRewriteUrl('', rewritePath(protocol))).toBe(
        `${window.location.origin}/v1${rewritePath(protocol)}`,
      )
    }
  })

  it('appends /v1 to a bare host and keeps an explicit version segment', () => {
    expect(buildRewriteUrl('https://api.openai.com', '/chat/completions')).toBe(
      'https://api.openai.com/v1/chat/completions',
    )
    expect(buildRewriteUrl('https://api.openai.com/v1', '/chat/completions')).toBe(
      'https://api.openai.com/v1/chat/completions',
    )
    // Trailing slashes must not double up.
    expect(buildRewriteUrl('https://api.openai.com/v1/', '/responses')).toBe(
      'https://api.openai.com/v1/responses',
    )
    // Non-OpenAI version depths are respected rather than forced to /v1.
    expect(buildRewriteUrl('https://open.bigmodel.cn/api/paas/v4', '/chat/completions')).toBe(
      'https://open.bigmodel.cn/api/paas/v4/chat/completions',
    )
    expect(buildRewriteUrl('https://generativelanguage.googleapis.com/v1beta', '/messages')).toBe(
      'https://generativelanguage.googleapis.com/v1beta/messages',
    )
  })

  it('maps each protocol to its documented path', () => {
    expect(rewritePath('openai-chat')).toBe('/chat/completions')
    expect(rewritePath('openai-responses')).toBe('/responses')
    expect(rewritePath('anthropic-messages')).toBe('/messages')
  })
})

describe('rewrite request bodies per protocol', () => {
  const instruction = buildRewriteInstruction('a cat', 'make it neon')

  it('openai-chat: Bearer auth, messages array, reasoning_effort', () => {
    const wire = buildRewriteWireRequest(
      mustResolve({ protocol: 'openai-chat', model: 'gpt-6-astra', effort: 'medium' }),
      'a cat',
      'make it neon',
    )
    expect(wire.url).toBe(`${window.location.origin}/v1/chat/completions`)
    expect(wire.headers.Authorization).toBe('Bearer sk-rewrite')
    expect(wire.body.model).toBe('gpt-6-astra')
    expect(wire.body.messages).toEqual([{ role: 'user', content: instruction }])
    expect(wire.body.reasoning_effort).toBe('medium')
    expect(wire.body.reasoning).toBeUndefined()
    expect(wire.body.thinking).toBeUndefined()
  })

  it('openai-chat: omits reasoning_effort entirely when unset', () => {
    const wire = buildRewriteWireRequest(mustResolve({ effort: 'unset' }), 'a cat', 'make it neon')
    expect('reasoning_effort' in wire.body).toBe(false)
    // …but keeps the legacy temperature the collapsed build sent.
    expect(wire.body.temperature).toBe(0.2)
  })

  it('openai-responses: Bearer auth, input string, reasoning.effort, store=false', () => {
    const wire = buildRewriteWireRequest(
      mustResolve({ protocol: 'openai-responses', model: 'gpt-5.5', effort: 'high' }),
      'a cat',
      'make it neon',
    )
    expect(wire.url).toBe(`${window.location.origin}/v1/responses`)
    expect(wire.headers.Authorization).toBe('Bearer sk-rewrite')
    expect(wire.body.model).toBe('gpt-5.5')
    expect(wire.body.input).toBe(instruction)
    expect(wire.body.messages).toBeUndefined()
    expect(wire.body.reasoning).toEqual({ effort: 'high' })
    expect(wire.body.store).toBe(false)
  })

  it('anthropic-messages: x-api-key + anthropic-version, thinking budget', () => {
    const wire = buildRewriteWireRequest(
      mustResolve({ protocol: 'anthropic-messages', model: 'claude-haiku-5-5', effort: 'low' }),
      'a cat',
      'make it neon',
    )
    expect(wire.url).toBe(`${window.location.origin}/v1/messages`)
    expect(wire.headers['x-api-key']).toBe('sk-rewrite')
    expect(wire.headers['anthropic-version']).toBe(ANTHROPIC_VERSION)
    // Bearer is wrong for Anthropic and must not be sent.
    expect(wire.headers.Authorization).toBeUndefined()
    // In-app gateway: no browser-access header needed.
    expect(wire.headers[ANTHROPIC_BROWSER_HEADER]).toBeUndefined()
    expect(wire.body.messages).toEqual([{ role: 'user', content: instruction }])
    expect(wire.body.thinking).toEqual({ type: 'enabled', budget_tokens: ANTHROPIC_THINKING_BUDGETS.low })
    expect(wire.body.max_tokens).toBeGreaterThan(ANTHROPIC_THINKING_BUDGETS.low)
    // Extended thinking rejects a custom temperature.
    expect('temperature' in wire.body).toBe(false)
  })

  it('anthropic-messages: no thinking block and temperature kept when unset', () => {
    const wire = buildRewriteWireRequest(
      mustResolve({ protocol: 'anthropic-messages', effort: 'unset' }),
      'a cat',
      'make it neon',
    )
    expect('thinking' in wire.body).toBe(false)
    expect(wire.body.temperature).toBe(0.2)
    expect(wire.body.max_tokens).toBe(ANTHROPIC_REPLY_HEADROOM_TOKENS)
  })

  it('anthropic-messages: asks for browser CORS only for a custom host', () => {
    const wire = buildRewriteWireRequest(
      mustResolve({ protocol: 'anthropic-messages', baseUrl: 'https://api.anthropic.com/v1' }),
      'a cat',
      'make it neon',
    )
    expect(wire.url).toBe('https://api.anthropic.com/v1/messages')
    expect(wire.headers[ANTHROPIC_BROWSER_HEADER]).toBe('true')
  })
})

describe('rewrite response parsing per protocol', () => {
  it('openai-chat: reads choices[0].message.content', () => {
    const text = parseRewriteResponse('openai-chat', {
      id: 'chatcmpl-1',
      object: 'chat.completion',
      choices: [{ index: 0, message: { role: 'assistant', content: '  neon cat  ' }, finish_reason: 'stop' }],
    })
    expect(text.trim()).toBe('neon cat')
  })

  it('openai-chat: joins content-part arrays and tolerates legacy choices[0].text', () => {
    expect(
      parseRewriteResponse('openai-chat', {
        choices: [{ message: { content: [{ type: 'text', text: 'neon ' }, { type: 'text', text: 'cat' }] } }],
      }),
    ).toBe('neon cat')
    expect(parseRewriteResponse('openai-chat', { choices: [{ text: 'legacy text' }] })).toBe('legacy text')
  })

  it('openai-chat: returns empty for a shape with no answer', () => {
    expect(parseRewriteResponse('openai-chat', { choices: [] })).toBe('')
    expect(parseRewriteResponse('openai-chat', {})).toBe('')
  })

  it('openai-responses: walks output and skips the leading reasoning item', () => {
    // Real Responses payloads put reasoning first; output[0] is NOT the message.
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

  it('openai-responses: prefers the output_text convenience field', () => {
    expect(parseRewriteResponse('openai-responses', { output_text: 'neon cat', output: [] })).toBe('neon cat')
  })

  it('openai-responses: concatenates several message output_text blocks', () => {
    const text = parseRewriteResponse('openai-responses', {
      output: [
        { type: 'message', content: [{ type: 'output_text', text: 'neon ' }] },
        { type: 'message', content: [{ type: 'output_text', text: 'cat' }] },
      ],
    })
    expect(text).toBe('neon cat')
  })

  it('anthropic-messages: reads text blocks and ignores thinking', () => {
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

  it('anthropic-messages: has no answer when only tool blocks came back', () => {
    expect(
      parseRewriteResponse('anthropic-messages', {
        content: [{ type: 'tool_use', id: 't1', name: 'x', input: {} }],
      }),
    ).toBe('')
  })

  it('survives garbage payloads instead of throwing', () => {
    for (const protocol of ['openai-chat', 'openai-responses', 'anthropic-messages'] as const) {
      expect(parseRewriteResponse(protocol, null)).toBe('')
      expect(parseRewriteResponse(protocol, 'oops')).toBe('')
      expect(parseRewriteResponse(protocol, { choices: 'x', output: 7, content: null })).toBe('')
    }
  })
})

describe('requestRewrite end-to-end with a stubbed fetch', () => {
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

  it('posts the dedicated key and returns the parsed prompt', async () => {
    const spy = stubFetch({ choices: [{ message: { content: ' rewrote it ' } }] })
    const result = await requestRewrite({
      config: config({ protocol: 'openai-chat' }),
      generationKey: 'sk-image',
      original: 'a cat',
      instruction: 'neon',
      signal: new AbortController().signal,
    })

    expect(result.prompt).toBe('rewrote it')
    expect(result.usesGenerationKey).toBe(false)
    expect(result.protocol).toBe('openai-chat')

    const [url, init] = spy.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe(`${window.location.origin}/v1/chat/completions`)
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer sk-rewrite')
  })

  it('never spends the image key once a rewrite key exists', async () => {
    const spy = stubFetch({ content: [{ type: 'text', text: 'ok' }] })
    await requestRewrite({
      config: config({ protocol: 'anthropic-messages', apiKey: 'sk-own' }),
      generationKey: 'sk-image',
      original: 'a cat',
      instruction: 'neon',
      signal: new AbortController().signal,
    })
    const [, init] = spy.mock.calls[0] as unknown as [string, RequestInit]
    expect((init.headers as Record<string, string>)['x-api-key']).toBe('sk-own')
    expect(JSON.stringify(init.headers)).not.toContain('sk-image')
  })

  it('rides the image key when the split config is off', async () => {
    const spy = stubFetch({ choices: [{ message: { content: 'ok' } }] })
    const result = await requestRewrite({
      config: { ...DEFAULT_REWRITE_CONFIG, enabled: false },
      generationKey: 'sk-image',
      original: 'a cat',
      instruction: 'neon',
      signal: new AbortController().signal,
    })
    expect(result.usesGenerationKey).toBe(true)
    const [, init] = spy.mock.calls[0] as unknown as [string, RequestInit]
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer sk-image')
  })

  it('rejects with the upstream message and status on an error response', async () => {
    stubFetch({ error: { type: 'invalid_request_error', message: 'model not found' } }, { status: 404 })
    await expect(
      requestRewrite({
        config: config(),
        generationKey: '',
        original: 'a cat',
        instruction: 'neon',
        signal: new AbortController().signal,
      }),
    ).rejects.toMatchObject({ status: 404, message: expect.stringContaining('model not found') })
  })

  it('rejects when the response carries no text', async () => {
    stubFetch({ choices: [{ message: { content: '   ' } }] })
    await expect(
      requestRewrite({
        config: config(),
        generationKey: '',
        original: 'a cat',
        instruction: 'neon',
        signal: new AbortController().signal,
      }),
    ).rejects.toThrow('AI 未返回新提示词')
  })

  it('rejects before fetching when no key is available at all', async () => {
    const spy = stubFetch({})
    await expect(
      requestRewrite({
        config: config({ apiKey: '' }),
        generationKey: '',
        original: 'a cat',
        instruction: 'neon',
        signal: new AbortController().signal,
      }),
    ).rejects.toThrow()
    expect(spy).not.toHaveBeenCalled()
  })
})

describe('storage key contract', () => {
  it('uses a key that cannot collide with the image token key', () => {
    expect(REWRITE_CONFIG_STORAGE_KEY).toBe('ig_rewriteConfig')
    expect(REWRITE_CONFIG_STORAGE_KEY).not.toBe('ig_apiKey')
  })
})
