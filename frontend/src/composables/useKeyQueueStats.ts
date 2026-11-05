import { onBeforeUnmount, watch, type Ref } from 'vue'
import type { ApiKey } from '@/types'
import { getKeyQueue, type KeyQueueItem } from '@/api/keyQueue'
import { getAdminUserKeyQueue } from '@/api/admin/keyQueue'

// Poll only while a visible page contains configured limits; no idle worker.
export function useKeyQueueStats(
  keys: Ref<ApiKey[]>,
  enabled: () => boolean = () => true,
  userId?: () => number | undefined
) {
  let epoch = 0
  let timer: ReturnType<typeof setTimeout> | undefined
  let controller: AbortController | undefined

  function stop() {
    epoch++
    clearTimeout(timer)
    controller?.abort()
  }

  watch(
    () => [enabled(), userId?.(), keys.value.map(key => `${key.id}:${key.concurrency_limit ?? 0}`).join(',')],
    () => {
      stop()
      if (!enabled() || !keys.value.some(key => (key.concurrency_limit ?? 0) > 0)) return
      const current = epoch
      const ids = keys.value.map(key => key.id)
      const owner = userId?.()

      async function refresh() {
        if (current !== epoch || !enabled()) return
        if (document.hidden) {
          timer = setTimeout(refresh, 3000)
          return
        }
        controller = new AbortController()
        try {
          const items: KeyQueueItem[] = []
          if (owner !== undefined) {
            items.push(...await getAdminUserKeyQueue(owner, controller.signal))
          } else {
            for (let i = 0; i < ids.length; i += 100) {
              items.push(...await getKeyQueue(ids.slice(i, i + 100), controller.signal))
            }
          }
          if (current !== epoch) return
          const counts = new Map(items.map(item => [item.id, item]))
          for (const key of keys.value) {
            const item = counts.get(key.id)
            key.queue_stats_available = !!item
            key.waiting_count = item?.current_waiting ?? null
            if (item) key.current_concurrency = item.current_concurrency
          }
        } catch {
          if (current === epoch) {
            for (const key of keys.value) {
              key.queue_stats_available = false
              key.waiting_count = null
            }
          }
        } finally {
          if (current === epoch) timer = setTimeout(refresh, 3000)
        }
      }
      timer = setTimeout(refresh, 3000)
    },
    { immediate: true }
  )
  onBeforeUnmount(stop)
}
