import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { nextTick } from 'vue'

import zh from '@/i18n/locales/zh'
import en from '@/i18n/locales/en'

// 这一组测试锁住「详情弹层里感叹号提示文本为空」这个回归。
//
// 真实根因不是 i18n 缺键（sinceHint 在 zh/en 里都存在），而是
// UserConcurrencyCell 给 HelpTooltip 传了 :text，而 HelpTooltip 的 prop 叫 content。
// content 收不到值 → 气泡内容为空。prop 名不匹配是静默失败，不报任何错，
// 所以只能靠「打开弹层后断言提示框里确实有文字」来守住。

const inflightResponse = {
  user_id: 2,
  total_inflight: 4,
  groups: [
    {
      group_id: 1,
      group_name: 'gpt-valid',
      inflight: 4,
      models: ['gpt-5'],
      earliest_started_at: '2026-01-01T10:00:00Z',
    },
  ],
}

vi.mock('@/api/admin/users', () => ({
  getUserInflight: vi.fn(async () => inflightResponse),
}))

import UserConcurrencyCell from '@/components/user/UserConcurrencyCell.vue'

function mountCell(locale: 'zh' | 'en') {
  const i18n = createI18n({
    legacy: false,
    locale,
    messages: { zh, en },
  })
  return mount(UserConcurrencyCell, {
    attachTo: document.body,
    props: {
      current: 4,
      max: 20,
      userId: 2,
      slotConcurrency: 4,
      inflightAvailable: true,
    },
    global: { plugins: [i18n] },
  })
}

function tooltipTexts(): string[] {
  const nodes = Array.from(document.body.querySelectorAll('[role="tooltip"]')) as HTMLElement[]
  return nodes.map((el) => (el.textContent ?? '').trim()).filter((text) => text.length > 0)
}

describe('UserConcurrencyCell inflight hint', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  it.each(['zh', 'en'] as const)('%s: 提示气泡有实际文本，不是空白', async (locale) => {
    const wrapper = mountCell(locale)
    // 点开详情（真实交互路径，而不是直接改内部状态）
    await wrapper.get('button').trigger('click')
    await nextTick()
    await nextTick()

    const texts = tooltipTexts()
    expect(texts.length).toBeGreaterThan(0)
    // sinceHint 的文案必须落到 DOM 上；prop 名写错时这里会是空白。
    const joined = texts.join(' | ')
    if (locale === 'zh') {
      expect(joined).toContain('最早一条请求开始于')
    } else {
      expect(joined).toContain('Earliest request started')
    }

    wrapper.unmount()
  })

  it('槽位与在途数不一致时给出两者关系的说明，而不是留两个无名数字', async () => {
    const i18n = createI18n({ legacy: false, locale: 'zh', messages: { zh, en } })
    const wrapper = mount(UserConcurrencyCell, {
      attachTo: document.body,
      props: {
        current: 91,          // 在途请求数
        max: 200,
        userId: 2,
        slotConcurrency: 39,  // 槽位占用数：与在途不同
        inflightAvailable: true,
      },
      global: { plugins: [i18n] },
    })
    await nextTick()

    const texts = tooltipTexts()
    const joined = texts.join(' | ')
    expect(joined).toContain('91')
    expect(joined).toContain('39')

    wrapper.unmount()
  })

  it('两个数一致时不显示差异提示，避免噪音', async () => {
    const i18n = createI18n({ legacy: false, locale: 'zh', messages: { zh, en } })
    const wrapper = mount(UserConcurrencyCell, {
      attachTo: document.body,
      props: {
        current: 7,
        max: 20,
        userId: 2,
        slotConcurrency: 7,
        inflightAvailable: true,
      },
      global: { plugins: [i18n] },
    })
    await nextTick()
    expect(tooltipTexts().join(' | ')).not.toContain('并发槽位占用')
    wrapper.unmount()
  })

  it('在途登记不可用时不声称是在途数', async () => {
    const i18n = createI18n({ legacy: false, locale: 'zh', messages: { zh, en } })
    const wrapper = mount(UserConcurrencyCell, {
      attachTo: document.body,
      props: {
        current: 39,
        max: 200,
        userId: 2,
        slotConcurrency: 39,
        inflightAvailable: false,
      },
      global: { plugins: [i18n] },
    })
    await nextTick()
    expect(tooltipTexts().join(' | ')).not.toContain('并发槽位占用')
    wrapper.unmount()
  })
})
