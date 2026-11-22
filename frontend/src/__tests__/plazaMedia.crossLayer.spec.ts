import { beforeEach, describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import PlazaModelPricingTable from '@/components/modelPlaza/PlazaModelPricingTable.vue'
import PricingEntryCard from '@/components/admin/channel/PricingEntryCard.vue'
import { createDefaultTimePricingForm } from '@/components/admin/channel/types'
import { readFileSync } from 'node:fs'
vi.mock('vue-i18n',async()=>({...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),useI18n:()=>({t:(key:string, values?:Record<string,unknown>)=>key==='mediaPricing.inputSurcharge' ? `${key} price:${values?.price} count:${values?.count}` : key})}))
const root=process.env.PLAZA_FIXTURE_DIR || process.cwd()+'/src/__tests__/fixtures/plaza'
beforeEach(() => { setActivePinia(createPinia()) })

describe('independent UI review real cross-layer fixtures',()=>{
 it('advertises the effective media period price returned by billing',()=>{
  const fixture=JSON.parse(readFileSync(root+'/ui-plaza-actual-dto.json','utf8')).models[0]
  const actualCost=JSON.parse(readFileSync(root+'/plaza-token.json','utf8')).actual_cost
  const wrapper=mount(PlazaModelPricingTable,{props:{models:[fixture],rateMultiplier:0.5}})
  console.log('BACKEND_FIXTURE_UI',JSON.stringify({rows:wrapper.findAll('tbody tr').length,ruleText:wrapper.get('[data-testid="media-pricing-rules"]').text(),actual_cost:actualCost}))
  expect(wrapper.findAll('tbody tr')).toHaveLength(2)
  const rules=wrapper.findAll('[data-testid="media-pricing-rules"]')
  expect(rules[0]!.text()).toContain('1 / mediaPricing.units.image')
  expect(rules[1]!.text()).toContain(`${actualCost} / mediaPricing.units.image`)
  expect(wrapper.findAll('tbody tr')[1]!.findAll('td').at(-1)!.text()).toBe('1.5x')
  wrapper.unmount()
 })
 it.each([{kind:'image',rate:0.5,expected:'1 / mediaPricing.units.request'},{kind:'video',rate:0.8,expected:'1.6 / mediaPricing.units.request'}])('matches endpoint-selected $kind independent rate for per_request rules',tc=>{
  const model={name:'review-media',platform:'openai',pricing:{billing_mode:'per_request' as const,media_pricing:{rules:[{unit:'request' as const,price:2}]},input_price:null,output_price:null,cache_write_price:null,cache_read_price:null,image_input_price:null,image_output_price:null,per_request_price:null,intervals:[]}}
  const wrapper=mount(PlazaModelPricingTable,{props:{models:[model],rateMultiplier:0.1,imageRateIndependent:true,imageRateMultiplier:0.5,videoRateIndependent:true,videoRateMultiplier:0.8}})
  console.log('INDEPENDENT_RATE_UI',JSON.stringify({endpoint:tc.kind,display:wrapper.get('[data-testid="media-pricing-rules"]').text(),backend_actual:tc.kind==='image'?1:1.6}))
  expect(wrapper.get('[data-testid="media-pricing-rules"]').text()).toContain(tc.expected)
  wrapper.unmount()
 })
 it.each(['image','video'] as const)('exposes saved media schedule controls for %s card',mode=>{
  const entry={models:['review-media'],billing_mode:mode,media_pricing:{rules:[{unit:'image' as const,price:2}]},input_price:null,output_price:null,cache_write_price:null,cache_read_price:null,image_input_price:null,image_output_price:null,per_request_price:null,intervals:[],time_pricing:{...createDefaultTimePricingForm(),periods:[{start_time:'00:00:00',end_time:'23:59:59',multiplier:3}]}}
  const wrapper=mount(PricingEntryCard,{props:{entry,enableTimePricing:true},global:{stubs:{ModelTagInput:true,Select:true,Icon:true}}})
  expect(wrapper.findComponent({name:'TimePricingSection'}).exists()).toBe(true)
  wrapper.unmount()
 })
})

const fixture = (name: string) => JSON.parse(readFileSync(root+'/'+name+'.json','utf8'))

describe('production DTO endpoint prices', () => {
 it.each(['image', 'video', 'general', 'token'])('matches actual %s settlement including prompt and input fees once', kind => {
  const {group, actual_cost} = fixture('dto-endpoint-'+kind)
  const wrapper = mount(PlazaModelPricingTable, {props:{models:group.models,rateMultiplier:group.rate_multiplier,userRateMultiplier:group.user_rate_multiplier,imageRateIndependent:group.image_rate_independent,imageRateMultiplier:group.image_rate_multiplier,videoRateIndependent:group.video_rate_independent,videoRateMultiplier:group.video_rate_multiplier}})
  try {
   const options=wrapper.findAll('tbody tr')[1]!.findAll('[data-testid="media-rate-option"]')
   const option = kind==='token' ? options[0]! : options.find(o => o.text().includes('mediaPricing.rate'+kind[0]!.toUpperCase()+kind.slice(1)))!
   const unitPrice = Number(option.text().match(/([\d.]+) \/ mediaPricing.units.request/)![1])
   const promptPrice = Number(option.text().match(/\+([\d.]+) \/ mediaPricing.units.request/)![1])
   const surcharge=option.get('[data-testid="media-request-pricing"]').text()
   const perInput=Number(surcharge.match(/price:([\d.]+)/)![1])
   const allowance=Number(surcharge.match(/count:([\d.]+)/)![1])
   const media=group.models[0].pricing.media_pricing
   const referenceFee=(3-allowance)*perInput
   expect(unitPrice+promptPrice+referenceFee).toBeCloseTo(actual_cost,12)
   expect(media.extra_input_image_price).toBe(0.25)
   expect(media.free_input_images).toBe(1)
   expect(media.duration_source).toBe('output')
   if(kind==='token') expect(options).toHaveLength(1)
   else {expect(options).toHaveLength(3);expect(wrapper.text()).toContain('mediaPricing.endpointRateHint')}
  } finally {wrapper.unmount()}
 })

 it('retains request/token mixed rules and zero prices in endpoint alternatives', () => {
  const group=fixture('ui-plaza-rates-dto')
  const wrapper=mount(PlazaModelPricingTable,{props:{models:group.models,rateMultiplier:999,userRateMultiplier:99}})
  try{
   const options=wrapper.findAll('[data-testid="media-rate-option"]')
   expect(options).toHaveLength(3)
   for (const [index,rate] of [0.2,0.5,0.8].entries()){
    expect(options[index]!.text()).toContain(`${2*rate} / mediaPricing.units.request`)
    expect(options[index]!.text()).toContain('0 / mediaPricing.units.token')
    expect(options[index]!.text()).toContain(`${0.01*rate} / mediaPricing.units.token`)
   }
   expect(wrapper.text()).not.toContain('999x')
   expect(wrapper.text()).not.toContain('99x')
  } finally {wrapper.unmount()}
 })
})
