<template>
  <AppLayout>
    <div
      class="flex flex-col gap-3"
      :class="fullscreen ? 'fixed inset-0 z-50 bg-gray-950 p-3' : ''"
      data-test="creative-plaza-view"
    >
      <!-- Unified Creative Plaza Header & Mode Switcher -->
      <section class="card px-4 py-3">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">
                {{ t('creative.subtitle') }}
              </h2>
              <span
                class="badge badge-success inline-flex items-center gap-1 text-[11px]"
                data-test="local-compute-badge"
              >
                <Icon name="cpu" size="xs" />
                {{ t('creative.localComputeBadge') }}
              </span>
              <span v-if="ownKeys.length" class="badge badge-gray text-[11px]">
                {{ t('creative.keys.syncedSummary', { keys: ownKeys.length, models: totalDiscoveredModels }) }}
              </span>
            </div>
            <p
              v-if="!fullscreen"
              class="mt-1 max-w-5xl text-xs leading-relaxed text-gray-500 dark:text-dark-400"
            >
              {{ t('creative.intro') }}
            </p>
          </div>

          <!-- Primary Mode Tabs -->
          <div class="flex flex-wrap items-center gap-2">
            <div
              class="inline-flex items-center gap-1 rounded-xl border border-gray-200 bg-gray-100/80 p-1 dark:border-dark-700 dark:bg-dark-800/80"
              role="tablist"
              :aria-label="t('creative.title')"
            >
              <button
                type="button"
                role="tab"
                :aria-selected="activeTab === 'toonflow'"
                data-test="creative-tab-toonflow"
                class="inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors"
                :class="
                  activeTab === 'toonflow'
                    ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-900 dark:text-primary-300'
                    : 'text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white'
                "
                @click="switchTab('toonflow')"
              >
                <Icon name="grid" size="xs" />
                <span>{{ t('creative.tabs.toonflow') }}</span>
              </button>
              <button
                type="button"
                role="tab"
                :aria-selected="activeTab === 'imagegen'"
                data-test="creative-tab-imagegen"
                class="inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors"
                :class="
                  activeTab === 'imagegen'
                    ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-900 dark:text-primary-300'
                    : 'text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white'
                "
                @click="switchTab('imagegen')"
              >
                <Icon name="sparkles" size="xs" />
                <span>{{ t('creative.tabs.imagegen') }}</span>
              </button>
            </div>
          </div>
        </div>

        <!-- Toonflow Quick Jump & Sub2API Gateway Key Bar -->
        <div
          v-if="activeTab === 'toonflow'"
          class="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-gray-100 pt-2.5 dark:border-dark-800"
        >
          <div class="flex flex-wrap items-center gap-1.5">
            <button
              v-for="item in quickNavItems"
              :key="item.id"
              type="button"
              :data-test="`toonflow-nav-${item.id}`"
              class="btn btn-secondary btn-xs inline-flex items-center gap-1"
              @click="jumpToonflow(item.hash)"
            >
              <Icon :name="item.icon" size="xs" />
              <span>{{ t(item.labelKey) }}</span>
            </button>
          </div>

          <div class="flex flex-wrap items-center gap-1.5">
            <button
              type="button"
              class="btn btn-secondary btn-xs inline-flex items-center gap-1"
              data-test="toggle-key-panel"
              @click="showKeyPanel = !showKeyPanel"
            >
              <Icon name="key" size="xs" />
              <span>{{ showKeyPanel ? t('creative.keys.toggleHide') : t('creative.keys.toggleShow') }}</span>
            </button>
            <button
              type="button"
              class="btn btn-secondary btn-xs inline-flex items-center gap-1"
              data-test="toggle-fullscreen"
              @click="fullscreen = !fullscreen"
            >
              <svg
                class="h-3 w-3"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
                stroke-width="1.75"
              >
                <path
                  v-if="fullscreen"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  d="M9 9V4.5M9 9H4.5M9 9L3.75 3.75M9 15v4.5M9 15H4.5M9 15l-5.25 5.25M15 9h4.5M15 9V4.5M15 9l5.25-5.25M15 15h4.5M15 15v4.5m0-4.5l5.25 5.25"
                />
                <path
                  v-else
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  d="M3.75 3.75v4.5m0-4.5h4.5m-4.5 0L9 9M3.75 20.25v-4.5m0 4.5h4.5m-4.5 0L9 15M20.25 3.75h-4.5m4.5 0v4.5m0-4.5L15 9m5.25 11.25h-4.5m4.5 0v-4.5m0 4.5L15 15"
                />
              </svg>
              <span>{{ fullscreen ? t('creative.quickNav.fullscreenExit') : t('creative.quickNav.fullscreenEnter') }}</span>
            </button>
            <a
              href="/toonflow/index.html#/home"
              target="_blank"
              rel="noopener noreferrer"
              class="btn btn-secondary btn-xs inline-flex items-center gap-1"
              data-test="open-toonflow-standalone"
            >
              <Icon name="externalLink" size="xs" />
              <span>{{ t('creative.quickNav.openNewTab') }}</span>
            </a>
          </div>
        </div>

        <!-- Collapsible Gateway Key Selector Panel -->
        <div
          v-if="activeTab === 'toonflow' && showKeyPanel"
          class="mt-3 rounded-xl border border-gray-200 bg-gray-50/70 p-3 dark:border-dark-700 dark:bg-dark-900/60"
          data-test="creative-key-panel"
        >
          <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
            <span class="text-xs font-medium text-gray-800 dark:text-dark-100">
              {{ t('creative.keys.panelTitle') }}
            </span>
            <button
              type="button"
              class="btn btn-secondary btn-xs inline-flex items-center gap-1"
              :disabled="loadingKeys"
              @click="refreshKeysAndModels"
            >
              <Icon name="refresh" size="xs" />
              <span>{{ t('creative.keys.refreshKeys') }}</span>
            </button>
          </div>

          <p
            v-if="!ownKeys.length && !loadingKeys"
            class="text-xs text-amber-600 dark:text-amber-300"
          >
            {{ t('creative.keys.noKeysHint') }}
          </p>

          <div v-else class="grid grid-cols-1 gap-2.5 sm:grid-cols-2 lg:grid-cols-4">
            <label
              v-for="role in keyRoleDefs"
              :key="role.field"
              class="flex flex-col gap-1 text-xs text-gray-600 dark:text-dark-300"
            >
              <span>{{ t(role.labelKey) }}</span>
              <select
                :value="bindings[role.field] ?? ''"
                class="input h-8 text-xs"
                :data-test="`key-select-${role.role}`"
                @change="onSelectKey(role.field, ($event.target as HTMLSelectElement).value)"
              >
                <option value="">{{ t('creative.keys.fallbackFirst') }}</option>
                <option v-for="opt in formattedKeyOptions" :key="opt.id" :value="opt.id">
                  {{ opt.label }}
                </option>
              </select>
            </label>
          </div>
        </div>
      </section>

      <!-- Real Upstream HBAI-Ltd/Toonflow-app Web Studio (Same-Origin Isolated Runtime) -->
      <section
        v-show="activeTab === 'toonflow'"
        class="relative overflow-hidden rounded-2xl border border-gray-200 bg-dark-950 shadow-sm dark:border-dark-700"
        :class="fullscreen ? 'min-h-0 flex-1' : 'h-[calc(100vh-14.5rem)] min-h-[680px]'"
        data-test="toonflow-frame-container"
      >
        <iframe
          ref="toonflowFrameRef"
          :src="toonflowInitialSrc"
          title="Toonflow Web Studio"
          class="h-full w-full border-0 bg-dark-950"
          data-test="toonflow-iframe"
          allow="clipboard-read; clipboard-write; microphone; fullscreen"
          @load="onToonflowFrameLoad"
        />
      </section>

      <!-- Existing Quick Image Studio (Preserved + Connected to Toonflow Local Asset Library) -->
      <section v-if="imageGenMounted" v-show="activeTab === 'imagegen'" data-test="quick-image-studio-container">
        <ImageGenView
          ref="quickStudioRef"
          embedded
          @send-to-canvas="onQuickImageToToonflow($event, 'canvas')"
          @save-to-assets="onQuickImageToToonflow($event, 'assets')"
        />
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import ImageGenView from '@/views/ImageGenView.vue'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import type { ApiKey } from '@/types'
import {
  discoverModelsForKey,
  findInheritedImageGenKeyId,
  listActiveUserApiKeys,
  loadCreativeKeyBindings,
  postToonflowMessage,
  saveCreativeKeyBindings,
  serializeKeysForToonflow,
  writeToonflowHostContext,
  type CreativeKeyBindings,
  type CreativeKeyRole,
  type Sub2ApiToonflowContext,
} from '@/features/creative/toonflowHostBridge'

type CreativeTab = 'toonflow' | 'imagegen'

const route = useRoute()
const router = useRouter()
const { t, locale } = useI18n()
const authStore = useAuthStore()
const appStore = useAppStore()

const toonflowFrameRef = ref<HTMLIFrameElement | null>(null)
const quickStudioRef = ref<InstanceType<typeof ImageGenView> | null>(null)

function resolveInitialTab(): CreativeTab {
  const queryTab = String(route.query.tab ?? '').trim()
  if (queryTab === 'imagegen') return 'imagegen'
  if (queryTab === 'toonflow' || queryTab === 'canvas' || queryTab === 'trumanMap') return 'toonflow'
  if (route.path === '/image-studio' || route.path === '/imagegen') return 'imagegen'
  return 'toonflow'
}

const activeTab = ref<CreativeTab>(resolveInitialTab())
const imageGenMounted = ref<boolean>(activeTab.value === 'imagegen')
const showKeyPanel = ref(false)
const fullscreen = ref(false)
const loadingKeys = ref(false)
const ownKeys = ref<ApiKey[]>([])
const discoveredModels = reactive<Record<number, string[]>>({})
const bindings = reactive<CreativeKeyBindings>(loadCreativeKeyBindings())

const toonflowInitialSrc = computed(() => {
  const section = String(route.query.section ?? '').trim()
  if (section) return `/toonflow/index.html#/home?section=${encodeURIComponent(section)}`
  if (String(route.query.tab ?? '').trim() === 'trumanMap') return '/toonflow/index.html#/home?section=trumanMap'
  return '/toonflow/index.html#/home'
})

const quickNavItems = [
  { id: 'home', labelKey: 'creative.quickNav.home', icon: 'home', hash: '#/home' },
  { id: 'workspace', labelKey: 'creative.quickNav.workspace', icon: 'grid', hash: '#/workspace' },
  { id: 'trumanMap', labelKey: 'creative.quickNav.trumanMap', icon: 'cube', hash: '#/home?section=trumanMap' },
  { id: 'pluginMarket', labelKey: 'creative.quickNav.pluginMarket', icon: 'badge', hash: '#/home?section=pluginMarket' },
  { id: 'explore', labelKey: 'creative.quickNav.explore', icon: 'sparkles', hash: '#/home?section=explore' },
] as const

const keyRoleDefs: { role: CreativeKeyRole; field: keyof CreativeKeyBindings; labelKey: string }[] = [
  { role: 'text', field: 'textKeyId', labelKey: 'creative.keys.textKey' },
  { role: 'image', field: 'imageKeyId', labelKey: 'creative.keys.imageKey' },
  { role: 'video', field: 'videoKeyId', labelKey: 'creative.keys.videoKey' },
  { role: 'audio', field: 'audioKeyId', labelKey: 'creative.keys.audioKey' },
]

const formattedKeyOptions = computed(() =>
  ownKeys.value.map((k) => {
    const groupLabel = k.group?.name ? ` · ${k.group.name}` : ''
    const platformLabel = k.group?.platform ? ` (${k.group.platform})` : ''
    return {
      id: k.id,
      label: `${k.name || `#${k.id}`}${groupLabel}${platformLabel}`,
    }
  }),
)

const totalDiscoveredModels = computed(() => {
  const unique = new Set<string>()
  for (const list of Object.values(discoveredModels)) {
    for (const id of list) unique.add(id)
  }
  return unique.size
})

function detectCurrentTheme(): 'dark' | 'light' {
  if (typeof document !== 'undefined' && document.documentElement.classList.contains('dark')) {
    return 'dark'
  }
  return 'light'
}

function buildHostContext(): Sub2ApiToonflowContext {
  const user = authStore.user
  return {
    origin: typeof window !== 'undefined' ? window.location.origin : '',
    locale: locale.value === 'en' ? 'en' : 'zh-CN',
    theme: detectCurrentTheme(),
    user: user
      ? {
          id: user.id,
          email: user.email,
          username: user.username,
          role: user.role,
          balance: user.balance,
        }
      : null,
    keys: serializeKeysForToonflow(ownKeys.value),
    selectedTextKeyId: bindings.textKeyId,
    selectedImageKeyId: bindings.imageKeyId,
    selectedVideoKeyId: bindings.videoKeyId,
    selectedAudioKeyId: bindings.audioKeyId,
    discoveredModels: { ...discoveredModels },
  }
}

function syncHostContextToToonflow(): void {
  const ctx = buildHostContext()
  writeToonflowHostContext(ctx)
  postToonflowMessage(toonflowFrameRef.value, 'syncContext', ctx)
}

function switchTab(tab: CreativeTab): void {
  activeTab.value = tab
  if (tab === 'imagegen') imageGenMounted.value = true
  if (route.query.tab !== tab) {
    void router.replace({ path: route.path, query: { ...route.query, tab } })
  }
}

function jumpToonflow(hash: string): void {
  activeTab.value = 'toonflow'
  postToonflowMessage(toonflowFrameRef.value, 'navigate', hash)
}

function onSelectKey(field: keyof CreativeKeyBindings, raw: string): void {
  const num = raw ? Number(raw) : null
  bindings[field] = num && Number.isFinite(num) && num > 0 ? num : null
  saveCreativeKeyBindings({ ...bindings })
  syncHostContextToToonflow()
}

async function refreshKeysAndModels(): Promise<void> {
  loadingKeys.value = true
  try {
    const keys = await listActiveUserApiKeys()
    ownKeys.value = keys
    if (keys.length) {
      const defaultId = findInheritedImageGenKeyId(keys) ?? keys[0]!.id
      if (!bindings.textKeyId || !keys.some((k) => k.id === bindings.textKeyId)) bindings.textKeyId = defaultId
      if (!bindings.imageKeyId || !keys.some((k) => k.id === bindings.imageKeyId)) bindings.imageKeyId = defaultId
      if (!bindings.videoKeyId || !keys.some((k) => k.id === bindings.videoKeyId)) bindings.videoKeyId = defaultId
      if (!bindings.audioKeyId || !keys.some((k) => k.id === bindings.audioKeyId)) bindings.audioKeyId = defaultId
      saveCreativeKeyBindings({ ...bindings })
    }
    syncHostContextToToonflow()

    const uniqueKeyIds = [...new Set([bindings.textKeyId, bindings.imageKeyId, bindings.videoKeyId, bindings.audioKeyId].filter((id): id is number => Boolean(id)))]
    await Promise.all(
      uniqueKeyIds.map(async (keyId) => {
        const keyObj = keys.find((k) => k.id === keyId)
        if (!keyObj) return
        const models = await discoverModelsForKey(keyObj.key)
        discoveredModels[keyId] = models
      }),
    )
    syncHostContextToToonflow()
  } finally {
    loadingKeys.value = false
  }
}

function onToonflowFrameLoad(): void {
  syncHostContextToToonflow()
}

function onQuickImageToToonflow(
  payload: { src: string; prompt?: string; title?: string },
  target: 'canvas' | 'assets',
): void {
  postToonflowMessage(toonflowFrameRef.value, 'importMedia', { ...payload, target })
  appStore.showSuccess(t('creative.actions.sentToToonflow'))
  if (target === 'canvas') {
    switchTab('toonflow')
  }
}

function onWindowMessage(event: MessageEvent): void {
  if (event.origin !== window.location.origin) return
  const msg = event.data as { source?: string; type?: string } | null
  if (!msg || msg.source !== 'toonflow-web') return
  if (msg.type === 'ready') {
    syncHostContextToToonflow()
  }
}

let themeObserver: MutationObserver | null = null

watch(
  () => [route.path, route.query.tab],
  () => {
    const next = resolveInitialTab()
    if (next !== activeTab.value) {
      activeTab.value = next
      if (next === 'imagegen') imageGenMounted.value = true
    }
  },
)

watch(locale, () => {
  syncHostContextToToonflow()
})

watch(
  () => authStore.user,
  () => {
    syncHostContextToToonflow()
  },
)

onMounted(() => {
  window.addEventListener('message', onWindowMessage)
  syncHostContextToToonflow()
  void refreshKeysAndModels()
  if (typeof MutationObserver !== 'undefined' && typeof document !== 'undefined') {
    themeObserver = new MutationObserver(() => syncHostContextToToonflow())
    themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
  }
})

onBeforeUnmount(() => {
  window.removeEventListener('message', onWindowMessage)
  themeObserver?.disconnect()
  themeObserver = null
})
</script>
