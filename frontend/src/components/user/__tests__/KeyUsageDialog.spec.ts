import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import KeyUsageDialog from '../KeyUsageDialog.vue'
import type { ApiKeyDailyUsagePoint, ApiKeyDailyUsageResponse } from '@/api/usage'
import type { ApiKey } from '@/types'

const { getDailyUsage } = vi.hoisted(() => ({ getDailyUsage: vi.fn() }))

vi.mock('@/api', () => ({
  usageAPI: { getMyApiKeyDailyUsage: getDailyUsage }
}))

const messages: Record<string, string> = {
  'keys.usageDialog.title': 'Key Usage Details',
  'keys.usageDialog.action': 'Details',
  'keys.usageDialog.period': 'Reporting period',
  'keys.usageDialog.presetToday': 'Today',
  'keys.usageDialog.preset7d': 'Last 7 days',
  'keys.usageDialog.preset30d': 'Last 30 days',
  'keys.usageDialog.range': '{start} to {end} (timezone {timezone})',
  'keys.usageDialog.today': 'Today',
  'keys.usageDialog.noTodayRecord': 'No usage recorded today',
  'keys.usageDialog.rangeTotal': 'Selected range',
  'keys.usageDialog.requests': 'requests',
  'keys.usageDialog.tokens': 'tokens',
  'keys.usageDialog.date': 'Date',
  'keys.usageDialog.actualCost': 'Actual charge',
  'keys.usageDialog.empty': 'No usage recorded in this period',
  'keys.usageDialog.failed': 'Failed to load usage',
  'keys.usageDialog.retry': 'Retry',
  'keys.usageDialog.close': 'Close'
}

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => {
      let text = messages[key] ?? key
      for (const [name, value] of Object.entries(params ?? {})) {
        text = text.split(`{${name}}`).join(String(value))
      }
      return text
    }
  })
}))

const BaseDialogStub = {
  props: ['show', 'title', 'width'],
  emits: ['close'],
  template: '<div v-if="show" data-test="base-dialog"><slot /><slot name="footer" /></div>'
}

enableAutoUnmount(afterEach)

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

function makeKey(id: number, name = `key-${id}`): ApiKey {
  return {
    id,
    user_id: 1,
    key: `sk-test-${id}-0123456789abcdef`,
    name,
    group_id: null,
    status: 'active',
    ip_whitelist: [],
    ip_blacklist: [],
    last_used_at: null,
    last_used_ip: null,
    quota: 0,
    quota_used: 0,
    expires_at: null,
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-01T00:00:00Z',
    current_concurrency: 0,
    rate_limit_5h: 0,
    rate_limit_1d: 0,
    rate_limit_7d: 0,
    usage_5h: 0,
    usage_1d: 0,
    usage_7d: 0,
    window_5h_start: null,
    window_1d_start: null,
    window_7d_start: null,
    reset_5h_at: null,
    reset_1d_at: null,
    reset_7d_at: null
  }
}

function point(date: string, overrides: Partial<ApiKeyDailyUsagePoint> = {}): ApiKeyDailyUsagePoint {
  return {
    date,
    requests: 1,
    input_tokens: 10,
    output_tokens: 5,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    total_tokens: 15,
    cost: 9.99,
    actual_cost: 0.1,
    ...overrides
  }
}

function response(
  items: ApiKeyDailyUsagePoint[],
  range: { days?: number; start?: string; end?: string } = {}
): ApiKeyDailyUsageResponse {
  return {
    items,
    days: range.days ?? 1,
    start_date: range.start ?? '2026-11-05',
    end_date: range.end ?? '2026-11-05'
  }
}

async function openDialog(apiKey: ApiKey = makeKey(7, 'primary-key')) {
  const wrapper = mount(KeyUsageDialog, {
    props: { show: false, apiKey: null },
    global: { stubs: { BaseDialog: BaseDialogStub } }
  })
  await wrapper.setProps({ show: true, apiKey })
  return wrapper
}

describe('KeyUsageDialog', () => {
  beforeEach(() => {
    getDailyUsage.mockReset()
    vi.spyOn(Intl.DateTimeFormat.prototype, 'resolvedOptions').mockReturnValue({
      locale: 'en-US',
      calendar: 'gregory',
      numberingSystem: 'latn',
      timeZone: 'America/New_York'
    } as Intl.ResolvedDateTimeFormatOptions)
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('requests the opened key for today with the browser timezone and shows actual cost', async () => {
    const pending = deferred<ApiKeyDailyUsageResponse>()
    getDailyUsage.mockReturnValueOnce(pending.promise)

    const wrapper = await openDialog()

    expect(getDailyUsage).toHaveBeenCalledTimes(1)
    const [apiKeyId, days, options] = getDailyUsage.mock.calls[0]
    expect(apiKeyId).toBe(7)
    expect(days).toBe(1)
    expect(options.timezone).toBe('America/New_York')
    expect(options.signal).toBeInstanceOf(AbortSignal)
    expect(wrapper.find('[data-testid="key-usage-loading"]').exists()).toBe(true)

    pending.resolve(
      response([point('2026-11-05', { requests: 4, total_tokens: 42, actual_cost: 0.25, cost: 88.8 })])
    )
    await flushPromises()

    expect(wrapper.get('[data-testid="key-usage-key-name"]').text()).toBe('primary-key')
    const range = wrapper.get('[data-testid="key-usage-range"]').text()
    expect(range).toContain('2026-11-05 to 2026-11-05')
    expect(range).toContain('America/New_York')
    expect(wrapper.get('[data-testid="key-usage-today-cost"]').text()).toBe('$0.2500')
    expect(wrapper.get('[data-testid="key-usage-range-cost"]').text()).toBe('$0.2500')
    expect(wrapper.findAll('[data-testid="key-usage-row"]')).toHaveLength(1)
    expect(wrapper.find('[data-testid="key-usage-loading"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('$88.8000')
  })

  it('switches to 30 days, sums actual cost and tokens, and sorts days newest first', async () => {
    getDailyUsage
      .mockResolvedValueOnce(response([point('2026-11-05', { actual_cost: 1, requests: 1, total_tokens: 10 })]))
      .mockResolvedValueOnce(
        response(
          [
            point('2026-10-07', { actual_cost: 0.5, requests: 2, total_tokens: 20 }),
            point('2026-11-05', { actual_cost: 0.25, requests: 1, total_tokens: 5 })
          ],
          { days: 30, start: '2026-10-07', end: '2026-11-05' }
        )
      )

    const wrapper = await openDialog()
    await flushPromises()
    await wrapper.get('[data-testid="key-usage-preset-30d"]').trigger('click')
    await flushPromises()

    expect(getDailyUsage).toHaveBeenLastCalledWith(
      7,
      30,
      expect.objectContaining({ timezone: 'America/New_York' })
    )
    expect(wrapper.get('[data-testid="key-usage-range-cost"]').text()).toBe('$0.7500')
    expect(wrapper.get('[data-testid="key-usage-today-cost"]').text()).toBe('$0.2500')
    expect(wrapper.text()).toContain('3 requests')
    expect(wrapper.text()).toContain('25 tokens')
    const dates = wrapper.findAll('[data-testid="key-usage-row"]').map((row) => row.get('td').text())
    expect(dates).toEqual(['2026-11-05', '2026-10-07'])
  })

  it('requests the 7-day window when the 7-day preset is selected', async () => {
    getDailyUsage.mockResolvedValue(response([]))
    const wrapper = await openDialog()
    await flushPromises()

    await wrapper.get('[data-testid="key-usage-preset-7d"]').trigger('click')
    await flushPromises()

    expect(getDailyUsage).toHaveBeenLastCalledWith(
      7,
      7,
      expect.objectContaining({ timezone: 'America/New_York', signal: expect.any(AbortSignal) })
    )
  })

  it('distinguishes zero usage from an empty period', async () => {
    getDailyUsage.mockResolvedValueOnce(
      response([point('2026-11-05', { requests: 0, total_tokens: 0, actual_cost: 0, cost: 0 })])
    )
    let wrapper = await openDialog()
    await flushPromises()

    expect(wrapper.findAll('[data-testid="key-usage-row"]')).toHaveLength(1)
    expect(wrapper.find('[data-testid="key-usage-empty"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="key-usage-today-cost"]').text()).toBe('$0.0000')
    expect(wrapper.find('[data-testid="key-usage-today-empty"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="key-usage-range-cost"]').text()).toBe('$0.0000')

    wrapper.unmount()
    getDailyUsage.mockResolvedValueOnce(response([]))
    wrapper = await openDialog()
    await flushPromises()

    expect(wrapper.find('[data-testid="key-usage-empty"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('No usage recorded in this period')
    expect(wrapper.get('[data-testid="key-usage-today-cost"]').text()).toBe('—')
    expect(wrapper.get('[data-testid="key-usage-today-empty"]').text()).toContain('No usage recorded today')
  })

  it('shows a dash and a no-record hint when the window has rows but not for today', async () => {
    getDailyUsage.mockResolvedValueOnce(
      response(
        [point('2026-11-02', { actual_cost: 0.4, requests: 2, total_tokens: 30 })],
        { days: 7, start: '2026-10-30', end: '2026-11-05' }
      )
    )
    const wrapper = await openDialog()
    await flushPromises()

    expect(wrapper.get('[data-testid="key-usage-today-cost"]').text()).toBe('—')
    expect(wrapper.get('[data-testid="key-usage-today-empty"]').text()).toContain('No usage recorded today')
    expect(wrapper.get('[data-testid="key-usage-today-cost"]').text()).not.toContain('$0.0000')
    expect(wrapper.get('[data-testid="key-usage-range-cost"]').text()).toBe('$0.4000')
    expect(wrapper.findAll('[data-testid="key-usage-row"]')).toHaveLength(1)
    expect(wrapper.find('[data-testid="key-usage-empty"]').exists()).toBe(false)
  })

  it('shows an error and reloads on retry', async () => {
    getDailyUsage
      .mockRejectedValueOnce(new Error('network down'))
      .mockResolvedValueOnce(response([point('2026-11-05', { actual_cost: 0.3 })]))

    const wrapper = await openDialog()
    await flushPromises()

    expect(wrapper.find('[data-testid="key-usage-error"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('Failed to load usage')

    await wrapper.get('[data-testid="key-usage-error"] button').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="key-usage-error"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-testid="key-usage-row"]')).toHaveLength(1)
    expect(wrapper.get('[data-testid="key-usage-range-cost"]').text()).toBe('$0.3000')
  })

  it('aborts the previous preset request and ignores its late response', async () => {
    const slowToday = deferred<ApiKeyDailyUsageResponse>()
    const fastRange = deferred<ApiKeyDailyUsageResponse>()
    getDailyUsage.mockReturnValueOnce(slowToday.promise).mockReturnValueOnce(fastRange.promise)

    const wrapper = await openDialog()
    await wrapper.get('[data-testid="key-usage-preset-30d"]').trigger('click')

    const firstSignal = getDailyUsage.mock.calls[0][2].signal as AbortSignal
    expect(firstSignal.aborted).toBe(true)

    fastRange.resolve(
      response([point('2026-11-05', { actual_cost: 0.5 })], {
        days: 30,
        start: '2026-10-07',
        end: '2026-11-05'
      })
    )
    await flushPromises()

    slowToday.resolve(response([point('2026-11-05', { actual_cost: 9.99 })]))
    await flushPromises()

    expect(wrapper.get('[data-testid="key-usage-range-cost"]').text()).toBe('$0.5000')
    expect(wrapper.text()).not.toContain('$9.9900')
    expect(wrapper.find('[data-testid="key-usage-loading"]').exists()).toBe(false)
  })

  it('cancels on close and ignores the closed request when reopened', async () => {
    const first = deferred<ApiKeyDailyUsageResponse>()
    const second = deferred<ApiKeyDailyUsageResponse>()
    getDailyUsage.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)

    const wrapper = await openDialog()
    await wrapper.setProps({ show: false })

    expect((getDailyUsage.mock.calls[0][2].signal as AbortSignal).aborted).toBe(true)

    await wrapper.setProps({ show: true })
    second.resolve(response([point('2026-11-05', { actual_cost: 0.2 })]))
    await flushPromises()

    first.resolve(response([point('2026-11-05', { actual_cost: 8 })]))
    await flushPromises()

    expect(wrapper.get('[data-testid="key-usage-range-cost"]').text()).toBe('$0.2000')
    expect(wrapper.text()).not.toContain('$8.0000')
  })

  it('cancels the previous key and keeps the newly selected key response', async () => {
    const firstKey = deferred<ApiKeyDailyUsageResponse>()
    const secondKey = deferred<ApiKeyDailyUsageResponse>()
    getDailyUsage.mockReturnValueOnce(firstKey.promise).mockReturnValueOnce(secondKey.promise)

    const wrapper = await openDialog(makeKey(1, 'first-key'))
    await wrapper.setProps({ apiKey: makeKey(2, 'second-key') })

    expect(getDailyUsage).toHaveBeenLastCalledWith(2, 1, expect.objectContaining({ timezone: 'America/New_York' }))
    expect((getDailyUsage.mock.calls[0][2].signal as AbortSignal).aborted).toBe(true)

    secondKey.resolve(response([point('2026-11-05', { actual_cost: 0.3 })]))
    await flushPromises()

    firstKey.resolve(response([point('2026-11-05', { actual_cost: 5 })]))
    await flushPromises()

    expect(wrapper.get('[data-testid="key-usage-key-name"]').text()).toBe('second-key')
    expect(wrapper.get('[data-testid="key-usage-range-cost"]').text()).toBe('$0.3000')
    expect(wrapper.text()).not.toContain('$5.0000')
  })
})
