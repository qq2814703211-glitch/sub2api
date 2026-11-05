import { nextTick } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import VideoBillingJobsCard from '../VideoBillingJobsCard.vue'
import type { VideoBillingJob } from '@/api/admin/videoBilling'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const { api } = vi.hoisted(() => ({ api: { list: vi.fn(), worker: vi.fn(), setWorker: vi.fn(), retry: vi.fn() } }))
vi.mock('@/api/admin/videoBilling', () => ({ videoBillingAPI: api }))
enableAutoUnmount(afterEach)

function job(state = 'reconciliation'): VideoBillingJob {
  return { id: 'job-1', task_id: 'task-1', user_id: 1, api_key_id: 2, account_id: 3, group_id: 4,
    provider: 'seedance', model: 'seedance', state, provider_status: 'succeeded', last_error: 'completion_usage_missing',
    attempts: 2, actual_cost: null, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', available_at: '2026-01-01T00:00:00Z' }
}
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(res => { resolve = res })
  return { promise, resolve }
}

describe('video settlement operations', () => {
  beforeEach(() => { vi.resetAllMocks(); api.list.mockResolvedValue([]); api.worker.mockResolvedValue(true) })
  it('shows loading and empty states', async () => {
    const pending = deferred<VideoBillingJob[]>()
    api.list.mockReturnValueOnce(pending.promise)
    const wrapper = mount(VideoBillingJobsCard)
    await nextTick()
    expect(wrapper.get('[role="status"]').text()).toContain('loading')
    pending.resolve([])
    await flushPromises()
    expect(wrapper.text()).toContain('videoBilling.empty')
  })
  it('shows load errors without presenting a successful empty result', async () => {
    api.list.mockRejectedValueOnce(new Error('offline'))
    const wrapper = mount(VideoBillingJobsCard)
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('videoBilling.loadError')
    expect(wrapper.text()).not.toContain('videoBilling.empty')
  })
  it('resumes an existing task and refreshes its state', async () => {
    api.list.mockResolvedValueOnce([job()]).mockResolvedValueOnce([job('done')])
    const wrapper = mount(VideoBillingJobsCard)
    await flushPromises()
    await wrapper.get('tbody button').trigger('click')
    await flushPromises()
    expect(api.retry).toHaveBeenCalledWith('job-1', expect.any(AbortSignal))
    expect(wrapper.text()).toContain('videoBilling.states.done')
    expect(wrapper.find('tbody button').exists()).toBe(false)
  })
  it('never offers retry for unknown creates or terminal failures', async () => {
    api.list.mockResolvedValue([{ ...job(), task_id: '' }, { ...job('failed'), id: 'job-2' }, { ...job('expired'), id: 'job-3' }])
    const wrapper = mount(VideoBillingJobsCard)
    await flushPromises()
    expect(wrapper.findAll('tbody button')).toHaveLength(0)
  })
  it('persists worker disable without changing task history', async () => {
    api.list.mockResolvedValue([job()])
    const wrapper = mount(VideoBillingJobsCard)
    await flushPromises()
    await wrapper.get('input[type="checkbox"]').setValue(false)
    await flushPromises()
    expect(api.setWorker).toHaveBeenCalledWith(false, expect.any(AbortSignal))
    expect((wrapper.get('input').element as HTMLInputElement).checked).toBe(false)
    expect(wrapper.text()).toContain('task-1')
  })
  it('aborts old loads and ignores late results after filtering', async () => {
    const old = deferred<VideoBillingJob[]>()
    api.list.mockReturnValueOnce(old.promise).mockResolvedValueOnce([job('done')])
    const wrapper = mount(VideoBillingJobsCard)
    const signal = api.list.mock.calls[0][2] as AbortSignal
    await wrapper.get('select').setValue('done')
    await flushPromises()
    expect(signal.aborted).toBe(true)
    old.resolve([{ ...job(), task_id: 'stale-task' }])
    await flushPromises()
    expect(wrapper.text()).not.toContain('stale-task')
    expect(wrapper.text()).toContain('videoBilling.states.done')
  })
  it('cancels requests on unmount', async () => {
    const pending = deferred<VideoBillingJob[]>()
    api.list.mockReturnValueOnce(pending.promise)
    const wrapper = mount(VideoBillingJobsCard)
    const signal = api.list.mock.calls[0][2] as AbortSignal
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
    pending.resolve([])
    await flushPromises()
  })
})
