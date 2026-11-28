<template>
  <AppLayout>
    <div
      class="flex flex-col gap-2"
      :class="fullscreen ? 'fixed inset-0 z-50 bg-white p-2 dark:bg-gray-950' : ''"
      data-test="creative-plaza-view"
    >
      <div class="flex flex-wrap items-center justify-between gap-2 px-1">
        <div class="flex items-center gap-2 min-w-0">
          <button
            v-if="!loadingKeys && ownKeys.length === 0"
            type="button"
            class="badge badge-warning inline-flex items-center gap-1 text-[11px] hover:opacity-90"
            data-test="creative-no-keys-link"
            @click="router.push('/keys')"
          >
            <Icon name="key" size="xs" />
            <span>{{ t('creative.keys.createKeyAction') }}</span>
          </button>
        </div>

        <div class="flex items-center gap-1.5">
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
            :href="studioInitialSrc"
            target="_blank"
            rel="noopener noreferrer"
            class="btn btn-secondary btn-xs inline-flex items-center gap-1"
            data-test="open-studio-standalone"
          >
            <Icon name="externalLink" size="xs" />
            <span>{{ t('creative.quickNav.openNewTab') }}</span>
          </a>
        </div>
      </div>

      <section
        class="relative w-full overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-gray-950"
        :style="{ height: fullscreen ? 'calc(100vh - 48px)' : 'calc(100vh - 152px)', minHeight: '560px' }"
        data-test="studio-iframe-container"
      >
        <iframe
          ref="studioFrameRef"
          :src="studioInitialSrc"
          :title="t('creative.title')"
          class="h-full w-full border-0"
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
const fullscreen = ref(false)
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
const studioInitialSrc = computed(() => `/toonflow/index.html${initialHash}`)

function detectCurrentTheme(): 'dark' | 'light' {
  if (typeof document !== 'undefined' && document.documentElement.classList.contains('dark')) {
    return 'dark'
  }
  return 'light'
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
    rewriteModel: bindings.rewriteModel ?? '',
    rewriteEffort: bindings.rewriteEffort ?? 'unset',
    discoveredModels: { ...discoveredModels },
    discoveredModelMeta: { ...discoveredModelMeta },
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
    themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
  }
})

onBeforeUnmount(() => {
  window.removeEventListener('message', onWindowMessage)
  themeObserver?.disconnect()
  themeObserver = null
})
</script>
