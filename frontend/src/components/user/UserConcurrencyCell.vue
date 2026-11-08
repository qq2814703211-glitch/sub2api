<template>
  <div class="flex items-center">
    <!-- 数值本身保持原样：并发槽位 current/max。点击展开在途明细。 -->
    <button
      type="button"
      :class="[
        'inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-xs font-medium transition-colors',
        statusClass,
        canExpand ? 'cursor-pointer hover:ring-1 hover:ring-primary-400/60' : 'cursor-default'
      ]"
      :aria-label="canExpand ? t('admin.users.inflight.expandHint') : undefined"
      @click="openDetail"
    >
      <svg class="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
        <path stroke-linecap="round" stroke-linejoin="round" d="M3.75 6A2.25 2.25 0 016 3.75h2.25A2.25 2.25 0 0110.5 6v2.25a2.25 2.25 0 01-2.25 2.25H6a2.25 2.25 0 01-2.25-2.25V6zM3.75 15.75A2.25 2.25 0 016 13.5h2.25a2.25 2.25 0 012.25 2.25V18a2.25 2.25 0 01-2.25 2.25H6A2.25 2.25 0 013.75 18v-2.25zM13.5 6a2.25 2.25 0 012.25-2.25H18A2.25 2.25 0 0120.25 6v2.25A2.25 2.25 0 0118 10.5h-2.25a2.25 2.25 0 01-2.25-2.25V6zM13.5 15.75a2.25 2.25 0 012.25-2.25H18a2.25 2.25 0 012.25 2.25V18A2.25 2.25 0 0118 20.25h-2.25A2.25 2.25 0 0113.5 18v-2.25z" />
      </svg>
      <span class="font-mono">{{ current }}</span>
      <span class="text-gray-500 dark:text-gray-400">/</span>
      <span class="font-mono">{{ max }}</span>
      <HelpTooltip
        v-if="slotHint"
        :content="slotHint"
      />
    </button>

    <BaseDialog :show="showDetail" :title="t('admin.users.inflight.title')" @close="showDetail = false">
      <div class="space-y-3">
        <div class="flex items-center justify-between gap-2">
          <p class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.users.inflight.subtitle') }}
          </p>
          <button
            type="button"
            class="btn btn-secondary btn-sm shrink-0"
            :disabled="loading"
            @click="loadDetail"
          >
            {{ loading ? t('common.loading') : t('common.refresh') }}
          </button>
        </div>

        <div
          v-if="error"
          class="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-900/50 dark:bg-red-900/20 dark:text-red-300"
        >
          {{ error }}
        </div>

        <div
          v-else-if="!loading && groups.length === 0"
          class="rounded-lg border border-gray-200 px-3 py-6 text-center text-xs text-gray-500 dark:border-dark-600 dark:text-gray-400"
        >
          {{ t('admin.users.inflight.empty') }}
        </div>

        <div v-else class="max-h-80 space-y-2 overflow-y-auto">
          <div
            v-for="group in groups"
            :key="group.group_id"
            class="card rounded-xl px-3 py-2"
          >
            <div class="flex items-center justify-between gap-2">
              <div class="flex min-w-0 items-center gap-2">
                <span class="truncate text-sm font-medium text-gray-900 dark:text-white">
                  {{ group.group_name || t('admin.users.inflight.ungrouped') }}
                </span>
                <span
                  class="inline-flex shrink-0 items-center gap-1 rounded-md bg-primary-50 px-1.5 py-0.5 text-[10px] font-medium text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
                >
                  <span class="font-mono">{{ group.inflight }}</span>
                  <span>{{ t('admin.users.inflight.requestsUnit') }}</span>
                </span>
              </div>
              <HelpTooltip
                v-if="group.earliest_started_at"
                :content="t('admin.users.inflight.sinceHint', { time: formatTime(group.earliest_started_at) })"
              />
            </div>
            <div v-if="group.models.length" class="mt-1.5 flex flex-wrap gap-1">
              <span
                v-for="model in group.models"
                :key="model"
                class="inline-flex items-center rounded bg-gray-100 px-1.5 py-0.5 font-mono text-[10px] text-gray-700 dark:bg-dark-700 dark:text-gray-300"
              >
                {{ model }}
              </span>
            </div>
            <div
              v-else
              class="mt-1 text-[10px] text-gray-400 dark:text-gray-500"
            >
              {{ t('admin.users.inflight.modelPending') }}
            </div>
            <div
              v-if="group.earliest_started_at"
              class="mt-1.5 text-[10px] text-gray-500 dark:text-gray-400"
            >
              {{ t('admin.users.inflight.startedAt', { time: formatTime(group.earliest_started_at) }) }}
            </div>
          </div>
        </div>
      </div>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import { getUserInflight, type UserInflightGroupSummary } from '@/api/admin/users'

const props = defineProps<{
  current: number
  max: number
  userId?: number
  // 并发槽位占用数；与 current（在途请求数）是不同口径，仅在两者都有值时提示差异。
  slotConcurrency?: number
  // 在途登记不可用时为 false，此时 current 已回退为槽位数，不应该再声称是在途数。
  inflightAvailable?: boolean
}>()

const { t } = useI18n()

const showDetail = ref(false)
const loading = ref(false)
const error = ref('')
const groups = ref<UserInflightGroupSummary[]>([])

// 没有用户 ID 时无法查在途明细，退回纯展示。
const canExpand = computed(() => typeof props.userId === 'number' && props.userId > 0)

// Status color based on usage
const statusClass = computed(() => {
  const { current, max } = props

  // Full: red
  if (current >= max && max > 0) {
    return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
  }
  // In use: yellow
  if (current > 0) {
    return 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400'
  }
  // Idle: gray
  return 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400'
})

function formatTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  return date.toLocaleString()
}

// 两个数口径不同，必须各自有名字，否则用户只会觉得其中一个是错的：
//   current（左）= 实时在途请求数，与详情弹层的按分组请求数是同一数据源
//   slot（props.slotConcurrency）= 并发槽位占用数，请求准入计数
// 槽位与在途在正常情况下应当相等（都是「一个请求一个成员」），但两者的过期
// 策略不同（槽位按 TTL 清理、在途按请求注销），短暂不一致是正常的瞬态差异。
const slotHint = computed(() => {
  if (!props.inflightAvailable || props.slotConcurrency === undefined) {
    return ''
  }
  if (props.slotConcurrency === props.current) {
    return ''
  }
  return t('admin.users.inflight.slotHint', {
    inflight: props.current,
    slots: props.slotConcurrency
  })
})

async function loadDetail() {
  if (!canExpand.value || !props.userId) {
    return
  }
  loading.value = true
  error.value = ''
  try {
    const data = await getUserInflight(props.userId)
    groups.value = data.groups ?? []
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
    groups.value = []
  } finally {
    loading.value = false
  }
}

function openDetail() {
  if (!canExpand.value) {
    return
  }
  showDetail.value = true
  // 每次打开都重新拉取：在途数据是瞬时快照，缓存没有意义。
  void loadDetail()
}
</script>
