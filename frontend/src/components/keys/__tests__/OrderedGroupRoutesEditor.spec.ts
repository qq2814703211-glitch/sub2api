import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import Editor from '../OrderedGroupRoutesEditor.vue'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const options = Array.from({ length: 30 }, (_, i) => ({ value: i + 1, label: `Group ${i + 1}` }))
describe('OrderedGroupRoutesEditor', () => {
  it('adds unique groups in order without a ten-group ceiling', async () => {
    const wrapper = mount(Editor, { props: { modelValue: options.slice(0, 11).map(g => g.value), options } })
    expect(wrapper.findAll('li')).toHaveLength(11)
    expect(wrapper.findAll('option')).toHaveLength(20)
    await wrapper.get('select').setValue('30')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([[1,2,3,4,5,6,7,8,9,10,11,30]])
  })
  it('reorders the primary and removes a fallback without mutating props', async () => {
    const ids = [4,2,3]
    const wrapper = mount(Editor, { props: { modelValue: ids, options } })
    await wrapper.findAll('li')[1].findAll('button')[0].trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([[2,4,3]])
    await wrapper.findAll('li')[2].findAll('button')[2].trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[1]).toEqual([[4,2]])
    expect(ids).toEqual([4,2,3])
  })
  it('protects boundaries and disables edits while saving', async () => {
    const wrapper = mount(Editor, { props: { modelValue: [1,2], options, disabled: true } })
    await wrapper.findAll('li')[1].findAll('button')[0].trigger('click')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.get('fieldset').attributes('disabled')).toBeDefined()
  })
  it('shows empty state and unavailable stored groups explicitly', () => {
    const wrapper = mount(Editor, { props: { modelValue: [], options: [] } })
    expect(wrapper.text()).toContain('keys.routes.empty')
    expect(wrapper.get('select').attributes('disabled')).toBeDefined()
    const legacy = mount(Editor, { props: { modelValue: [99], options: [] } })
    expect(legacy.text()).toContain('#99')
  })
})
