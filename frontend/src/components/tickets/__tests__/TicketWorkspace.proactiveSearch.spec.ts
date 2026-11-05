import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'

import TicketWorkspace from '../TicketWorkspace.vue'
import en from '@/i18n/locales/en'

const api = vi.hoisted(() => ({ list: vi.fn() }))

vi.mock('@/api/admin/users', () => ({ usersAPI: { list: api.list } }))

vi.mock('@/api/tickets', () => ({
  newClientID: () => '00000000-0000-4000-8000-000000000000',
  ticketCategories: ['billing', 'api', 'account', 'other'],
  ticketsAPI: () => ({ proactiveCreate: vi.fn() })
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() })
}))

vi.mock('@/stores/ticketNotifications', () => ({
  useTicketNotificationsStore: () => ({ refresh: vi.fn(), userUnread: { value: 0 }, adminUnread: { value: 0 } })
}))

vi.mock('@/composables/useTicketWorkspace', async () => {
  const { reactive, ref } = await import('vue')
  return {
    useTicketWorkspace: () => ({
      filters: reactive({ search: '', status: '', category: '', priority: '', assigned_to: 'all', page: 1, page_size: 20 }),
      items: ref([]),
      pages: ref(1),
      stats: reactive({ total: 0, unread: 0 }),
      detail: ref(null),
      selectedID: ref(null),
      loading: ref(false),
      detailLoading: ref(false),
      loadingOlder: ref(false),
      refreshing: ref(false),
      mutating: ref(false),
      listError: ref(''),
      detailError: ref(''),
      syncFailed: ref(false),
      revision: ref(0),
      draft: ref(''),
      notificationSettings: ref({ enabled: false, admin_email: '', pending_emails: 0, failed_emails: 0 }),
      settingsLoading: ref(false),
      settingsSaving: ref(false),
      refresh: vi.fn(),
      older: vi.fn(),
      markRead: vi.fn(),
      send: vi.fn(),
      update: vi.fn(),
      create: vi.fn(),
      loadSettings: vi.fn(),
      saveSettings: vi.fn(),
      resetFilters: vi.fn(),
      select: vi.fn(),
      back: vi.fn(),
      changePage: vi.fn()
    })
  }
})

const i18n = createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: { en } })

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

async function mountWorkspace() {
  const wrapper = mount(TicketWorkspace, {
    props: { admin: true },
    global: {
      plugins: [i18n],
      stubs: {
        teleport: true,
        TicketTimeline: true,
        TicketStatusBadge: true,
        Select: true,
        ConfirmDialog: true,
        Icon: true
      }
    }
  })
  await flushPromises()
  return wrapper
}

async function openProactive(wrapper: Awaited<ReturnType<typeof mountWorkspace>>) {
  const button = wrapper.findAll('button').find(candidate => candidate.text().includes(en.tickets.proactiveNew))
  if (!button) throw new Error('proactive entry button was not rendered')
  await button.trigger('click')
  await flushPromises()
  return wrapper.get('#proactive-ticket-form input')
}

beforeEach(() => {
  vi.useFakeTimers()
  api.list.mockReset()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('TicketWorkspace proactive user search', () => {
  it('ignores a late response from an earlier query', async () => {
    const first = deferred<{ items: Array<{ id: number; username: string; email: string }> }>()
    const second = deferred<{ items: Array<{ id: number; username: string; email: string }> }>()
    api.list.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)

    const wrapper = await mountWorkspace()
    const input = await openProactive(wrapper)

    await input.setValue('an')
    await vi.advanceTimersByTimeAsync(300)
    expect(api.list).toHaveBeenCalledTimes(1)
    expect(api.list.mock.calls[0][2]).toMatchObject({ search: 'an' })
    const firstSignal = (api.list.mock.calls[0][3] as { signal?: AbortSignal } | undefined)?.signal

    await input.setValue('anna')
    await vi.advanceTimersByTimeAsync(300)
    expect(api.list).toHaveBeenCalledTimes(2)
    expect(api.list.mock.calls[1][2]).toMatchObject({ search: 'anna' })

    second.resolve({ items: [{ id: 22, username: 'anna', email: 'anna@example.test' }] })
    await flushPromises()
    expect(wrapper.text()).toContain('anna@example.test')

    first.resolve({ items: [{ id: 11, username: 'an-stale', email: 'stale@example.test' }] })
    await flushPromises()
    expect(wrapper.text()).not.toContain('stale@example.test')
    expect(wrapper.text()).toContain('anna@example.test')
    expect(firstSignal?.aborted).toBe(true)

    wrapper.unmount()
  })

  it('cancels the in-flight search when the dialog closes', async () => {
    const pending = deferred<{ items: Array<{ id: number; username: string; email: string }> }>()
    api.list.mockReturnValueOnce(pending.promise)

    const wrapper = await mountWorkspace()
    const input = await openProactive(wrapper)
    await input.setValue('bo')
    await vi.advanceTimersByTimeAsync(300)
    expect(api.list).toHaveBeenCalledTimes(1)
    const signal = (api.list.mock.calls[0][3] as { signal?: AbortSignal } | undefined)?.signal

    const cancel = wrapper.findAll('button').find(candidate => candidate.text() === en.tickets.cancel)
    if (!cancel) throw new Error('proactive cancel button was not rendered')
    await cancel.trigger('click')
    await flushPromises()

    expect(signal?.aborted).toBe(true)
    expect(wrapper.find('#proactive-ticket-form').exists()).toBe(false)

    pending.resolve({ items: [{ id: 33, username: 'bob', email: 'bob@example.test' }] })
    await flushPromises()

    wrapper.unmount()
  })

  it('aborts a pending search when the workspace unmounts', async () => {
    const pending = deferred<{ items: Array<{ id: number; username: string; email: string }> }>()
    api.list.mockReturnValueOnce(pending.promise)

    const wrapper = await mountWorkspace()
    const input = await openProactive(wrapper)
    await input.setValue('ca')
    await vi.advanceTimersByTimeAsync(300)
    const signal = (api.list.mock.calls[0][3] as { signal?: AbortSignal } | undefined)?.signal

    wrapper.unmount()
    expect(signal?.aborted).toBe(true)
  })
})
