<template>
  <AppLayout full-bleed>
    <div class="flex h-full w-full flex-col overflow-hidden" data-test="creative-plaza-view">
      <div
        v-if="!loadingKeys && ownKeys.length === 0"
        class="flex shrink-0 items-center justify-between gap-2 border-b border-gray-200 bg-white px-3 py-1.5 dark:border-dark-800 dark:bg-gray-950"
      >
        <button
          type="button"
          class="badge badge-warning inline-flex items-center gap-1 text-[11px] hover:opacity-90"
          data-test="creative-no-keys-link"
          @click="router.push('/keys')"
        >
          <Icon name="key" size="xs" />
          <span>{{ t('creative.keys.createKeyAction') }}</span>
        </button>
      </div>

      <section
        class="relative flex-1 min-h-0 w-full overflow-hidden bg-white dark:bg-gray-950"
        data-test="studio-iframe-container"
      >
        <iframe
          ref="studioFrameRef"
          :src="studioInitialSrc"
          :title="t('creative.title')"
          class="block h-full w-full border-0"
          allow="clipboard-read; clipboard-write; microphone"
          data-test="studio-iframe"
          @load="onStudioFrameLoad"
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
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { isDarkSurface } from '@/features/universe/theme'
import { sanitizeUrl } from '@/utils/url'
import type { ApiKey } from '@/types'
import {
  discoverModelDetailsForKey,
  findInheritedImageGenKeyId,
  listActiveUserApiKeys,
  loadCreativeKeyBindings,
  postToonflowMessage,
  saveCreativeKeyBindings,
  serializeKeysForToonflow,
  writeToonflowHostContext,
  type CreativeKeyBindings,
  type DiscoveredModelMeta,
  type Sub2ApiToonflowContext,
} from '@/features/creative/toonflowHostBridge'

const route = useRoute()
const router = useRouter()
const { t, locale } = useI18n()
const authStore = useAuthStore()
const appStore = useAppStore()

const studioFrameRef = ref<HTMLIFrameElement | null>(null)
const loadingKeys = ref(true)
const ownKeys = ref<ApiKey[]>([])
const discoveredModels = reactive<Record<number, string[]>>({})
const discoveredModelMeta = reactive<Record<number, DiscoveredModelMeta[]>>({})
const bindings = reactive<CreativeKeyBindings>(loadCreativeKeyBindings())

function resolveTargetHash(): string {
  const section = String(route.query.section ?? '').trim()
  if (section) return `#/home?section=${encodeURIComponent(section)}`
  const queryTab = String(route.query.tab ?? '').trim()
  if (route.path === '/image-studio' || route.path === '/imagegen' || queryTab === 'imagegen') {
    return '#/home?section=quickImage'
  }
  if (queryTab === 'trumanMap') return '#/home?section=trumanMap'
  return '#/home'
}

const initialHash = resolveTargetHash()
const studioInitialSrc = computed(() => `/creative-studio/${initialHash}`)

function detectCurrentTheme(): 'dark' | 'light' {
  return isDarkSurface() ? 'dark' : 'light'
}

const sanitizedSiteLogo = computed(() =>
  sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }),
)

function buildHostContext(): Sub2ApiToonflowContext {
  const user = authStore.user
  return {
    origin: typeof window !== 'undefined' ? window.location.origin : '',
    locale: locale.value === 'en' ? 'en' : 'zh-CN',
    theme: detectCurrentTheme(),
    siteName: '',
    siteLogo: sanitizedSiteLogo.value,
    user: user
      ? {
          id: user.id,
          email: user.email,
          username: user.username,
          role: user.role,
        }
      : null,
    keys: serializeKeysForToonflow(ownKeys.value),
    selectedTextKeyId: bindings.textKeyId,
    selectedImageKeyId: bindings.imageKeyId,
    selectedVideoKeyId: bindings.videoKeyId,
    selectedAudioKeyId: bindings.audioKeyId,
    defaultTextModel: bindings.defaultTextModel ?? '',
    defaultImageModel: bindings.defaultImageModel ?? '',
    defaultVideoModel: bindings.defaultVideoModel ?? '',
    defaultAudioModel: bindings.defaultAudioModel ?? '',
    rewriteModel: bindings.rewriteModel ?? '',
    rewriteEffort: bindings.rewriteEffort ?? 'unset',
    discoveredModels: Object.fromEntries(
      Object.entries(discoveredModels).map(([k, list]) => [k, Array.isArray(list) ? [...list] : []]),
    ),
    discoveredModelMeta: Object.fromEntries(
      Object.entries(discoveredModelMeta).map(([k, list]) => [
        k,
        Array.isArray(list)
          ? list.map((item) => ({
              ...item,
              ...(item.reasoningEfforts
                ? { reasoningEfforts: item.reasoningEfforts.map((e) => ({ ...e })) }
                : {}),
            }))
          : [],
      ]),
    ),
  }
}

function syncHostContextToStudio(): void {
  const ctx = buildHostContext()
  writeToonflowHostContext(ctx)
  postToonflowMessage(studioFrameRef.value, 'syncContext', ctx)
}

async function discoverModelsForKeyIds(keyIds: Array<number | null | undefined>, force = false): Promise<void> {
  const uniqueIds = [
    ...new Set(keyIds.filter((id): id is number => typeof id === 'number' && Number.isFinite(id) && id > 0)),
  ]
  const toFetch = force ? uniqueIds : uniqueIds.filter((id) => !discoveredModels[id]?.length)
  if (!toFetch.length) return
  await Promise.all(
    toFetch.map(async (keyId) => {
      const keyObj = ownKeys.value.find((k) => k.id === keyId)
      if (!keyObj) return
      const { models, meta } = await discoverModelDetailsForKey(keyObj.key)
      discoveredModels[keyId] = models
      discoveredModelMeta[keyId] = meta
    }),
  )
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
    syncHostContextToStudio()

    await discoverModelsForKeyIds(
      [bindings.textKeyId, bindings.imageKeyId, bindings.videoKeyId, bindings.audioKeyId],
      true,
    )
    syncHostContextToStudio()
  } finally {
    loadingKeys.value = false
  }
}

function normalizeOptionalKeyId(value: unknown): number | null {
  if (value === null || value === undefined || value === '') return null
  const num = Number(value)
  return Number.isFinite(num) && num > 0 ? num : null
}

function handleStudioBindingsUpdate(payload: Record<string, unknown>): void {
  if ('textKeyId' in payload) bindings.textKeyId = normalizeOptionalKeyId(payload.textKeyId)
  if ('imageKeyId' in payload) bindings.imageKeyId = normalizeOptionalKeyId(payload.imageKeyId)
  if ('videoKeyId' in payload) bindings.videoKeyId = normalizeOptionalKeyId(payload.videoKeyId)
  if ('audioKeyId' in payload) bindings.audioKeyId = normalizeOptionalKeyId(payload.audioKeyId)
  if (typeof payload.defaultTextModel === 'string') bindings.defaultTextModel = payload.defaultTextModel
  if (typeof payload.defaultImageModel === 'string') bindings.defaultImageModel = payload.defaultImageModel
  if (typeof payload.defaultVideoModel === 'string') bindings.defaultVideoModel = payload.defaultVideoModel
  if (typeof payload.defaultAudioModel === 'string') bindings.defaultAudioModel = payload.defaultAudioModel
  if (typeof payload.rewriteModel === 'string') bindings.rewriteModel = payload.rewriteModel
  if (typeof payload.rewriteEffort === 'string') bindings.rewriteEffort = payload.rewriteEffort
  saveCreativeKeyBindings({ ...bindings })
  syncHostContextToStudio()
  void discoverModelsForKeyIds([
    bindings.textKeyId,
    bindings.imageKeyId,
    bindings.videoKeyId,
    bindings.audioKeyId,
  ]).then(() => {
    syncHostContextToStudio()
  })
}

function handleStudioDiscoveredModelsUpdate(payload: Record<string, unknown>): void {
  const keyId = normalizeOptionalKeyId(payload.keyId)
  if (!keyId) return
  const models = Array.isArray(payload.models)
    ? [...new Set(payload.models.map((item) => String(item || '').trim()).filter(Boolean))]
    : []
  const meta = Array.isArray(payload.meta)
    ? (payload.meta as DiscoveredModelMeta[])
    : models.map((id) => ({ id }))
  discoveredModels[keyId] = models
  discoveredModelMeta[keyId] = meta
  syncHostContextToStudio()
}

function onStudioFrameLoad(): void {
  syncHostContextToStudio()
}

function onWindowMessage(event: MessageEvent): void {
  if (event.origin !== window.location.origin) return
  const msg = event.data as { source?: string; type?: string; payload?: unknown } | null
  if (!msg || msg.source !== 'toonflow-web') return
  if (msg.type === 'ready') {
    syncHostContextToStudio()
  } else if (msg.type === 'updateKeyBindings' && msg.payload && typeof msg.payload === 'object') {
    handleStudioBindingsUpdate(msg.payload as Record<string, unknown>)
  } else if (msg.type === 'updateDiscoveredModels' && msg.payload && typeof msg.payload === 'object') {
    handleStudioDiscoveredModelsUpdate(msg.payload as Record<string, unknown>)
  } else if (msg.type === 'refreshKeys') {
    void refreshKeysAndModels()
  } else if (msg.type === 'openSiteKeys') {
    void router.push('/keys')
  }
}

let themeObserver: MutationObserver | null = null

watch(
  () => [route.path, route.query.tab, route.query.section],
  () => {
    postToonflowMessage(studioFrameRef.value, 'navigate', resolveTargetHash())
  },
)

watch(locale, () => {
  syncHostContextToStudio()
})

watch(
  () => [authStore.user, sanitizedSiteLogo.value],
  () => {
    syncHostContextToStudio()
  },
)

onMounted(() => {
  window.addEventListener('message', onWindowMessage)
  syncHostContextToStudio()
  void refreshKeysAndModels()
  if (typeof MutationObserver !== 'undefined' && typeof document !== 'undefined') {
    themeObserver = new MutationObserver(() => syncHostContextToStudio())
    themeObserver.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['class', 'data-universe-theme'],
    })
  }
})

onBeforeUnmount(() => {
  window.removeEventListener('message', onWindowMessage)
  themeObserver?.disconnect()
  themeObserver = null
})
</script>
