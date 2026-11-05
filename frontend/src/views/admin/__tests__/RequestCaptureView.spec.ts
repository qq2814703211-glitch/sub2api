import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent } from 'vue'
import RequestCaptureView from '../RequestCaptureView.vue'
const api = vi.hoisted(() => ({ getConfig: vi.fn(), listTasks: vi.fn(), saveConfig: vi.fn(), createTask: vi.fn(), stopTask: vi.fn(), deleteTask: vi.fn(), listRecords: vi.fn(), getRecord: vi.fn(), getContent: vi.fn(), exportCapture: vi.fn(), authorizeSensitiveRead: vi.fn(), canStreamExport: () => true }))
vi.mock('@/api/admin/requestCaptures', () => api)
vi.mock('vue-i18n', async (importOriginal) => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
const task = { id: 'task-a', instance_id: 'instance', target_type: 'user', target_id: 27, target_name: 'fixture', status: 'running', requests: 1, bytes: 4, partial: 0, skipped: 0 }
const record = { id: 'record-a', task_id: 'task-a', request_id: 'fixture-request', status: 503, parts: [{ name: 'client.txt', stage: 'client_request', bytes: 12 }] }
const make = () => mount(RequestCaptureView, { global: { stubs: { AppLayout: defineComponent({ template: '<div><slot /></div>' }), BaseDialog: defineComponent({ props: ['show'], template: '<div v-if="show"><slot /></div>' }), TotpStepUpDialog: true } } })
beforeEach(() => { vi.clearAllMocks(); vi.useFakeTimers(); api.getConfig.mockResolvedValue({ enabled: true, storage_ready: true, instance_id: 'instance', quota_mib: 16, retention_days: 1 }); api.listTasks.mockResolvedValue({ items: [task], has_more: false, stats: { used_bytes: 4, buffer_bytes: 4, active_requests: 0, admission_skipped: 0 } }); api.listRecords.mockResolvedValue({ items: [record], has_more: false }); api.getRecord.mockResolvedValue(record); api.stopTask.mockResolvedValue(undefined); api.createTask.mockResolvedValue(task); api.saveConfig.mockImplementation(async value => value) })
afterEach(() => vi.useRealTimers())
describe('request capture management', () => {
 it('shows settings, creates a bounded task and stops it', async () => {
  const wrapper = make(); await flushPromises(); expect(wrapper.text()).toContain('fixture');
  await wrapper.get('[data-testid="open-create"]').trigger('click'); await wrapper.get('[data-testid="target-id"]').setValue(27); await wrapper.get('#capture-create').trigger('submit'); await flushPromises(); expect(api.createTask).toHaveBeenCalledWith({ target_type: 'user', target_id: 27, duration_minutes: 10, save_media: false });
  const stop = wrapper.findAll('button').find(b => b.text() === 'admin.requestCapture.stop'); await stop!.trigger('click'); await flushPromises(); expect(api.stopTask).toHaveBeenCalledWith('task-a'); wrapper.unmount()
 })
 it('requires an explicit sensitive read and ignores a stale body response', async () => {
  let resolve!: (value: unknown) => void; api.getContent.mockReturnValue(new Promise(r => { resolve = r }));
  const wrapper = make(); await flushPromises(); await wrapper.get('[data-testid="select-task-a"]').trigger('click'); await flushPromises(); await wrapper.get('[data-testid="record-record-a"]').trigger('click'); await flushPromises(); expect(api.getContent).not.toHaveBeenCalled();
  await wrapper.get('[data-testid="read-content"]').trigger('click'); await flushPromises(); expect(api.getContent).toHaveBeenCalledTimes(1); const signal = api.getContent.mock.calls[0][4];
  await wrapper.get('[data-testid="select-task-a"]').trigger('click'); resolve({ text: 'stale confidential fixture', has_more: false, next_offset: 5 }); await flushPromises(); expect(signal.aborted).toBe(true); expect(wrapper.text()).not.toContain('stale confidential fixture'); wrapper.unmount()
 })
 it('passes the step-up preflight and an abort signal to streaming export', async () => {
  const wrapper = make(); await flushPromises(); await wrapper.get('[data-testid="select-task-a"]').trigger('click'); await flushPromises(); await wrapper.get('[data-testid="record-record-a"]').trigger('click'); await flushPromises(); await wrapper.get('[data-testid="export-record"]').trigger('click'); await flushPromises(); const call = api.exportCapture.mock.calls[0]; expect(call.slice(0,2)).toEqual(['task-a','record-a']); await call[2](); expect(api.authorizeSensitiveRead).toHaveBeenCalledOnce(); wrapper.unmount(); expect(call[3].aborted).toBe(true)
 })
 it('renders initialization failures and disabled storage without creating tasks', async () => {
  api.getConfig.mockRejectedValueOnce(new Error('storage unavailable')); const first = make(); await flushPromises(); expect(first.get('[role="alert"]').text()).toContain('storage unavailable'); first.unmount();
  api.getConfig.mockResolvedValueOnce({ enabled: false, storage_ready: false, quota_mib: 16, retention_days: 1 }); const second = make(); await flushPromises(); expect(second.get('[data-testid="open-create"]').attributes('disabled')).toBeDefined(); second.unmount()
 })
})
