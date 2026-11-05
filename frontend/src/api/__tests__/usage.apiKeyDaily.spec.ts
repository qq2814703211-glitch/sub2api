import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get } }))

import { getMyApiKeyDailyUsage } from '@/api/usage'

describe('getMyApiKeyDailyUsage', () => {
  beforeEach(() => {
    get.mockReset()
  })

  it('sends the selected day window and IANA timezone to the existing daily endpoint', async () => {
    const payload = {
      items: [],
      days: 7,
      start_date: '2026-10-30',
      end_date: '2026-11-05'
    }
    get.mockResolvedValue({ data: payload })
    const controller = new AbortController()

    const result = await getMyApiKeyDailyUsage(12, 7, {
      timezone: 'Asia/Shanghai',
      signal: controller.signal
    })

    expect(get).toHaveBeenCalledWith('/user/api-keys/12/usage/daily', {
      params: { days: 7, timezone: 'Asia/Shanghai' },
      signal: controller.signal
    })
    expect(result).toBe(payload)
  })

  it('keeps the 30-day default and omits timezone when the caller does not supply one', async () => {
    get.mockResolvedValue({
      data: { items: [], days: 30, start_date: '2026-10-07', end_date: '2026-11-05' }
    })

    await getMyApiKeyDailyUsage(3)

    expect(get).toHaveBeenCalledWith('/user/api-keys/3/usage/daily', {
      params: { days: 30 },
      signal: undefined
    })
  })
})
