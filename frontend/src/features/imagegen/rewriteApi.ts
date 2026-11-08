/**
 * Protocol dispatch for the AI prompt-rewrite call.
 *
 * Three wire formats are supported, each with its own URL, auth headers, body
 * shape and response extraction:
 *
 *   openai-chat       POST {root}/chat/completions   Authorization: Bearer
 *                     body    { model, messages, reasoning_effort? }
 *                     result  choices[0].message.content
 *
 *   openai-responses  POST {root}/responses          Authorization: Bearer
 *                     body    { model, input, reasoning: { effort }? }
 *                     result  output[].content[].text  (type == output_text)
 *
 *   anthropic-messages POST {root}/messages          x-api-key + anthropic-version
 *                     body    { model, max_tokens, messages, thinking? }
 *                     result  content[].text           (type == text)
 *
 * The shapes above were checked against this repository's own server-side
 * implementations rather than from memory: `internal/service/channel_monitor_checker.go`
 * carries one adapter per protocol (paths, headers, and the
 * `extractAnthropicMonitorText` / `extractOpenAIResponsesText` extractors), and
 * `internal/pkg/apicompat/types.go` pins the exact JSON field names for both
 * the Anthropic Messages and OpenAI Responses bodies. The Responses extractor
 * in particular cannot assume `output[0]` is the message — reasoning items are
 * emitted first — which is why this one walks the whole array and filters on
 * `type`.
 *
 * Every builder and parser here is pure so the dispatch can be unit-tested
 * without a network.
 */
import { ImageGenError } from './api'
import {
  type ResolvedRewriteRequest,
  type RewriteContext,
  type RewriteEffort,
  type RewriteProtocol,
  REWRITE_API_BASE_URL,
  anthropicMaxTokens,
  anthropicThinkingBudget,
  resolveRewriteRequest,
  type RewriteConfig,
} from './rewriteConfig'

/** Anthropic requires this header on every Messages call. */
export const ANTHROPIC_VERSION = '2023-06-01'
/**
 * Anthropic's documented opt-in for calling the API straight from a browser.
 * Without it the preflight has no CORS headers and the fetch dies opaquely.
 * Only sent when the caller pointed the rewrite at an external host — the
 * in-app gateway never needs it.
 */
export const ANTHROPIC_BROWSER_HEADER = 'anthropic-dangerous-direct-browser-access'

/**
 * The instruction sent to the rewrite model. Copied verbatim from the page
 * this feature was ported from; changing it changes rewrite quality, so it is
 * not a place for tidying.
 */
export function buildRewriteInstruction(original: string, instruction: string): string {
  return `现在你是一名专业的AI提示词工程师，将以下生图内容提示词修改为用户所需要的新提示词，原提示词:"${original}",用户修改期待:"${instruction}"。直接且只能输出修改后的提示词，严禁输入非修改后提示词内容`
}

/**
 * 地址写死之后，这里只做一件事：把固定根地址与协议后缀拼起来。
 *
 * 上一版允许用户填任意 API root，于是要判断「末段是不是版本号、要不要补 /v1」；
 * 现在根地址由 {@link REWRITE_API_BASE_URL} 决定，那段猜测逻辑连同它的
 * 「地址非法」失败分支一起下线——存量配置里的脏地址不会再让改写卡死。
 * 保留函数签名（baseUrl 参数被忽略）是为了让三协议构造的调用点保持原样。
 */
export function buildRewriteUrl(_baseUrl: string, path: string): string {
  return `${REWRITE_API_BASE_URL}/v1${path}`
}

/** The endpoint path (after the version segment) for each protocol. */
export function rewritePath(protocol: RewriteProtocol): string {
  switch (protocol) {
    case 'openai-chat':
      return '/chat/completions'
    case 'openai-responses':
      return '/responses'
    case 'anthropic-messages':
      return '/messages'
  }
}

export interface RewriteWireRequest {
  url: string
  headers: Record<string, string>
  body: Record<string, unknown>
}

/**
 * 思考强度落到具体协议字段时的取值。
 *
 * `off` 在 OpenAI 族里不是一个合法档位，关闭思考要发 `none`
 * （后端 `filterOpenAIResponsesNoneReasoningEffortForAccount` 认的就是 `none`）；
 * `xhigh` 直接透传，后端 `NormalizeMaxReasoningEffort` 接受它。
 * `unset` 返回 null 表示「整个字段都不发」。
 */
function wireEffort(effort: RewriteEffort): string | null {
  if (effort === 'unset') return null
  if (effort === 'off') return 'none'
  return effort
}

/**
 * Build the concrete HTTP request for one protocol.
 *
 * 思考强度为 `unset` 时整个字段都不发，因此没动过这项设置的 profile 发出的
 * body 与折叠版完全一致。
 */
export function buildRewriteWireRequest(
  request: ResolvedRewriteRequest,
  original: string,
  instruction: string,
): RewriteWireRequest {
  const { protocol, apiKey, baseUrl, model, effort } = request
  const content = buildRewriteInstruction(original, instruction)
  const url = buildRewriteUrl(baseUrl, rewritePath(protocol))
  const effortValue = wireEffort(effort)

  if (protocol === 'openai-chat') {
    const body: Record<string, unknown> = {
      model,
      messages: [{ role: 'user', content }],
      temperature: 0.2,
    }
    if (effortValue !== null) body.reasoning_effort = effortValue
    return {
      url,
      headers: { Authorization: `Bearer ${apiKey}`, 'Content-Type': 'application/json' },
      body,
    }
  }

  if (protocol === 'openai-responses') {
    const body: Record<string, unknown> = {
      model,
      input: content,
      // Rewrites are throwaway; nothing should be retained server-side.
      store: false,
    }
    if (effortValue !== null) body.reasoning = { effort: effortValue }
    return {
      url,
      headers: { Authorization: `Bearer ${apiKey}`, 'Content-Type': 'application/json' },
      body,
    }
  }

  const budget = anthropicThinkingBudget(effort)
  const body: Record<string, unknown> = {
    model,
    max_tokens: anthropicMaxTokens(effort),
    messages: [{ role: 'user', content }],
  }
  // Extended thinking rejects a custom temperature, so it is only sent when
  // no thinking block is requested.
  if (budget === null) body.temperature = 0.2
  else body.thinking = { type: 'enabled', budget_tokens: budget }

  const headers: Record<string, string> = {
    'x-api-key': apiKey,
    'anthropic-version': ANTHROPIC_VERSION,
    'Content-Type': 'application/json',
  }
  // 不发送浏览器直连头：改写地址固定为站内网关（同源策略下由本站反代），
  // 而 `anthropic-dangerous-direct-browser-access` 只在打第三方 Anthropic 主机时才需要。

  return { url, headers, body }
}

/** Flatten a chat-completions content field, which may be a string or parts. */
function flattenChatContent(content: unknown): string {
  if (typeof content === 'string') return content
  if (!Array.isArray(content)) return ''
  return content
    .map((part) => {
      if (typeof part === 'string') return part
      if (part && typeof part === 'object') {
        const text = (part as { text?: unknown }).text
        if (typeof text === 'string') return text
      }
      return ''
    })
    .join('')
}

/** `choices[0].message.content`, tolerating `choices[0].text` legacy shapes. */
function extractOpenAIChatText(payload: Record<string, unknown>): string {
  const choices = payload.choices
  if (!Array.isArray(choices) || !choices.length) return ''
  const first = choices[0] as Record<string, unknown> | null | undefined
  if (!first || typeof first !== 'object') return ''
  const message = first.message as Record<string, unknown> | undefined
  const fromMessage = flattenChatContent(message?.content)
  if (fromMessage.trim()) return fromMessage
  return typeof first.text === 'string' ? first.text : ''
}

/**
 * Aggregate the assistant text out of a Responses payload.
 *
 * `output_text` is the SDK's convenience field and is checked first because
 * OpenAI-compatible gateway rewrites sometimes emit it directly. The real wire
 * format needs the walk: `output` may lead with `reasoning` / tool items, so
 * indexing into `output[0]` is wrong.
 */
function extractResponsesText(payload: Record<string, unknown>): string {
  const shortcut = payload.output_text
  if (typeof shortcut === 'string' && shortcut.trim()) return shortcut

  const output = payload.output
  if (!Array.isArray(output)) return ''
  const texts: string[] = []
  for (const item of output) {
    if (!item || typeof item !== 'object') continue
    const entry = item as Record<string, unknown>
    const itemType = typeof entry.type === 'string' ? entry.type : ''
    if (itemType && itemType !== 'message') continue
    const content = entry.content
    if (!Array.isArray(content)) continue
    for (const block of content) {
      if (!block || typeof block !== 'object') continue
      const part = block as Record<string, unknown>
      const partType = typeof part.type === 'string' ? part.type : ''
      if (partType && partType !== 'output_text') continue
      if (typeof part.text === 'string' && part.text.trim()) texts.push(part.text)
    }
  }
  return texts.join('')
}

/** `content[].text` where the block type is `text`. */
function extractAnthropicText(payload: Record<string, unknown>): string {
  const content = payload.content
  if (!Array.isArray(content)) return ''
  const parts: string[] = []
  for (const block of content) {
    if (!block || typeof block !== 'object') continue
    const entry = block as Record<string, unknown>
    if (entry.type !== 'text') continue
    if (typeof entry.text === 'string' && entry.text.trim()) parts.push(entry.text)
  }
  return parts.join('\n')
}

/** Pull the rewritten prompt out of a successful response body. */
export function parseRewriteResponse(protocol: RewriteProtocol, payload: unknown): string {
  if (!payload || typeof payload !== 'object') return ''
  const record = payload as Record<string, unknown>
  switch (protocol) {
    case 'openai-chat':
      return extractOpenAIChatText(record)
    case 'openai-responses':
      return extractResponsesText(record)
    case 'anthropic-messages':
      return extractAnthropicText(record)
  }
}

/**
 * Read the human-readable message out of an error body.
 *
 * Anthropic nests it as `error.message`; OpenAI does too, but compatible
 * gateways also put `message` at the top level. Both are checked.
 */
export function extractRewriteError(payload: unknown, response: Response): string {
  const record = payload && typeof payload === 'object' ? (payload as Record<string, unknown>) : {}
  const error = record.error && typeof record.error === 'object' ? (record.error as Record<string, unknown>) : {}
  const detail = [
    typeof error.message === 'string' ? error.message : '',
    typeof record.message === 'string' ? record.message : '',
  ].filter(Boolean)
  const headline = detail[0] || `${response.status} ${response.statusText}`
  const extras = [
    typeof error.type === 'string' ? error.type : '',
    typeof error.code === 'string' ? error.code : '',
    typeof record.type === 'string' && record.type !== 'error' ? record.type : '',
  ].filter(Boolean)
  return [headline, ...extras].join(' | ')
}

export interface RewriteCallOptions {
  config: RewriteConfig
  /** 改写令牌与分组平台，由 UI 从站内密钥列表解析得到。 */
  context: RewriteContext
  original: string
  instruction: string
  signal: AbortSignal
}

export interface RewriteCallResult {
  prompt: string
  /** 本次请求由生图密钥（或旧版手填令牌）承担时为 true，供 UI 如实告知。 */
  usesGenerationKey: boolean
  protocol: RewriteProtocol
}

/**
 * 跑一次改写。成功时给出改写后的提示词；失败时抛 {@link ImageGenError} 并带上
 * 上游原始信息。
 *
 * 校验放在这里而不是调用方，保证永远不会用空令牌或空模型发请求。
 */
export async function requestRewrite(options: RewriteCallOptions): Promise<RewriteCallResult> {
  const { config, context, original, instruction, signal } = options
  const resolved = resolveRewriteRequest(config, context)
  if (!resolved.ok) {
    throw new ImageGenError('AI 改写未选密钥，且没有可回落的生图密钥')
  }

  const wire = buildRewriteWireRequest(resolved.request, original, instruction)
  const response = await fetch(wire.url, {
    method: 'POST',
    headers: wire.headers,
    body: JSON.stringify(wire.body),
    signal,
  })

  const payload = (await response.json().catch(() => null)) as unknown
  if (!response.ok) throw new ImageGenError(extractRewriteError(payload, response), response.status)

  const text = parseRewriteResponse(resolved.request.protocol, payload).trim()
  if (!text) throw new ImageGenError('AI 未返回新提示词')
  return {
    prompt: text,
    usesGenerationKey: resolved.request.usesGenerationKey,
    protocol: resolved.request.protocol,
  }
}

/** Re-exported so callers can build an effort dropdown without another import. */
export type { RewriteEffort }
