/**
 * models.dev 官方参考价同步的变更史 + 三档计价口径配置 API。
 *
 * 同步任务在后端按 pricing.hash_check_interval_minutes 自动跑，按
 * `usd < cny-official < manual` 三档优先级合并，只补/改「价表里还没有手动
 * 定价」的模型（ADR 0010）。本文件覆盖两组接口：
 *
 *   1. 只读：变更史列表与同步状态（`/admin/model-pricing-sync/{history,status}`）。
 *   2. 读写：`manual` 口径配置（本卡片），落 settings 键 `pricing.cny_official`。
 *
 * 为什么 `pricing.cny_official` 不用 `PUT /admin/settings`：该接口按
 * `UpdateSettingsRequest` 结构体的字段白名单逐项写库，未知 JSON 键会被
 * encoding/json 静默丢弃，整份 payload 里带 `pricing.cny_official` 只会
 * 200 而什么都不写。所以这个键走同组的专用端点：
 *
 *   GET /api/v1/admin/model-pricing-sync/cny-official
 *   PUT /api/v1/admin/model-pricing-sync/cny-official
 *
 * 载荷即 settings 里存的 JSON 本体，不额外包壳。
 */

import { apiClient } from '../client'
import type { PaginatedResponse } from '@/types'

export interface ModelPricingSyncHistoryRow {
  id: number
  model: string
  field: string
  old_value?: number | null
  new_value?: number | null
  source: string
  applied_at: string
  reason: string
}

export interface ModelPricingSyncHistoryQuery {
  page?: number
  page_size?: number
  model?: string
  field?: string
  source?: string
}

export interface ModelPricingSyncStatus {
  enabled: boolean
  last_run?: string
  last_error?: string
  last_applied?: number
  interval_seconds?: number
  override_file?: string
  /** 三档口径优先级，后端固定给出 ["usd","cny-official","manual"]。 */
  tier_priority?: string[]
  /** 人民币口径的同步侧状态（后端 Status() 输出）。 */
  cny_official?: ModelPricingSyncCnyStatus
  pricing_v2?: {
    active_revision: string
    models: Record<string, PricingModelStatus>
    source_errors?: Record<string, string>
    /** 本进程真实加载并用于计费的修订。scope 恒为 "process"，不是全局承诺。 */
    applied_revision?: string
    applied_at?: string
    /** 最近一次从库里读到的已提交修订，以及读取时间。 */
    head_revision?: string
    head_checked_at?: string
    head_age_seconds?: number
    /** active==head 且校验新鲜、本地投影健康时才是 true。 */
    converged?: boolean
    stale?: boolean
    scope?: 'process'
    node?: string
    head_error?: string
    follow?: {
      mode: 'notify' | 'poll'
      backstop_seconds: number
      wakes: number
      reconnects: number
      last_wake_at?: string
      last_reconcile_at?: string
      last_error?: string
    }
  }
}

export const BILLING_MODES = ['token', 'per_request', 'per_second', 'per_image'] as const
export type PricingBillingMode = (typeof BILLING_MODES)[number]
export type PricingQuoteRates = Record<string, number | null>

export interface PriceSyncQuote {
  model: string
  provider: string
  currency: 'SITE' | 'CNY' | 'USD'
  billing_mode: PricingBillingMode
  rates: PricingQuoteRates
  tiers?: { threshold: number; inclusive?: boolean; rates: PricingQuoteRates }[]
  windows?: { timezone: string; weekdays?: number[]; start: string; end: string; exclude_cn_holidays?: boolean; rates: PricingQuoteRates }[]
  source_url: string
  evidence: string
  verified_at: string
}

export interface PricingModelStatus {
  mode: 'auto' | 'manual'
  auto?: PriceSyncQuote | null
  effective?: PriceSyncQuote | null
  issue?: string
  external?: boolean
}

/**
 * `status.cny_official` 的来源是后端 `ModelPricingSyncService.Status()`，
 * 是只读的「同步跑成什么样」，不等于 settings 里的配置本体。
 *
 * `national_vendor` 是后端白名单的真实来源，卡片折叠区用它，避免前端硬编码
 * 清单与后端漂移。
 */
export interface ModelPricingSyncCnyStatus {
  enabled?: boolean
  last_error?: string
  last_applied?: number
  source_url?: string
  site_timezone?: string
  /** 18 个国模 vendor id。 */
  national_vendor?: string[]
  report?: Record<string, unknown>
}

export type ModelPricingSyncHistoryResponse = PaginatedResponse<ModelPricingSyncHistoryRow>

export async function listHistory(params: ModelPricingSyncHistoryQuery): Promise<ModelPricingSyncHistoryResponse> {
  const { data } = await apiClient.get('/admin/model-pricing-sync/history', { params })
  return data
}

export async function getStatus(): Promise<ModelPricingSyncStatus> {
  const { data } = await apiClient.get('/admin/model-pricing-sync/status')
  return data
}

/**
 * `basellm/llm-metadata` 的全量价目表。CNY 是独立分支
 * （`cost.currency_options.CNY`），不是美元换算，每日自动更新。
 */
export const DEFAULT_CNY_SOURCE_URL =
  'https://raw.githubusercontent.com/basellm/llm-metadata/main/dist/api/all.json'

/**
 * 实测确认的 18 个国模 vendor id（design-013）。
 *
 * 仅作只读展示的后备清单：后端在配置响应里返回 `cn_vendors` 时用后端的，
 * 没返回时用这份，免得折叠区永远空着。判定逻辑始终在后端，前端不参与。
 */
export const DEFAULT_CN_VENDORS = [
  'alibaba-cn',
  'alibaba-coding-plan-cn',
  'alibaba-token-plan-cn',
  'bailing',
  'minimax-cn',
  'minimax-cn-coding-plan',
  'moonshotai-cn',
  'sensenova',
  'stepfun',
  'stepfun-step-plan',
  'tencent-coding-plan',
  'tencent-token-plan',
  'tencent-tokenhub',
  'volcengine',
  'volcengine-coding-plan',
  'xiaomi-token-plan-cn',
  'zhipuai',
  'zhipuai-coding-plan'
] as const

/**
 * 单模型的长上下文阶梯三字段，字段名与后端 `CNYOfficialManualPrice` 的
 * json tag 一一对应，不要改名。
 *
 * - `long_context_threshold`：进入长上下文档的输入 token 阈值（含缓存读/写 token）。
 * - `long_context_input_multiplier`：超阈值后输入侧（输入 + 缓存读 + 缓存写）的倍率。
 * - `long_context_output_multiplier`：超阈值后输出的倍率。
 *
 * 三个字段后端都带 `omitempty`：留空就不落库；阈值 0 等价于不启用阶梯。
 * 缓存读/写没有独立倍率，计费时与输入一起乘输入倍率。
 */
export interface PricingManualLongContext {
  long_context_threshold?: number
  long_context_input_multiplier?: number
  long_context_output_multiplier?: number
}

/** Token prices are site units per million tokens; fixed prices are per billing unit. */
export interface PricingManualOverride extends PricingManualLongContext, PricingManualExtraFields {
  billing_mode?: PricingBillingMode
  auto_restore?: boolean
  input?: number | null
  output?: number | null
  cache_read?: number | null
  cache_write?: number | null
  cache_write_1h?: number | null
  cache_write_30m?: number | null
  cache_write_12h?: number | null
  per_request_price?: number | null
  per_second_price?: number | null
}

export const MANUAL_TOKEN_FIELDS = [
  'input', 'output', 'cache_read', 'cache_write', 'cache_write_1h', 'cache_write_30m', 'cache_write_12h'
] as const

export const MANUAL_UNIT_FIELDS = {
  per_request: 'per_request_price',
  per_second: 'per_second_price',
  per_image: 'image_output_per_image'
} as const

export const MANUAL_RATE_KEYS = {
  input: 'input_cost_per_token',
  output: 'output_cost_per_token',
  cache_read: 'cache_read_input_token_cost',
  cache_write: 'cache_creation_input_token_cost',
  cache_write_1h: 'cache_creation_input_token_cost_above_1hr',
  cache_write_30m: 'cache_creation_input_token_cost_30m',
  cache_write_12h: 'cache_creation_input_token_cost_12h',
  per_request_price: 'cost_per_request',
  per_second_price: 'cost_per_second',
  image_output_per_image: 'output_cost_per_image',
  input_priority: 'input_cost_per_token_priority',
  output_priority: 'output_cost_per_token_priority',
  cache_read_priority: 'cache_read_input_token_cost_priority',
  cache_write_priority: 'cache_creation_input_token_cost_priority',
  image_input: 'input_cost_per_image_token',
  image_cache_read: 'cache_read_input_image_token_cost',
  image_output_token: 'output_cost_per_image_token',
  cache_storage_per_hour: 'cache_storage_cost_per_token_per_hour'
} as const satisfies Partial<Record<keyof PricingManualOverride, string>>

export function getRateUnit(field: string): PricingBillingMode | 'storage' | undefined {
  switch (field) {
    case 'cost_per_request': return 'per_request'
    case 'cost_per_second': return 'per_second'
    case 'output_cost_per_image': return 'per_image'
    case 'cache_storage_cost_per_token_per_hour': return 'storage'
    default: return field.includes('_token') ? 'token' : undefined
  }
}

/**
 * 可选计费项。默认不出现在页面上，运营在条目旁用「添加计费项」目录按需添加，
 * 已添加的可以删掉。
 *
 * 这些字段在计费链路上都有真实扣费点，不是仅供展示：
 *   - `*_priority`             priority 服务档单价，命中时替换基准价
 *   - `image_*`                图片输入 / 图片缓存读 / 图片输出 token
 *   - `image_output_per_image` 图片按张计价
 *   - `cache_storage_per_hour` 显式上下文缓存存储费（元 / 百万 token·小时）
 *
 * 字段名与后端 `CNYOfficialManualPrice` 的 json tag 一一对应，不要改名。
 */
export interface PricingManualExtraFields {
  input_priority?: number | null
  output_priority?: number | null
  cache_read_priority?: number | null
  cache_write_priority?: number | null
  image_input?: number | null
  image_cache_read?: number | null
  image_output_token?: number | null
  image_output_per_image?: number | null
  cache_storage_per_hour?: number | null
}

/** 可选计费项的一个目录条目。 */
export interface ManualExtraFieldSpec {
  key: keyof PricingManualExtraFields & string
  /** 所在分组，用于目录分组显示。 */
  group: 'priority' | 'image' | 'storage'
  /** 计价单位，写进输入框后缀。 */
  unit: 'per_million_tokens' | 'per_million_tokens_hour' | 'per_image'
}

/**
 * 「添加计费项」目录。与后端 `cnyOfficialManualExtraFields` 一一对应 ——
 * 后端新增一项却没同步这里，页面上就永远加不出那个计费项。
 */
export const MANUAL_EXTRA_FIELDS: readonly ManualExtraFieldSpec[] = [
  { key: 'input_priority', group: 'priority', unit: 'per_million_tokens' },
  { key: 'output_priority', group: 'priority', unit: 'per_million_tokens' },
  { key: 'cache_read_priority', group: 'priority', unit: 'per_million_tokens' },
  { key: 'cache_write_priority', group: 'priority', unit: 'per_million_tokens' },
  { key: 'image_input', group: 'image', unit: 'per_million_tokens' },
  { key: 'image_cache_read', group: 'image', unit: 'per_million_tokens' },
  { key: 'image_output_token', group: 'image', unit: 'per_million_tokens' },
  { key: 'image_output_per_image', group: 'image', unit: 'per_image' },
  { key: 'cache_storage_per_hour', group: 'storage', unit: 'per_million_tokens_hour' }
] as const

/** settings 键 `pricing.cny_official` 的 JSON 结构。 */
export interface PricingCnyOfficialConfig {
  enabled: boolean
  source_url: string
  manual_overrides: Record<string, PricingManualOverride>
  /** 后端命中的国模 vendor id（只读，回写时不提交）。 */
  cn_vendors?: string[]
  revision?: string
  active_revision?: string
}

/** Revision comes from the last read; activation and vendor fields are read-only. */
export interface PricingCnyOfficialUpdate {
  enabled: boolean
  source_url: string
  manual_overrides: Record<string, PricingManualOverride>
  revision?: string
}

/**
 * 可选的数值字段（长上下文三兄弟）。与 `toPrice` 不同：缺省/空串/非数一律
 * 返回 `undefined`，让调用方把键整个省掉 —— 后端这三个字段带 `omitempty`，
 * 写 0 和不写并不完全等价，少写才是「不启用」的准确表达。
 */
function toOptionalNumber(value: unknown): number | undefined {
  if (typeof value !== 'number' && typeof value !== 'string') return undefined
  if (typeof value === 'string' && value.trim() === '') return undefined
  const parsed = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(parsed)) return undefined
  return parsed
}

function normalizeOverrides(value: unknown): Record<string, PricingManualOverride> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  const out: Record<string, PricingManualOverride> = Object.create(null)
  for (const [model, price] of Object.entries(value as Record<string, unknown>)) {
    const key = model.trim()
    if (!key) continue
    const entry = (price && typeof price === 'object' ? price : {}) as Record<string, unknown>
    const override: PricingManualOverride = {}
    if (BILLING_MODES.includes(entry.billing_mode as PricingBillingMode)) {
      override.billing_mode = entry.billing_mode as PricingBillingMode
    }
    if (typeof entry.auto_restore === 'boolean') override.auto_restore = entry.auto_restore
    for (const field of Object.keys(MANUAL_RATE_KEYS) as (keyof typeof MANUAL_RATE_KEYS)[]) {
      const parsed = entry[field] === null ? null : toOptionalNumber(entry[field])
      if (parsed !== undefined) override[field] = parsed
    }
    const threshold = toOptionalNumber(entry.long_context_threshold)
    const inputMultiplier = toOptionalNumber(entry.long_context_input_multiplier)
    const outputMultiplier = toOptionalNumber(entry.long_context_output_multiplier)
    if (threshold !== undefined) override.long_context_threshold = threshold
    if (inputMultiplier !== undefined) override.long_context_input_multiplier = inputMultiplier
    if (outputMultiplier !== undefined) override.long_context_output_multiplier = outputMultiplier
    out[key] = override
  }
  return out
}

/**
 * 后端可能把 settings 里的原始字符串直接回传（`GetRawSettings` 返回的是
 * string），也可能已经解成对象，两种都收。
 */
export function normalizeCnyOfficialConfig(raw: unknown): PricingCnyOfficialConfig {
  let value: unknown = raw
  if (typeof value === 'string') {
    const text = value.trim()
    if (!text) {
      value = {}
    } else {
      try {
        value = JSON.parse(text)
      } catch {
        value = {}
      }
    }
  }
  const source = (value && typeof value === 'object' ? value : {}) as Record<string, unknown>
  const vendors = Array.isArray(source.cn_vendors)
    ? source.cn_vendors.filter((item): item is string => typeof item === 'string' && item.trim() !== '')
    : []

  return {
    enabled: source.enabled !== false,
    source_url: typeof source.source_url === 'string' && source.source_url.trim()
      ? source.source_url.trim()
      : DEFAULT_CNY_SOURCE_URL,
    manual_overrides: normalizeOverrides(source.manual_overrides),
    ...(typeof source.revision === 'string' ? { revision: source.revision } : {}),
    ...(typeof source.active_revision === 'string' ? { active_revision: source.active_revision } : {}),
    ...(vendors.length > 0 ? { cn_vendors: vendors } : {})
  }
}

/** 读取 `manual` 口径配置。 */
export async function getCnyOfficialConfig(): Promise<PricingCnyOfficialConfig> {
  const { data } = await apiClient.get<unknown>('/admin/model-pricing-sync/cny-official')
  return normalizeCnyOfficialConfig(data)
}

/** 写入 `manual` 口径配置，返回后端落库后的结果。 */
export async function updateCnyOfficialConfig(
  config: PricingCnyOfficialUpdate
): Promise<PricingCnyOfficialConfig> {
  const payload: PricingCnyOfficialUpdate = {
    enabled: config.enabled,
    source_url: config.source_url.trim() || DEFAULT_CNY_SOURCE_URL,
    manual_overrides: normalizeOverrides(config.manual_overrides),
    ...(config.revision !== undefined ? { revision: config.revision } : {})
  }
  const { data } = await apiClient.put<unknown>('/admin/model-pricing-sync/cny-official', payload)
  return normalizeCnyOfficialConfig(data ?? payload)
}

export default {
  listHistory,
  getStatus,
  getCnyOfficialConfig,
  updateCnyOfficialConfig
}
