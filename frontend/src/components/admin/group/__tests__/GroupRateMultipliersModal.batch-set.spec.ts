import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import type { AdminGroup } from '@/types'
import GroupRateMultipliersModal from '../GroupRateMultipliersModal.vue'

const mocks = vi.hoisted(() => ({ getGroupRateMultipliers: vi.fn(), batchSetGroupRateMultipliers: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { groups: mocks } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn(), showError: vi.fn() }) }))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: unknown) =>
      params && typeof params === 'object' && 'count' in params
        ? `${key}(${(params as { count: number }).count})`
        : key
  })
}))
enableAutoUnmount(afterEach)
beforeEach(() => vi.resetAllMocks())

function entries(count: number) {
  return Array.from({ length: count }, (_, index) => ({
    user_id: index + 1,
    user_name: `user${index + 1}`,
    user_email: `user${index + 1}@example.com`,
    user_notes: '',
    user_status: 'active',
    rate_multiplier: 2,
    rpm_override: index === 0 ? 100 : null
  }))
}

async function openEditor(rows = 2) {
  mocks.getGroupRateMultipliers.mockResolvedValue(entries(rows))
  const wrapper = mount(GroupRateMultipliersModal, {
    props: { show: false, group: { id: 1, name: 'Group', platform: 'openai', rate_multiplier: 1 } as AdminGroup },
    global: {
      stubs: {
        BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' },
        Icon: true,
        PlatformIcon: true,
        Pagination: true
      }
    }
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

const setAllButton = (wrapper: Awaited<ReturnType<typeof openEditor>>) => wrapper.get('[data-testid="apply-batch-set-rate"]')
const rateValues = (wrapper: Awaited<ReturnType<typeof openEditor>>) =>
  wrapper.findAll('tbody input[type="number"]').map(input => (input.element as HTMLInputElement).value)
const findButton = (wrapper: Awaited<ReturnType<typeof openEditor>>, text: string) =>
  wrapper.findAll('button').find(button => button.text() === text)

describe('GroupRateMultipliersModal set-all draft', () => {
  it('sets every configured entry across pages, not just the visible page', async () => {
    const wrapper = await openEditor(12)
    expect(wrapper.findAll('tbody tr')).toHaveLength(10)
    expect(wrapper.text()).toContain('admin.groups.batchSetScope(12)')

    await wrapper.get('#batch-set-rate').setValue('0.5')
    expect(setAllButton(wrapper).attributes('disabled')).toBeUndefined()
    await setAllButton(wrapper).trigger('click')

    expect(rateValues(wrapper)).toEqual(Array(10).fill('0.5'))
    expect(wrapper.findAll('button').some(button => button.text() === 'common.save')).toBe(true)

    await findButton(wrapper, 'common.save')!.trigger('click')
    await flushPromises()
    expect(mocks.batchSetGroupRateMultipliers).toHaveBeenCalledTimes(1)
    expect(mocks.batchSetGroupRateMultipliers).toHaveBeenCalledWith(
      1,
      Array.from({ length: 12 }, (_, index) => ({ user_id: index + 1, rate_multiplier: 0.5 }))
    )
  })

  it('reverts the whole set-all draft to the server snapshot on cancel', async () => {
    const wrapper = await openEditor(2)
    await wrapper.get('#batch-set-rate').setValue('0.25')
    await setAllButton(wrapper).trigger('click')
    expect(rateValues(wrapper)).toEqual(['0.25', '0.25'])

    await findButton(wrapper, 'admin.groups.revertChanges')!.trigger('click')
    expect(rateValues(wrapper)).toEqual(['2', '2'])
    expect(wrapper.findAll('button').some(button => button.text() === 'common.save')).toBe(false)
    expect(mocks.batchSetGroupRateMultipliers).not.toHaveBeenCalled()
  })

  it('keeps the multiply-factor action independent and does not touch rpm_override', async () => {
    const wrapper = await openEditor(2)
    await wrapper.get('input[placeholder="0.5"]').setValue('3')
    await findButton(wrapper, 'admin.groups.applyMultiplier')!.trigger('click')
    expect(rateValues(wrapper)).toEqual(['6', '6'])

    await wrapper.get('#batch-set-rate').setValue('0.5')
    await setAllButton(wrapper).trigger('click')
    expect(rateValues(wrapper)).toEqual(['0.5', '0.5'])

    await findButton(wrapper, 'common.save')!.trigger('click')
    await flushPromises()
    expect(mocks.batchSetGroupRateMultipliers).toHaveBeenCalledWith(1, [
      { user_id: 1, rate_multiplier: 0.5 },
      { user_id: 2, rate_multiplier: 0.5 }
    ])
    const payload = mocks.batchSetGroupRateMultipliers.mock.calls[0][1] as Array<Record<string, unknown>>
    expect(payload.every(entry => Object.keys(entry).sort().join(',') === 'rate_multiplier,user_id')).toBe(true)
  })

  it('warns and refuses to save a positive value that rounds to zero at six decimals', async () => {
    const wrapper = await openEditor(2)
    await wrapper.get('#batch-set-rate').setValue('0.0000001')

    expect(wrapper.get('[role="alert"]').text()).toBe('admin.groups.batchSetTooSmall')
    expect(setAllButton(wrapper).attributes('disabled')).toBeDefined()
    await setAllButton(wrapper).trigger('click')
    expect(rateValues(wrapper)).toEqual(['2', '2'])
    expect(wrapper.findAll('button').some(button => button.text() === 'common.save')).toBe(false)
    expect(mocks.batchSetGroupRateMultipliers).not.toHaveBeenCalled()
  })

  it.each(['', '0', '-1'])('disables set-all for invalid input %j', async (value) => {
    const wrapper = await openEditor(2)
    await wrapper.get('#batch-set-rate').setValue(value)

    expect(setAllButton(wrapper).attributes('disabled')).toBeDefined()
    if (value !== '') {
      expect(wrapper.get('[role="alert"]').text()).toBe('admin.groups.batchSetInvalid')
    } else {
      expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    }
    await setAllButton(wrapper).trigger('click')
    expect(rateValues(wrapper)).toEqual(['2', '2'])
    expect(mocks.batchSetGroupRateMultipliers).not.toHaveBeenCalled()
  })

  it('applies the same six-decimal precision as the draft editor', async () => {
    const wrapper = await openEditor(1)
    await wrapper.get('#batch-set-rate').setValue('1.23456789')
    await setAllButton(wrapper).trigger('click')
    expect(rateValues(wrapper)).toEqual(['1.234568'])

    await findButton(wrapper, 'common.save')!.trigger('click')
    await flushPromises()
    expect(mocks.batchSetGroupRateMultipliers).toHaveBeenCalledWith(1, [{ user_id: 1, rate_multiplier: 1.234568 }])
  })
})
