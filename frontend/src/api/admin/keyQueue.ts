import { apiClient } from '../client'
import type { KeyQueueItem } from '../keyQueue'

export async function getAdminUserKeyQueue(userId: number, signal: AbortSignal): Promise<KeyQueueItem[]> {
  const { data } = await apiClient.get<{ items: KeyQueueItem[] }>(
    `/admin/users/${userId}/api-keys/concurrency`,
    { signal }
  )
  return data.items
}
