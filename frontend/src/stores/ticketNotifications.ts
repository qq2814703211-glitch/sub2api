import { ref } from 'vue'
import { defineStore } from 'pinia'
import { ticketsAPI } from '@/api/tickets'

/**
 * Unread badges for the two ticket inboxes.
 *
 * `userUnread` is the signed-in account's own inbox (`GET /tickets/stats`).
 * `adminUnread` is the shared support queue (`GET /admin/tickets/stats`) and is
 * only polled after the caller proves the admin role. They are deliberately
 * separate counters: an administrator who never files tickets has user unread 0
 * while the queue may still be full, and the reverse is equally possible.
 *
 * Polling pauses while the tab is hidden and the last request is aborted when the
 * sidebar disappears, so a stale response can never resurrect a badge count.
 */
export const useTicketNotificationsStore = defineStore('ticketNotifications', () => {
  const userUnread = ref(0)
  const adminUnread = ref(0)
  const loading = ref(false)
  let timer: ReturnType<typeof setInterval> | undefined
  let controller: AbortController | null = null
  let subscribers = 0
  let adminEnabled = false
  const userAPI = ticketsAPI(false)
  const adminAPI = ticketsAPI(true)

  async function loadUnread(api: ReturnType<typeof ticketsAPI>, target: typeof userUnread, signal: AbortSignal) {
    try {
      const stats = await api.stats(signal)
      if (!signal.aborted) target.value = stats.unread || 0
    } catch {
      /* The badge is best-effort; the workspace still shows the authoritative count. */
    }
  }

  async function refresh() {
    if (document.hidden) return
    controller?.abort()
    const current = new AbortController()
    controller = current
    loading.value = true
    try {
      const requests = [loadUnread(userAPI, userUnread, current.signal)]
      if (adminEnabled) requests.push(loadUnread(adminAPI, adminUnread, current.signal))
      await Promise.all(requests)
    } finally {
      if (controller === current) loading.value = false
    }
  }

  /**
   * Enables the admin queue counter. Callers must pass the real signed-in role;
   * the backend additionally rejects the admin endpoint for non-admin tokens.
   */
  function setAdmin(enabled: boolean) {
    adminEnabled = enabled
    if (!enabled) adminUnread.value = 0
  }

  function onVisibility() {
    if (!document.hidden) void refresh()
  }

  function start(options: { admin?: boolean } = {}) {
    setAdmin(Boolean(options.admin))
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
    userUnread.value = 0
    adminUnread.value = 0
  }

  return { userUnread, adminUnread, loading, refresh, setAdmin, start, stop, reset }
})
