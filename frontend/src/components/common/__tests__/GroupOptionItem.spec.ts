import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import GroupOptionItem from '../GroupOptionItem.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ cachedPublicSettings: null }),
}))

describe('GroupOptionItem description layout', () => {
  it('applies multiline and overflow-safe text styles', () => {
    const description = 'First section\nvery-long-unbroken-description-value-that-must-not-overflow'
    const wrapper = mount(GroupOptionItem, {
      props: {
        name: 'Example group',
        platform: 'openai',
        description,
      },
      global: {
        stubs: {
          GroupBadge: true,
        },
      },
    })

    const descriptionElement = wrapper
      .findAll('span')
      .find((element) => element.text() === description)

    expect(descriptionElement).toBeDefined()
    expect(descriptionElement?.classes()).toContain('whitespace-pre-line')
    expect(descriptionElement?.classes()).toContain('[overflow-wrap:anywhere]')
    expect(descriptionElement?.classes()).toContain('line-clamp-3')
    expect(wrapper.find('[title]').attributes('title')).toBe(description)
  })

  it('renders passive rate-change badges when the available group carries signals', () => {
    const wrapper = mount(GroupOptionItem, {
      props: {
        name: 'Example group',
        platform: 'openai',
        rateMultiplier: 0.08,
        signals: {
          recent_rate_change: {
            direction: 'up',
            delta: 0.02,
            previous_rate: 0.06,
            changed_at: '2026-11-04T10:30:00Z',
          },
        },
      },
      global: {
        stubs: {
          GroupBadge: true,
        },
      },
    })

    const badge = wrapper.get('[data-testid="group-rate-signal-rate"]')
    expect(badge.element.tagName).toBe('SPAN')
    expect(badge.attributes('role')).toBe('img')
    expect(badge.text()).toBe('0.02')
    expect(wrapper.find('button[data-testid="group-rate-signal-rate"]').exists()).toBe(false)
  })
})
