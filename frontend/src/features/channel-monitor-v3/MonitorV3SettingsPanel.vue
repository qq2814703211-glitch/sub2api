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
          <div class="flex items-center gap-2">
            <input
              v-model.number="draft.cache_average_window_days"
              type="number"
              min="1"
              max="90"
              class="input w-24 text-sm"
              :aria-label="t('channelMonitorV3.admin.cacheWindowTitle')"
            />
            <span class="text-xs text-gray-500 dark:text-dark-400">{{ t('channelMonitorV3.admin.days') }}</span>
          </div>
        </div>
      </div>

      <!-- Probe identity: V3's own, no longer borrowed from channel_monitors. -->
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
          <li v-for="row in groupRows" :key="row.id" class="flex flex-wrap items-center gap-3 px-5 py-3">
            <Toggle v-model="row.entry.enabled" />
            <div class="min-w-0 flex-1">
              <div class="truncate text-sm font-medium text-gray-900 dark:text-white">{{ row.name || t('channelMonitorV3.unknownGroup') }}</div>
              <div class="mt-0.5 flex flex-wrap items-center gap-2 text-[11px] text-gray-500 dark:text-dark-400">
                <span class="rounded bg-gray-100 px-1.5 py-0.5 font-mono dark:bg-dark-700">{{ row.platform }}</span>
                <span class="font-mono">#{{ row.id }}</span>
              </div>
            </div>
            <label class="flex items-center gap-1.5 text-xs text-gray-600 dark:text-dark-300">
              <input v-model="row.entry.probe_enabled" type="checkbox" class="h-3.5 w-3.5" />
              {{ t('channelMonitorV3.admin.probeEnabled') }}
            </label>
          </li>
        </ul>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
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

const loading = ref(true)
const saving = ref(false)
const probing = ref(false)
const draft = ref<ChannelMonitorV3Config | null>(null)
const original = ref('')
const candidates = ref<ChannelMonitorV3MonitorableGroup[]>([])
const apiKeyInput = ref('')
const extraModelsText = ref('')

// Probe-capable providers only: antigravity has no probe adapter, and the backend
// rejects it, so offering it here would only produce a 400 on save.
const providerOptions = computed(() =>
  PROVIDERS.filter((provider) => !QUOTA_ONLY_PROVIDERS.includes(provider)).map((provider) => ({
    value: provider,
    label: t(`monitorCommon.providers.${provider}`),
  })),
)

const dirty = computed(() => (draft.value ? serialize(draft.value) !== original.value : false))

interface GroupRow {
  id: number
  name: string
  platform: string
  entry: ChannelMonitorV3GroupConfig
}

/**
 * Rows are the union of every candidate group and every configured group: a group
 * that has since been deleted still has to be uncheckable, otherwise it would be
 * stuck in the config with no way to remove it.
 */
const groupRows = computed<GroupRow[]>(() => {
  const configured = new Map<number, ChannelMonitorV3GroupConfig>()
  for (const entry of draft.value?.groups ?? []) configured.set(entry.group_id, entry)
  const rows: GroupRow[] = []
  const seen = new Set<number>()
  for (const candidate of candidates.value) {
    const entry = configured.get(candidate.id) ?? {
      group_id: candidate.id,
      enabled: false,
      models: [],
      probe_enabled: true,
    }
    rows.push({ id: candidate.id, name: candidate.name, platform: candidate.platform, entry })
    seen.add(candidate.id)
  }
  for (const [id, entry] of configured) {
    if (seen.has(id)) continue
    rows.push({
      id,
      name: t('channelMonitorV3.admin.deletedGroup'),
      platform: '—',
      entry,
    })
  }
  return rows.sort((a, b) => (a.platform === b.platform ? a.id - b.id : a.platform.localeCompare(b.platform)))
})

const selectedGroupCount = computed(() => groupRows.value.filter((row) => row.entry.enabled).length)

function serialize(config: ChannelMonitorV3Config): string {
  // api_key / updated_at are intentionally excluded: the key is write-only and the
  // timestamp changes on every save, so neither should mark the form dirty.
  return JSON.stringify({
    enabled: config.enabled,
    cache_average_window_days: config.cache_average_window_days,
    probe: {
      endpoint: config.probe.endpoint,
      provider: config.probe.provider,
      primary_model: config.probe.primary_model,
      extra_models: config.probe.extra_models,
      interval_seconds: config.probe.interval_seconds,
    },
    groups: [...(config.groups ?? [])]
      .map((group) => ({
        group_id: group.group_id,
        enabled: group.enabled,
        models: group.models ?? [],
        probe_enabled: group.probe_enabled,
      }))
      .sort((a, b) => a.group_id - b.group_id),
  })
}

async function load() {
  loading.value = true
  try {
    const [config, groups] = await Promise.all([getV3Config(), listV3Groups()])
    draft.value = config
    candidates.value = groups
    extraModelsText.value = (config.probe.extra_models ?? []).join(', ')
    apiKeyInput.value = ''
    original.value = serialize(config)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('channelMonitorV3.admin.loadFailed')))
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!draft.value) return
  saving.value = true
  try {
    const payload = {
      ...draft.value,
      // Only groups that are actually checked belong in the stored config. The row
      // list shows every candidate; persisting the unchecked ones would make the
      // board's group list grow silently whenever a group is created.
      groups: groupRows.value.filter((row) => row.entry.enabled).map((row) => ({
        group_id: row.id,
        enabled: true,
        models: row.entry.models ?? [],
        probe_enabled: row.entry.probe_enabled,
      })),
      probe: {
        ...draft.value.probe,
        extra_models: extraModelsText.value
          .split(',')
          .map((model) => model.trim())
          .filter(Boolean),
      },
      api_key: apiKeyInput.value,
    }
    const updated = await updateV3Config(payload)
    draft.value = updated
    apiKeyInput.value = ''
    extraModelsText.value = (updated.probe.extra_models ?? []).join(', ')
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
