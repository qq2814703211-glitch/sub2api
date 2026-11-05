import { apiClient } from './client'

export interface KeyQueueItem {
  id: number
  current_concurrency: number
  current_waiting: number
}

export async function getKeyQueue(ids: number[], signal: AbortSignal): Promise<KeyQueueItem[]> {
  const { data } = await apiClient.get<{ items: KeyQueueItem[] }>('/keys/concurrency', {
    params: { ids: ids.join(',') },
    signal
  })
  return data.items
}
