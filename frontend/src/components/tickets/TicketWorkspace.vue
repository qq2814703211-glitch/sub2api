<template>
  <TablePageLayout>
    <!-- Filters: search, scope, and the page's actions -->
    <template #filters>
      <div class="flex flex-col gap-3">
        <div class="flex flex-wrap items-center gap-3">
          <div class="relative w-full sm:w-64">
            <label class="sr-only" for="ticket-search">{{ t('tickets.search') }}</label>
            <Icon
              name="search"
              size="md"
              class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-500 dark:text-gray-400"
            />
            <input
              id="ticket-search"
              v-model="filters.search"
              type="search"
              maxlength="160"
              class="input pl-10"
              :placeholder="t(admin ? 'tickets.adminSearch' : 'tickets.search')"
            />
          </div>
          <Select
            v-model="filters.category"
            :options="categoryOptions"
            :aria-label="t('tickets.category')"
            class="w-full sm:w-40"
          />
          <Select
            v-if="admin"
            v-model="filters.assigned_to"
            :options="assigneeOptions"
            :aria-label="t('tickets.assignedTo')"
            class="w-full sm:w-40"
          />

          <div class="flex w-full flex-wrap items-center gap-2 sm:ml-auto sm:w-auto sm:gap-3">
            <span
              v-if="syncFailed"
              class="text-xs font-medium text-amber-600 dark:text-amber-400"
              role="status"
            >{{ t('tickets.syncFailed') }}</span>
            <button
              type="button"
              class="btn btn-secondary btn-icon"
              :disabled="refreshing"
              :title="t('tickets.refresh')"
              :aria-label="t('tickets.refresh')"
              @click="refresh(true)"
            >
              <Icon name="refresh" size="md" :class="{ 'animate-spin': refreshing }" />
            </button>
            <button v-if="admin" type="button" class="btn btn-secondary" @click="openSettings">
              <Icon name="cog" size="md" />{{ t('tickets.notificationSettings') }}
            </button>
            <button
              v-if="!admin"
              type="button"
              class="btn btn-primary"
              :disabled="loading"
              @click="newTicketOpen = true"
            >
              <Icon name="plus" size="md" />{{ t('tickets.newTicket') }}
            </button>
            <button v-else type="button" class="btn btn-primary" @click="openProactive">
              <Icon name="plus" size="md" />{{ t('tickets.proactiveNew') }}
            </button>
          </div>
        </div>

        <div class="flex flex-wrap items-center justify-between gap-3">
          <nav class="tabs max-w-full overflow-x-auto" :aria-label="t('tickets.status')">
            <button
              v-for="tab in statusTabs"
              :key="tab || 'all'"
              type="button"
              class="tab whitespace-nowrap"
              :class="{ 'tab-active': filters.status === tab }"
              :aria-pressed="filters.status === tab"
              @click="filters.status = tab"
            >
              {{ tab === 'active' ? t('tickets.notClosed') : tab ? t(`tickets.statuses.${tab}`) : t('tickets.all') }}
            </button>
          </nav>
          <p class="flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-dark-400">
            <span>{{ t('tickets.count', { count: stats.total }) }}</span>
            <span v-if="stats.unread" class="badge badge-danger">
              {{ t('tickets.unread') }} {{ stats.unread }}
            </span>
          </p>
        </div>
      </div>
    </template>

    <!-- Table slot: inbox on the left, conversation on the right -->
    <template #table>
      <div class="flex h-full min-h-0 flex-col lg:flex-row">
        <aside
          class="min-h-0 flex-col border-b border-gray-200 dark:border-dark-800 lg:flex lg:w-[340px] lg:shrink-0 lg:border-b-0 lg:border-r"
          :class="selectedID ? 'hidden lg:flex' : 'flex'"
          :aria-label="t(admin ? 'tickets.adminTitle' : 'tickets.title')"
        >
          <div class="min-h-0 flex-1 overflow-y-auto overscroll-contain" :aria-busy="loading">
            <div v-if="loading && !items.length" class="space-y-4 p-4" role="status" :aria-label="t('tickets.loading')">
              <div v-for="i in 4" :key="i" class="space-y-2">
                <div class="skeleton h-3 w-1/3"></div>
                <div class="skeleton h-3 w-4/5"></div>
                <div class="skeleton h-3 w-2/3"></div>
              </div>
            </div>
            <div
              v-else-if="listError"
              class="flex min-h-[12rem] flex-col items-center justify-center gap-2 px-4 py-10 text-center"
              role="alert"
            >
              <Icon name="exclamationCircle" size="lg" class="text-rose-500" />
              <p class="text-sm text-gray-600 dark:text-gray-300">{{ listError }}</p>
              <button type="button" class="btn btn-secondary btn-sm mt-1" @click="refresh(true)">
                {{ t('tickets.retry') }}
              </button>
            </div>
            <div
              v-else-if="!items.length"
              class="flex min-h-[12rem] flex-col items-center justify-center gap-2 px-4 py-10 text-center"
            >
              <Icon name="inbox" size="xl" class="mb-1 h-10 w-10 text-gray-300 dark:text-dark-600" />
              <p class="text-sm font-medium text-gray-600 dark:text-gray-300">
                {{ t(hasFilters ? 'tickets.emptyFiltered' : 'tickets.empty') }}
              </p>
              <button
                v-if="hasFilters"
                type="button"
                class="btn btn-secondary btn-sm mt-1"
                @click="resetFilters"
              >
                {{ t('tickets.resetFilters') }}
              </button>
              <button
                v-else-if="!admin"
                type="button"
                class="btn btn-primary btn-sm mt-1"
                @click="newTicketOpen = true"
              >
                {{ t('tickets.newTicket') }}
              </button>
            </div>

            <template v-else>
              <button
                v-for="item in items"
                :key="item.id"
                type="button"
                class="flex w-full flex-col gap-1.5 border-b border-gray-100 px-4 py-3 text-left transition-colors duration-150 last:border-b-0 dark:border-dark-800/70"
                :class="selectedID === item.id
                  ? 'bg-primary-50 ring-1 ring-inset ring-primary-200 dark:bg-primary-900/30 dark:ring-primary-800/60'
                  : 'hover:bg-gray-50 dark:hover:bg-dark-800/60'"
                :aria-current="selectedID === item.id ? 'true' : undefined"
                @click="select(item.id)"
              >
                <span class="flex items-center justify-between gap-2">
                  <span class="font-mono text-xs text-gray-500 dark:text-dark-400">#{{ item.id }}</span>
                  <time
                    class="text-xs text-gray-500 dark:text-dark-400"
                    :datetime="item.last_message_at"
                    :title="formatDate(item.last_message_at)"
                  >{{ shortDate(item.last_message_at) }}</time>
                </span>
                <span class="flex items-center gap-2">
                  <strong
                    class="min-w-0 flex-1 truncate text-sm text-gray-900 dark:text-white"
                    :class="{ 'font-semibold': item.unread_count > 0 }"
                  >{{ item.subject }}</strong>
                  <span
                    v-if="item.unread_count"
                    class="shrink-0 rounded-full bg-rose-500 px-1.5 text-[10px] font-bold leading-4 text-white"
                    :aria-label="t('tickets.unread')"
                  >{{ item.unread_count > 99 ? '99+' : item.unread_count }}</span>
                </span>
                <span class="block truncate text-xs text-gray-500 dark:text-dark-400">{{ item.last_message_preview }}</span>
                <span class="mt-0.5 flex items-center justify-between gap-2">
                  <TicketStatusBadge :status="item.status" />
                  <span class="truncate text-xs text-gray-500 dark:text-dark-400">
                    {{ admin ? (item.user_name || item.user_email) : t(`tickets.categories.${item.category}`) }}
                  </span>
                </span>
              </button>
            </template>
          </div>

          <footer class="flex items-center justify-between gap-2 border-t border-gray-200 px-4 py-2.5 dark:border-dark-800">
            <span class="text-xs text-gray-500 dark:text-dark-400">{{ t('tickets.page', { page: filters.page, pages }) }}</span>
            <div class="flex gap-1.5">
              <button
                type="button"
                class="btn btn-secondary !p-2"
                :disabled="filters.page <= 1 || loading"
                :aria-label="t('tickets.previous')"
                @click="changePage(filters.page - 1)"
              >
                <Icon name="chevronLeft" size="sm" />
              </button>
              <button
                type="button"
                class="btn btn-secondary !p-2"
                :disabled="filters.page >= pages || loading"
                :aria-label="t('tickets.next')"
                @click="changePage(filters.page + 1)"
              >
                <Icon name="chevronRight" size="sm" />
              </button>
            </div>
          </footer>
        </aside>

        <section
          class="min-h-[70vh] min-w-0 flex-col lg:min-h-0 lg:flex-1"
          :class="selectedID ? 'flex' : 'hidden lg:flex'"
          :aria-busy="detailLoading"
        >
          <div v-if="!selectedID" class="empty-state h-full">
            <Icon name="chatBubble" size="xl" class="mb-4 h-12 w-12 text-gray-300 dark:text-dark-600" />
            <h2 class="empty-state-title">{{ t('tickets.selectTicket') }}</h2>
            <p class="empty-state-description">{{ t(admin ? 'tickets.adminEmptyHint' : 'tickets.emptyHint') }}</p>
          </div>
          <div
            v-else-if="detailLoading && !detail"
            class="flex h-full items-center justify-center gap-2 text-sm text-gray-500 dark:text-dark-400"
          >
            <Icon name="refresh" size="sm" class="animate-spin" />{{ t('tickets.loading') }}
          </div>
          <div v-else-if="!detail" class="empty-state h-full" role="alert">
            <Icon name="exclamationCircle" size="xl" class="mb-4 h-12 w-12 text-rose-500" />
            <p class="empty-state-description">{{ detailError || t('tickets.loadFailed') }}</p>
            <div class="mt-3 flex gap-3">
              <button type="button" class="btn btn-secondary btn-sm" @click="refresh(true)">{{ t('tickets.retry') }}</button>
              <button type="button" class="btn btn-ghost btn-sm" @click="back">{{ t('tickets.back') }}</button>
            </div>
          </div>
          <template v-else>
            <header class="flex flex-col gap-3 border-b border-gray-200 p-4 dark:border-dark-800 sm:p-5">
              <div class="flex items-start gap-3">
                <button
                  type="button"
                  class="btn btn-secondary btn-icon shrink-0 lg:hidden"
                  :aria-label="t('tickets.back')"
                  @click="back"
                >
                  <Icon name="arrowLeft" size="md" />
                </button>
                <div class="min-w-0 flex-1">
                  <div class="flex flex-wrap items-center gap-2">
                    <span class="font-mono text-xs text-gray-500 dark:text-dark-400">#{{ detail.ticket.id }}</span>
                    <TicketStatusBadge :status="detail.ticket.status" />
                    <span class="text-xs text-gray-500 dark:text-dark-400">
                      {{ t(`tickets.categories.${detail.ticket.category}`) }}
                    </span>
                  </div>
                  <h2 class="mt-1.5 break-words text-lg font-semibold text-gray-900 dark:text-white">
                    {{ detail.ticket.subject }}
                  </h2>
                  <div class="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-gray-500 dark:text-dark-400">
                    <time :datetime="detail.ticket.created_at">{{ formatDate(detail.ticket.created_at) }}</time>
                    <span v-if="detail.ticket.assignee_name">{{ t('tickets.assignee') }}: {{ detail.ticket.assignee_name }}</span>
                    <span v-else-if="admin">{{ t('tickets.unassigned') }}</span>
                    <span v-if="detail.ticket.contact">{{ t('tickets.contact') }}: {{ detail.ticket.contact }}</span>
                  </div>
                </div>
                <div class="flex shrink-0 items-center gap-2">
                  <button
                    v-if="admin"
                    type="button"
                    class="btn btn-secondary btn-sm"
                    :disabled="mutating"
                    @click="update({ assigned_to: detail.ticket.assignee_id ? 'unassigned' : 'me' })"
                  >
                    {{ detail.ticket.assignee_id ? t('tickets.release') : t('tickets.claim') }}
                  </button>
                  <button
                    v-if="detail.ticket.status !== 'closed'"
                    type="button"
                    class="btn btn-secondary btn-icon"
                    :disabled="mutating"
                    :title="t('tickets.close')"
                    :aria-label="t('tickets.close')"
                    @click="closeDialogOpen = true"
                  >
                    <Icon name="xCircle" size="md" />
                  </button>
                </div>
              </div>

              <dl
                v-if="admin && detail.requester"
                class="grid grid-cols-2 gap-3 rounded-lg border border-gray-200 bg-gray-50 p-3 text-xs dark:border-dark-800 dark:bg-dark-800/50 sm:grid-cols-4"
              >
                <div>
                  <dt class="text-gray-500 dark:text-dark-400">{{ t('tickets.todayTokens') }}</dt>
                  <dd class="mt-0.5 font-semibold text-gray-900 dark:text-white">
                    {{ detail.requester.usage_stats_available ? formatTokensK(detail.requester.today_tokens ?? 0) : '—' }}
                  </dd>
                </div>
                <div>
                  <dt class="text-gray-500 dark:text-dark-400">{{ t('tickets.todayCost') }}</dt>
                  <dd class="mt-0.5 font-semibold text-gray-900 dark:text-white">
                    {{ detail.requester.usage_stats_available ? formatCurrency(detail.requester.today_cost ?? 0) : '—' }}
                  </dd>
                </div>
                <div>
                  <dt class="text-gray-500 dark:text-dark-400">{{ t('tickets.recharged14d') }}</dt>
                  <dd class="mt-0.5 font-semibold text-gray-900 dark:text-white">
                    {{ detail.requester.recharge_stats_available ? formatCurrency(detail.requester.recharged_14d ?? 0) : '—' }}
                  </dd>
                </div>
                <div>
                  <dt class="text-gray-500 dark:text-dark-400">{{ t('tickets.balance') }}</dt>
                  <dd class="mt-0.5 font-semibold text-gray-900 dark:text-white">{{ formatCurrency(detail.requester.balance) }}</dd>
                </div>
                <div class="col-span-2 min-w-0">
                  <dt class="text-gray-500 dark:text-dark-400">{{ t('tickets.requester') }}</dt>
                  <dd class="mt-0.5 truncate text-gray-700 dark:text-gray-200">
                    {{ detail.requester.username || ('#' + detail.requester.id) }} · {{ detail.requester.email }}
                  </dd>
                </div>
                <div class="col-span-2">
                  <dt class="text-gray-500 dark:text-dark-400">{{ t('tickets.accountStatus') }}</dt>
                  <dd class="mt-0.5 text-gray-700 dark:text-gray-200">
                    {{ t(`tickets.requesterStatus.${detail.requester.status}`) }}
                  </dd>
                </div>
              </dl>
            </header>

            <div
              v-if="detailError"
              class="mx-4 mt-3 flex flex-wrap items-center gap-2 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:border-amber-900/50 dark:bg-amber-900/30 dark:text-amber-300 sm:mx-5"
              role="alert"
            >
              <span>{{ detailError }}</span>
              <button type="button" class="font-semibold underline" @click="refresh(true)">{{ t('tickets.retry') }}</button>
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

            <div v-if="!atBottom && hasNewMessages" class="flex justify-center py-2">
              <button type="button" class="btn btn-secondary btn-sm" @click="jumpToLatest">
                <Icon name="arrowDown" size="sm" />{{ t('tickets.newMessages') }}
              </button>
            </div>

            <footer
              v-if="detail.ticket.status === 'closed'"
              class="flex items-center justify-between gap-3 border-t border-gray-200 p-4 dark:border-dark-800"
            >
              <span class="inline-flex items-center gap-1.5 text-sm text-gray-500 dark:text-dark-400">
                <Icon name="lock" size="sm" />{{ t('tickets.closed') }}
              </span>
              <button type="button" class="btn btn-secondary" :disabled="mutating" @click="update({ status: 'open' })">
                <Icon name="refresh" size="sm" />{{ t('tickets.reopen') }}
              </button>
            </footer>
            <form
              v-else
              class="border-t border-gray-200 p-3 dark:border-dark-800 sm:p-4"
              @submit.prevent="sendReply"
              @keydown="handleComposeKey"
            >
              <label class="sr-only" for="ticket-reply">{{ t('tickets.reply') }}</label>
              <textarea
                id="ticket-reply"
                ref="replyInput"
                v-model="draft"
                rows="3"
                maxlength="10000"
                class="input resize-y"
                :disabled="mutating"
                :placeholder="t('tickets.replyPlaceholder')"
              ></textarea>
              <div class="mt-2 flex flex-wrap items-center justify-between gap-2">
                <span class="text-xs text-gray-500 dark:text-dark-400">
                  {{ draft.length ? draft.length + ' / 10000' : t('tickets.sendHint') }}
                </span>
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
    </template>
  </TablePageLayout>

  <BaseDialog :show="newTicketOpen" :title="t('tickets.newTicket')" width="normal" :close-on-escape="!mutating" :show-close-button="!mutating" @close="newTicketOpen = false">
    <form id="new-ticket-form" class="space-y-4" @submit.prevent="submitTicket">
      <label class="block">
        <span class="input-label">{{ t('tickets.subject') }}</span>
        <input v-model="newTicket.subject" class="input" maxlength="160" required :placeholder="t('tickets.subjectPlaceholder')" :disabled="mutating" />
      </label>
      <label class="block">
        <span class="input-label">{{ t('tickets.category') }}</span>
        <Select v-model="newTicket.category" :options="newTicketCategoryOptions" :disabled="mutating" />
      </label>
      <label class="block">
        <span class="input-label">{{ t('tickets.contact') }}</span>
        <input v-model="newTicket.contact" class="input" maxlength="200" :placeholder="t('tickets.contactPlaceholder')" :disabled="mutating" autocomplete="off" />
      </label>
      <label class="block">
        <span class="input-label">{{ t('tickets.content') }}</span>
        <textarea v-model="newTicket.content" class="input" rows="6" maxlength="10000" required :placeholder="t('tickets.contentPlaceholder')" :disabled="mutating"></textarea>
      </label>
      <p class="text-right text-xs text-gray-500 dark:text-dark-400">{{ newTicket.content.length }} / 10000</p>
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
      <p class="rounded-lg border border-primary-200 bg-primary-50 px-3 py-2 text-xs text-primary-800 dark:border-primary-900/50 dark:bg-primary-900/30 dark:text-primary-300">
        {{ t('tickets.proactiveHint') }}
      </p>
      <label class="block">
        <span class="input-label">{{ t('tickets.proactiveUser') }}</span>
        <input v-model="proactiveUserSearch" class="input" maxlength="120" :placeholder="t('tickets.proactiveUserPlaceholder')" :disabled="proactiveSending" autocomplete="off" @input="onProactiveSearchInput" />
      </label>
      <p v-if="proactiveSearching" class="text-sm text-gray-500 dark:text-dark-400">{{ t('tickets.proactiveSearching') }}</p>
      <div v-else-if="proactiveUsers.length" class="max-h-52 overflow-y-auto rounded-lg border border-gray-200 bg-white dark:border-dark-800 dark:bg-dark-900">
        <button
          v-for="candidate in proactiveUsers"
          :key="candidate.id"
          type="button"
          class="flex w-full items-center justify-between gap-3 border-b border-gray-100 px-3 py-2 text-left text-sm text-gray-800 transition-colors last:border-b-0 hover:bg-gray-50 dark:border-dark-800 dark:text-gray-200 dark:hover:bg-dark-800"
          @click="pickProactiveUser(candidate)"
        >
          <span class="min-w-0">
            <strong class="block truncate">{{ candidate.username || '#' + candidate.id }}</strong>
            <span class="block truncate text-xs text-gray-500 dark:text-dark-400">{{ candidate.email }}</span>
          </span>
          <span class="shrink-0 font-mono text-xs text-gray-500 dark:text-dark-400">#{{ candidate.id }}</span>
        </button>
      </div>
      <p v-else-if="proactiveSearched && proactiveUserSearch.trim()" class="text-sm text-gray-500 dark:text-dark-400">{{ t('tickets.proactiveNoUsers') }}</p>
      <p v-if="proactiveUser" class="text-sm text-emerald-600 dark:text-emerald-400">{{ t('tickets.proactiveSelected', { user: proactiveUser.username || '#' + proactiveUser.id, email: proactiveUser.email }) }}</p>
      <label class="block">
        <span class="input-label">{{ t('tickets.proactiveSubject') }}</span>
        <input v-model="proactiveSubject" class="input" maxlength="160" :placeholder="t('tickets.proactiveSubjectPlaceholder')" :disabled="proactiveSending" />
      </label>
      <label class="block">
        <span class="input-label">{{ t('tickets.proactiveContent') }}</span>
        <textarea v-model="proactiveContent" class="input" rows="6" maxlength="10000" required :placeholder="t('tickets.proactiveContentPlaceholder')" :disabled="proactiveSending"></textarea>
      </label>
      <p class="text-right text-xs text-gray-500 dark:text-dark-400">{{ proactiveContent.length }} / 10000</p>
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
      <label class="flex items-start justify-between gap-4 rounded-lg border border-gray-200 p-3 dark:border-dark-800">
        <span>
          <span class="block text-sm font-medium text-gray-900 dark:text-white">{{ t('tickets.emailEnabled') }}</span>
          <span class="input-hint">{{ t('tickets.emailEnabledHint') }}</span>
        </span>
        <input v-model="settingsForm.enabled" type="checkbox" class="mt-1 h-5 w-5 rounded border-gray-300 text-primary-600 focus:ring-primary-500" :disabled="settingsSaving" />
      </label>
      <label class="block">
        <span class="input-label">{{ t('tickets.adminEmail') }}</span>
        <input v-model="settingsForm.admin_email" class="input" maxlength="200" :placeholder="t('tickets.adminEmailPlaceholder')" :disabled="settingsSaving" autocomplete="off" />
        <span class="input-hint">{{ t('tickets.adminEmailHint') }}</span>
      </label>
      <dl class="grid grid-cols-2 gap-3 rounded-lg border border-gray-200 bg-gray-50 p-3 text-xs dark:border-dark-800 dark:bg-dark-800/50">
        <div>
          <dt class="text-gray-500 dark:text-dark-400">{{ t('tickets.pendingEmails') }}</dt>
          <dd class="mt-0.5 font-semibold text-gray-900 dark:text-white">{{ notificationSettings.pending_emails }}</dd>
        </div>
        <div>
          <dt class="text-gray-500 dark:text-dark-400">{{ t('tickets.failedEmails') }}</dt>
          <dd class="mt-0.5 font-semibold text-gray-900 dark:text-white">{{ notificationSettings.failed_emails }}</dd>
        </div>
      </dl>
      <p class="input-hint">{{ t('tickets.deliveryNote') }}</p>
    </div>
    <div v-else class="py-6 text-center text-sm text-gray-500 dark:text-dark-400">
      {{ settingsLoading ? t('tickets.loading') : t('tickets.loadFailed') }}
    </div>
    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="settingsSaving" @click="settingsOpen = false">{{ t('tickets.cancel') }}</button>
      <button type="button" class="btn btn-primary" :disabled="settingsSaving || !notificationSettings" @click="submitSettings">
        <Icon :name="settingsSaving ? 'refresh' : 'check'" size="sm" :class="{ 'animate-spin': settingsSaving }" />{{ t('tickets.save') }}
      </button>
    </template>
  </BaseDialog>

  <ConfirmDialog :show="closeDialogOpen" :title="t('tickets.closeTitle')" :message="t('tickets.closeMessage')" :confirm-text="t('tickets.close')" @cancel="closeDialogOpen = false" @confirm="closeTicket" />
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, reactive, ref, toRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { useTicketNotificationsStore } from '@/stores/ticketNotifications'
import Icon from '@/components/icons/Icon.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
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
let proactiveSearchSeq = 0
let proactiveSearchController: AbortController | null = null
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
  proactiveSearching.value = false
  proactiveClientID.value = newClientID()
  proactiveFingerprint = ''
}
function closeProactive() {
  if (proactiveSending.value) return
  proactiveOpen.value = false
  clearTimeout(proactiveSearchTimer)
  cancelProactiveSearch()
}
function cancelProactiveSearch() {
  proactiveSearchSeq += 1
  proactiveSearchController?.abort()
  proactiveSearchController = null
  proactiveSearching.value = false
}
function onProactiveSearchInput() {
  proactiveUser.value = null
  clearTimeout(proactiveSearchTimer)
  cancelProactiveSearch()
  proactiveSearched.value = false
  if (!proactiveUserSearch.value.trim()) {
    proactiveUsers.value = []
    return
  }
  proactiveSearchTimer = setTimeout(() => void searchProactiveUsers(), 300)
}
async function searchProactiveUsers() {
  const query = proactiveUserSearch.value.trim()
  const seq = ++proactiveSearchSeq
  proactiveSearchController?.abort()
  proactiveUsers.value = []
  if (!query) {
    proactiveSearched.value = false
    proactiveSearching.value = false
    return
  }
  const controller = new AbortController()
  proactiveSearchController = controller
  proactiveSearched.value = false
  proactiveSearching.value = true
  try {
    const result = await usersAPI.list(1, 10, { search: query }, { signal: controller.signal })
    // A slower earlier query must never replace the candidates of the query
    // currently in the box, otherwise an admin can select the wrong user.
    if (seq !== proactiveSearchSeq || controller.signal.aborted || query !== proactiveUserSearch.value.trim()) return
    proactiveUsers.value = result.items || []
    proactiveSearched.value = true
  } catch {
    if (seq !== proactiveSearchSeq || controller.signal.aborted) return
    proactiveUsers.value = []
    proactiveSearched.value = true
  } finally {
    if (seq === proactiveSearchSeq) proactiveSearching.value = false
  }
}
function pickProactiveUser(user: AdminUser) {
  clearTimeout(proactiveSearchTimer)
  cancelProactiveSearch()
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
    clearTimeout(proactiveSearchTimer)
    cancelProactiveSearch()
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

onBeforeUnmount(() => {
  clearTimeout(proactiveSearchTimer)
  cancelProactiveSearch()
})
</script>
