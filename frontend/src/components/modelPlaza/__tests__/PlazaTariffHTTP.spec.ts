import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import { describe, expect, it } from 'vitest'
import type { ModelPlazaResponse } from '@/api/modelPlaza'
import zh from '@/i18n/locales/zh'
import en from '@/i18n/locales/en'
import PlazaGroupSection from '../PlazaGroupSection.vue'

// The golden response is emitted by TestModelPlazaHTTPAbsoluteWindows through Gin.
const { data } = JSON.parse(readFileSync(resolve('src/components/modelPlaza/__tests__/fixtures/plaza-tariff-http.json'), 'utf8')) as { data: ModelPlazaResponse }

function render(groupID: number, locale = 'zh', response = data) {
  const group = response.groups.find(g => g.id === groupID)!
  return mount(PlazaGroupSection, {
    props: { group },
    global: { plugins: [createPinia(), createI18n({ legacy: false, locale, messages: { zh, en } })] }
  })
}

describe('Model Plaza HTTP tariff response', () => {
  it('shows time, weekday and holiday conditions with distinct absolute component prices', () => {
    const wrapper = render(51)
    const paid = wrapper.get('[data-window-price="paid"]')
    expect(paid.text()).toContain('09:00–13:00')
    expect(paid.text()).toContain('Asia/Shanghai')
    expect(paid.text()).toMatch(/周一.+周三.+周五/)
    expect(paid.text()).toContain('中国法定节假日除外')
    expect(paid.text()).toContain('站内单位 / 百万 tokens')
    for (const [component, amount] of Object.entries({ input: '4.50', output: '2.00', read: '0.75', write: '3.00', write30m: '0.00', write12h: '6.50' })) {
      expect(paid.get(`[data-component="${component}"] dd`).text()).toBe(amount)
    }
    expect(paid.find('[data-component="write1h"]').exists()).toBe(false)
    const reference = wrapper.get('[data-window-price="reference"]')
    expect(reference.get('[data-component="input"] dd').text()).toBe('9.00')
    expect(reference.get('[data-component="write12h"] dd').text()).toBe('13.00')
  })

  it('keeps reference windows separate from group peak and explicit selling prices', () => {
    const peak = render(52, 'en')
    expect(peak.text()).toContain('Peak hours')
    expect(peak.find('[data-window-price="paid"]').exists()).toBe(false)
    expect(peak.get('[data-window-price="reference"]').text()).toContain('Excludes Chinese public holidays')
    const sale = render(53)
    expect(sale.find('[data-window-price="paid"]').exists()).toBe(false)
    const cells = sale.findAll('tbody tr')[0].findAll('td')
    expect(cells[1].text()).toBe('0.00')
    expect(cells[2].text()).toBe('38.00')
  })

  it('renders reference flat and tiered TTLs and keeps all three media units', () => {
    const wrapper = render(51)
    const rows = wrapper.findAll('tbody tr')
    const row = (name: string) => rows.find(r => r.get('td').text().includes(name))!
    // 实付列：基础价、30m、12h 各自独立，且都乘用户专属 0.5 倍率。
    expect(row('fixture-window').findAll('td')[3].text()).toMatch(/写入\s*2\.00.*30m\s*2\.50.*12h\s*3\.50/s)
    expect(row('fixture-tier').findAll('td')[3].text()).toMatch(/30m\s*2\.50.*12h\s*3\.00.*30m\s*0\.00.*12h\s*5\.50/s)
    expect(row('fixture-window').findAll('td')[6].text()).toMatch(/30m\s*5\.00.*12h\s*7\.00/s)
    expect(row('fixture-tier').findAll('td')[6].text()).toMatch(/30m\s*0\.00.*12h\s*11\.00/s)
    expect(row('fixture-request').findAll('td')[1].text()).toMatch(/0\.00.*\/ 次/)
    expect(row('fixture-second').findAll('td')[1].text()).toMatch(/0\.1875.*\/ 秒/)
    expect(row('fixture-image').findAll('td')[1].text()).toMatch(/1\.0625.*\/ 张/)
  })

  it('keeps a missing sale 30m missing instead of borrowing the reference price', () => {
    const wrapper = render(54)
    const rows = wrapper.findAll('tbody tr')
    const row = rows.find(r => r.get('td').text().includes('fixture-window'))!
    const cells = row.findAll('td')
    // 运营卡显式普通写价 8e-6 × 组倍率 2 = 16.00；30m/12h 在售价里是缺项。
    expect(cells[3].text()).toMatch(/写入\s*16\.00/)
    expect(cells[3].text()).not.toContain('30m')
    expect(cells[3].text()).not.toContain('12h')
    // 同一条目的参考列仍然披露 30m 5.00 —— 两列语义不同。
    expect(cells[6].text()).toMatch(/30m\s*5\.00/)
  })

  const publishedPath = process.env.PLAZA_ADMIN_PLAZA
  it.runIf(!!publishedPath)('shows the site rates from the admin edit published to PostgreSQL', () => {
    const { data: published } = JSON.parse(readFileSync(publishedPath!, 'utf8')) as { data: ModelPlazaResponse }
    const wrapper = render(51, 'zh', published)
    const rows = wrapper.findAll('tbody tr')
    const cells = (name: string) => rows.find(row => row.get('td').text().includes(name))!.findAll('td')
    expect(cells('fixture-manual')[1].text()).toBe('4.00')
    expect(cells('fixture-manual')[2].text()).toBe('22.00')
    // 运营发布的人工 30m 显式 0 与 12h 13 直接进实付列（组倍率 2）。
    expect(cells('fixture-manual')[3].text()).toMatch(/30m\s*0\.00.*12h\s*26\.00/s)
    expect(cells('fixture-manual')[6].text()).toMatch(/30m\s*0\.00.*12h\s*13\.00/s)
    expect(cells('fixture-manual')[6].text()).not.toContain('1h')
    expect(cells('fixture-request')[1].text()).toMatch(/0\.00.*\/ 次/)
    expect(cells('fixture-second')[1].text()).toMatch(/0\.75.*\/ 秒/)
    expect(cells('fixture-image')[1].text()).toMatch(/4\.25.*\/ 张/)
  })
})
