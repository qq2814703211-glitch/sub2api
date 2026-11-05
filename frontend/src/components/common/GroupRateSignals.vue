<template>
  <span
    v-if="items.length > 0"
    class="inline-flex shrink-0 items-center gap-1"
    data-group-signals
    data-testid="group-rate-signals"
  >
    <template v-for="item in items" :key="item.kind">
      <button
        v-if="interactive"
        type="button"
        :class="[chipClass, item.tone, interactiveClass]"
        :aria-label="item.aria"
        :data-testid="item.testid"
        :data-direction="item.direction"
        @pointerenter="onPointerEnter($event, item)"
        @pointerleave="onPointerLeave"
        @pointerup="onPointerUp($event, item)"
        @focus="onFocus($event, item)"
        @blur="closeDetail"
        @keydown.escape.stop="closeDetail"
      >
        <svg
          v-if="item.direction"
          class="h-2.5 w-2.5"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="3"
          aria-hidden="true"
        >
          <path
            v-if="item.direction === 'up'"
            stroke-linecap="round"
            stroke-linejoin="round"
            d="M12 19V5M5 12l7-7 7 7"
          />
          <path
            v-else
            stroke-linecap="round"
            stroke-linejoin="round"
            d="M12 5v14M5 12l7 7 7-7"
          />
        </svg>
        <span aria-hidden="true">{{ item.label }}</span>
      </button>
      <!-- 在外层按钮/选项中嵌套控件无效，这里保持带标签的被动徽标。 -->
      <span
        v-else
        role="img"
        :class="[chipClass, item.tone]"
        :aria-label="item.aria"
        :title="`${item.title} · ${item.detail}`"
        :data-testid="item.testid"
        :data-direction="item.direction"
      >
        <svg
          v-if="item.direction"
          class="h-2.5 w-2.5"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="3"
          aria-hidden="true"
        >
          <path
            v-if="item.direction === 'up'"
            stroke-linecap="round"
            stroke-linejoin="round"
            d="M12 19V5M5 12l7-7 7 7"
          />
          <path
            v-else
            stroke-linecap="round"
            stroke-linejoin="round"
            d="M12 5v14M5 12l7 7 7-7"
          />
        </svg>
        <span aria-hidden="true">{{ item.label }}</span>
      </span>
    </template>

    <Teleport to="body">
      <div
        v-if="openItem"
        role="tooltip"
        aria-hidden="true"
        :data-testid="`${openItem.testid}-detail`"
        :style="detailStyle"
        class="pointer-events-none fixed z-[99999] w-max max-w-56 space-y-0.5 rounded-lg bg-gray-900 px-2.5 py-2 text-xs leading-snug text-white shadow-xl ring-1 ring-white/10 dark:bg-gray-800"
      >
        <p class="font-medium text-white">{{ openItem.title }}</p>
        <p class="text-gray-300">{{ openItem.detail }}</p>
      </div>
    </Teleport>
  </span>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { GroupSignalSource } from '@/utils/groupRateSignals'
import { formatRateValue, formatSignalTime } from '@/utils/groupRateSignals'

/**
 * 分组倍率旁的紧凑提示：近期基础倍率变化的箭头 + 差值，以及 24h 内上线
 * 分组的 NEW 徽标。详情在指针悬停、键盘聚焦时打开，触屏点按切换。
 */
const props = withDefaults(defineProps<{
  /** 通常是 GET /groups/available 返回的分组。 */
  signals?: GroupSignalSource | null
  /** 订阅分组不展示每 token 倍率，箭头没有指向对象。 */
  showRateChange?: boolean
  /** 在外层按钮或选项内时传 false，避免嵌套可交互控件。 */
  interactive?: boolean
}>(), {
  signals: null,
  showRateChange: true,
  interactive: true,
})

interface SignalItem {
  kind: 'rate' | 'new'
  testid: string
  direction?: 'up' | 'down'
  label: string
  tone: string
  title: string
  detail: string
  aria: string
}

const { t, locale } = useI18n()

const chipClass =
  'inline-flex min-h-4 min-w-4 items-center justify-center gap-0.5 rounded-md px-1.5 py-0.5 font-mono text-[10px] font-semibold leading-none tabular-nums'
// before:-inset-1.5 扩大触控目标，不改变 16px 徽标的外观。
const interactiveClass =
  'relative cursor-help touch-manipulation transition-colors before:absolute before:-inset-1.5 focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-current motion-reduce:transition-none'

const items = computed<SignalItem[]>(() => {
  const source = props.signals
  if (!source) return []
  const result: SignalItem[] = []
  const lang = locale?.value

  const change = props.showRateChange ? source.recent_rate_change : null
  if (change) {
    const up = change.direction === 'up'
    const params = {
      delta: formatRateValue(change.delta),
      previous: formatRateValue(change.previous_rate),
      time: formatSignalTime(change.changed_at, lang),
    }
    result.push({
      kind: 'rate',
      testid: 'group-rate-signal-rate',
      direction: change.direction,
      label: params.delta,
      // 上调用警示色，下调用优惠色；都与峰时倍率的琥珀色提示文案区分开。
      tone: up
        ? 'bg-amber-50 text-amber-700 dark:bg-amber-900/20 dark:text-amber-300'
        : 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300',
      title: up ? t('groupSignals.rateUpTitle', params) : t('groupSignals.rateDownTitle', params),
      detail: t('groupSignals.changedAt', params),
      aria: up ? t('groupSignals.ariaRateUp', params) : t('groupSignals.ariaRateDown', params),
    })
  }

  if (source.is_new) {
    const time = formatSignalTime(source.launched_at, lang)
    const rate = typeof source.launch_rate === 'number' ? formatRateValue(source.launch_rate) : null
    result.push({
      kind: 'new',
      testid: 'group-rate-signal-new',
      label: t('groupSignals.newBadge'),
      // 细描边把 NEW 与相邻的用户专属倍率徽标区分开（两者都用主色）。
      tone: 'bg-primary-50 text-primary-700 ring-1 ring-inset ring-primary-200 dark:bg-primary-900/20 dark:text-primary-300',
      title: t('groupSignals.newTitle'),
      detail: rate === null
        ? t('groupSignals.launchedAt', { time })
        : t('groupSignals.launchedAtRate', { time, rate }),
      aria: rate === null
        ? t('groupSignals.ariaNew', { time })
        : t('groupSignals.ariaNewRate', { time, rate }),
    })
  }
  return result
})

const openKind = ref<'rate' | 'new' | null>(null)
const openItem = computed(() => items.value.find((item) => item.kind === openKind.value) ?? null)
const activeTrigger = ref<HTMLElement | null>(null)
const detailStyle = ref<{ top: string; left: string; transform: string }>({
  top: '0px',
  left: '0px',
  transform: 'translate(-50%, -100%)',
})

let openTimer: ReturnType<typeof setTimeout> | null = null
let closeTimer: ReturnType<typeof setTimeout> | null = null

function clearTimers() {
  if (openTimer !== null) {
    clearTimeout(openTimer)
    openTimer = null
  }
  if (closeTimer !== null) {
    clearTimeout(closeTimer)
    closeTimer = null
  }
}

function updatePosition() {
  const el = activeTrigger.value
  if (!el || typeof window === 'undefined') return
  const rect = el.getBoundingClientRect()
  const below = rect.top < 96
  const half = 112
  const left = Math.min(Math.max(rect.left + rect.width / 2, half), Math.max(window.innerWidth - half, half))
  detailStyle.value = {
    top: `${below ? rect.bottom + 8 : rect.top - 8}px`,
    left: `${left}px`,
    transform: below ? 'translate(-50%, 0)' : 'translate(-50%, -100%)',
  }
}

function openDetail(item: SignalItem) {
  clearTimers()
  openKind.value = item.kind
  void nextTick(updatePosition)
}

function closeDetail() {
  clearTimers()
  openKind.value = null
}

function scheduleOpen(item: SignalItem) {
  clearTimers()
  openTimer = setTimeout(() => openDetail(item), 120)
}

function scheduleClose() {
  clearTimers()
  closeTimer = setTimeout(closeDetail, 120)
}

function onPointerEnter(event: PointerEvent, item: SignalItem) {
  if (event.pointerType === 'touch') return
  activeTrigger.value = event.currentTarget as HTMLElement
  scheduleOpen(item)
}

function onPointerLeave() {
  if (openKind.value === null) {
    clearTimers()
    return
  }
  scheduleClose()
}

function onPointerUp(event: PointerEvent, item: SignalItem) {
  if (event.pointerType !== 'touch') return
  activeTrigger.value = event.currentTarget as HTMLElement
  if (openKind.value === item.kind) {
    closeDetail()
    return
  }
  openDetail(item)
}

function onFocus(event: FocusEvent, item: SignalItem) {
  activeTrigger.value = event.currentTarget as HTMLElement
  openDetail(item)
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') closeDetail()
}

onMounted(() => {
  document.addEventListener('keydown', onKeydown)
  window.addEventListener('resize', updatePosition)
  window.addEventListener('scroll', updatePosition, true)
})

onBeforeUnmount(() => {
  clearTimers()
  document.removeEventListener('keydown', onKeydown)
  window.removeEventListener('resize', updatePosition)
  window.removeEventListener('scroll', updatePosition, true)
})
</script>
