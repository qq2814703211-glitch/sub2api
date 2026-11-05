import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { deleteMany } from '../keys'

const { del } = vi.hoisted(() => ({ del: vi.fn() }))
vi.mock('../client', () => ({ apiClient: { delete: del } }))

describe('API key bulk deletion', () => {
  beforeEach(() => {
    del.mockReset()
  })

  it('deletes each unique id once and reports per-key failures', async () => {
    const failure = new Error('Forbidden')
    del.mockImplementation((url: string) => url === '/keys/2'
      ? Promise.reject(failure)
      : Promise.resolve({ data: { message: 'ok' } }))

    const result = await deleteMany([1, 2, 1, 3])

    expect(del.mock.calls).toEqual([['/keys/1'], ['/keys/2'], ['/keys/3']])
    expect(result.succeeded.sort()).toEqual([1, 3])
    expect(result.failed).toEqual([2])
  })

  it('limits in-flight deletes to the pool size and continues after a failure', async () => {
    const settle: Array<() => void> = []
    let inFlight = 0
    let peak = 0
    del.mockImplementation(() => new Promise((resolve, reject) => {
      inFlight++
      peak = Math.max(peak, inFlight)
      const index = settle.length
      settle.push(() => {
        inFlight--
        if (index === 0) reject(new Error('network error'))
        else resolve({ data: { message: 'ok' } })
      })
    }))

    const pending = deleteMany([1, 2, 3, 4, 5, 6, 7])
    expect(del).toHaveBeenCalledTimes(4)
    settle.slice(0, 4).forEach((finish) => finish())
    await flushPromises()
    expect(del).toHaveBeenCalledTimes(7)
    settle.slice(4).forEach((finish) => finish())
    const result = await pending

    expect(peak).toBe(4)
    expect(result.succeeded).toEqual([2, 3, 4, 5, 6, 7])
    expect(result.failed).toEqual([1])
  })

  it('does not send requests when no keys are selected', async () => {
    expect(await deleteMany([])).toEqual({ succeeded: [], failed: [] })
    expect(del).not.toHaveBeenCalled()
  })
})
