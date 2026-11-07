import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import ChannelMonitorV3Timeline from '../ChannelMonitorV3Timeline.vue'

// The axis under this component reads "past → now", so the newest bucket has to
// sit flush against the right edge. Padding missing slots on the right (the old
// behaviour) put the newest bar in the middle of the card as soon as a channel
// had fewer filled buckets than the window length — which is exactly the
// "最新柱跑到中间去了" report.

const i18n = createI18n({
  legacy: false,
  locale: 'zh-CN',
  messages: {
    'zh-CN': {
      monitorCommon: { history60pts: '{n} 格', nextUpdateIn: '{n} 秒', past: 'past', now: 'now' },
      channelMonitorV3: { timelineTooltip: '{time} {availability} {cache} {ttft}' },
    },
  },
})

function bucket(offsetMinutes: number) {
  return {
    bucket_start: new Date(Date.UTC(2026, 10, 7, 10, offsetMinutes, 0)).toISOString(),
    health: { overall: 'healthy' },
    metrics: {
      error_rate: 0,
      cache_rate: 0.5,
      ttft: { p50_ms: 200 },
    },
  }
}

function mountTimeline(buckets: ReturnType<typeof bucket>[], length: number) {
  return mount(ChannelMonitorV3Timeline, {
    props: { buckets, length, countdownSeconds: 60 },
    global: { plugins: [i18n] },
  })
}

function slotKeys(wrapper: ReturnType<typeof mountTimeline>): string[] {
  return wrapper.findAll('.v3-bar-slot').map(slot => slot.find('.v3-bar-hitbox').attributes('aria-label') ?? '')
}

describe('ChannelMonitorV3Timeline bucket alignment', () => {
  it('pads missing slots on the past side so the newest bar touches the right edge', () => {
    const wrapper = mountTimeline([bucket(0), bucket(5), bucket(10)], 6)
    const labels = slotKeys(wrapper)
    expect(labels).toHaveLength(6)

    // First three slots are the empty/past padding (no tooltip → aria-label "-").
    expect(labels.slice(0, 3).every(label => label === '-')).toBe(true)
    // The three real buckets keep their chronological order in the last slots.
    expect(labels.slice(3).every(label => label !== '-')).toBe(true)
  })

  it('fills every slot when buckets already match the window length', () => {
    const buckets = Array.from({ length: 18 }, (_, i) => bucket(i * 5))
    const wrapper = mountTimeline(buckets, 18)
    expect(slotKeys(wrapper).every(label => label !== '-')).toBe(true)
  })

  it('trims to the newest buckets when more are supplied than the window', () => {
    const buckets = Array.from({ length: 10 }, (_, i) => bucket(i * 5))
    const wrapper = mountTimeline(buckets, 3)
    const labels = slotKeys(wrapper)
    expect(labels).toHaveLength(3)
    expect(labels.every(label => label !== '-')).toBe(true)
  })
})
