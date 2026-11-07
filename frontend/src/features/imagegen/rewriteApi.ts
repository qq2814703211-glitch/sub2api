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
import { buildGatewayUrl } from '@/api/client'
import { ImageGenError } from './api'
import {
  type ResolvedRewriteRequest,
  type RewriteEffort,
  type RewriteProtocol,
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

const VERSION_SEGMENT = /^v\d+[a-z0-9]*$/i

/**
 * Join an API root with a protocol suffix.
 *
 * The root is what a user copies out of a vendor's docs. Those docs stop at
 * different depths — `https://api.openai.com`, `https://api.openai.com/v1`,
 * `https://open.bigmodel.cn/api/paas/v4` — so a root whose last path segment
 * already looks like a version (`v1`, `v4`, `v1beta`) is used as-is and
 * anything else gets `/v1` appended. An empty root means this gateway, which
 * serves all three protocols on `/v1`.
 */
export function buildRewriteUrl(baseUrl: string, path: string): string {
  const root = baseUrl.trim().replace(/\/+$/, '')
  if (!root) return buildGatewayUrl(`/v1${path}`)
  const lastSegment = root.slice(root.lastIndexOf('/') + 1)
  const prefix = VERSION_SEGMENT.test(lastSegment) ? root : `${root}/v1`
  return `${prefix}${path}`
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
 * Build the concrete HTTP request for one protocol.
 *
 * Reasoning effort is omitted entirely at `unset`, so a profile that never
 * touched the setting sends exactly the body the collapsed build sent.
 */
export function buildRewriteWireRequest(
  request: ResolvedRewriteRequest,
  original: string,
  instruction: string,
): RewriteWireRequest {
  const { protocol, apiKey, baseUrl, model, effort } = request
  const content = buildRewriteInstruction(original, instruction)
  const url = buildRewriteUrl(baseUrl, rewritePath(protocol))

  if (protocol === 'openai-chat') {
    const body: Record<string, unknown> = {
      model,
      messages: [{ role: 'user', content }],
      temperature: 0.2,
    }
    if (effort !== 'unset') body.reasoning_effort = effort
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
    if (effort !== 'unset') body.reasoning = { effort }
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
  if (baseUrl.trim()) headers[ANTHROPIC_BROWSER_HEADER] = 'true'

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
  /** The image-generation key, used only when the split config defers to it. */
  generationKey: string
  original: string
  instruction: string
  signal: AbortSignal
}

export interface RewriteCallResult {
  prompt: string
  /** Which key actually paid for the call, for the UI to report. */
  usesGenerationKey: boolean
  protocol: RewriteProtocol
}

/**
 * Run one rewrite. Resolves with the raw rewritten prompt; rejects with an
 * {@link ImageGenError} carrying the upstream message.
 *
 * Validation failures are raised here rather than by the caller so a rewrite
 * can never be attempted with an empty key or model.
 */
export async function requestRewrite(options: RewriteCallOptions): Promise<RewriteCallResult> {
  const { config, generationKey, original, instruction, signal } = options
  const resolved = resolveRewriteRequest(config, generationKey)
  if (!resolved.ok) {
    throw new ImageGenError(
      resolved.reason === 'bad-base-url'
        ? 'AI 改写接口地址无效，请填写 http(s) 开头的地址'
        : 'AI 改写未配置令牌，且没有可回落的生图令牌',
    )
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
