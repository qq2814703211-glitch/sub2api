import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useTicketNotificationsStore } from '../ticketNotifications'

const mocks = vi.hoisted(() => ({
  userStats: vi.fn(),
  adminStats: vi.fn()
}))

vi.mock('@/api/tickets', () => ({
  ticketsAPI: (admin: boolean) => ({ stats: admin ? mocks.adminStats : mocks.userStats })
}))

beforeEach(() => {
  setActivePinia(createPinia())
  vi.resetAllMocks()
  Object.defineProperty(document, 'hidden', { configurable: true, value: false })
})

describe('ticket notification badges', () => {
  it('keeps the user inbox and the admin queue on independent counters', async () => {
    mocks.userStats.mockResolvedValue({ total: 4, unread: 3 })
    mocks.adminStats.mockResolvedValue({ total: 20, unread: 7 })
    const store = useTicketNotificationsStore()
    store.start({ admin: true })

    await vi.waitFor(() => expect(store.adminUnread).toBe(7))

    expect(store.userUnread).toBe(3)
    expect(store.adminUnread).toBe(7)
    expect(mocks.userStats).toHaveBeenCalledTimes(1)
    expect(mocks.adminStats).toHaveBeenCalledTimes(1)
    store.stop()
  })

  it('never requests admin stats for a non-admin sidebar caller', async () => {
    mocks.userStats.mockResolvedValue({ total: 4, unread: 3 })
    const store = useTicketNotificationsStore()
    store.start({ admin: false })

    await vi.waitFor(() => expect(store.userUnread).toBe(3))

    expect(mocks.adminStats).not.toHaveBeenCalled()
    expect(store.adminUnread).toBe(0)
    store.stop()
  })

  it('drops the admin count when the admin role goes away', async () => {
    mocks.userStats.mockResolvedValue({ total: 4, unread: 3 })
    mocks.adminStats.mockResolvedValue({ total: 20, unread: 7 })
    const store = useTicketNotificationsStore()
    await store.refresh()
    await store.refresh()
    expect(store.adminUnread).toBe(0)

    store.setAdmin(true)
    await store.refresh()
    expect(store.adminUnread).toBe(7)

    store.setAdmin(false)
    expect(store.adminUnread).toBe(0)
    expect(store.userUnread).toBe(3)
  })

  it('skips both requests while the document is hidden', async () => {
    Object.defineProperty(document, 'hidden', { configurable: true, value: true })
    const store = useTicketNotificationsStore()
    store.setAdmin(true)
    await store.refresh()
    expect(mocks.userStats).not.toHaveBeenCalled()
    expect(mocks.adminStats).not.toHaveBeenCalled()
    expect(store.userUnread).toBe(0)
    expect(store.adminUnread).toBe(0)
  })

  it('polls both counters while subscribed and stops cleanly', async () => {
    vi.useFakeTimers()
    mocks.userStats.mockResolvedValue({ total: 1, unread: 1 })
    mocks.adminStats.mockResolvedValue({ total: 2, unread: 2 })
    const store = useTicketNotificationsStore()
    store.start({ admin: true })
    await vi.advanceTimersByTimeAsync(0)
    expect(store.userUnread).toBe(1)
    expect(store.adminUnread).toBe(2)
    await vi.advanceTimersByTimeAsync(30000)
    expect(mocks.userStats.mock.calls.length).toBeGreaterThanOrEqual(2)
    expect(mocks.adminStats.mock.calls.length).toBeGreaterThanOrEqual(2)
    store.stop()
    const calls = mocks.userStats.mock.calls.length
    await vi.advanceTimersByTimeAsync(60000)
    expect(mocks.userStats.mock.calls.length).toBe(calls)
    expect(mocks.adminStats.mock.calls.length).toBe(calls)
    vi.useRealTimers()
  })

  it('keeps the previous count for the failed inbox without starving the other', async () => {
    mocks.userStats.mockResolvedValueOnce({ total: 1, unread: 2 }).mockResolvedValueOnce({ total: 1, unread: 4 })
    mocks.adminStats.mockResolvedValueOnce({ total: 9, unread: 5 }).mockRejectedValueOnce(new Error('offline'))
    const store = useTicketNotificationsStore()
    store.setAdmin(true)
    await store.refresh()
    await store.refresh()
    expect(store.userUnread).toBe(4)
    expect(store.adminUnread).toBe(5)
  })

  it('clears both counters on reset', async () => {
    mocks.userStats.mockResolvedValue({ total: 1, unread: 2 })
    mocks.adminStats.mockResolvedValue({ total: 9, unread: 5 })
    const store = useTicketNotificationsStore()
    store.setAdmin(true)
    await store.refresh()
    store.reset()
    expect(store.userUnread).toBe(0)
    expect(store.adminUnread).toBe(0)
  })
})
