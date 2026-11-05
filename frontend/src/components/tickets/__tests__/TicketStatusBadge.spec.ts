import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import TicketStatusBadge from '../TicketStatusBadge.vue'
import type { TicketStatus } from '@/api/tickets'

vi.mock('vue-i18n', async () => {
  const { testI18n } = await import('@/__tests__/i18nTest')
  return { useI18n: () => testI18n }
})

describe('TicketStatusBadge', () => {
  it.each([
    ['open', 'Open'],
    ['in_progress', 'In progress'],
    ['waiting_user', 'Waiting for user'],
    ['resolved', 'Resolved'],
    ['closed', 'Closed']
  ] as Array<[TicketStatus, string]>)('renders %s as %s', (status, label) => {
    const wrapper = mount(TicketStatusBadge, { props: { status } })
    expect(wrapper.text()).toContain(label)
  })

  it('uses distinct styling for active and closed states', () => {
    const open = mount(TicketStatusBadge, { props: { status: 'open' } })
    const closed = mount(TicketStatusBadge, { props: { status: 'closed' } })
    expect(open.classes().join(' ')).toContain('amber')
    expect(closed.classes().join(' ')).toContain('gray')
  })
})
