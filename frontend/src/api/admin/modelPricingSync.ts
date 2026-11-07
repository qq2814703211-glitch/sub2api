/**
 * models.dev 官方参考价同步的变更史 API。
 *
 * 同步任务在后端按 pricing.hash_check_interval_minutes 自动跑，只补/改
 * 「价表里还没有手动定价」的模型（ADR 0010）。这里只读两个接口：
 * 变更史列表与同步状态。
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

export default {
  listHistory,
  getStatus
}
