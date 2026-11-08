/**
 * 「AI 改写」下拉选项的数据来源。
 *
 * 用户不需要手填模型、也不需要选协议，界面得替他把这两件事查出来。两个事实
 * 都来自站内已有接口，本模块不新造端点：
 *
 *   1. 可选模型 —— 用所选密钥打 `GET {站内}/v1/models`。这是网关自己的模型
 *      发现端点，作用域天然就是这个密钥所属分组（`GatewayHandler.Models` 按
 *      `apiKey.Group` 过滤），所以换一个密钥模型列表就跟着变，不需要额外传
 *      group_id。同一份响应里还带着 xAI 风格的思考强度元数据
 *      （`supportsReasoningEffort` / `reasoningEfforts`，见
 *      `backend/internal/handler/gateway_handler.go` 的 `grokModelListItem`），
 *      正好拿来收窄档位。
 *   2. 分组平台 —— 用 `api/keys.ts` 的 `list()` 拿到密钥自带的 `group.platform`，
 *      交给 `detectRewriteProtocol` 判协议。
 *
 * 网络失败一律降级为空列表 / 默认协议，让界面回落到「全量七档 + 手输模型」之外的
 * 安全路径，而不是把整张卡片打崩。
 */
import { keysAPI } from '@/api/keys'
import type { ApiKey } from '@/types'
import {
  REWRITE_API_BASE_URL,
  type RewriteModelOption,
  type RewriteProtocol,
  type RewriteReasoningEffortOption,
  detectRewriteProtocol,
} from './rewriteConfig'

/** 模型发现路径。与生图密钥校验用的是同一个端点。 */
export const REWRITE_MODELS_PATH = '/v1/models'

/** 拼出站内网关的绝对地址，ui 上只展示、不提供编辑。 */
export function rewriteGatewayUrl(path: string): string {
  const suffix = path.startsWith('/') ? path : `/${path}`
  return `${REWRITE_API_BASE_URL}${suffix}`
}

/**
 * 图片/视频生成模型不能拿来做文本改写。名字里带这些标记的直接从候选里剔掉，
 * 避免用户选中一个在 chat 端点上必定 400 的模型。识别不出的模型一律保留——
 * 漏报比误杀安全。
 */
const NON_TEXT_MODEL_MARKER = /image|dall-?e|flux|midjourney|stable-diffusion|sd[-_]?xl|video|veo|sora|tts|whisper|embed/i

export function isTextRewriteModel(id: string): boolean {
  return !NON_TEXT_MODEL_MARKER.test(id)
}

interface RawModelEntry {
  id?: unknown
  model?: unknown
  supportsReasoningEffort?: unknown
  supports_reasoning_effort?: unknown
  reasoningEffort?: unknown
  reasoningEfforts?: unknown
}

/**
 * 解析 `/v1/models` 响应。
 *
 * 后端返回 camelCase 的 `supportsReasoningEffort` / `reasoningEfforts`；顺带容忍
 * 兼容网关常见的 snake_case 变体。任何形状异常都按「没有能力元数据」处理，
 * 于是 `effortOptionsForModel` 会给全量七档。
 */
export function parseRewriteModels(payload: unknown): RewriteModelOption[] {
  if (!payload || typeof payload !== 'object') return []
  const data = (payload as { data?: unknown }).data
  if (!Array.isArray(data)) return []

  const out: RewriteModelOption[] = []
  const seen = new Set<string>()

  for (const entry of data) {
    if (!entry || typeof entry !== 'object') continue
    const raw = entry as RawModelEntry
    const id = String(raw.id ?? raw.model ?? '').trim()
    if (!id || seen.has(id) || !isTextRewriteModel(id)) continue
    seen.add(id)

    const supportsRaw = raw.supportsReasoningEffort ?? raw.supports_reasoning_effort
    const option: RewriteModelOption = { id }
    if (typeof supportsRaw === 'boolean') option.supportsReasoningEffort = supportsRaw
    if (typeof raw.reasoningEffort === 'string') option.reasoningEffort = raw.reasoningEffort
    if (Array.isArray(raw.reasoningEfforts)) {
      const efforts: RewriteReasoningEffortOption[] = []
      for (const item of raw.reasoningEfforts) {
        if (!item || typeof item !== 'object') continue
        const record = item as { value?: unknown; label?: unknown; default?: unknown }
        const value = String(record.value ?? '').trim()
        if (!value) continue
        const parsed: RewriteReasoningEffortOption = { value }
        if (typeof record.label === 'string') parsed.label = record.label
        if (record.default === true) parsed.default = true
        efforts.push(parsed)
      }
      if (efforts.length > 0) option.reasoningEfforts = efforts
    }
    out.push(option)
  }

  out.sort((a, b) => a.id.localeCompare(b.id))
  return out
}

/**
 * 拉取某个密钥能用来自改写的模型。
 *
 * 失败（网络、401、非 JSON）返回空数组，调用方据此提示「拿不到模型列表，
 * 请检查密钥」，同时思考强度退回全量七档。
 */
export async function fetchRewriteModels(
  key: string,
  signal: AbortSignal,
): Promise<RewriteModelOption[]> {
  const cleaned = String(key ?? '').trim()
  if (!cleaned) return []
  try {
    const response = await fetch(rewriteGatewayUrl(REWRITE_MODELS_PATH), {
      method: 'GET',
      headers: { Authorization: `Bearer ${cleaned}` },
      signal,
    })
    if (!response.ok) return []
    return parseRewriteModels(await response.json().catch(() => null))
  } catch {
    return []
  }
}

/** 「校验令牌」的结果，附带这条密钥所属分组判出来的协议，供说明文案展示。 */
export interface RewriteTokenCheck {
  /** 密钥本身可用（模型列表非空）。 */
  ok: boolean
  /** 该密钥所属分组平台的协议判定结果；拿不到平台时为 null。 */
  protocol: RewriteProtocol | null
  modelCount: number
  /** 失败时的原始提示。 */
  message: string
}

/**
 * 校验一个改写令牌：真打一次 `GET /v1/models`。
 *
 * 与生图密钥的校验分开，因为两者判据不同——生图要求列表里出现 image 模型，
 * 改写要求出现任何可做文本的模型。协议判定顺带用同一把密钥的 group.platform
 * 完成，避免界面再单独请求一次密钥详情。
 */
export async function verifyRewriteToken(
  keyId: number | null,
  key: string,
  signal: AbortSignal,
): Promise<RewriteTokenCheck> {
  const cleaned = String(key ?? '').trim()
  if (!cleaned) {
    return { ok: false, protocol: null, modelCount: 0, message: '请先选择 AI 改写密钥' }
  }

  const [platform, models] = await Promise.all([
    keyId === null ? Promise.resolve('') : lookupKeyPlatform(keyId),
    fetchRewriteModels(cleaned, signal),
  ])

  const protocol = platform ? detectRewriteProtocol(platform, '') : null
  if (models.length === 0) {
    return {
      ok: false,
      protocol,
      modelCount: 0,
      message: '令牌不可用：没有返回任何可用于改写的模型',
    }
  }
  return { ok: true, protocol, modelCount: models.length, message: `令牌可用，共 ${models.length} 个可选模型` }
}

/** 从用户自己的密钥列表里读该密钥的分组平台；失败返回空串。 */
async function lookupKeyPlatform(keyId: number): Promise<string> {
  try {
    const key = await keysAPI.getById(keyId)
    return platformOfKey(key)
  } catch {
    return ''
  }
}

function platformOfKey(key: ApiKey | null | undefined): string {
  return String(key?.group?.platform ?? '').trim()
}

/** 一次请求拿到「我的全部密钥」，供「AI 改写密钥」下拉使用。 */
export async function listOwnKeys(signal?: AbortSignal): Promise<ApiKey[]> {
  try {
    const page = await keysAPI.list(1, 100, undefined, { signal })
    return page.items ?? []
  } catch {
    return []
  }
}

/** 在密钥列表里按 id 找明文与分组平台。 */
export function resolveKeyContext(
  keys: ApiKey[],
  keyId: number | null,
): { key: string; platform: string } {
  if (keyId === null) return { key: '', platform: '' }
  const found = keys.find((item) => item.id === keyId)
  if (!found) return { key: '', platform: '' }
  return { key: String(found.key ?? '').trim(), platform: platformOfKey(found) }
}
