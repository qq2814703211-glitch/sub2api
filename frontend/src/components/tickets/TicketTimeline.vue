<template>
  <div
    ref="root"
    class="min-h-0 flex-1 basis-auto overflow-y-auto overscroll-contain px-4 py-4 sm:px-5"
    role="log"
    aria-live="polite"
    @scroll="onScroll"
  >
    <div v-if="hasMore" class="flex justify-center pb-3">
      <button type="button" class="btn btn-secondary btn-sm" :disabled="loadingOlder" @click="emit('older')">
        {{ loadingOlder ? t('tickets.loadingOlder') : t('tickets.loadOlder') }}
      </button>
    </div>

    <template v-for="message in messages" :key="message.id">
      <div v-if="message.kind === 'event'" class="my-3 flex justify-center">
        <span class="max-w-full rounded-full border border-gray-200 bg-gray-50 px-3 py-1 text-center text-xs text-gray-500 dark:border-dark-800 dark:bg-dark-800/60 dark:text-dark-300">
          {{ eventText(message) }}
        </span>
      </div>
      <article v-else class="mb-4 flex" :class="message.author_role === 'admin' ? 'justify-start' : 'justify-end'">
        <div class="max-w-[85%] sm:max-w-[75%]">
          <div class="mb-1 flex items-center gap-2 text-xs text-gray-500 dark:text-dark-400" :class="message.author_role === 'admin' ? '' : 'justify-end'">
            <span class="font-medium text-gray-700 dark:text-gray-300">
              {{ message.author_role === 'admin' ? t('tickets.support') : (message.author_name || t('tickets.you')) }}
            </span>
            <time :datetime="message.created_at">{{ formatDate(message.created_at) }}</time>
          </div>
          <div
            class="whitespace-pre-wrap break-words rounded-2xl px-4 py-3 text-sm leading-relaxed"
            :class="message.author_role === 'admin'
              ? 'rounded-tl-sm border border-gray-200 bg-gray-50 text-gray-800 dark:border-dark-800 dark:bg-dark-800 dark:text-gray-100'
              : 'rounded-tr-sm bg-primary-700 text-white dark:bg-primary-700'"
          >{{ message.content }}</div>
        </div>
      </article>
    </template>
  </div>
</template>

<script setup lang="ts">
import { nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TicketMessage } from '@/api/tickets'

defineProps<{
  messages: TicketMessage[]
  admin: boolean
  hasMore: boolean
  loadingOlder: boolean
}>()
const emit = defineEmits<{ older: []; bottom: [value: boolean] }>()
const { t, locale } = useI18n()
const root = ref<HTMLElement | null>(null)

const dateLocale = () => (locale.value === 'zh' ? 'zh-CN' : 'en-GB')
const formatDate = (value: string) =>
  new Intl.DateTimeFormat(dateLocale(), { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value))

const statusLabel = (value: unknown) => {
  const status = typeof value === 'string' ? value : ''
  return status ? t(`tickets.statuses.${status}`) : '—'
}
const priorityLabel = (value: unknown) => {
  const priority = typeof value === 'string' ? value : ''
  return priority ? t(`tickets.priorities.${priority}`) : '—'
}

function eventText(message: TicketMessage): string {
  const from = message.event_data?.from
  const to = message.event_data?.to
  switch (message.event_type) {
    case 'status_changed':
      return t('tickets.events.statusChanged', { from: statusLabel(from), to: statusLabel(to) })
    case 'priority_changed':
      return t('tickets.events.priorityChanged', { from: priorityLabel(from), to: priorityLabel(to) })
    case 'assignment_changed':
      return to === null || to === undefined
        ? t('tickets.events.unassigned')
        : t('tickets.events.assigned', { target: typeof to === 'number' ? `#${to}` : String(to) })
    default:
      return message.content
  }
}

function onScroll() {
  const element = root.value
  if (!element) return
  emit('bottom', element.scrollHeight - element.scrollTop - element.clientHeight < 48)
}

async function scrollToBottom(smooth = false) {
  await nextTick()
  const element = root.value
  if (!element) return
  element.scrollTo({ top: element.scrollHeight, behavior: smooth ? 'smooth' : 'auto' })
}

function capturePosition() {
  const element = root.value
  return element ? { scrollTop: element.scrollTop, scrollHeight: element.scrollHeight } : null
}

async function restorePosition(position: { scrollTop: number; scrollHeight: number }) {
  await nextTick()
  const element = root.value
  if (!element) return
  element.scrollTop = element.scrollHeight - position.scrollHeight + position.scrollTop
}

defineExpose({ scrollToBottom, capturePosition, restorePosition })
</script>
