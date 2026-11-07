/**
 * Configuration for the AI prompt-rewrite call.
 *
 * The GloryCode original kept two separate credentials: one for image
 * generation and one for the LLM that rewrites prompts. The port collapsed
 * them into the image key, which forces rewrite traffic through a key that may
 * only be entitled to image models — and rules out pointing the rewrite at a
 * different vendor entirely.
 *
 * This module owns the rewrite side of that split: its own key, base URL,
 * wire protocol, model and reasoning effort. Everything is optional; an
 * untouched install falls back to the image key against this gateway over
 * OpenAI chat completions, which is byte-for-byte what the collapsed version
 * did, so existing browser storage keeps working.
 */

/** Storage key for the persisted rewrite config. Additive — nothing is migrated away. */
export const REWRITE_CONFIG_STORAGE_KEY = 'ig_rewriteConfig'
/**
 * The collapsed build never persisted a rewrite model (it was a bare ref), but
 * a hand-edited or pre-port profile could carry one. Read it once as a seed.
 */
export const LEGACY_REWRITE_MODEL_KEY = 'ig_rewriteModel'

/** Wire protocols the rewrite call can speak. */
export type RewriteProtocol = 'openai-chat' | 'openai-responses' | 'anthropic-messages'

export const REWRITE_PROTOCOLS: readonly RewriteProtocol[] = [
  'openai-chat',
  'openai-responses',
  'anthropic-messages',
] as const

/**
 * Thinking intensity.
 *
 * `unset` means "send no reasoning parameter at all" — that is the legacy
 * behaviour and the default, so nothing changes for anyone who does not opt in.
 */
export type RewriteEffort = 'unset' | 'minimal' | 'low' | 'medium' | 'high' | 'max'

export const REWRITE_EFFORTS: readonly RewriteEffort[] = [
  'unset',
  'minimal',
  'low',
  'medium',
  'high',
  'max',
] as const

/**
 * Effort levels each protocol can actually carry.
 *
 * Chat Completions on the OpenAI spec stops at `high`; the Responses API and
 * Anthropic's `output_config.effort` both publish a `max` tier. Offering a
 * level the protocol cannot express would be a silent no-op, so the list
 * narrows when the protocol changes.
 */
export const REWRITE_EFFORTS_BY_PROTOCOL: Record<RewriteProtocol, readonly RewriteEffort[]> = {
  'openai-chat': ['unset', 'minimal', 'low', 'medium', 'high'],
  'openai-responses': ['unset', 'minimal', 'low', 'medium', 'high', 'max'],
  'anthropic-messages': ['unset', 'minimal', 'low', 'medium', 'high', 'max'],
}

/**
 * Anthropic has no discrete effort field — `thinking.budget_tokens` is a raw
 * token count, and the API rejects anything under 1024. These are the buckets
 * this app maps the shared levels onto. `max` stays at 32768 so that
 * `max_tokens` (budget + reply headroom) still fits every served model's
 * output ceiling.
 */
export const ANTHROPIC_THINKING_BUDGETS: Record<Exclude<RewriteEffort, 'unset'>, number> = {
  minimal: 1024,
  low: 2048,
  medium: 8192,
  high: 16384,
  max: 32768,
}

/** Head-room reserved for the rewritten prompt itself, on top of the budget. */
export const ANTHROPIC_REPLY_HEADROOM_TOKENS = 4096

/**
 * Model names offered as autocomplete per protocol. Free text is always
 * accepted — these are hints pulled from what this deployment and the major
 * vendors actually serve, not an allowlist.
 */
export const REWRITE_MODEL_SUGGESTIONS: Record<RewriteProtocol, readonly string[]> = {
  'openai-chat': [
    'gpt-6-astra',
    'gpt-5.5',
    'gpt-4.1-mini',
    'deepseek-chat',
    'gemini-3-pro',
    'claude-sonnet-5-5',
  ],
  'openai-responses': ['gpt-6-astra', 'gpt-5.5', 'gpt-5.5-mini', 'gpt-4.1'],
  'anthropic-messages': ['claude-haiku-5-5', 'claude-sonnet-5-5', 'claude-opus-5-5'],
}

/** The model the collapsed build hard-coded; kept so behaviour does not drift. */
export const DEFAULT_REWRITE_MODEL = 'gpt-6-astra'
export const DEFAULT_REWRITE_PROTOCOL: RewriteProtocol = 'openai-chat'
export const DEFAULT_REWRITE_EFFORT: RewriteEffort = 'unset'

export interface RewriteConfig {
  /**
   * Opt in to the split configuration. While false the rewrite keeps using the
   * image key against this gateway — except for `model`, which stayed editable
   * in the collapsed UI and stays editable here.
   */
  enabled: boolean
  /** Dedicated rewrite credential. Empty falls back to the image key. */
  apiKey: string
  /** API root for the rewrite endpoint. Empty means this gateway. */
  baseUrl: string
  protocol: RewriteProtocol
  model: string
  effort: RewriteEffort
}

export const DEFAULT_REWRITE_CONFIG: RewriteConfig = {
  enabled: false,
  apiKey: '',
  baseUrl: '',
  protocol: DEFAULT_REWRITE_PROTOCOL,
  model: DEFAULT_REWRITE_MODEL,
  effort: DEFAULT_REWRITE_EFFORT,
}

export function isRewriteProtocol(value: unknown): value is RewriteProtocol {
  return typeof value === 'string' && (REWRITE_PROTOCOLS as readonly string[]).includes(value)
}

export function isRewriteEffort(value: unknown): value is RewriteEffort {
  return typeof value === 'string' && (REWRITE_EFFORTS as readonly string[]).includes(value)
}

/**
 * Narrow an effort to what the selected protocol can express, stepping down to
 * the deepest supported level rather than discarding the user's intent.
 */
export function coerceEffortForProtocol(effort: unknown, protocol: RewriteProtocol): RewriteEffort {
  const allowed = REWRITE_EFFORTS_BY_PROTOCOL[protocol] ?? REWRITE_EFFORTS
  if (isRewriteEffort(effort) && allowed.includes(effort)) return effort
  if (isRewriteEffort(effort)) {
    const want = REWRITE_EFFORTS.indexOf(effort)
    // Walk down the shared ladder so `max` becomes `high` on chat completions.
    for (let index = want; index >= 0; index -= 1) {
      const candidate = REWRITE_EFFORTS[index]
      if (allowed.includes(candidate)) return candidate
    }
  }
  return DEFAULT_REWRITE_EFFORT
}

/**
 * Accepts anything an older profile could hold and returns a usable config.
 *
 * Tolerated shapes: `undefined`/`null`, a bare model string (what a
 * pre-split profile would have had), and a partial object with unknown
 * protocol/effort values or wrong types for any field. Every field falls back
 * independently, so one corrupt value never discards the rest.
 */
export function normalizeRewriteConfig(raw: unknown, legacyModel?: string | null): RewriteConfig {
  const seedModel = String(legacyModel ?? '').trim()

  if (typeof raw === 'string') {
    const model = raw.trim()
    return {
      ...DEFAULT_REWRITE_CONFIG,
      model: model || seedModel || DEFAULT_REWRITE_MODEL,
      // A bare string carries no key, so it cannot be an enabled split config.
      enabled: false,
    }
  }

  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    return {
      ...DEFAULT_REWRITE_CONFIG,
      model: seedModel || DEFAULT_REWRITE_MODEL,
    }
  }

  const source = raw as Record<string, unknown>
  const protocol = isRewriteProtocol(source.protocol) ? source.protocol : DEFAULT_REWRITE_PROTOCOL
  const model = typeof source.model === 'string' ? source.model.trim() : ''

  return {
    enabled: source.enabled === true,
    apiKey: typeof source.apiKey === 'string' ? source.apiKey : '',
    baseUrl: typeof source.baseUrl === 'string' ? normalizeRewriteBaseUrl(source.baseUrl) : '',
    protocol,
    model: model || seedModel || DEFAULT_REWRITE_MODEL,
    effort: coerceEffortForProtocol(source.effort, protocol),
  }
}

/**
 * Trim a user-typed API root. Keeps the value verbatim otherwise — the
 * endpoint builder decides whether `/v1` still has to be appended.
 */
export function normalizeRewriteBaseUrl(value: unknown): string {
  return String(value ?? '')
    .trim()
    .replace(/\/+$/, '')
}

/** True when the string is an absolute http(s) URL, as a base URL must be. */
export function isValidRewriteBaseUrl(value: string): boolean {
  const trimmed = value.trim()
  if (!trimmed) return true
  try {
    const url = new URL(trimmed)
    return url.protocol === 'http:' || url.protocol === 'https:'
  } catch {
    return false
  }
}

export interface ResolvedRewriteRequest {
  protocol: RewriteProtocol
  apiKey: string
  baseUrl: string
  model: string
  effort: RewriteEffort
  /** True when the request rides on the image key because no dedicated key is set. */
  usesGenerationKey: boolean
}

export type RewriteResolveResult =
  | { ok: true; request: ResolvedRewriteRequest }
  | { ok: false; reason: 'missing-key' | 'bad-base-url' }

/**
 * Decide what the rewrite call actually sends.
 *
 * The image key is only consulted when the split config is off — or when it is
 * on but left blank, which is the UI-advertised fallback. Nothing here touches
 * the image key's own state, so a missing rewrite key can never break
 * generation and vice versa.
 *
 * `model` is not validated here: `normalizeRewriteConfig` always substitutes
 * the default for a blank one, so a request can never be built with an empty
 * model in the first place.
 */
export function resolveRewriteRequest(
  config: RewriteConfig,
  generationKey: string,
): RewriteResolveResult {
  const normalized = normalizeRewriteConfig(config)
  const dedicatedKey = normalized.apiKey.trim()
  const fallbackKey = generationKey.trim()

  if (normalized.enabled && !dedicatedKey && !fallbackKey) return { ok: false, reason: 'missing-key' }
  const apiKey = normalized.enabled ? dedicatedKey || fallbackKey : fallbackKey
  if (!apiKey) return { ok: false, reason: 'missing-key' }

  // A base URL only applies to the split config; the fallback always targets
  // this gateway so an operator cannot accidentally redirect image billing.
  const baseUrl = normalized.enabled ? normalized.baseUrl : ''
  if (!isValidRewriteBaseUrl(baseUrl)) return { ok: false, reason: 'bad-base-url' }

  return {
    ok: true,
    request: {
      protocol: normalized.enabled ? normalized.protocol : DEFAULT_REWRITE_PROTOCOL,
      apiKey,
      baseUrl,
      model: normalized.model,
      effort: normalized.enabled ? normalized.effort : DEFAULT_REWRITE_EFFORT,
      usesGenerationKey: !dedicatedKey || !normalized.enabled,
    },
  }
}

/** `max_tokens` for an Anthropic call, always strictly above `budget_tokens`. */
export function anthropicMaxTokens(effort: RewriteEffort): number {
  if (effort === 'unset') return ANTHROPIC_REPLY_HEADROOM_TOKENS
  return ANTHROPIC_THINKING_BUDGETS[effort] + ANTHROPIC_REPLY_HEADROOM_TOKENS
}

/** `thinking.budget_tokens`, or null when the caller asked for no thinking block. */
export function anthropicThinkingBudget(effort: RewriteEffort): number | null {
  if (effort === 'unset') return null
  return ANTHROPIC_THINKING_BUDGETS[effort]
}
