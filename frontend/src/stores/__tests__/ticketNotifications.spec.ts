import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useTicketNotificationsStore } from '../ticketNotifications'

const mocks = vi.hoisted(() => ({ stats: vi.fn() }))
vi.mock('@/api/tickets', () => ({ ticketsAPI: () => ({ stats: mocks.stats }) }))

beforeEach(() => {
  setActivePinia(createPinia())
  vi.resetAllMocks()
  Object.defineProperty(document, 'hidden', { configurable: true, value: false })
})

describe('ticket notification badge', () => {
  it('stores the unread count from the stats endpoint', async () => {
    mocks.stats.mockResolvedValue({ total: 4, unread: 3 })
    const store = useTicketNotificationsStore()
    await store.refresh()
    expect(store.unread).toBe(3)
  })

  it('skips the request while the document is hidden', async () => {
    Object.defineProperty(document, 'hidden', { configurable: true, value: true })
    const store = useTicketNotificationsStore()
    await store.refresh()
    expect(mocks.stats).not.toHaveBeenCalled()
    expect(store.unread).toBe(0)
  })

  it('polls while subscribed and stops cleanly', async () => {
    vi.useFakeTimers()
    mocks.stats.mockResolvedValue({ total: 1, unread: 1 })
    const store = useTicketNotificationsStore()
    store.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(store.unread).toBe(1)
    await vi.advanceTimersByTimeAsync(30000)
    expect(mocks.stats.mock.calls.length).toBeGreaterThanOrEqual(2)
    store.stop()
    const calls = mocks.stats.mock.calls.length
    await vi.advanceTimersByTimeAsync(60000)
    expect(mocks.stats.mock.calls.length).toBe(calls)
    vi.useRealTimers()
  })

  it('keeps the previous count when a poll fails', async () => {
    mocks.stats.mockResolvedValueOnce({ total: 1, unread: 2 }).mockRejectedValueOnce(new Error('offline'))
    const store = useTicketNotificationsStore()
    await store.refresh()
    await store.refresh()
    expect(store.unread).toBe(2)
  })
})
