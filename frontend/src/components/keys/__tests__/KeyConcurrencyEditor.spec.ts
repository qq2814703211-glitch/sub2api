import {afterEach,beforeEach,describe,expect,it,vi} from 'vitest'
import {enableAutoUnmount,flushPromises,mount} from '@vue/test-utils'
import KeyConcurrencyEditor from '../KeyConcurrencyEditor.vue'
import type {ApiKey} from '@/types'
const {save}=vi.hoisted(()=>({save:vi.fn()}))
vi.mock('@/api/admin/apiKeys',()=>({updateConcurrency:save}))
vi.mock('vue-i18n',()=>({useI18n:()=>({t:(key:string)=>key})}))
enableAutoUnmount(afterEach)
beforeEach(()=>{save.mockReset()})
const key=(id=7)=>({id,concurrency_limit:3,current_concurrency:2,waiting_count:1,queue_stats_available:true}) as ApiKey
describe('key concurrency configuration',()=>{
 it('round trips positive limits and zero',async()=>{
  const wrapper=mount(KeyConcurrencyEditor,{props:{apiKey:key()}})
  expect(wrapper.get('input').element.value).toBe('3')
  await wrapper.get('input').setValue('0');save.mockResolvedValue({api_key:{...key(),concurrency_limit:0}})
  await wrapper.get('form').trigger('submit');await flushPromises()
  expect(save).toHaveBeenCalledWith(7,0,expect.any(AbortSignal))
  expect(wrapper.emitted('updated')?.[0]?.[0]).toMatchObject({concurrency_limit:0})
 })
 it('shows saving, validation and API errors',async()=>{
  const wrapper=mount(KeyConcurrencyEditor,{props:{apiKey:key()}})
  await wrapper.get('input').setValue('-1');await wrapper.get('form').trigger('submit');expect(save).not.toHaveBeenCalled()
  expect(wrapper.get('[role=alert]').text()).toBe('keys.concurrencyInvalid')
  await wrapper.get('input').setValue('5');let reject!:(error:Error)=>void;save.mockReturnValue(new Promise((_,r)=>{reject=r}))
  await wrapper.get('form').trigger('submit');expect(wrapper.get('button').attributes('disabled')).toBeDefined();reject(new Error('Redis unavailable'));await flushPromises()
  expect(wrapper.get('[role=alert]').text()).toBe('Redis unavailable')
 })
 it('cancels and ignores an obsolete user response',async()=>{
  let resolve!:(value:unknown)=>void
  save.mockReturnValue(new Promise(r=>{resolve=r}))
  const wrapper=mount(KeyConcurrencyEditor,{props:{apiKey:key()}})
  await wrapper.get('form').trigger('submit');const signal=save.mock.calls[0][2] as AbortSignal
  await wrapper.setProps({apiKey:key(8)});expect(signal.aborted).toBe(true)
  resolve({api_key:key()});await flushPromises();expect(wrapper.emitted('updated')).toBeUndefined()
 })
 it('reports unavailable stats instead of claiming no waiting requests',()=>{
  const wrapper=mount(KeyConcurrencyEditor,{props:{apiKey:{...key(),waiting_count:null,queue_stats_available:false}}})
  expect(wrapper.text()).toContain('keys.queueUnavailable')
 })
})
