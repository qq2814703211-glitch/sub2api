import { ref } from 'vue'
import { defineStore } from 'pinia'
import { ticketsAPI } from '@/api/tickets'

/**
 * Unread badge shared by the sidebar and the ticket workspace.
 *
 * Polling pauses while the tab is hidden and the last request is aborted when the
 * sidebar disappears, so a stale response can never resurrect a badge count.
 */
export const useTicketNotificationsStore = defineStore('ticketNotifications', () => {
  const unread = ref(0)
  const loading = ref(false)
  let timer: ReturnType<typeof setInterval> | undefined
  let controller: AbortController | null = null
  let subscribers = 0
  const api = ticketsAPI(false)

  async function refresh() {
    if (document.hidden) return
    controller?.abort()
    const current = new AbortController()
    controller = current
    loading.value = true
    try {
      const stats = await api.stats(current.signal)
      if (!current.signal.aborted) unread.value = stats.unread || 0
    } catch {
      /* The badge is best-effort; the workspace still shows the authoritative count. */
    } finally {
      if (controller === current) loading.value = false
    }
  }

  function onVisibility() {
    if (!document.hidden) void refresh()
  }

  function start() {
    subscribers += 1
    if (subscribers > 1) return
    void refresh()
    timer = setInterval(() => void refresh(), 30000)
    document.addEventListener('visibilitychange', onVisibility)
  }

  function stop() {
    subscribers = Math.max(0, subscribers - 1)
    if (subscribers > 0) return
    clearInterval(timer)
    timer = undefined
    controller?.abort()
    controller = null
    loading.value = false
    document.removeEventListener('visibilitychange', onVisibility)
  }

  function reset() {
    unread.value = 0
  }

  return { unread, loading, refresh, start, stop, reset }
})
