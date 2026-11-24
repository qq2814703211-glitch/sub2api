/**
 * AI 改写（提示词重写）的配置模型。
 *
 * 与上一版的差别（0.1.2 返工，用户原话为准）：
 *   1. 不再让用户手填令牌、手选协议、手填接口地址。密钥从「站内已有密钥」里选，
 *      协议按密钥所属分组的平台自动判定，接口地址写死为站内地址。
 *   2. 思考强度按所选模型的能力收窄；拿不到模型能力元数据时给全量七档。
 *   3. 旧版 localStorage（手填 apiKey / baseUrl / protocol）仍能读出来：
 *      apiKey 降级成只读兜底令牌（UI 不再暴露编辑），baseUrl 与 protocol 直接忽略
 *      —— 地址与协议现在由常量与自适应决定，存量脏值不能再把改写卡死在
 *      「地址无效」这条分支上。
 *
 * 三协议常量、Anthropic thinking budget 映射这部分是上一版已验证过的正确实现，
 * 原样保留。
 */

/** 持久化键。保持原名，这样老配置能被读到并迁移。 */
export const REWRITE_CONFIG_STORAGE_KEY = 'ig_rewriteConfig'
/**
 * 折叠版（合并单 Key）从未持久化改写模型，但移植期或手改过的 profile 可能留下它。
 * 仍然读一次当种子。
 */
export const LEGACY_REWRITE_MODEL_KEY = 'ig_rewriteModel'

function readRewriteApiBaseUrl(): string {
  const injected = (globalThis as { __APP_CONFIG__?: { rewrite_api_base_url?: string } }).__APP_CONFIG__
  const configured = injected?.rewrite_api_base_url?.trim().replace(/\/$/, '') ?? ''
  if (configured) return configured
  if (typeof location !== 'undefined' && location.origin && location.origin !== 'null') {
    return location.origin
  }
  return ''
}

/**
 * 改写接口根地址。部署时只改 config.yaml 的 site.rewrite_api_base_url，
 * 由服务端注入 window.__APP_CONFIG__。留空则用浏览器当前站点。
 */
export const REWRITE_API_BASE_URL = readRewriteApiBaseUrl()

/** 地址固定后，URL 拼接的根路径随之固定；保留常量便于测试与展示。 */
export const REWRITE_API_ROOT = `${REWRITE_API_BASE_URL}/v1`

/** 三种线上协议。用户不选，由 {@link detectRewriteProtocol} 判定。 */
export type RewriteProtocol = 'openai-chat' | 'openai-responses' | 'anthropic-messages'

export const REWRITE_PROTOCOLS: readonly RewriteProtocol[] = [
  'openai-chat',
  'openai-responses',
  'anthropic-messages',
] as const

/**
 * 思考强度。
 *
 *   unset —— 不发送任何思考参数（自动 / 上游默认），也是旧配置的默认值
 *   off   —— 显式关闭思考
 *   minimal / low / medium / high / xhigh / max —— 与后端
 *            `openAIReasoningEffortValues` / `anthropicReasoningEffortValues` 对齐的档位
 */
export type RewriteEffort = 'unset' | 'off' | 'minimal' | 'low' | 'medium' | 'high' | 'xhigh' | 'max'

export const REWRITE_EFFORTS: readonly RewriteEffort[] = [
  'unset',
  'off',
  'minimal',
  'low',
  'medium',
  'high',
  'xhigh',
  'max',
] as const

/**
 * 用户可显式选择的七档（不含 `unset` 自动档）。
 * 后台/文档提到「全量七档」时指的就是这个列表。
 */
export const REWRITE_EFFORT_LEVELS: readonly RewriteEffort[] = [
  'off',
  'minimal',
  'low',
  'medium',
  'high',
  'xhigh',
  'max',
] as const

/**
 * 每种协议能表达的思考强度。
 *
 * 后端 `openAIReasoningEffortValues` 是 minimal/low/medium/high/xhigh/max，
 * `anthropicReasoningEffortValues` 是 low/medium/high/xhigh/max（Anthropic 没有
 * minimal 的语义，但 budget 映射仍保留 1024 兜底）。`off` 在三协议下都能表达：
 * OpenAI 族发 `none`，Anthropic 族则是不发 thinking 块。
 */
export const REWRITE_EFFORTS_BY_PROTOCOL: Record<RewriteProtocol, readonly RewriteEffort[]> = {
  'openai-chat': REWRITE_EFFORTS,
  'openai-responses': REWRITE_EFFORTS,
  'anthropic-messages': REWRITE_EFFORTS,
}

/**
 * Anthropic 没有离散 effort 字段，只有 `thinking.budget_tokens`，
 * 且低于 1024 会被拒。这里把共享档位映射成 budget。`max` 停在 32768，
 * 保证 `max_tokens`（budget + 回复余量）仍在服务模型的输出上限内。
 */
export const ANTHROPIC_THINKING_BUDGETS: Record<Exclude<RewriteEffort, 'unset' | 'off'>, number> = {
  minimal: 1024,
  low: 2048,
  medium: 8192,
  high: 16384,
  xhigh: 24576,
  max: 32768,
}

/** 留给「改写后的提示词」本身的余量，加在 budget 之上。 */
export const ANTHROPIC_REPLY_HEADROOM_TOKENS = 4096

/** 折叠版硬编码的模型，保留以免行为漂移。 */
export const DEFAULT_REWRITE_MODEL = 'gpt-6-astra'
export const DEFAULT_REWRITE_PROTOCOL: RewriteProtocol = 'openai-chat'
export const DEFAULT_REWRITE_EFFORT: RewriteEffort = 'unset'

/**
 * 一个模型对思考强度的能力描述，对应 `/v1/models` 里 xAI/Grok 条目的
 * camelCase 字段（见 `backend/internal/handler/gateway_handler.go` 的
 * `grokModelListItem`）。
 */
export interface RewriteReasoningEffortOption {
  value: string
  label?: string
  default?: boolean
}

export interface RewriteModelEffortMeta {
  supportsReasoningEffort?: boolean
  /** 模型默认档位，例如 `high`。 */
  reasoningEffort?: string
  /** 该模型支持的档位；有值即视为精确清单。 */
  reasoningEfforts?: RewriteReasoningEffortOption[]
}

/** 一个分组内可选模型及其思考强度能力。 */
export interface RewriteModelOption extends RewriteModelEffortMeta {
  id: string
}

export interface RewriteConfig {
  /**
   * 选中的 AI 改写密钥 ID（站内密钥列表里的 id）。`null` 表示未选，
   * 此时改写在有生图密钥的前提下回落到生图密钥。
   */
  keyId: number | null
  /**
   * 旧版手填令牌。只读：UI 不再暴露编辑入口，仅用于平滑过渡，
   * 让存量用户不会因为「改成下拉」而突然失去改写能力。
   */
  legacyApiKey: string
  model: string
  effort: RewriteEffort
}

export const DEFAULT_REWRITE_CONFIG: RewriteConfig = {
  keyId: null,
  legacyApiKey: '',
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
 * 把思考强度收窄到协议能表达的档位，向下取最深的可用档而不是直接丢弃用户意图。
 */
export function coerceEffortForProtocol(effort: unknown, protocol: RewriteProtocol): RewriteEffort {
  const allowed = REWRITE_EFFORTS_BY_PROTOCOL[protocol] ?? REWRITE_EFFORTS
  if (isRewriteEffort(effort) && allowed.includes(effort)) return effort
  if (isRewriteEffort(effort)) {
    const want = REWRITE_EFFORTS.indexOf(effort)
    for (let index = want; index >= 0; index -= 1) {
      const candidate = REWRITE_EFFORTS[index]
      if (allowed.includes(candidate)) return candidate
    }
  }
  return DEFAULT_REWRITE_EFFORT
}

/** 密钥 ID 归一：只接受正整数，其余视为「未选」。 */
function normalizeKeyId(raw: unknown): number | null {
  const value = typeof raw === 'number' ? raw : Number(String(raw ?? '').trim())
  if (!Number.isFinite(value) || value <= 0) return null
  return Math.trunc(value)
}

/**
 * 接受旧 profile 可能存在的任何形状，返回可用配置。
 *
 * 容忍的形状：`undefined`/`null`、裸模型名字符串（拆分前只有模型）、
 * 以及字段各自损坏的部分对象。每个字段独立兜底，一个坏值不会带走其余字段。
 * 旧版的 `apiKey` 迁移到 `legacyApiKey`；`baseUrl` 与 `protocol` 直接丢弃
 * —— 地址写死、协议自适应，留着它们只会造出「地址无效」这种假错误。
 */
export function normalizeRewriteConfig(raw: unknown, legacyModel?: string | null): RewriteConfig {
  const seedModel = String(legacyModel ?? '').trim()

  if (typeof raw === 'string') {
    const model = raw.trim()
    return {
      ...DEFAULT_REWRITE_CONFIG,
      model: model || seedModel || DEFAULT_REWRITE_MODEL,
    }
  }

  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    return {
      ...DEFAULT_REWRITE_CONFIG,
      model: seedModel || DEFAULT_REWRITE_MODEL,
    }
  }

  const source = raw as Record<string, unknown>
  const model = typeof source.model === 'string' ? source.model.trim() : ''
  const legacyApiKey =
    typeof source.legacyApiKey === 'string'
      ? source.legacyApiKey.trim()
      : typeof source.apiKey === 'string'
        ? source.apiKey.trim()
        : ''

  return {
    keyId: normalizeKeyId(source.keyId ?? source.key_id),
    legacyApiKey,
    model: model || seedModel || DEFAULT_REWRITE_MODEL,
    // 协议不再由用户选择，但仍要按自适应后的协议收窄档位；这里只做合法性检查，
    // 真正的收窄发生在 resolveRewriteRequest。
    effort: isRewriteEffort(source.effort) ? source.effort : DEFAULT_REWRITE_EFFORT,
  }
}

/** 分组平台 → 网关协议。与后端 `domain.UsesOpenAIGateway` 的判定同源。 */
const ANTHROPIC_PROTOCOL_PLATFORMS = new Set(['anthropic', 'typesafe', 'antigravity'])

/** 模型名看起来是 Claude 家族（composite 分组需要靠它区分协议）。 */
function modelLooksAnthropic(model: string): boolean {
  return /(^|[-_/])claude|opus|sonnet|haiku/i.test(model)
}

/**
 * 协议自适应。
 *
 * 判定链：所选密钥 → 密钥所属分组（`ApiKey.group.platform`）→ 模型名。
 * 后端网关的路由事实（`internal/server/routes/gateway.go` +
 * `internal/domain/platforms.go`）：
 *   - Anthropic 网关平台（anthropic / typesafe）原生就是 `/v1/messages`；
 *   - OpenAI 网关平台（openai / grok / kimi / zhipu / deepseek / minimax …）
 *     与 gemini 都由 OpenAIGateway 承接，`/v1/chat/completions` 一律可用
 *     （chat→responses 的转换在后端完成），这正好对应
 *     `channel_monitor_checker.go` 里 MonitorProviderOpenAI 默认走 chat_completions
 *     的选择，所以默认落在 `openai-chat`；
 *   - composite 分组一个 Key 通吃多平台，按模型名判断，Claude 家族走
 *     `/v1/messages`，其余走 `openai-chat`。
 * 任何拿不准的情况都回落 `openai-chat`——这与折叠版行为一致，风险最低。
 */
export function detectRewriteProtocol(platform: string, model: string): RewriteProtocol {
  const normalized = String(platform ?? '').trim().toLowerCase()
  if (normalized === 'composite') {
    return modelLooksAnthropic(model) ? 'anthropic-messages' : DEFAULT_REWRITE_PROTOCOL
  }
  if (ANTHROPIC_PROTOCOL_PLATFORMS.has(normalized)) return 'anthropic-messages'
  return DEFAULT_REWRITE_PROTOCOL
}

/**
 * 按模型能力给出可选的思考强度。
 *
 *   - 模型给了精确档位清单 → 只给这些（并再按协议收窄）；
 *   - `supportsReasoningEffort === true` 但没给清单 → 全量七档；
 *   - `supportsReasoningEffort === false` → 只有 `off`；
 *   - 字段缺失（非 Grok 平台拿不到元数据）→ 全量七档，让用户自己选。
 * 返回值不含 `unset`，那个「自动」项由 UI 单独加。
 */
export function effortOptionsForModel(
  meta: RewriteModelEffortMeta | null | undefined,
  protocol: RewriteProtocol,
): RewriteEffort[] {
  const allowed = REWRITE_EFFORTS_BY_PROTOCOL[protocol] ?? REWRITE_EFFORTS
  const keep = (values: readonly RewriteEffort[]) => {
    const seen = new Set<RewriteEffort>()
    const out: RewriteEffort[] = []
    for (const value of values) {
      if (!allowed.includes(value) || seen.has(value)) continue
      seen.add(value)
      out.push(value)
    }
    return out
  }

  const declared = (meta?.reasoningEfforts ?? [])
    .map((item) => String(item?.value ?? '').trim().toLowerCase())
    .filter((value): value is RewriteEffort => isRewriteEffort(value))
  if (declared.length > 0) {
    const filtered = keep(declared.filter((value) => value !== 'unset'))
    // 清单里只出现具体档位时，`off` 仍应可选：关掉思考永远是安全的方向。
    if (filtered.length > 0 && !filtered.includes('off')) filtered.unshift('off')
    if (filtered.length > 0) return filtered
  }

  if (meta?.supportsReasoningEffort === false) return keep(['off'])
  return keep(REWRITE_EFFORT_LEVELS)
}

/** 模型元数据里声明的默认档位，用作「自动」的提示。 */
export function defaultEffortForModel(meta: RewriteModelEffortMeta | null | undefined): RewriteEffort | null {
  const declaredDefault = (meta?.reasoningEfforts ?? []).find((item) => item?.default === true)
  const fromList = String(declaredDefault?.value ?? '').trim().toLowerCase()
  if (isRewriteEffort(fromList) && fromList !== 'unset') return fromList
  const fromField = String(meta?.reasoningEffort ?? '').trim().toLowerCase()
  if (isRewriteEffort(fromField) && fromField !== 'unset') return fromField
  return null
}

export interface RewriteContext {
  /** 已选 AI 改写密钥的明文（由密钥列表按 id 解析得到）。 */
  rewriteKey: string
  /** 生图密钥明文，作为兜底。 */
  generationKey: string
  /** 密钥所属分组平台，供协议自适应使用。 */
  platform: string
}

export interface ResolvedRewriteRequest {
  protocol: RewriteProtocol
  apiKey: string
  baseUrl: string
  model: string
  effort: RewriteEffort
  /** 为 true 时本次请求走的是生图密钥，UI 需要如实告知用户。 */
  usesGenerationKey: boolean
}

export type RewriteResolveResult =
  | { ok: true; request: ResolvedRewriteRequest }
  | { ok: false; reason: 'missing-key' }

/**
 * 决定改写请求实际发什么。
 *
 * 令牌优先级：选中的改写密钥 → 旧版手填令牌 → 生图密钥。后两者都算「用生图侧
 * 的凭证」，会通过 `usesGenerationKey` 告知 UI，其中旧版手填令牌仍然不是生图
 * 令牌，所以单独判为 false 之外还要看它是否等于生图密钥——这里按「有没有选中
 * 改写密钥」判定，语义与界面徽标一致。
 *
 * 地址永远取 {@link REWRITE_API_BASE_URL}，因此不存在「地址非法」这种失败原因，
 * 存量里的脏 baseUrl 也不会再把改写卡死。
 */
export function resolveRewriteRequest(
  config: RewriteConfig,
  context: RewriteContext,
): RewriteResolveResult {
  const normalized = normalizeRewriteConfig(config)
  const rewriteKey = String(context.rewriteKey ?? '').trim()
  const legacyKey = normalized.legacyApiKey.trim()
  const generationKey = String(context.generationKey ?? '').trim()

  const hasDedicated = rewriteKey.length > 0 || legacyKey.length > 0
  const apiKey = rewriteKey || legacyKey || generationKey
  if (!apiKey) return { ok: false, reason: 'missing-key' }

  const protocol = detectRewriteProtocol(context.platform, normalized.model)

  return {
    ok: true,
    request: {
      protocol,
      apiKey,
      baseUrl: REWRITE_API_BASE_URL,
      model: normalized.model,
      effort: coerceEffortForProtocol(normalized.effort, protocol),
      usesGenerationKey: !hasDedicated,
    },
  }
}

/** Anthropic 请求的 `max_tokens`，永远严格大于 `budget_tokens`。 */
export function anthropicMaxTokens(effort: RewriteEffort): number {
  if (effort === 'unset' || effort === 'off') return ANTHROPIC_REPLY_HEADROOM_TOKENS
  return ANTHROPIC_THINKING_BUDGETS[effort] + ANTHROPIC_REPLY_HEADROOM_TOKENS
}

/** `thinking.budget_tokens`；`unset`/`off` 返回 null 表示不发送 thinking 块。 */
export function anthropicThinkingBudget(effort: RewriteEffort): number | null {
  if (effort === 'unset' || effort === 'off') return null
  return ANTHROPIC_THINKING_BUDGETS[effort]
}
