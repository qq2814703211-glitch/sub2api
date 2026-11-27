<template>
  <AppLayout>
    <div class="space-y-3" data-test="creative-plaza-view">
      <!-- Unified Creative Plaza Header & Mode Switcher -->
      <section class="card px-4 py-3">
        <div class="flex flex-wrap items-start justify-between gap-4">
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
              <span class="badge badge-gray text-[11px]">
                {{ t('creative.localStorageLabel', { usage: localUsageSummary }) }}
              </span>
            </div>
            <p class="mt-1.5 max-w-4xl text-xs leading-relaxed text-gray-500 dark:text-dark-400">
              {{ t('creative.intro') }}
            </p>
          </div>

          <!-- Primary Mode Tabs -->
          <div
            class="inline-flex flex-wrap items-center gap-1 rounded-xl border border-gray-200 bg-gray-100/80 p-1 dark:border-dark-700 dark:bg-dark-800/80"
            role="tablist"
            :aria-label="t('creative.title')"
          >
            <button
              v-for="tab in tabs"
              :key="tab.id"
              type="button"
              role="tab"
              :aria-selected="activeTab === tab.id"
              :data-test="`creative-tab-${tab.id}`"
              class="inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors"
              :class="
                activeTab === tab.id
                  ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-900 dark:text-primary-300'
                  : 'text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white'
              "
              @click="switchTab(tab.id)"
            >
              <Icon :name="tab.icon" size="xs" />
              <span>{{ t(tab.labelKey) }}</span>
              <span
                v-if="tab.badge !== undefined"
                class="rounded-full bg-gray-200/80 px-1.5 py-0.5 text-[10px] text-gray-700 dark:bg-dark-700 dark:text-dark-200"
              >
                {{ tab.badge }}
              </span>
            </button>
          </div>
        </div>

        <!-- Project & Shared Gateway Key Strip (shown for Canvas / Storyboard / Assets) -->
        <div
          v-if="activeTab !== 'imagegen' && activeProject"
          class="mt-4 flex flex-wrap items-center justify-between gap-2 border-t border-gray-100 pt-3 dark:border-dark-800"
        >
          <!-- Local project selector & actions -->
          <div class="flex flex-wrap items-center gap-2">
            <label class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-400">
              <span class="hidden sm:inline">{{ t('creative.project.selectorLabel') }}</span>
              <select
                :value="activeProject.id"
                class="input h-8 w-56 text-xs font-medium"
                data-test="creative-project-select"
                @change="selectProject(($event.target as HTMLSelectElement).value)"
              >
                <option v-for="proj in projects" :key="proj.id" :value="proj.id">
                  {{ proj.name }}
                </option>
              </select>
            </label>

            <div class="relative">
              <select
                v-model="newTemplatePick"
                class="input h-8 w-44 text-xs"
                :aria-label="t('creative.project.newProject')"
                @change="onPickNewTemplate"
              >
                <option value="">+ {{ t('creative.project.newProject') }}...</option>
                <option value="short-drama">{{ t('creative.project.templateShortDrama') }}</option>
                <option value="character-sheet">{{ t('creative.project.templateCharacterSheet') }}</option>
                <option value="blank">{{ t('creative.project.templateBlank') }}</option>
              </select>
            </div>

            <button type="button" class="btn btn-secondary btn-sm text-xs" @click="onRenameProject">
              {{ t('creative.project.rename') }}
            </button>
            <button type="button" class="btn btn-secondary btn-sm text-xs" @click="duplicateActiveProject()">
              {{ t('creative.project.duplicate') }}
            </button>
            <button type="button" class="btn btn-secondary btn-sm text-xs" @click="exportActiveProject(false)">
              <Icon name="download" size="xs" />
              {{ t('creative.project.exportJson') }}
            </button>
            <label class="btn btn-secondary btn-sm cursor-pointer text-xs">
              <Icon name="upload" size="xs" />
              {{ t('creative.project.importJson') }}
              <input
                type="file"
                accept=".json,.toonflow.json,application/json"
                class="hidden"
                @change="onImportProjectFile"
              />
            </label>
            <button
              type="button"
              class="btn btn-ghost btn-sm text-xs text-red-600 dark:text-red-400"
              @click="removeActiveProject()"
            >
              <Icon name="trash" size="xs" />
              {{ t('creative.project.delete') }}
            </button>
          </div>

          <!-- Toggle shared gateway key panel -->
          <button
            type="button"
            class="btn btn-secondary btn-sm text-xs"
            data-test="creative-keys-toggle"
            @click="keyStripOpen = !keyStripOpen"
          >
            <Icon name="key" size="xs" />
            {{ keyStripOpen ? t('creative.keys.toggleHide') : t('creative.keys.toggleShow') }}
          </button>
        </div>

        <!-- Collapsible Station Gateway Key Bindings for Canvas / Storyboard -->
        <div
          v-if="activeTab !== 'imagegen' && keyStripOpen"
          class="mt-3 rounded-xl border border-gray-200 bg-gray-50/80 p-3 dark:border-dark-700 dark:bg-dark-800/60"
          data-test="creative-keys-panel"
        >
          <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
            <span class="text-xs font-semibold text-gray-900 dark:text-white">
              {{ t('creative.keys.panelTitle') }}
            </span>
            <div class="flex items-center gap-2 text-[11px] text-gray-500 dark:text-dark-400">
              <span>
                {{
                  t('creative.keys.modelsDiscovered', {
                    text: textModels.length,
                    image: imageModels.length,
                    video: videoModels.length,
                  })
                }}
              </span>
              <button
                type="button"
                class="btn btn-ghost btn-sm text-[11px]"
                :disabled="modelsLoading"
                @click="loadOwnKeys()"
              >
                <Icon name="refresh" size="xs" />
                {{ t('creative.keys.refreshKeys') }}
              </button>
            </div>
          </div>

          <div class="grid grid-cols-1 gap-2.5 md:grid-cols-3">
            <label class="block">
              <span class="input-label text-[11px]">{{ t('creative.keys.imageKey') }}</span>
              <select
                :value="keyBindings.imageKeyId === null ? '' : String(keyBindings.imageKeyId)"
                class="input h-8 text-xs"
                data-test="creative-image-key-select"
                @change="onKeySelect('imageKeyId', ($event.target as HTMLSelectElement).value)"
              >
                <option value="">{{ t('creative.keys.selectPlaceholder') }}</option>
                <option v-for="k in formattedKeyOptions" :key="k.id" :value="String(k.id)">
                  {{ k.label }}
                </option>
              </select>
            </label>

            <label class="block">
              <span class="input-label text-[11px]">{{ t('creative.keys.textKey') }}</span>
              <select
                :value="keyBindings.textKeyId === null ? '' : String(keyBindings.textKeyId)"
                class="input h-8 text-xs"
                data-test="creative-text-key-select"
                @change="onKeySelect('textKeyId', ($event.target as HTMLSelectElement).value)"
              >
                <option value="">{{ t('creative.keys.fallbackToImage') }}</option>
                <option v-for="k in formattedKeyOptions" :key="k.id" :value="String(k.id)">
                  {{ k.label }}
                </option>
              </select>
            </label>

            <label class="block">
              <span class="input-label text-[11px]">{{ t('creative.keys.videoKey') }}</span>
              <select
                :value="keyBindings.videoKeyId === null ? '' : String(keyBindings.videoKeyId)"
                class="input h-8 text-xs"
                data-test="creative-video-key-select"
                @change="onKeySelect('videoKeyId', ($event.target as HTMLSelectElement).value)"
              >
                <option value="">{{ t('creative.keys.fallbackToImage') }}</option>
                <option v-for="k in formattedKeyOptions" :key="k.id" :value="String(k.id)">
                  {{ k.label }}
                </option>
              </select>
            </label>
          </div>
        </div>
      </section>

      <!-- Tab 1: Toonflow Infinite Canvas -->
      <InfiniteCanvas
        v-if="activeTab === 'canvas' && activeProject"
        :nodes="activeProject.nodes"
        :edges="activeProject.edges"
        :viewport="activeProject.viewport"
        :running="running"
        :can-undo="canUndo"
        :can-redo="canRedo"
        :text-models="textModels"
        :image-models="imageModels"
        :video-models="videoModels"
        @add-node="(type, x, y) => addNode(type, { x, y })"
        @update-title="updateNodeTitle"
        @update-data="updateNodeData"
        @move-node="moveNode"
        @duplicate-node="duplicateNode"
        @remove-node="(id) => removeNodes([id])"
        @connect="connectNodes"
        @remove-edge="removeEdge"
        @arrange="arrangeCanvas"
        @undo="undo"
        @redo="redo"
        @set-viewport="setViewport"
        @run-node="runSingleNode"
        @run-workflow="runWorkflow()"
        @stop-execution="stopExecution"
        @drop-files="handleCanvasFileDrop"
        @spawn-frame-node="spawnImageNodeFromFrame"
        @save-media-asset="(payload) => void saveMediaToAssets(payload)"
        @send-to-quick-studio="openInQuickStudio"
      />

      <!-- Tab 2: Fused Existing Quick Image Studio (kept mounted once visited for state continuity) -->
      <div v-show="activeTab === 'imagegen'">
        <ImageGenView
          v-if="imageGenMounted"
          ref="quickStudioRef"
          embedded
          @send-to-canvas="onQuickImageToCanvas"
          @save-to-assets="onQuickImageToAssets"
        />
      </div>

      <!-- Tab 3: Toonflow-Style Script & Storyboard Workspace -->
      <StoryboardWorkspace
        v-if="activeTab === 'storyboard' && activeProject"
        :synopsis="activeProject.scriptSynopsis"
        :shots="activeProject.shots"
        :generating="generatingShots"
        :text-models="textModels"
        @update-synopsis="updateScriptSynopsis"
        @add-shot="addStoryboardShot()"
        @update-shot="updateStoryboardShot"
        @remove-shot="removeStoryboardShot"
        @move-shot="moveStoryboardShot"
        @generate-shots="(count, model) => void generateStoryboardFromSynopsis(count, model)"
        @generate-shot-keyframe="onGenerateShotKeyframe"
        @push-to-canvas="onPushStoryboardToCanvas"
        @export-markdown="exportStoryboardMarkdown"
        @save-media-asset="(payload) => void saveMediaToAssets(payload)"
        @send-to-quick-studio="openInQuickStudio"
      />

      <!-- Tab 4: Browser-Local Asset Library -->
      <AssetLibraryPanel
        v-if="activeTab === 'assets'"
        :assets="assets"
        :local-usage-summary="localUsageSummary"
        @upload-files="(files, cat) => void uploadAssetFiles(files, cat)"
        @insert-to-canvas="onInsertAssetToCanvas"
        @send-to-quick-studio="openInQuickStudio"
        @remove-asset="(id) => void removeAsset(id)"
        @clear-assets="() => void clearAssets()"
      />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import ImageGenView from '@/views/ImageGenView.vue'
import InfiniteCanvas from '@/features/creative/InfiniteCanvas.vue'
import StoryboardWorkspace from '@/features/creative/StoryboardWorkspace.vue'
import AssetLibraryPanel from '@/features/creative/AssetLibraryPanel.vue'
import { useCreativePlaza } from '@/features/creative/useCreativePlaza'
import type {
  CreativeAsset,
  CreativeKeyBindings,
  CreativeTab,
  QuickImageBridgePayload,
  WorkflowTemplateId,
} from '@/features/creative/types'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const appStore = useAppStore()

function notify(message: string, kind: 'success' | 'error' | 'info' = 'info'): void {
  appStore.showToast(kind, message, kind === 'error' ? 5000 : 3000)
}

const {
  projects,
  activeProject,
  assets,
  localUsageSummary,
  ownKeys,
  keyBindings,
  textModels,
  imageModels,
  videoModels,
  modelsLoading,
  canUndo,
  canRedo,
  running,
  generatingShots,
  initialize,
  loadOwnKeys,
  setKeyBinding,
  selectProject,
  createProject,
  renameActiveProject,
  duplicateActiveProject,
  removeActiveProject,
  exportActiveProject,
  importProjectFile,
  addNode,
  updateNodeTitle,
  updateNodeData,
  moveNode,
  duplicateNode,
  removeNodes,
  connectNodes,
  removeEdge,
  arrangeCanvas,
  setViewport,
  handleCanvasFileDrop,
  undo,
  redo,
  runSingleNode,
  runWorkflow,
  stopExecution,
  spawnImageNodeFromFrame,
  updateScriptSynopsis,
  addStoryboardShot,
  updateStoryboardShot,
  removeStoryboardShot,
  moveStoryboardShot,
  generateStoryboardFromSynopsis,
  generateKeyframeForShot,
  pushStoryboardToCanvas,
  exportStoryboardMarkdown,
  saveMediaToAssets,
  uploadAssetFiles,
  removeAsset,
  clearAssets,
  insertAssetToCanvas,
  sendQuickImageToCanvas,
} = useCreativePlaza(notify)

function resolveInitialTab(): CreativeTab {
  const queryTab = String(route.query.tab ?? '').trim()
  if (queryTab === 'canvas' || queryTab === 'imagegen' || queryTab === 'storyboard' || queryTab === 'assets') {
    return queryTab
  }
  // Legacy /image-studio and /imagegen bookmarks open directly on the Quick Image Studio tab
  // while still exposing the full Creative Plaza tabs at the top.
  if (route.path === '/image-studio' || route.path === '/imagegen') {
    return 'imagegen'
  }
  return 'canvas'
}

const activeTab = ref<CreativeTab>(resolveInitialTab())
const imageGenMounted = ref(activeTab.value === 'imagegen')
const keyStripOpen = ref(false)
const newTemplatePick = ref<'' | WorkflowTemplateId>('')
const quickStudioRef = ref<{ loadBridgePayload?: (payload: QuickImageBridgePayload) => Promise<void> } | null>(null)

const tabs = computed<
  Array<{ id: CreativeTab; labelKey: string; icon: 'grid' | 'sparkles' | 'book' | 'cube'; badge?: number }>
>(() => [
  {
    id: 'canvas',
    labelKey: 'creative.tabs.canvas',
    icon: 'grid',
    badge: activeProject.value?.nodes.length ?? 0,
  },
  {
    id: 'imagegen',
    labelKey: 'creative.tabs.imagegen',
    icon: 'sparkles',
  },
  {
    id: 'storyboard',
    labelKey: 'creative.tabs.storyboard',
    icon: 'book',
    badge: activeProject.value?.shots.length ?? 0,
  },
  {
    id: 'assets',
    labelKey: 'creative.tabs.assets',
    icon: 'cube',
    badge: assets.value.length,
  },
])

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

function switchTab(tab: CreativeTab): void {
  activeTab.value = tab
  if (tab === 'imagegen') imageGenMounted.value = true
  if (route.query.tab !== tab) {
    void router.replace({ path: route.path, query: { ...route.query, tab } })
  }
}

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

function onKeySelect(role: keyof CreativeKeyBindings, raw: string): void {
  const num = raw ? Number(raw) : null
  setKeyBinding(role, num && Number.isFinite(num) && num > 0 ? num : null)
}

async function onPickNewTemplate(): Promise<void> {
  if (!newTemplatePick.value) return
  const tpl = newTemplatePick.value
  newTemplatePick.value = ''
  await createProject(tpl)
}

function onRenameProject(): void {
  if (!activeProject.value) return
  const next = window.prompt(t('creative.project.renamePrompt'), activeProject.value.name)
  if (next && next.trim()) {
    renameActiveProject(next.trim())
  }
}

async function onImportProjectFile(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  try {
    await importProjectFile(file)
  } catch (error) {
    notify(
      t('creative.project.importFailed', {
        error: error instanceof Error ? error.message : String(error),
      }),
      'error',
    )
  }
}

async function onGenerateShotKeyframe(shotId: string, done: () => void): Promise<void> {
  try {
    await generateKeyframeForShot(shotId)
  } finally {
    done()
  }
}

function onPushStoryboardToCanvas(replaceExisting: boolean): void {
  pushStoryboardToCanvas(replaceExisting)
  switchTab('canvas')
}

function onInsertAssetToCanvas(asset: CreativeAsset): void {
  insertAssetToCanvas(asset)
  switchTab('canvas')
}

function onQuickImageToCanvas(payload: { src: string; prompt?: string; title?: string }): void {
  sendQuickImageToCanvas(payload)
  switchTab('canvas')
}

async function onQuickImageToAssets(payload: {
  src: string
  prompt?: string
  format?: string
  title?: string
}): Promise<void> {
  await saveMediaToAssets({
    name: payload.title || '快捷生图作品',
    category: 'generated',
    kind: 'image',
    mimeType: `image/${payload.format || 'png'}`,
    dataUrl: payload.src,
    prompt: payload.prompt,
  })
}

async function openInQuickStudio(payload: QuickImageBridgePayload): Promise<void> {
  imageGenMounted.value = true
  switchTab('imagegen')
  await nextTick()
  await quickStudioRef.value?.loadBridgePayload?.(payload)
}

onMounted(() => {
  void initialize()
})
</script>
