import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import en from '../locales/en/admin'
import zh from '../locales/zh/admin'

const pages = import.meta.glob(['../../views/admin/{AccountOpsView,OpsCenterView,UpstreamMonitorView}.vue', '../../components/admin/operations/UpstreamHistoryPanel.vue'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>

describe('account operations registered locale namespace', () => {
  it('registers identical nonempty messages in both admin namespaces', () => {
    expect(Object.keys(en.accountOps).sort()).toEqual(Object.keys(zh.accountOps).sort())
    expect(Object.keys(en.accountOps.history).sort()).toEqual(Object.keys(zh.accountOps.history).sort())
    for (const messages of [en.accountOps, zh.accountOps]) {
      const { history, ...flatMessages } = messages
      expect(Object.values(flatMessages).every(value => typeof value === 'string' && value.trim().length > 0)).toBe(true)
      expect(Object.values(history).every(value => typeof value === 'string' && value.trim().length > 0)).toBe(true)
    }
  })

  it('resolves every page message through the real i18n runtime', () => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: { admin: en }, zh: { admin: zh } } })
    expect(Object.keys(pages)).toHaveLength(4)
    for (const source of Object.values(pages)) {
      expect(source).not.toMatch(/const text\s*=/)
      expect(source).not.toMatch(/locale\.value\.startsWith/)
      const keys = [...source.matchAll(/t\('(?<key>admin\.accountOps\.[^']+)'/g)].map(match => match.groups!.key)
      expect(keys.length).toBeGreaterThan(0)
      for (const locale of ['en', 'zh'] as const) {
        i18n.global.locale.value = locale
        for (const key of keys) {
          expect(i18n.global.te(key)).toBe(true)
          expect(i18n.global.t(key)).not.toBe(key)
        }
      }
    }
  })
})
