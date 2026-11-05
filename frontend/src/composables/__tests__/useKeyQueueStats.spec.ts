import {afterEach,beforeEach,describe,expect,it,vi} from 'vitest'
import {mount,flushPromises} from '@vue/test-utils'
import {defineComponent,ref,nextTick} from 'vue'
import {useKeyQueueStats} from '../useKeyQueueStats'
import type {ApiKey} from '@/types'
const {load,adminLoad}=vi.hoisted(()=>({load:vi.fn(),adminLoad:vi.fn()}))
vi.mock('@/api/keyQueue',()=>({getKeyQueue:load}))
vi.mock('@/api/admin/keyQueue',()=>({getAdminUserKeyQueue:adminLoad}))
beforeEach(()=>{vi.useFakeTimers();load.mockReset();adminLoad.mockReset()})
afterEach(()=>{vi.useRealTimers()})
describe('visible key queue statistics',()=>{
 it('polls only configured visible keys and aborts when removed',async()=>{
 const keys=ref<ApiKey[]>([{id:7,concurrency_limit:1} as ApiKey]);let signal:AbortSignal|undefined
 const wrapper=mount(defineComponent({setup(){useKeyQueueStats(keys);return{}},template:'<div />'}))
 load.mockImplementation((_ids,s)=>{signal=s;return new Promise(()=>{})})
 await vi.advanceTimersByTimeAsync(3000);expect(load).toHaveBeenCalledOnce()
 keys.value=[];await nextTick();expect(signal?.aborted).toBe(true)
 await vi.advanceTimersByTimeAsync(6000);expect(load).toHaveBeenCalledOnce();wrapper.unmount()
 })
 it('applies fresh counts and reports failed refresh as unavailable',async()=>{
 const keys=ref<ApiKey[]>([{id:7,concurrency_limit:1} as ApiKey])
 const wrapper=mount(defineComponent({setup(){useKeyQueueStats(keys);return{}},template:'<div />'}))
 load.mockResolvedValueOnce([{id:7,current_concurrency:1,current_waiting:2}]).mockRejectedValueOnce(new Error('offline'))
 await vi.advanceTimersByTimeAsync(3000);await flushPromises();expect(keys.value[0]).toMatchObject({current_concurrency:1,waiting_count:2,queue_stats_available:true})
 await vi.advanceTimersByTimeAsync(3000);await flushPromises();expect(keys.value[0]).toMatchObject({waiting_count:null,queue_stats_available:false});wrapper.unmount()
 })
 it('uses the administrator endpoint and discards a closed modal response',async()=>{
 const keys=ref<ApiKey[]>([{id:7,concurrency_limit:1} as ApiKey]);const visible=ref(true)
 let resolve!:(value:unknown)=>void
 adminLoad.mockReturnValue(new Promise(r=>{resolve=r}))
 const wrapper=mount(defineComponent({setup(){useKeyQueueStats(keys,()=>visible.value,()=>9);return{}},template:'<div />'}))
 await vi.advanceTimersByTimeAsync(3000);expect(adminLoad).toHaveBeenCalledWith(9,expect.any(AbortSignal));expect(load).not.toHaveBeenCalled()
 visible.value=false;await nextTick();expect(adminLoad.mock.calls[0][1].aborted).toBe(true)
 resolve([{id:7,current_concurrency:9,current_waiting:9}]);await flushPromises()
 expect(keys.value[0].current_concurrency).toBeUndefined();await vi.advanceTimersByTimeAsync(9000);expect(adminLoad).toHaveBeenCalledOnce();wrapper.unmount()
 })
 it('runs no background request when every limit is zero',async()=>{
 const wrapper=mount(defineComponent({setup(){useKeyQueueStats(ref([{id:7,concurrency_limit:0} as ApiKey]));return{}},template:'<div />'}))
 await vi.advanceTimersByTimeAsync(10000);expect(load).not.toHaveBeenCalled();wrapper.unmount()
 })
})
