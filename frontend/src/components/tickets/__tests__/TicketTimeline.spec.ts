import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import TicketTimeline from '../TicketTimeline.vue'
import type { TicketMessage } from '@/api/tickets'

vi.mock('vue-i18n', async () => {
  const { testI18n } = await import('@/__tests__/i18nTest')
  return { useI18n: () => testI18n }
})

function message(overrides: Partial<TicketMessage>): TicketMessage {
  return {
    id: 1,
    ticket_id: 5,
    author_id: 9,
    author_role: 'user',
    author_name: 'Alice',
    content: 'hello',
    kind: 'reply',
    created_at: '2026-09-25T08:00:00Z',
    ...overrides
  }
}

describe('TicketTimeline', () => {
  it('renders events with translated state transitions and separates the two sides', () => {
    const wrapper = mount(TicketTimeline, {
      props: {
        messages: [
          message({ id: 1, kind: 'event', event_type: 'status_changed', event_data: { from: 'open', to: 'in_progress' }, content: '状态：待处理 → 处理中' }),
          message({ id: 2, content: 'user text' }),
          message({ id: 3, author_role: 'admin', author_name: 'Support', content: 'admin text' })
        ],
        admin: false,
        hasMore: false,
        loadingOlder: false
      }
    })
    const text = wrapper.text()
    expect(text).toContain('Status: Open → In progress')
    expect(text).toContain('user text')
    expect(text).toContain('admin text')
    expect(text).toContain('Alice')
    expect(text).toContain('Support')
    expect(wrapper.findAll('article')).toHaveLength(2)
  })

  it('renders assignment events and escapes plain-text bodies', () => {
    const wrapper = mount(TicketTimeline, {
      props: {
        messages: [
          message({ id: 1, kind: 'event', event_type: 'assignment_changed', event_data: { from: null, to: 99 }, content: '管理员已接单' }),
          message({ id: 2, content: '<script>alert(1)</script>' })
        ],
        admin: true,
        hasMore: true,
        loadingOlder: false
      }
    })
    expect(wrapper.text()).toContain('Assigned to #99')
    expect(wrapper.html()).not.toContain('<script>alert(1)</script>')
    expect(wrapper.text()).toContain('<script>alert(1)</script>')
    expect(wrapper.text()).toContain('Load earlier messages')
  })

  it('emits older when the load-older button is used', async () => {
    const wrapper = mount(TicketTimeline, {
      props: { messages: [message({})], admin: false, hasMore: true, loadingOlder: false }
    })
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('older')).toHaveLength(1)
  })
})
