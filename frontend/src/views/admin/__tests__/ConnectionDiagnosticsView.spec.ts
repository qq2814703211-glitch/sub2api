import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import View from '../ConnectionDiagnosticsView.vue'
import en from '@/i18n/locales/en'
import zh from '@/i18n/locales/zh'

const api = vi.hoisted(() => ({ list: vi.fn(), settings: vi.fn(), setEnabled: vi.fn() }))
vi.mock('@/api/admin/connectionDiagnostics', () => ({ default: api }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
const stats = { dropped: 3, queue_depth: 0, write_failures: 1, retention_failures: 0, worker_running: false }
const settings = { enabled: false, retention_days: 30, body_capture: false, stats }
const row = { trace_id: 'trace-1', request_id: 'request-1', started_at: '2026-11-01T00:00:00Z', state: 'failed', source: 'application', method: 'POST', path: '/v1/responses', model: 'test-model', detail: { status: 200, total_ms: 12.5, terminal: 'failed', events: {}, spans: [], attempts: [] } }
const page = { items: [row], has_more: true, page: 1, page_size: 20, retention_days: 30 }
function mountView(locale = 'en') { return mount(View, { global: { plugins: [createI18n({ legacy: false, locale, messages: { en, zh } })] } }) }
beforeEach(() => { vi.resetAllMocks(); api.settings.mockResolvedValue(settings); api.list.mockResolvedValue(page); api.setEnabled.mockResolvedValue({ ...settings, enabled: true }) })

describe('connection diagnostics management', () => {
  it.each(['en', 'zh'])('renders translated rows, expands details and applies filters in %s', async (locale) => {
    const wrapper = mountView(locale)
    await flushPromises()
    expect(wrapper.text()).not.toContain('admin.connectionDiagnostics.')
    expect(wrapper.text()).toContain('test-model')
    expect(wrapper.text()).toContain('HTTP 200')
    const detail = wrapper.find('button[aria-expanded]')
    await detail.trigger('click')
    expect(wrapper.text()).toContain('trace-1')
    await wrapper.find('input[maxlength="128"]').setValue('request-1')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(api.list).toHaveBeenLastCalledWith(expect.objectContaining({ request_id: 'request-1', page: 1 }), expect.any(AbortSignal))
    await wrapper.find('input[type="checkbox"]').setValue(true)
    await flushPromises()
    expect(api.setEnabled).toHaveBeenCalledWith(true, expect.any(AbortSignal))
    wrapper.unmount()
  })
  it('ignores stale list responses and aborts on unmount', async () => {
    let resolveOld!: (value: typeof page) => void
    api.list.mockImplementationOnce(() => new Promise((resolve) => { resolveOld = resolve }))
    const wrapper = mountView()
    await flushPromises()
    const firstSignal = api.list.mock.calls[0][1] as AbortSignal
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(firstSignal.aborted).toBe(true)
    resolveOld({ ...page, items: [{ ...row, model: 'stale' }] })
    await flushPromises()
    expect(wrapper.text()).not.toContain('stale')
    expect(wrapper.text()).toContain('test-model')
    const lastSignal = api.list.mock.calls[1][1] as AbortSignal
    wrapper.unmount()
    expect(lastSignal.aborted).toBe(true)
  })
  it('renders empty and failure states', async () => {
    api.list.mockResolvedValueOnce({ ...page, items: [] })
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.text()).toContain('No connection traces found')
    api.list.mockRejectedValueOnce(new Error('storage unavailable'))
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').text()).toContain('storage unavailable')
    wrapper.unmount()
  })
})
