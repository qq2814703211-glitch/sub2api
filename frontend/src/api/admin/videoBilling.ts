import { apiClient } from '../client'

export interface VideoBillingJob {
  id: string
  task_id: string
  user_id: number
  api_key_id: number
  account_id: number
  group_id: number
  provider: string
  model: string
  state: string
  provider_status: string
  last_error: string
  attempts: number
  actual_cost: number | null
  created_at: string
  updated_at: string
  available_at: string
}

export const videoBillingAPI = {
  async list(state: string, before: string, signal?: AbortSignal) {
    const { data } = await apiClient.get<{ items: VideoBillingJob[] }>('/admin/ops/video-billing/jobs', {
      params: { state, before, limit: 50 }, signal
    })
    return data.items
  },
  async worker(signal?: AbortSignal) {
    const { data } = await apiClient.get<{ enabled: boolean }>('/admin/ops/video-billing/worker', { signal })
    return data.enabled
  },
  async setWorker(enabled: boolean, signal?: AbortSignal) {
    await apiClient.put('/admin/ops/video-billing/worker', { enabled }, { signal })
  },
  async retry(id: string, signal?: AbortSignal) {
    await apiClient.post(`/admin/ops/video-billing/jobs/${encodeURIComponent(id)}/retry`, {}, { signal })
  }
}
