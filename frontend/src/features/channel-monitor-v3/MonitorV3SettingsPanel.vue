<template>
  <section class="mx-auto w-full max-w-6xl space-y-5 px-1 py-2 sm:px-2">
    <header
      class="page-header mb-0 flex flex-wrap items-center justify-between gap-3 rounded-3xl bg-white p-5 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700 sm:p-6"
    >
      <div class="min-w-0">
        <h2 class="page-title flex items-center gap-2 text-xl font-semibold text-gray-900 dark:text-white">
          <span class="inline-flex h-8 w-8 items-center justify-center rounded-lg bg-indigo-50 text-indigo-500 dark:bg-indigo-900/30 dark:text-indigo-400">
            <Icon name="chart" size="sm" />
          </span>
          {{ t('channelMonitorV3.admin.title') }}
        </h2>
        <p class="page-description mt-1.5 text-xs text-gray-500 dark:text-gray-400">
          {{ t('channelMonitorV3.admin.description') }}
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <button type="button" class="btn btn-secondary" :disabled="probing || !draft?.enabled" @click="probeNow">
          <Icon name="refresh" size="sm" />
          {{ probing ? t('channelMonitorV3.admin.probing') : t('channelMonitorV3.admin.probeNow') }}
        </button>
        <button type="button" class="btn btn-primary" :disabled="saving || !dirty" @click="save">
          <Icon name="check" size="sm" />
          {{ saving ? t('channelMonitorV3.admin.saving') : t('channelMonitorV3.admin.save') }}
        </button>
      </div>
    </header>

    <!-- V3 keeps its own switch: this banner exists so an operator never mistakes
         the global V1/V2 switches for V3's (ADR 0008). -->
    <div
      class="rounded-xl border border-indigo-200 bg-indigo-50 px-4 py-3 text-sm text-indigo-900 dark:border-indigo-900/50 dark:bg-indigo-950/40 dark:text-indigo-100"
      role="status"
    >
      {{ t('channelMonitorV3.admin.independenceNotice') }}
    </div>

    <div
      v-if="loading"
      class="card flex min-h-[200px] items-center justify-center !rounded-3xl !border-0 text-sm text-gray-500 shadow-sm ring-1 ring-gray-900/5 dark:text-dark-300 dark:ring-dark-700"
    >
      <span>{{ t('channelMonitorV3.admin.loading') }}</span>
    </div>

    <template v-else-if="draft">
      <!-- Switch + cache window -->
      <div class="card divide-y divide-gray-100 !rounded-3xl !border-0 shadow-sm ring-1 ring-gray-900/5 dark:divide-dark-700 dark:!bg-dark-800 dark:ring-dark-700">
        <div class="flex flex-wrap items-center justify-between gap-4 px-5 py-4">
          <div class="min-w-0">
            <strong class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('channelMonitorV3.admin.enableTitle') }}</strong>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('channelMonitorV3.admin.enableHint') }}</p>
          </div>
          <Toggle v-model="draft.enabled" />
        </div>
        <div class="flex flex-wrap items-center justify-between gap-4 px-5 py-4">
          <div class="min-w-0">
            <strong class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('channelMonitorV3.admin.cacheWindowTitle') }}</strong>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('channelMonitorV3.admin.cacheWindowHint') }}</p>
          </div>
          <div class="flex flex-col items-end gap-1">
            <div class="flex items-center gap-2">
              <input
                v-model.number="windowValue"
                type="number"
                :min="1"
                :max="windowMax"
                class="input w-24 text-sm"
                :aria-label="t('channelMonitorV3.admin.cacheWindowTitle')"
                :aria-invalid="!windowValid"
              />
              <!-- Unit is a separate选择 rather than one number that silently changes
                   meaning: a minute window has to reach SQL as minutes. -->
              <Select v-model="windowUnit" class="w-24" :options="windowUnitOptions" />
            </div>
            <span v-if="!windowValid" class="text-[11px] text-red-600 dark:text-red-400" role="alert">
              {{ t('channelMonitorV3.admin.windowInvalid', { max: windowMax, unit: windowUnitLabel }) }}
            </span>
            <span v-else class="text-[11px] text-gray-500 dark:text-dark-400">
              {{ t('channelMonitorV3.admin.windowRangeHint', { max: MAX_WINDOW_DAYS, maxMinutes: MAX_WINDOW_MINUTES }) }}
            </span>
          </div>
        </div>
      </div>

      <!-- Global probe identity: the default every group inherits. -->
      <div class="card !rounded-3xl !border-0 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700">
        <div class="border-b border-gray-100 px-5 py-4 dark:border-dark-700">
          <strong class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('channelMonitorV3.admin.probeTitle') }}</strong>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('channelMonitorV3.admin.probeHint') }}</p>
        </div>
        <div class="grid gap-4 px-5 py-4 sm:grid-cols-2">
          <label class="block">
            <span class="text-xs font-medium text-gray-600 dark:text-dark-300">{{ t('channelMonitorV3.admin.endpoint') }}</span>
            <input v-model.trim="draft.probe.endpoint" type="url" class="input mt-1 w-full text-sm" placeholder="https://api.example.com" />
          </label>
          <label class="block">
            <span class="text-xs font-medium text-gray-600 dark:text-dark-300">{{ t('channelMonitorV3.admin.provider') }}</span>
            <Select v-model="draft.probe.provider" class="mt-1 w-full" :options="providerOptions" />
          </label>
          <label class="block">
            <span class="text-xs font-medium text-gray-600 dark:text-dark-300">{{ t('channelMonitorV3.admin.primaryModel') }}</span>
            <input v-model.trim="draft.probe.primary_model" type="text" class="input mt-1 w-full text-sm" placeholder="claude-sonnet-4" />
          </label>
          <label class="block">
            <span class="text-xs font-medium text-gray-600 dark:text-dark-300">{{ t('channelMonitorV3.admin.interval') }}</span>
            <input v-model.number="draft.probe.interval_seconds" type="number" min="15" max="3600" class="input mt-1 w-full text-sm" />
          </label>
          <label class="block sm:col-span-2">
            <span class="text-xs font-medium text-gray-600 dark:text-dark-300">{{ t('channelMonitorV3.admin.extraModels') }}</span>
            <input
              v-model.trim="extraModelsText"
              type="text"
              class="input mt-1 w-full text-sm"
              :placeholder="t('channelMonitorV3.admin.extraModelsPlaceholder')"
            />
          </label>
          <label class="block sm:col-span-2">
            <span class="text-xs font-medium text-gray-600 dark:text-dark-300">{{ t('channelMonitorV3.admin.apiKey') }}</span>
            <input
              v-model.trim="apiKeyInput"
              type="password"
              autocomplete="new-password"
              class="input mt-1 w-full text-sm"
              :placeholder="draft.api_key_set ? t('channelMonitorV3.admin.apiKeyKeep') : t('channelMonitorV3.admin.apiKeyPlaceholder')"
            />
            <span class="mt-1 block text-[11px] text-gray-500 dark:text-dark-400">
              {{ draft.api_key_set ? t('channelMonitorV3.admin.apiKeyStored') : t('channelMonitorV3.admin.apiKeyMissing') }}
            </span>
          </label>
        </div>
      </div>

      <!-- Group picker: the only precondition for V3 is being checked here. -->
      <div class="card !rounded-3xl !border-0 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700">
          <div class="min-w-0">
            <strong class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('channelMonitorV3.admin.groupsTitle') }}</strong>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('channelMonitorV3.admin.groupsHint') }}</p>
          </div>
          <span class="text-xs text-gray-500 dark:text-dark-400">
            {{ t('channelMonitorV3.admin.groupsSelected', { selected: selectedGroupCount, total: groupRows.length }) }}
          </span>
        </div>
        <div v-if="groupRows.length === 0" class="px-5 py-6 text-sm text-gray-500 dark:text-dark-400">
          {{ t('channelMonitorV3.admin.groupsEmpty') }}
        </div>
        <ul v-else class="divide-y divide-gray-100 dark:divide-dark-700">
          <li
            v-for="row in groupRows"
            :key="row.id"
            class="px-5 py-3"
            :class="row.deleted ? 'bg-red-50/60 dark:bg-red-950/20' : ''"
            :data-group-id="row.id"
            :data-deleted="row.deleted ? 'true' : 'false'"
          >
            <div class="flex flex-wrap items-center gap-3">
              <!-- A stale entry must be impossible to re-enable: the group is gone,
                   so checking it would silently monitor nothing. -->
              <Toggle v-if="!row.deleted" v-model="row.entry.enabled" />
              <span
                v-else
                class="inline-flex h-6 w-11 flex-shrink-0 items-center justify-center rounded-full border-2 border-dashed border-red-300 text-red-400 dark:border-red-800"
                :aria-label="t('channelMonitorV3.admin.deletedGroup')"
              >
                <Icon name="exclamationTriangle" size="sm" />
              </span>
              <div class="min-w-0 flex-1">
                <div
                  class="truncate text-sm font-medium"
                  :class="row.deleted
                    ? 'text-red-700 line-through dark:text-red-300'
                    : 'text-gray-900 dark:text-white'"
                >
                  {{ row.name }}
                </div>
                <div class="mt-0.5 flex flex-wrap items-center gap-2 text-[11px] text-gray-500 dark:text-dark-400">
                  <span class="rounded bg-gray-100 px-1.5 py-0.5 font-mono dark:bg-dark-700">{{ row.platform }}</span>
                  <span class="font-mono">#{{ row.id }}</span>
                  <span v-if="row.deleted" class="text-red-600 dark:text-red-400">
                    {{ t('channelMonitorV3.admin.deletedGroupHint') }}
                  </span>
                  <span v-else-if="row.entry.enabled" class="text-gray-500 dark:text-dark-400">
                    {{ row.entry.probe_inherited === false ? t('channelMonitorV3.admin.groupProbeOwn') : t('channelMonitorV3.admin.groupProbeInherited') }}
                  </span>
                </div>
              </div>
              <template v-if="!row.deleted">
                <label class="flex items-center gap-1.5 text-xs text-gray-600 dark:text-dark-300">
                  <input v-model="row.entry.probe_enabled" type="checkbox" class="h-3.5 w-3.5" />
                  {{ t('channelMonitorV3.admin.probeEnabled') }}
                </label>
                <button
                  type="button"
                  class="btn btn-secondary btn-sm"
                  :aria-expanded="expandedGroups.has(row.id)"
                  :data-group-probe-toggle="row.id"
                  @click="toggleGroupProbe(row.id)"
                >
                  {{ expandedGroups.has(row.id) ? t('channelMonitorV3.admin.groupProbeCollapse') : t('channelMonitorV3.admin.groupProbeExpand') }}
                </button>
              </template>
              <button
                v-else
                type="button"
                class="btn btn-secondary btn-sm"
                :data-group-remove="row.id"
                @click="removeDeletedGroup(row.id)"
              >
                <Icon name="trash" size="sm" />
                {{ t('channelMonitorV3.admin.removeDeletedGroup') }}
              </button>
            </div>

            <!-- Per-group probe identity. Each group holds different models and
                 therefore needs its own key; empty means "inherit the global one". -->
            <div
              v-if="!row.deleted && expandedGroups.has(row.id)"
              class="mt-3 rounded-xl border border-gray-200 bg-gray-50/60 px-4 py-3 dark:border-dark-600 dark:bg-dark-900/40"
              :data-group-probe-panel="row.id"
            >
              <label class="mb-3 flex items-center gap-2 text-xs font-medium text-gray-700 dark:text-dark-200">
                <input
                  type="checkbox"
                  class="h-3.5 w-3.5"
                  :checked="isInheriting(row)"
                  :data-group-probe-inherit="row.id"
                  @change="onInheritToggle(row, ($event.target as HTMLInputElement).checked)"
                />
                {{ t('channelMonitorV3.admin.groupProbeInheritToggle') }}
                <span class="font-normal text-gray-500 dark:text-dark-400">
                  {{ isInheriting(row) ? t('channelMonitorV3.admin.groupProbeInherited') : t('channelMonitorV3.admin.groupProbeOwn') }}
                </span>
              </label>

              <div class="grid gap-3 sm:grid-cols-2">
                <label class="block">
                  <span class="text-[11px] font-medium text-gray-600 dark:text-dark-300">{{ t('channelMonitorV3.admin.endpoint') }}</span>
                  <input
                    v-model.trim="row.entry.probe!.endpoint"
                    type="url"
                    class="input mt-1 w-full text-sm"
                    :disabled="isInheriting(row)"
                    placeholder="https://api.example.com"
                  />
                </label>
                <label class="block">
                  <span class="text-[11px] font-medium text-gray-600 dark:text-dark-300">{{ t('channelMonitorV3.admin.provider') }}</span>
                  <Select
                    v-model="row.entry.probe!.provider"
                    class="mt-1 w-full"
                    :options="providerOptions"
                    :disabled="isInheriting(row)"
                  />
                </label>
                <label class="block">
                  <span class="text-[11px] font-medium text-gray-600 dark:text-dark-300">{{ t('channelMonitorV3.admin.primaryModel') }}</span>
                  <input
                    v-model.trim="row.entry.probe!.primary_model"
                    type="text"
                    class="input mt-1 w-full text-sm"
                    :disabled="isInheriting(row)"
                    placeholder="claude-sonnet-4"
                  />
                </label>
                <label class="block">
                  <span class="text-[11px] font-medium text-gray-600 dark:text-dark-300">{{ t('channelMonitorV3.admin.interval') }}</span>
                  <input
                    v-model.number="row.entry.probe!.interval_seconds"
                    type="number"
                    min="0"
                    max="3600"
                    class="input mt-1 w-full text-sm"
                    :disabled="isInheriting(row)"
                    :placeholder="String(draft.probe.interval_seconds || 300)"
                  />
                  <span class="mt-0.5 block text-[11px] text-gray-500 dark:text-dark-400">
                    {{ t('channelMonitorV3.admin.groupIntervalHint') }}
                  </span>
                </label>
                <label class="block sm:col-span-2">
                  <span class="text-[11px] font-medium text-gray-600 dark:text-dark-300">{{ t('channelMonitorV3.admin.groupModels') }}</span>
                  <input
                    v-model.trim="groupModelsText[row.id]"
                    type="text"
                    class="input mt-1 w-full text-sm"
                    :disabled="isInheriting(row)"
                    :placeholder="t('channelMonitorV3.admin.groupModelsPlaceholder')"
                  />
                </label>
                <label class="block sm:col-span-2">
                  <span class="text-[11px] font-medium text-gray-600 dark:text-dark-300">{{ t('channelMonitorV3.admin.groupApiKey') }}</span>
                  <input
                    v-model.trim="groupKeyInputs[row.id]"
                    type="password"
                    autocomplete="new-password"
                    class="input mt-1 w-full text-sm"
                    :disabled="isInheriting(row)"
                    :placeholder="row.entry.api_key_set
                      ? t('channelMonitorV3.admin.groupApiKeyKeep')
                      : t('channelMonitorV3.admin.groupApiKeyMissing')"
                  />
                  <span class="mt-0.5 block text-[11px] text-gray-500 dark:text-dark-400">
                    {{ row.entry.api_key_set ? t('channelMonitorV3.admin.groupApiKeyStored') : t('channelMonitorV3.admin.groupApiKeyMissing') }}
                  </span>
                </label>
              </div>
            </div>
          </li>
        </ul>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import Toggle from '@/components/common/Toggle.vue'
import Select from '@/components/common/Select.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { PROVIDERS, QUOTA_ONLY_PROVIDERS } from '@/constants/channelMonitor'
import {
  getV3Config,
  listV3Groups,
  probeV3Now,
  updateV3Config,
} from '@/api/channelMonitorV3'
import type {
  ChannelMonitorV3Config,
  ChannelMonitorV3GroupConfig,
  ChannelMonitorV3MonitorableGroup,
  ChannelMonitorV3ProbeConfig,
  ChannelMonitorV3WindowUnit,
} from '@/api/channelMonitorV3'

/**
 * Channel Monitor V3 configuration surface.
 *
 * Rendered as the V3 tab of `/admin/channels/monitor`. It is a panel, not a page:
 * the shared monitor view owns `<AppLayout>` and the tab chrome. V3 still owns its
 * switch, its group list and its probe identity, and saving here writes
 * `channel_monitor_v3_config` only.
 */

const { t } = useI18n()
const appStore = useAppStore()

/** Backend limits: 90 days == 129600 minutes, one physical cap for both units. */
const MAX_WINDOW_DAYS = 90
const MAX_WINDOW_MINUTES = MAX_WINDOW_DAYS * 24 * 60

const loading = ref(true)
const saving = ref(false)
const probing = ref(false)
const draft = ref<ChannelMonitorV3Config | null>(null)
const original = ref('')
const candidates = ref<ChannelMonitorV3MonitorableGroup[]>([])
const apiKeyInput = ref('')
const extraModelsText = ref('')
const windowValue = ref(30)
const windowUnit = ref<ChannelMonitorV3WindowUnit>('day')
/** Groups whose probe block is expanded. */
const expandedGroups = ref(new Set<number>())
/** Per-group plain keys (write-only; never echoed back). */
const groupKeyInputs = reactive<Record<number, string>>({})
/** Per-group model list as text, so commas survive typing. */
const groupModelsText = reactive<Record<number, string>>({})
/** Sticky list of deleted group ids the operator has not removed yet. */
const removedDangling = ref(new Set<number>())

/**
 * Live row state, built once per load and mutated in place.
 *
 * It deliberately does NOT live inside a computed: a computed would hand back a
 * fresh entry object for every candidate that is not in the stored config, so
 * typing into a not-yet-configured group's probe (or just toggling another row)
 * would silently discard it. One array, mutated in place.
 */
const rows = ref<GroupRow[]>([])

// Probe-capable providers only: antigravity has no probe adapter, and the backend
// rejects it, so offering it here would only produce a 400 on save.
const providerOptions = computed(() =>
  PROVIDERS.filter((provider) => !QUOTA_ONLY_PROVIDERS.includes(provider)).map((provider) => ({
    value: provider,
    label: t(`monitorCommon.providers.${provider}`),
  })),
)

const windowUnitOptions = computed(() => [
  { value: 'minute', label: t('channelMonitorV3.admin.windowUnitMinute') },
  { value: 'day', label: t('channelMonitorV3.admin.windowUnitDay') },
])

const windowMax = computed(() =>
  windowUnit.value === 'minute' ? MAX_WINDOW_MINUTES : MAX_WINDOW_DAYS,
)

const windowUnitLabel = computed(() =>
  windowUnit.value === 'minute'
    ? t('channelMonitorV3.admin.windowUnitMinute')
    : t('channelMonitorV3.admin.windowUnitDay'),
)

// Validating here (not mutating the value) is what keeps a unit switch from
// silently rewriting what the operator typed.
const windowValid = computed(() => {
  const value = Number(windowValue.value)
  return Number.isFinite(value) && value >= 1 && value <= windowMax.value
})

const dirty = computed(() => (draft.value ? serialize(draft.value) !== original.value : false))

interface GroupRow {
  id: number
  name: string
  platform: string
  entry: ChannelMonitorV3GroupConfig
  /** True when the group is only in the config, not in the candidate list. */
  deleted: boolean
}

function emptyProbe(): ChannelMonitorV3ProbeConfig {
  return {
    endpoint: '',
    provider: 'anthropic',
    primary_model: '',
    extra_models: [],
    interval_seconds: 0,
  }
}

/** A group inherits the global probe until it has both an endpoint and a model. */
function isInheriting(row: GroupRow): boolean {
  const probe = row.entry.probe
  return !probe || (!probe.endpoint && !probe.primary_model)
}

function onInheritToggle(row: GroupRow, useGlobal: boolean) {
  if (useGlobal) {
    row.entry.probe = emptyProbe()
    groupKeyInputs[row.id] = ''
    groupModelsText[row.id] = ''
    return
  }
  // Seed from the global identity so the operator starts from something that works.
  // A half-filled probe is rejected by the backend on purpose.
  row.entry.probe = {
    endpoint: draft.value?.probe.endpoint ?? '',
    provider: draft.value?.probe.provider ?? 'anthropic',
    primary_model: draft.value?.probe.primary_model ?? '',
    extra_models: [...(draft.value?.probe.extra_models ?? [])],
    interval_seconds: row.entry.probe?.interval_seconds || 0,
  }
}

function toggleGroupProbe(groupID: number) {
  const next = new Set(expandedGroups.value)
  if (next.has(groupID)) next.delete(groupID)
  else next.add(groupID)
  expandedGroups.value = next
}

/**
 * Build the row list: every candidate group, plus every configured group that is
 * no longer a candidate.
 *
 * The union is deliberate: a group that has since been deleted must stay visible,
 * otherwise it is stuck in the config with no way for an operator to remove it.
 * What it must NOT do is look like a normal row — a deleted entry cannot be
 * enabled, shows its own id and a red warning, and can be removed outright.
 */
function buildRows(config: ChannelMonitorV3Config, available: ChannelMonitorV3MonitorableGroup[]): GroupRow[] {
  const configured = new Map<number, ChannelMonitorV3GroupConfig>()
  for (const entry of config.groups ?? []) configured.set(entry.group_id, entry)
  const built: GroupRow[] = []
  const seen = new Set<number>()
  for (const candidate of available) {
    const stored = configured.get(candidate.id)
    built.push({
      id: candidate.id,
      name: candidate.name,
      platform: candidate.platform,
      deleted: false,
      entry: {
        group_id: candidate.id,
        enabled: stored?.enabled ?? false,
        models: [...(stored?.models ?? [])],
        probe_enabled: stored?.probe_enabled ?? true,
        probe: stored?.probe ? { ...stored.probe, extra_models: [...(stored.probe.extra_models ?? [])] } : emptyProbe(),
        api_key_set: stored?.api_key_set ?? false,
        probe_inherited: stored?.probe_inherited ?? true,
      },
    })
    seen.add(candidate.id)
  }
  for (const [id, stored] of configured) {
    if (seen.has(id)) continue
    built.push({
      id,
      name: t('channelMonitorV3.admin.deletedGroupNamed', { id }),
      platform: '—',
      deleted: true,
      entry: {
        group_id: id,
        // Force it off: the group is gone, so an enabled stale entry would claim
        // to be monitored while probing nothing.
        enabled: false,
        models: [...(stored.models ?? [])],
        probe_enabled: false,
        probe: stored.probe ? { ...stored.probe, extra_models: [...(stored.probe.extra_models ?? [])] } : emptyProbe(),
        api_key_set: stored.api_key_set ?? false,
        probe_inherited: stored.probe_inherited ?? true,
      },
    })
  }
  return built.sort((a, b) => {
    // Deleted entries sink to the bottom: they need attention, not prominence.
    if (a.deleted !== b.deleted) return a.deleted ? 1 : -1
    return a.platform === b.platform ? a.id - b.id : a.platform.localeCompare(b.platform)
  })
}

/** Visible rows: removed dangling entries drop out immediately. */
const groupRows = computed<GroupRow[]>(() =>
  rows.value.filter((row) => !row.deleted || !removedDangling.value.has(row.id)),
)

const selectedGroupCount = computed(() => groupRows.value.filter((row) => row.entry.enabled).length)

function removeDeletedGroup(groupID: number) {
  const next = new Set(removedDangling.value)
  next.add(groupID)
  removedDangling.value = next
}

function serialize(config: ChannelMonitorV3Config): string {
  // api_key / group keys / updated_at are intentionally excluded: keys are
  // write-only and the timestamp changes on every save, so neither should mark
  // the form dirty.
  return JSON.stringify({
    enabled: config.enabled,
    window: { value: windowValue.value, unit: windowUnit.value },
    probe: {
      endpoint: config.probe.endpoint,
      provider: config.probe.provider,
      primary_model: config.probe.primary_model,
      extra_models: config.probe.extra_models,
      interval_seconds: config.probe.interval_seconds,
    },
    // Including the dangling set is what makes "remove a deleted group" a real
    // change: without it the row already dropped out of `groups`, so the form
    // would look untouched and the save button would stay disabled.
    dangling: [...removedDangling.value].sort((a, b) => a - b),
    // Snapshot the live rows, not the response object: editing a group before it
    // has ever been saved only exists on the row.
    groups: groupRows.value
      .filter((row) => !row.deleted)
      .map((row) => ({
        group_id: row.id,
        enabled: row.entry.enabled,
        models: splitModels(groupModelsText[row.id]).length
          ? splitModels(groupModelsText[row.id])
          : row.entry.models,
        probe_enabled: row.entry.probe_enabled,
        probe: {
          endpoint: row.entry.probe?.endpoint ?? '',
          provider: row.entry.probe?.provider ?? '',
          primary_model: row.entry.probe?.primary_model ?? '',
          extra_models: row.entry.probe?.extra_models ?? [],
          interval_seconds: row.entry.probe?.interval_seconds ?? 0,
        },
      }))
      .sort((a, b) => a.group_id - b.group_id),
  })
}

function splitModels(text: string | undefined): string[] {
  return (text ?? '')
    .split(',')
    .map((model) => model.trim())
    .filter(Boolean)
}

async function load() {
  loading.value = true
  try {
    const [config, groups] = await Promise.all([getV3Config(), listV3Groups()])
    draft.value = config
    candidates.value = groups
    extraModelsText.value = (config.probe.extra_models ?? []).join(', ')
    apiKeyInput.value = ''
    removedDangling.value = new Set()
    // The value+unit pair is authoritative; fall back to the legacy days field.
    if (config.cache_average_window_value && config.cache_average_window_unit) {
      windowValue.value = config.cache_average_window_value
      windowUnit.value = config.cache_average_window_unit
    } else {
      windowValue.value = config.cache_average_window_days || 30
      windowUnit.value = 'day'
    }
    resetRowInputs(config)
    rows.value = buildRows(config, groups)
    original.value = serialize(config)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('channelMonitorV3.admin.loadFailed')))
  } finally {
    loading.value = false
  }
}

/** Align the per-row text inputs with a freshly loaded/saved config. */
function resetRowInputs(config: ChannelMonitorV3Config) {
  for (const key of Object.keys(groupModelsText)) delete groupModelsText[Number(key)]
  for (const key of Object.keys(groupKeyInputs)) delete groupKeyInputs[Number(key)]
  for (const group of config.groups ?? []) {
    groupModelsText[group.group_id] = (group.models ?? []).join(', ')
    groupKeyInputs[group.group_id] = ''
  }
}

async function save() {
  if (!draft.value) return
  if (!windowValid.value) {
    appStore.showError(t('channelMonitorV3.admin.windowInvalid', {
      max: windowMax.value,
      unit: windowUnitLabel.value,
    }))
    return
  }
  saving.value = true
  try {
    const groupAPIKeys: Record<string, string> = {}
    const payload = {
      ...draft.value,
      // Only enabled groups belong in the stored config, and deleted entries are
      // dropped: they are shown so the operator can act on them, not to persist.
      groups: groupRows.value
        .filter((row) => !row.deleted && row.entry.enabled)
        .map((row) => ({
          group_id: row.id,
          enabled: true,
          models: splitModels(groupModelsText[row.id]).length
            ? splitModels(groupModelsText[row.id])
            : (row.entry.models ?? []),
          probe_enabled: row.entry.probe_enabled,
          probe: isInheriting(row)
            ? emptyProbe()
            : {
                endpoint: row.entry.probe!.endpoint,
                provider: row.entry.probe!.provider,
                primary_model: row.entry.probe!.primary_model,
                extra_models: row.entry.probe!.extra_models ?? [],
                interval_seconds: row.entry.probe!.interval_seconds || 0,
              },
        }))
        .map((group) => {
          const key = (groupKeyInputs[group.group_id] ?? '').trim()
          if (key) groupAPIKeys[String(group.group_id)] = key
          return group
        }),
      probe: {
        ...draft.value.probe,
        extra_models: splitModels(extraModelsText.value),
      },
      cache_average_window_value: windowValue.value,
      cache_average_window_unit: windowUnit.value,
      api_key: apiKeyInput.value,
      group_api_keys: groupAPIKeys,
    }
    const updated = await updateV3Config(payload)
    draft.value = updated
    apiKeyInput.value = ''
    extraModelsText.value = (updated.probe.extra_models ?? []).join(', ')
    removedDangling.value = new Set()
    if (updated.cache_average_window_value && updated.cache_average_window_unit) {
      windowValue.value = updated.cache_average_window_value
      windowUnit.value = updated.cache_average_window_unit
    }
    resetRowInputs(updated)
    rows.value = buildRows(updated, candidates.value)
    original.value = serialize(updated)
    appStore.showSuccess(t('channelMonitorV3.admin.saveSuccess'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('channelMonitorV3.admin.saveFailed')))
  } finally {
    saving.value = false
  }
}

async function probeNow() {
  probing.value = true
  try {
    await probeV3Now()
    appStore.showSuccess(t('channelMonitorV3.admin.probeSuccess'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('channelMonitorV3.admin.probeFailed')))
  } finally {
    probing.value = false
  }
}

onMounted(load)
</script>
