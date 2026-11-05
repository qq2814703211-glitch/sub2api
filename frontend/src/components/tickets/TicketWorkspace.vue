<template>
  <section class="tickets-page">
    <header class="mb-4 flex flex-wrap items-start justify-between gap-3">
      <div class="flex items-start gap-3">
        <span class="flex h-11 w-11 items-center justify-center rounded-xl bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300">
          <Icon :name="admin ? 'inbox' : 'chat'" size="lg" />
        </span>
        <div>
          <h1 class="text-xl font-bold text-gray-900 dark:text-white">{{ t(admin ? 'tickets.adminTitle' : 'tickets.title') }}</h1>
          <p class="mt-0.5 text-sm text-gray-500 dark:text-dark-300">
            {{ t('tickets.count', { count: stats.total }) }}
            <span v-if="stats.unread" class="ml-2 rounded-full bg-rose-100 px-2 py-0.5 text-xs font-semibold text-rose-700 dark:bg-rose-900/40 dark:text-rose-300">
              {{ t('tickets.unread') }} {{ stats.unread }}
            </span>
          </p>
        </div>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <span v-if="syncFailed" class="text-xs font-medium text-amber-600 dark:text-amber-400" role="status">{{ t('tickets.syncFailed') }}</span>
        <button
          type="button"
          class="inline-flex h-9 w-9 items-center justify-center rounded-lg border border-gray-200 bg-white text-gray-600 transition hover:bg-gray-50 disabled:opacity-60 dark:border-dark-700 dark:bg-dark-800 dark:text-dark-200 dark:hover:bg-dark-700"
          :disabled="refreshing"
          :title="t('tickets.refresh')"
          :aria-label="t('tickets.refresh')"
          @click="refresh(true)"
        >
          <Icon name="refresh" size="sm" :class="{ 'animate-spin': refreshing }" />
        </button>
        <button v-if="admin" type="button" class="btn btn-secondary" @click="openSettings">
          <Icon name="cog" size="sm" />{{ t('tickets.notificationSettings') }}
        </button>
        <button v-if="!admin" type="button" class="btn btn-primary" :disabled="loading" @click="newTicketOpen = true">
          <Icon name="plus" size="sm" />{{ t('tickets.newTicket') }}
        </button>
        <button v-else type="button" class="btn btn-primary" @click="openProactive">
          <Icon name="plus" size="sm" />{{ t('tickets.proactiveNew') }}
        </button>
      </div>
    </header>

    <nav class="mb-3 flex flex-wrap gap-1.5" :aria-label="t('tickets.status')">
      <button
        v-for="tab in statusTabs"
        :key="tab || 'all'"
        type="button"
        class="rounded-full px-3 py-1.5 text-sm font-medium transition"
        :class="filters.status === tab
          ? 'bg-primary-600 text-white'
          : 'bg-white text-gray-600 ring-1 ring-gray-200 hover:bg-gray-50 dark:bg-dark-800 dark:text-dark-200 dark:ring-dark-700 dark:hover:bg-dark-700'"
        :aria-pressed="filters.status === tab"
        @click="filters.status = tab"
      >
        {{ tab === 'active' ? t('tickets.notClosed') : tab ? t(`tickets.statuses.${tab}`) : t('tickets.all') }}
      </button>
    </nav>

    <div class="tickets-workspace" :class="{ 'tickets-detail-open': selectedID }">
      <aside class="tickets-inbox" :aria-label="t(admin ? 'tickets.adminTitle' : 'tickets.title')">
        <div class="space-y-2 border-b border-gray-200 p-3 dark:border-dark-700">
          <label class="relative block">
            <span class="sr-only">{{ t('tickets.search') }}</span>
            <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
            <input
              v-model="filters.search"
              type="search"
              maxlength="160"
              class="input w-full pl-9"
              :placeholder="t(admin ? 'tickets.adminSearch' : 'tickets.search')"
            />
          </label>
          <div class="flex gap-2">
            <Select v-model="filters.category" :options="categoryOptions" :aria-label="t('tickets.category')" class="min-w-0 flex-1" />
            <Select
              v-if="admin"
              v-model="filters.assigned_to"
              :options="assigneeOptions"
              :aria-label="t('tickets.assignedTo')"
              class="min-w-0 flex-1"
            />
          </div>
        </div>

        <div class="tickets-list" :aria-busy="loading">
          <div v-if="loading && !items.length" class="space-y-3 p-4" role="status" :aria-label="t('tickets.loading')">
            <div v-for="i in 4" :key="i" class="animate-pulse space-y-2">
              <div class="h-3 w-1/3 rounded bg-gray-200 dark:bg-dark-700"></div>
              <div class="h-3 w-4/5 rounded bg-gray-200 dark:bg-dark-700"></div>
              <div class="h-3 w-2/3 rounded bg-gray-200 dark:bg-dark-700"></div>
            </div>
          </div>
          <div v-else-if="listError" class="p-6 text-center" role="alert">
            <Icon name="exclamationCircle" size="lg" class="mx-auto text-rose-500" />
            <p class="mt-2 text-sm text-gray-600 dark:text-dark-200">{{ listError }}</p>
            <button type="button" class="mt-2 text-sm font-medium text-primary-600 hover:underline" @click="refresh(true)">{{ t('tickets.retry') }}</button>
          </div>
          <div v-else-if="!items.length" class="p-8 text-center">
            <Icon name="inbox" size="xl" class="mx-auto text-gray-300 dark:text-dark-600" />
            <p class="mt-2 text-sm text-gray-500 dark:text-dark-300">{{ t(hasFilters ? 'tickets.emptyFiltered' : 'tickets.empty') }}</p>
            <button v-if="hasFilters" type="button" class="mt-2 text-sm font-medium text-primary-600 hover:underline" @click="resetFilters">{{ t('tickets.resetFilters') }}</button>
            <button v-else-if="!admin" type="button" class="mt-2 text-sm font-medium text-primary-600 hover:underline" @click="newTicketOpen = true">{{ t('tickets.newTicket') }}</button>
          </div>
          <template v-else>
            <button
              v-for="item in items"
              :key="item.id"
              type="button"
              class="ticket-list-item"
              :class="{ 'ticket-list-item-selected': selectedID === item.id, 'ticket-list-item-unread': item.unread_count > 0 }"
              :aria-current="selectedID === item.id ? 'true' : undefined"
              @click="select(item.id)"
            >
              <span class="flex items-center justify-between gap-2 text-xs text-gray-400">
                <span class="font-mono">#{{ item.id }}</span>
                <time :datetime="item.last_message_at" :title="formatDate(item.last_message_at)">{{ shortDate(item.last_message_at) }}</time>
              </span>
              <span class="mt-1 flex items-center gap-2">
                <strong class="min-w-0 flex-1 truncate text-sm text-gray-800 dark:text-dark-100">{{ item.subject }}</strong>
                <span v-if="item.unread_count" class="rounded-full bg-rose-500 px-1.5 py-0.5 text-[10px] font-bold text-white" :aria-label="t('tickets.unread')">
                  {{ item.unread_count > 99 ? '99+' : item.unread_count }}
                </span>
              </span>
              <span class="mt-1 block truncate text-xs text-gray-500 dark:text-dark-400">{{ item.last_message_preview }}</span>
              <span class="mt-2 flex items-center justify-between gap-2">
                <TicketStatusBadge :status="item.status" />
                <span class="truncate text-[11px] text-gray-400">{{ admin ? (item.user_name || item.user_email) : t(`tickets.categories.${item.category}`) }}</span>
              </span>
            </button>
          </template>
        </div>

        <footer class="flex items-center justify-between border-t border-gray-200 px-3 py-2 text-xs text-gray-500 dark:border-dark-700 dark:text-dark-300">
          <span>{{ t('tickets.page', { page: filters.page, pages }) }}</span>
          <div class="flex gap-1">
            <button type="button" class="ticket-page-button" :disabled="filters.page <= 1 || loading" :aria-label="t('tickets.previous')" @click="changePage(filters.page - 1)">
              <Icon name="chevronLeft" size="sm" />
            </button>
            <button type="button" class="ticket-page-button" :disabled="filters.page >= pages || loading" :aria-label="t('tickets.next')" @click="changePage(filters.page + 1)">
              <Icon name="chevronRight" size="sm" />
            </button>
          </div>
        </footer>
      </aside>

      <section class="tickets-detail" :aria-busy="detailLoading">
        <div v-if="!selectedID" class="flex h-full flex-col items-center justify-center gap-2 p-8 text-center">
          <Icon name="chat" size="xl" class="text-gray-300 dark:text-dark-600" />
          <h2 class="text-base font-semibold text-gray-700 dark:text-dark-100">{{ t('tickets.selectTicket') }}</h2>
          <p class="max-w-md text-sm text-gray-500 dark:text-dark-300">{{ t(admin ? 'tickets.adminEmptyHint' : 'tickets.emptyHint') }}</p>
        </div>
        <div v-else-if="detailLoading && !detail" class="flex h-full items-center justify-center gap-2 text-sm text-gray-500">
          <Icon name="refresh" size="sm" class="animate-spin" />{{ t('tickets.loading') }}
        </div>
        <div v-else-if="!detail" class="flex h-full flex-col items-center justify-center gap-2 p-8 text-center" role="alert">
          <Icon name="exclamationCircle" size="lg" class="text-rose-500" />
          <p class="text-sm text-gray-600 dark:text-dark-200">{{ detailError || t('tickets.loadFailed') }}</p>
          <div class="flex gap-3">
            <button type="button" class="text-sm font-medium text-primary-600 hover:underline" @click="refresh(true)">{{ t('tickets.retry') }}</button>
            <button type="button" class="text-sm font-medium text-gray-500 hover:underline" @click="back">{{ t('tickets.back') }}</button>
          </div>
        </div>
        <template v-else>
          <header class="border-b border-gray-200 p-4 dark:border-dark-700">
            <div class="flex items-start gap-2">
              <button type="button" class="tickets-mobile-back ticket-page-button" :aria-label="t('tickets.back')" @click="back">
                <Icon name="arrowLeft" size="sm" />
              </button>
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <span class="font-mono text-xs text-gray-400">#{{ detail.ticket.id }}</span>
                  <TicketStatusBadge :status="detail.ticket.status" />
                  <span class="text-xs text-gray-400">{{ t(`tickets.categories.${detail.ticket.category}`) }}</span>
                </div>
                <h2 class="mt-1 break-words text-lg font-semibold text-gray-900 dark:text-white">{{ detail.ticket.subject }}</h2>
                <div class="mt-1 flex flex-wrap items-center gap-3 text-xs text-gray-500 dark:text-dark-300">
                  <time :datetime="detail.ticket.created_at">{{ formatDate(detail.ticket.created_at) }}</time>
                  <span v-if="detail.ticket.assignee_name">{{ t('tickets.assignee') }}: {{ detail.ticket.assignee_name }}</span>
                  <span v-else-if="admin">{{ t('tickets.unassigned') }}</span>
                  <span v-if="detail.ticket.contact">{{ t('tickets.contact') }}: {{ detail.ticket.contact }}</span>
                </div>
              </div>
              <div class="flex shrink-0 items-center gap-1">
                <button
                  v-if="admin"
                  type="button"
                  class="btn btn-secondary !px-2.5 !py-1.5 text-xs"
                  :disabled="mutating"
                  @click="update({ assigned_to: detail.ticket.assignee_id ? 'unassigned' : 'me' })"
                >
                  {{ detail.ticket.assignee_id ? t('tickets.release') : t('tickets.claim') }}
                </button>
                <button
                  v-if="detail.ticket.status !== 'closed'"
                  type="button"
                  class="ticket-page-button"
                  :disabled="mutating"
                  :title="t('tickets.close')"
                  :aria-label="t('tickets.close')"
                  @click="closeDialogOpen = true"
                >
                  <Icon name="xCircle" size="md" />
                </button>
              </div>
            </div>

            <template v-if="admin && detail.requester">
              <dl class="mt-3 grid grid-cols-2 gap-2 rounded-xl bg-gray-50 p-3 text-xs sm:grid-cols-4 dark:bg-dark-800">
                <div><dt class="text-gray-400">{{ t('tickets.todayTokens') }}</dt><dd class="mt-0.5 font-semibold text-gray-800 dark:text-dark-100">{{ detail.requester.usage_stats_available ? formatTokensK(detail.requester.today_tokens ?? 0) : '—' }}</dd></div>
                <div><dt class="text-gray-400">{{ t('tickets.todayCost') }}</dt><dd class="mt-0.5 font-semibold text-gray-800 dark:text-dark-100">{{ detail.requester.usage_stats_available ? formatCurrency(detail.requester.today_cost ?? 0) : '—' }}</dd></div>
                <div><dt class="text-gray-400">{{ t('tickets.recharged14d') }}</dt><dd class="mt-0.5 font-semibold text-gray-800 dark:text-dark-100">{{ detail.requester.recharge_stats_available ? formatCurrency(detail.requester.recharged_14d ?? 0) : '—' }}</dd></div>
                <div><dt class="text-gray-400">{{ t('tickets.balance') }}</dt><dd class="mt-0.5 font-semibold text-gray-800 dark:text-dark-100">{{ formatCurrency(detail.requester.balance) }}</dd></div>
                <div class="col-span-2 truncate"><dt class="text-gray-400">{{ t('tickets.requester') }}</dt><dd class="mt-0.5 truncate text-gray-700 dark:text-dark-200">{{ detail.requester.username || ('#' + detail.requester.id) }} · {{ detail.requester.email }}</dd></div>
                <div class="col-span-2"><dt class="text-gray-400">{{ t('tickets.accountStatus') }}</dt><dd class="mt-0.5 text-gray-700 dark:text-dark-200">{{ t(`tickets.requesterStatus.${detail.requester.status}`) }}</dd></div>
              </dl>
            </template>
          </header>

          <div v-if="detailError" class="mx-4 mt-3 rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:bg-amber-900/30 dark:text-amber-300" role="alert">
            {{ detailError }}
            <button type="button" class="ml-2 font-semibold underline" @click="refresh(true)">{{ t('tickets.retry') }}</button>
          </div>

          <TicketTimeline
            ref="timeline"
            :messages="detail.messages"
            :admin="admin"
            :has-more="detail.has_more"
            :loading-older="loadingOlder"
            @older="loadOlder"
            @bottom="onBottom"
          />

          <div v-if="!atBottom && hasNewMessages" class="flex justify-center pb-2">
            <button type="button" class="btn btn-secondary text-xs" @click="jumpToLatest">
              <Icon name="arrowDown" size="sm" />{{ t('tickets.newMessages') }}
            </button>
          </div>

          <footer v-if="detail.ticket.status === 'closed'" class="flex items-center justify-between gap-3 border-t border-gray-200 p-4 text-sm dark:border-dark-700">
            <span class="inline-flex items-center gap-1.5 text-gray-500 dark:text-dark-300"><Icon name="lock" size="sm" />{{ t('tickets.closed') }}</span>
            <button type="button" class="btn btn-secondary" :disabled="mutating" @click="update({ status: 'open' })">
              <Icon name="refresh" size="sm" />{{ t('tickets.reopen') }}
            </button>
          </footer>
          <form v-else class="border-t border-gray-200 p-3 dark:border-dark-700" @submit.prevent="sendReply" @keydown="handleComposeKey">
            <label class="sr-only" for="ticket-reply">{{ t('tickets.reply') }}</label>
            <textarea
              id="ticket-reply"
              ref="replyInput"
              v-model="draft"
              rows="3"
              maxlength="10000"
              class="input w-full resize-y"
              :disabled="mutating"
              :placeholder="t('tickets.replyPlaceholder')"
            ></textarea>
            <div class="mt-2 flex flex-wrap items-center justify-between gap-2">
              <span class="text-xs text-gray-400">{{ draft.length ? draft.length + ' / 10000' : t('tickets.sendHint') }}</span>
              <div class="flex items-center gap-2">
                <Select
                  v-if="admin"
                  v-model="adminReplyStatus"
                  :options="replyStatusOptions"
                  :aria-label="t('tickets.status')"
                  class="w-36"
                />
                <button type="submit" class="btn btn-primary" :disabled="mutating || !draft.trim()">
                  <Icon :name="mutating ? 'refresh' : 'arrowUp'" size="sm" :class="{ 'animate-spin': mutating }" />
                  {{ t('tickets.send') }}
                </button>
              </div>
            </div>
          </form>
        </template>
      </section>
    </div>

    <BaseDialog :show="newTicketOpen" :title="t('tickets.newTicket')" width="normal" :close-on-escape="!mutating" :show-close-button="!mutating" @close="newTicketOpen = false">
      <form id="new-ticket-form" class="space-y-4" @submit.prevent="submitTicket">
        <label class="block text-sm font-medium text-gray-700 dark:text-dark-100">{{ t('tickets.subject') }}
          <input v-model="newTicket.subject" class="input mt-1 w-full" maxlength="160" required :placeholder="t('tickets.subjectPlaceholder')" :disabled="mutating" />
        </label>
        <label class="block text-sm font-medium text-gray-700 dark:text-dark-100">{{ t('tickets.category') }}
          <Select v-model="newTicket.category" :options="newTicketCategoryOptions" :disabled="mutating" class="mt-1" />
        </label>
        <label class="block text-sm font-medium text-gray-700 dark:text-dark-100">{{ t('tickets.contact') }}
          <input v-model="newTicket.contact" class="input mt-1 w-full" maxlength="200" :placeholder="t('tickets.contactPlaceholder')" :disabled="mutating" autocomplete="off" />
        </label>
        <label class="block text-sm font-medium text-gray-700 dark:text-dark-100">{{ t('tickets.content') }}
          <textarea v-model="newTicket.content" class="input mt-1 w-full" rows="6" maxlength="10000" required :placeholder="t('tickets.contentPlaceholder')" :disabled="mutating"></textarea>
        </label>
        <p class="text-right text-xs text-gray-400">{{ newTicket.content.length }} / 10000</p>
      </form>
      <template #footer>
        <button type="button" class="btn btn-secondary" :disabled="mutating" @click="newTicketOpen = false">{{ t('tickets.cancel') }}</button>
        <button type="submit" form="new-ticket-form" class="btn btn-primary" :disabled="mutating || !newTicket.subject.trim() || !newTicket.content.trim()">
          <Icon :name="mutating ? 'refresh' : 'plus'" size="sm" :class="{ 'animate-spin': mutating }" />{{ t('tickets.create') }}
        </button>
      </template>
    </BaseDialog>

    <BaseDialog :show="proactiveOpen" :title="t('tickets.proactiveTitle')" width="normal" :close-on-escape="!proactiveSending" :show-close-button="!proactiveSending" @close="closeProactive">
      <form id="proactive-ticket-form" class="space-y-3" @submit.prevent="submitProactiveTicket">
        <p class="rounded-lg bg-primary-50 px-3 py-2 text-xs text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">{{ t('tickets.proactiveHint') }}</p>
        <label class="block text-sm font-medium text-gray-700 dark:text-dark-100">{{ t('tickets.proactiveUser') }}
          <input v-model="proactiveUserSearch" class="input mt-1 w-full" maxlength="120" :placeholder="t('tickets.proactiveUserPlaceholder')" :disabled="proactiveSending" autocomplete="off" @input="onProactiveSearchInput" />
        </label>
        <p v-if="proactiveSearching" class="text-sm text-gray-500">{{ t('tickets.proactiveSearching') }}</p>
        <div v-else-if="proactiveUsers.length" class="max-h-52 overflow-y-auto rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
          <button v-for="candidate in proactiveUsers" :key="candidate.id" type="button" class="flex w-full items-center justify-between gap-3 px-3 py-2 text-left text-sm hover:bg-gray-50 dark:hover:bg-dark-700" @click="pickProactiveUser(candidate)">
            <span class="min-w-0"><strong class="block truncate">{{ candidate.username || '#' + candidate.id }}</strong><span class="block truncate text-xs text-gray-500">{{ candidate.email }}</span></span>
            <span class="shrink-0 text-xs text-gray-400">#{{ candidate.id }}</span>
          </button>
        </div>
        <p v-else-if="proactiveSearched && proactiveUserSearch.trim()" class="text-sm text-gray-500">{{ t('tickets.proactiveNoUsers') }}</p>
        <p v-if="proactiveUser" class="text-sm text-emerald-600 dark:text-emerald-400">{{ t('tickets.proactiveSelected', { user: proactiveUser.username || '#' + proactiveUser.id, email: proactiveUser.email }) }}</p>
        <label class="block text-sm font-medium text-gray-700 dark:text-dark-100">{{ t('tickets.proactiveSubject') }}
          <input v-model="proactiveSubject" class="input mt-1 w-full" maxlength="160" :placeholder="t('tickets.proactiveSubjectPlaceholder')" :disabled="proactiveSending" />
        </label>
        <label class="block text-sm font-medium text-gray-700 dark:text-dark-100">{{ t('tickets.proactiveContent') }}
          <textarea v-model="proactiveContent" class="input mt-1 w-full" rows="6" maxlength="10000" required :placeholder="t('tickets.proactiveContentPlaceholder')" :disabled="proactiveSending"></textarea>
        </label>
        <p class="text-right text-xs text-gray-400">{{ proactiveContent.length }} / 10000</p>
      </form>
      <template #footer>
        <button type="button" class="btn btn-secondary" :disabled="proactiveSending" @click="closeProactive">{{ t('tickets.cancel') }}</button>
        <button type="submit" form="proactive-ticket-form" class="btn btn-primary" :disabled="proactiveSending || !proactiveUser || !proactiveContent.trim()">
          <Icon :name="proactiveSending ? 'refresh' : 'arrowUp'" size="sm" :class="{ 'animate-spin': proactiveSending }" />{{ t('tickets.proactiveSend') }}
        </button>
      </template>
    </BaseDialog>

    <BaseDialog :show="settingsOpen" :title="t('tickets.notificationSettings')" width="normal" :close-on-escape="!settingsSaving" :show-close-button="!settingsSaving" @close="settingsOpen = false">
      <div v-if="notificationSettings" class="space-y-4">
        <label class="flex items-start justify-between gap-4 rounded-lg border border-gray-200 p-3 dark:border-dark-700">
          <span>
            <span class="block text-sm font-medium text-gray-800 dark:text-dark-100">{{ t('tickets.emailEnabled') }}</span>
            <span class="mt-0.5 block text-xs text-gray-500 dark:text-dark-300">{{ t('tickets.emailEnabledHint') }}</span>
          </span>
          <input v-model="settingsForm.enabled" type="checkbox" class="mt-1 h-5 w-5 rounded border-gray-300 text-primary-600 focus:ring-primary-500" :disabled="settingsSaving" />
        </label>
        <label class="block text-sm font-medium text-gray-700 dark:text-dark-100">{{ t('tickets.adminEmail') }}
          <input v-model="settingsForm.admin_email" class="input mt-1 w-full" maxlength="200" :placeholder="t('tickets.adminEmailPlaceholder')" :disabled="settingsSaving" autocomplete="off" />
          <span class="mt-1 block text-xs text-gray-500 dark:text-dark-300">{{ t('tickets.adminEmailHint') }}</span>
        </label>
        <dl class="grid grid-cols-2 gap-2 rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-800">
          <div><dt class="text-gray-400">{{ t('tickets.pendingEmails') }}</dt><dd class="mt-0.5 font-semibold text-gray-800 dark:text-dark-100">{{ notificationSettings.pending_emails }}</dd></div>
          <div><dt class="text-gray-400">{{ t('tickets.failedEmails') }}</dt><dd class="mt-0.5 font-semibold text-gray-800 dark:text-dark-100">{{ notificationSettings.failed_emails }}</dd></div>
        </dl>
        <p class="text-xs text-gray-500 dark:text-dark-300">{{ t('tickets.deliveryNote') }}</p>
      </div>
      <div v-else class="py-6 text-center text-sm text-gray-500">{{ settingsLoading ? t('tickets.loading') : t('tickets.loadFailed') }}</div>
      <template #footer>
        <button type="button" class="btn btn-secondary" :disabled="settingsSaving" @click="settingsOpen = false">{{ t('tickets.cancel') }}</button>
        <button type="button" class="btn btn-primary" :disabled="settingsSaving || !notificationSettings" @click="submitSettings">
          <Icon :name="settingsSaving ? 'refresh' : 'check'" size="sm" :class="{ 'animate-spin': settingsSaving }" />{{ t('tickets.save') }}
        </button>
      </template>
    </BaseDialog>

    <ConfirmDialog :show="closeDialogOpen" :title="t('tickets.closeTitle')" :message="t('tickets.closeMessage')" :confirm-text="t('tickets.close')" @cancel="closeDialogOpen = false" @confirm="closeTicket" />
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, reactive, ref, toRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { useTicketNotificationsStore } from '@/stores/ticketNotifications'
import Icon from '@/components/icons/Icon.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select from '@/components/common/Select.vue'
import TicketStatusBadge from '@/components/tickets/TicketStatusBadge.vue'
import TicketTimeline from '@/components/tickets/TicketTimeline.vue'
import { useTicketWorkspace } from '@/composables/useTicketWorkspace'
import { newClientID, ticketCategories, ticketsAPI, type CreateTicket } from '@/api/tickets'
import { usersAPI } from '@/api/admin/users'
import type { AdminUser } from '@/types'
import { formatCurrency, formatTokensK } from '@/utils/format'

const props = defineProps<{ admin: boolean }>()
const { t, locale } = useI18n()
const app = useAppStore()
const notifications = useTicketNotificationsStore()
const {
  filters, items, pages, stats, detail, selectedID, loading, detailLoading, loadingOlder, refreshing, mutating,
  listError, detailError, syncFailed, revision, draft, notificationSettings, settingsLoading, settingsSaving,
  refresh, older, markRead, send, update, create, loadSettings, saveSettings, resetFilters, select, back, changePage
} = useTicketWorkspace(toRef(props, 'admin'))

const statusTabs = ['', 'active', 'closed'] as const
const hasFilters = computed(() => !!(filters.search || filters.status || filters.category || (props.admin && filters.assigned_to !== 'all')))
const categoryOptions = computed(() => [
  { value: '', label: t('tickets.allCategories') },
  ...ticketCategories.map((category) => ({ value: category, label: t(`tickets.categories.${category}`) }))
])
const newTicketCategoryOptions = computed(() => ticketCategories.map((category) => ({ value: category, label: t(`tickets.categories.${category}`) })))
const assigneeOptions = computed(() => [
  { value: 'all', label: t('tickets.assigneeAll') },
  { value: 'mine', label: t('tickets.assigneeMine') },
  { value: 'unassigned', label: t('tickets.assigneeUnassigned') }
])
const replyStatusOptions = computed(() => [
  { value: 'waiting_user', label: t('tickets.statuses.waiting_user') },
  { value: 'in_progress', label: t('tickets.statuses.in_progress') },
  { value: 'resolved', label: t('tickets.statuses.resolved') }
])
const adminReplyStatus = ref<'waiting_user' | 'in_progress' | 'resolved'>('waiting_user')

const newTicketOpen = ref(false)
const proactiveOpen = ref(false)
const proactiveUserSearch = ref('')
const proactiveUsers = ref<AdminUser[]>([])
const proactiveSearched = ref(false)
const proactiveSearching = ref(false)
const proactiveUser = ref<AdminUser | null>(null)
const proactiveSubject = ref('')
const proactiveContent = ref('')
const proactiveSending = ref(false)
const proactiveClientID = ref(newClientID())
let proactiveFingerprint = ''
let proactiveSearchTimer: ReturnType<typeof setTimeout> | undefined
const closeDialogOpen = ref(false)
const settingsOpen = ref(false)
const settingsForm = reactive({ enabled: true, admin_email: '' })
const replyInput = ref<HTMLTextAreaElement | null>(null)
const timeline = ref<InstanceType<typeof TicketTimeline> | null>(null)
const atBottom = ref(true)
const hasNewMessages = ref(false)
const newTicket = reactive<CreateTicket>({ subject: '', content: '', contact: '', category: 'api', priority: 'normal', client_id: newClientID() })
let lastTicketID = 0
let lastMessageID = 0
let createFingerprint = ''

const dateLocale = computed(() => (locale.value === 'zh' ? 'zh-CN' : 'en-GB'))
const formatDate = (value: string) => new Intl.DateTimeFormat(dateLocale.value, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value))
const shortDate = (value: string) => new Intl.DateTimeFormat(dateLocale.value, { month: '2-digit', day: '2-digit' }).format(new Date(value))

watch(revision, async () => {
  if (!detail.value) return
  const id = detail.value.ticket.id
  const messageID = detail.value.messages.at(-1)?.id || 0
  const initial = lastTicketID !== id
  const shouldScroll = initial || atBottom.value
  if (!initial && messageID > lastMessageID && !atBottom.value) hasNewMessages.value = true
  lastTicketID = id
  lastMessageID = messageID
  await nextTick()
  if (shouldScroll) {
    await timeline.value?.scrollToBottom()
    void markRead()
    void notifications.refresh()
  }
})
watch(selectedID, () => {
  closeDialogOpen.value = false
  atBottom.value = true
  hasNewMessages.value = false
})

function onBottom(bottom: boolean) {
  atBottom.value = bottom
  if (bottom) {
    hasNewMessages.value = false
    void markRead()
    void notifications.refresh()
  }
}
async function jumpToLatest() {
  await timeline.value?.scrollToBottom(true)
  void markRead()
}
async function loadOlder() {
  const id = selectedID.value
  const position = timeline.value?.capturePosition()
  await older()
  if (id === selectedID.value && position) await timeline.value?.restorePosition(position)
}
async function sendReply() {
  const id = selectedID.value
  const status = props.admin ? adminReplyStatus.value : undefined
  if (await send(status)) {
    if (id === selectedID.value) {
      await timeline.value?.scrollToBottom(true)
      replyInput.value?.focus()
    }
    void notifications.refresh()
  }
}
function handleComposeKey(event: KeyboardEvent) {
  if (event.key !== 'Enter' || (!event.metaKey && !event.ctrlKey)) return
  event.preventDefault()
  if (mutating.value || !draft.value.trim()) return
  void sendReply()
}
function openProactive() {
  proactiveOpen.value = true
  proactiveUsers.value = []
  proactiveSearched.value = false
  proactiveClientID.value = newClientID()
  proactiveFingerprint = ''
}
function closeProactive() {
  if (!proactiveSending.value) proactiveOpen.value = false
}
function onProactiveSearchInput() {
  proactiveUser.value = null
  clearTimeout(proactiveSearchTimer)
  proactiveSearchTimer = setTimeout(() => void searchProactiveUsers(), 300)
}
async function searchProactiveUsers() {
  const query = proactiveUserSearch.value.trim()
  proactiveSearched.value = false
  if (!query) {
    proactiveUsers.value = []
    return
  }
  proactiveSearching.value = true
  try {
    const result = await usersAPI.list(1, 10, { search: query })
    proactiveUsers.value = result.items || []
    proactiveSearched.value = true
  } catch {
    proactiveUsers.value = []
  } finally {
    proactiveSearching.value = false
  }
}
function pickProactiveUser(user: AdminUser) {
  proactiveUser.value = user
  proactiveUserSearch.value = `${user.username || '#' + user.id}${user.email ? ' · ' + user.email : ''}`
  proactiveUsers.value = []
  proactiveSearched.value = false
}
async function submitProactiveTicket() {
  if (!proactiveUser.value) {
    app.showError(t('tickets.proactiveSelectUser'))
    return
  }
  if (!proactiveContent.value.trim()) {
    app.showError(t('tickets.proactiveContentRequired'))
    return
  }
  proactiveSending.value = true
  try {
    // Reuse the same id while the message is unchanged so a lost response does
    // not create a duplicate proactive ticket.
    const fingerprint = JSON.stringify([proactiveUser.value.id, proactiveSubject.value, proactiveContent.value])
    if (fingerprint !== proactiveFingerprint) {
      proactiveClientID.value = newClientID()
      proactiveFingerprint = fingerprint
    }
    const created = await ticketsAPI(true).proactiveCreate({
      user_id: proactiveUser.value.id,
      subject: proactiveSubject.value.trim(),
      content: proactiveContent.value.trim(),
      client_id: proactiveClientID.value
    })
    proactiveOpen.value = false
    proactiveUser.value = null
    proactiveUserSearch.value = ''
    proactiveUsers.value = []
    proactiveSubject.value = ''
    proactiveContent.value = ''
    proactiveClientID.value = newClientID()
    proactiveFingerprint = ''
    await refresh(true)
    if (created?.ticket?.id) select(created.ticket.id)
    app.showSuccess(t('tickets.proactiveCreated'))
  } catch {
    app.showError(t('tickets.failed'))
  } finally {
    proactiveSending.value = false
  }
}
async function closeTicket() {
  closeDialogOpen.value = false
  await update({ status: 'closed' })
}
async function submitTicket() {
  if (!newTicket.subject.trim() || !newTicket.content.trim()) {
    app.showError(t('tickets.required'))
    return
  }
  // Reuse the same client_id while the form content is unchanged, so a lost
  // response or double submit cannot create a duplicate conversation.
  const fingerprint = JSON.stringify([newTicket.subject, newTicket.content, newTicket.contact, newTicket.category, newTicket.priority])
  if (fingerprint !== createFingerprint) {
    newTicket.client_id = newClientID()
    createFingerprint = fingerprint
  }
  if (await create({ ...newTicket, subject: newTicket.subject.trim(), content: newTicket.content.trim(), contact: newTicket.contact.trim() })) {
    newTicketOpen.value = false
    Object.assign(newTicket, { subject: '', content: '', contact: '', category: 'api', priority: 'normal', client_id: newClientID() })
    createFingerprint = ''
  }
}
async function openSettings() {
  settingsOpen.value = true
  await loadSettings()
  settingsForm.enabled = notificationSettings.value?.enabled ?? true
  settingsForm.admin_email = notificationSettings.value?.admin_email ?? ''
}
async function submitSettings() {
  if (await saveSettings({ enabled: settingsForm.enabled, admin_email: settingsForm.admin_email.trim() })) {
    settingsOpen.value = false
  }
}

watch(
  () => detail.value?.ticket.id,
  () => {
    adminReplyStatus.value = 'waiting_user'
  }
)
</script>

<style scoped>
.tickets-workspace {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 0.75rem;
  min-height: 32rem;
}
.tickets-inbox,
.tickets-detail {
  display: flex;
  min-height: 0;
  flex-direction: column;
  overflow: hidden;
  border: 1px solid rgb(229 231 235);
  border-radius: 0.9rem;
  background: #fff;
}
.tickets-list {
  min-height: 12rem;
  flex: 1;
  overflow-y: auto;
  overscroll-behavior: contain;
}
.ticket-list-item {
  display: block;
  width: 100%;
  border-bottom: 1px solid rgb(243 244 246);
  padding: 0.75rem 0.9rem;
  text-align: left;
  transition: background-color 120ms ease;
}
.ticket-list-item:hover {
  background: rgb(249 250 251);
}
.ticket-list-item-selected {
  background: rgb(240 249 255);
  box-shadow: inset 3px 0 0 rgb(14 165 233);
}
.ticket-list-item-unread strong {
  font-weight: 700;
}
.ticket-page-button {
  display: inline-flex;
  height: 2rem;
  width: 2rem;
  align-items: center;
  justify-content: center;
  border-radius: 0.5rem;
  border: 1px solid rgb(229 231 235);
  background: #fff;
  color: rgb(75 85 99);
}
.ticket-page-button:hover:not(:disabled) {
  background: rgb(249 250 251);
}
.ticket-page-button:disabled {
  opacity: 0.5;
}
.tickets-mobile-back {
  display: inline-flex;
}
@media (min-width: 1024px) {
  .tickets-workspace {
    grid-template-columns: minmax(280px, 360px) minmax(0, 1fr);
    height: calc(100vh - 13rem);
    min-height: 36rem;
  }
  .tickets-inbox,
  .tickets-detail {
    min-height: 0;
  }
  .tickets-mobile-back {
    display: none;
  }
}
@media (max-width: 1023px) {
  .tickets-detail-open .tickets-inbox {
    display: none;
  }
  .tickets-workspace:not(.tickets-detail-open) .tickets-detail {
    display: none;
  }
  .tickets-detail {
    min-height: 70vh;
  }
}
:global(.dark) .tickets-inbox,
:global(.dark) .tickets-detail {
  border-color: rgb(51 65 85);
  background: rgb(30 41 59);
}
:global(.dark) .ticket-list-item {
  border-color: rgb(51 65 85);
}
:global(.dark) .ticket-list-item:hover {
  background: rgb(51 65 85);
}
:global(.dark) .ticket-list-item-selected {
  background: rgb(12 74 110);
}
:global(.dark) .ticket-page-button {
  border-color: rgb(51 65 85);
  background: rgb(30 41 59);
  color: rgb(203 213 225);
}
</style>
