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
  /** 每 token 单价（USD），0 表示此前无值 */
  old_value: number
  /** 每 token 单价（USD） */
  new_value: number
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

/** 单模型手动覆盖价，单位「元 / 百万 tokens」。 */
export interface PricingManualOverride extends PricingManualLongContext {
  input: number
  output: number
  cache_read: number
}

/** settings 键 `pricing.cny_official` 的 JSON 结构。 */
export interface PricingCnyOfficialConfig {
  enabled: boolean
  source_url: string
  manual_overrides: Record<string, PricingManualOverride>
  /** 后端命中的国模 vendor id（只读，回写时不提交）。 */
  cn_vendors?: string[]
}

/** 写入体：只提交可写的三个字段，`cn_vendors` 由后端自己算。 */
export interface PricingCnyOfficialUpdate {
  enabled: boolean
  source_url: string
  manual_overrides: Record<string, PricingManualOverride>
}

function toPrice(value: unknown): number {
  const parsed = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(parsed) || parsed < 0) return 0
  return parsed
}

/**
 * 可选的数值字段（长上下文三兄弟）。与 `toPrice` 不同：缺省/空串/非数一律
 * 返回 `undefined`，让调用方把键整个省掉 —— 后端这三个字段带 `omitempty`，
 * 写 0 和不写并不完全等价，少写才是「不启用」的准确表达。
 */
function toOptionalNumber(value: unknown): number | undefined {
  if (value === undefined || value === null) return undefined
  if (typeof value === 'string' && value.trim() === '') return undefined
  const parsed = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(parsed)) return undefined
  return parsed
}

function normalizeOverrides(value: unknown): Record<string, PricingManualOverride> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  const out: Record<string, PricingManualOverride> = {}
  for (const [model, price] of Object.entries(value as Record<string, unknown>)) {
    const key = model.trim()
    if (!key) continue
    const entry = (price && typeof price === 'object' ? price : {}) as Record<string, unknown>
    const override: PricingManualOverride = {
      input: toPrice(entry.input),
      output: toPrice(entry.output),
      cache_read: toPrice(entry.cache_read)
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
    manual_overrides: normalizeOverrides(config.manual_overrides)
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
