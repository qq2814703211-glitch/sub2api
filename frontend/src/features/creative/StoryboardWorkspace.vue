<template>
  <div class="space-y-5" data-test="creative-storyboard-workspace">
    <!-- AI Director & Story Synopsis -->
    <section class="card">
      <div class="card-body space-y-3">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div class="min-w-0">
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('creative.storyboard.directorTitle') }}
            </h3>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
              {{ t('creative.storyboard.directorHint') }}
            </p>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              :disabled="shots.length === 0"
              @click="emit('pushToCanvas', false)"
            >
              <Icon name="arrowRight" size="xs" />
              {{ t('creative.storyboard.pushAppend') }}
            </button>
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              :disabled="shots.length === 0"
              @click="emit('pushToCanvas', true)"
            >
              <Icon name="sync" size="xs" />
              {{ t('creative.storyboard.pushReplace') }}
            </button>
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              :disabled="shots.length === 0"
              @click="emit('exportMarkdown')"
            >
              <Icon name="download" size="xs" />
              {{ t('creative.storyboard.exportMd') }}
            </button>
          </div>
        </div>

        <textarea
          :value="synopsis"
          rows="3"
          class="input w-full resize-y"
          :placeholder="t('creative.storyboard.synopsisPlaceholder')"
          @input="emit('updateSynopsis', ($event.target as HTMLTextAreaElement).value)"
        />

        <div class="flex flex-wrap items-center gap-2">
          <label class="flex items-center gap-1.5 text-xs text-gray-600 dark:text-dark-300">
            <span>{{ t('creative.storyboard.shotCount') }}</span>
            <select v-model.number="targetShotCount" class="input h-8 w-20 text-xs">
              <option v-for="n in [2, 3, 4, 5, 6, 8]" :key="n" :value="n">{{ n }}</option>
            </select>
          </label>
          <input
            v-model="directorModel"
            list="creative-storyboard-text-models"
            class="input h-8 w-52 text-xs"
            :placeholder="t('creative.storyboard.directorModelPlaceholder')"
          />
          <button
            type="button"
            class="btn btn-primary btn-sm"
            data-test="storyboard-ai-generate"
            :disabled="generating"
            @click="emit('generateShots', targetShotCount, directorModel)"
          >
            <Icon name="sparkles" size="xs" />
            {{ generating ? t('creative.storyboard.generating') : t('creative.storyboard.aiGenerate') }}
          </button>
          <button type="button" class="btn btn-secondary btn-sm" @click="emit('addShot')">
            <Icon name="plus" size="xs" />
            {{ t('creative.storyboard.addShot') }}
          </button>
          <button
            v-if="playableShots.length > 0"
            type="button"
            class="btn btn-secondary btn-sm"
            @click="togglePreviewPlayer"
          >
            <Icon :name="previewPlaying ? 'x' : 'play'" size="xs" />
            {{ previewPlaying ? t('creative.storyboard.stopPreview') : t('creative.storyboard.localPreview') }}
          </button>
        </div>
      </div>
    </section>

    <!-- Local browser storyboard slideshow preview -->
    <section v-if="previewOpen && currentPreviewShot" class="card overflow-hidden">
      <div class="card-header flex items-center justify-between gap-2">
        <span class="text-xs font-semibold text-gray-900 dark:text-white">
          {{ t('creative.storyboard.previewHeading') }} · #{{ currentPreviewShot.index }} {{ currentPreviewShot.title }}
        </span>
        <div class="flex items-center gap-1.5">
          <button type="button" class="btn btn-secondary btn-sm" @click="stepPreview(-1)">
            <Icon name="chevronLeft" size="xs" />
          </button>
          <span class="text-xs text-gray-500 dark:text-dark-400">
            {{ previewIndex + 1 }} / {{ playableShots.length }}
          </span>
          <button type="button" class="btn btn-secondary btn-sm" @click="stepPreview(1)">
            <Icon name="chevronRight" size="xs" />
          </button>
          <button type="button" class="btn btn-ghost btn-sm" @click="closePreview">
            <Icon name="x" size="xs" />
          </button>
        </div>
      </div>
      <div class="relative flex min-h-[300px] items-center justify-center bg-black p-4">
        <video
          v-if="currentPreviewShot.videoUrl"
          :src="currentPreviewShot.videoUrl"
          controls
          autoplay
          class="max-h-[400px] rounded-lg"
        />
        <img
          v-else-if="currentPreviewShot.imageDataUrl"
          :src="currentPreviewShot.imageDataUrl"
          :alt="currentPreviewShot.title"
          class="max-h-[400px] rounded-lg object-contain"
        />
        <div
          v-if="currentPreviewShot.dialogue"
          class="absolute bottom-4 left-1/2 max-w-2xl -translate-x-1/2 rounded-lg bg-black/75 px-4 py-2 text-center text-sm text-white backdrop-blur"
        >
          {{ currentPreviewShot.dialogue }}
        </div>
      </div>
    </section>

    <!-- Shot cards -->
    <div v-if="shots.length === 0" class="card px-6 py-10 text-center">
      <p class="text-sm font-medium text-gray-700 dark:text-dark-200">
        {{ t('creative.storyboard.emptyTitle') }}
      </p>
      <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
        {{ t('creative.storyboard.emptyHint') }}
      </p>
    </div>

    <div v-else class="grid grid-cols-1 gap-4 lg:grid-cols-2">
      <article
        v-for="(shot, idx) in shots"
        :key="shot.id"
        class="card flex flex-col justify-between"
      >
        <div class="card-body space-y-3">
          <div class="flex items-center justify-between gap-2">
            <div class="flex min-w-0 flex-1 items-center gap-2">
              <span class="badge badge-primary shrink-0">#{{ shot.index }}</span>
              <input
                :value="shot.scene"
                type="text"
                class="input h-7 w-28 text-xs"
                :placeholder="t('creative.storyboard.sceneLabel')"
                @input="emit('updateShot', shot.id, { scene: ($event.target as HTMLInputElement).value })"
              />
              <input
                :value="shot.title"
                type="text"
                class="input h-7 min-w-0 flex-1 text-xs font-medium"
                :placeholder="t('creative.storyboard.titleLabel')"
                @input="emit('updateShot', shot.id, { title: ($event.target as HTMLInputElement).value })"
              />
            </div>
            <div class="flex shrink-0 items-center gap-1">
              <input
                :value="shot.durationSec"
                type="number"
                min="2"
                max="15"
                class="input h-7 w-14 text-center text-xs"
                :title="t('creative.nodes.durationSec')"
                @input="emit('updateShot', shot.id, { durationSec: Number(($event.target as HTMLInputElement).value) || 5 })"
              />
              <button
                type="button"
                class="btn btn-ghost btn-icon"
                :disabled="idx === 0"
                @click="emit('moveShot', idx, idx - 1)"
              >
                <Icon name="arrowUp" size="xs" />
              </button>
              <button
                type="button"
                class="btn btn-ghost btn-icon"
                :disabled="idx === shots.length - 1"
                @click="emit('moveShot', idx, idx + 1)"
              >
                <Icon name="arrowDown" size="xs" />
              </button>
              <button
                type="button"
                class="btn btn-ghost btn-icon text-red-600 dark:text-red-400"
                @click="emit('removeShot', shot.id)"
              >
                <Icon name="trash" size="xs" />
              </button>
            </div>
          </div>

          <!-- Keyframe preview / upload -->
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-[170px_minmax(0,1fr)]">
            <div class="space-y-2">
              <div class="flex h-28 w-full items-center justify-center overflow-hidden rounded-lg border border-gray-200 bg-gray-100 dark:border-dark-700 dark:bg-dark-800">
                <img
                  v-if="shot.imageDataUrl"
                  :src="shot.imageDataUrl"
                  :alt="shot.title"
                  class="h-full w-full object-cover"
                />
                <span v-else class="px-2 text-center text-[11px] text-gray-400 dark:text-dark-400">
                  {{ t('creative.storyboard.noKeyframe') }}
                </span>
              </div>
              <div class="flex flex-wrap gap-1">
                <button
                  type="button"
                  class="btn btn-primary btn-sm flex-1 text-[11px]"
                  :disabled="busyShotId === shot.id"
                  @click="onGenerateShotKeyframe(shot.id)"
                >
                  <Icon name="sparkles" size="xs" />
                  {{ busyShotId === shot.id ? t('imagegen.generating') : t('creative.storyboard.genKeyframe') }}
                </button>
                <label class="btn btn-secondary btn-sm cursor-pointer text-[11px]">
                  <Icon name="upload" size="xs" />
                  <input
                    type="file"
                    accept="image/png,image/jpeg,image/webp"
                    class="hidden"
                    @change="onUploadShotImage(shot.id, $event)"
                  />
                </label>
              </div>
              <div v-if="shot.imageDataUrl" class="flex gap-1">
                <button
                  type="button"
                  class="btn btn-secondary btn-sm flex-1 text-[10px]"
                  @click="emit('saveMediaAsset', { name: `分镜#${shot.index}-${shot.title}`, category: 'scene', kind: 'image', mimeType: 'image/png', dataUrl: shot.imageDataUrl, prompt: shot.visualPrompt })"
                >
                  {{ t('creative.actions.saveAsset') }}
                </button>
                <button
                  type="button"
                  class="btn btn-secondary btn-sm flex-1 text-[10px]"
                  @click="emit('sendToQuickStudio', { prompt: shot.visualPrompt, imageDataUrl: shot.imageDataUrl, title: shot.title })"
                >
                  {{ t('creative.actions.editInQuickStudio') }}
                </button>
              </div>
            </div>

            <div class="space-y-2">
              <label class="block">
                <span class="input-label text-[11px]">{{ t('creative.storyboard.visualPrompt') }}</span>
                <textarea
                  :value="shot.visualPrompt"
                  rows="3"
                  class="input w-full resize-y text-xs"
                  @input="emit('updateShot', shot.id, { visualPrompt: ($event.target as HTMLTextAreaElement).value })"
                />
              </label>
              <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
                <label class="block">
                  <span class="input-label text-[11px]">{{ t('creative.storyboard.cameraMotion') }}</span>
                  <input
                    :value="shot.cameraMotion"
                    type="text"
                    class="input h-8 text-xs"
                    @input="emit('updateShot', shot.id, { cameraMotion: ($event.target as HTMLInputElement).value })"
                  />
                </label>
                <label class="block">
                  <span class="input-label text-[11px]">{{ t('creative.storyboard.dialogue') }}</span>
                  <input
                    :value="shot.dialogue"
                    type="text"
                    class="input h-8 text-xs"
                    @input="emit('updateShot', shot.id, { dialogue: ($event.target as HTMLInputElement).value })"
                  />
                </label>
              </div>
            </div>
          </div>
        </div>
      </article>
    </div>

    <datalist id="creative-storyboard-text-models">
      <option v-for="m in textModels" :key="m" :value="m" />
    </datalist>
  </div>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { optimizeLocalImageDataUrl } from './mediaBrowser'
import type { AssetCategory, QuickImageBridgePayload, StoryboardShot } from './types'

const props = defineProps<{
  synopsis: string
  shots: StoryboardShot[]
  generating: boolean
  textModels: string[]
}>()

const emit = defineEmits<{
  updateSynopsis: [value: string]
  addShot: []
  updateShot: [shotId: string, patch: Partial<StoryboardShot>]
  removeShot: [shotId: string]
  moveShot: [fromIndex: number, toIndex: number]
  generateShots: [count: number, model: string]
  generateShotKeyframe: [shotId: string, done: () => void]
  pushToCanvas: [replaceExisting: boolean]
  exportMarkdown: []
  saveMediaAsset: [payload: { name: string; category: AssetCategory; kind: 'image' | 'video'; mimeType: string; dataUrl: string; prompt?: string }]
  sendToQuickStudio: [payload: QuickImageBridgePayload]
}>()

const { t } = useI18n()

const targetShotCount = ref(4)
const directorModel = ref('')
const busyShotId = ref<string | null>(null)
const previewOpen = ref(false)
const previewPlaying = ref(false)
const previewIndex = ref(0)
let previewTimer: ReturnType<typeof setTimeout> | null = null

const playableShots = computed(() =>
  props.shots.filter((shot) => Boolean(shot.imageDataUrl || shot.videoUrl)),
)

const currentPreviewShot = computed(
  () => playableShots.value[previewIndex.value] ?? playableShots.value[0] ?? null,
)

function scheduleNextSlide(): void {
  if (previewTimer) clearTimeout(previewTimer)
  if (!previewPlaying.value || playableShots.value.length <= 1) return
  const sec = Math.max(2, currentPreviewShot.value?.durationSec || 4)
  previewTimer = setTimeout(() => {
    previewIndex.value = (previewIndex.value + 1) % playableShots.value.length
    scheduleNextSlide()
  }, sec * 1000)
}

function togglePreviewPlayer(): void {
  if (previewPlaying.value) {
    closePreview()
    return
  }
  previewIndex.value = 0
  previewOpen.value = true
  previewPlaying.value = true
  scheduleNextSlide()
}

function stepPreview(delta: number): void {
  if (!playableShots.value.length) return
  previewIndex.value =
    (previewIndex.value + delta + playableShots.value.length) % playableShots.value.length
  if (previewPlaying.value) scheduleNextSlide()
}

function closePreview(): void {
  if (previewTimer) clearTimeout(previewTimer)
  previewTimer = null
  previewPlaying.value = false
  previewOpen.value = false
}

function onGenerateShotKeyframe(shotId: string): void {
  busyShotId.value = shotId
  emit('generateShotKeyframe', shotId, () => {
    if (busyShotId.value === shotId) busyShotId.value = null
  })
}

async function onUploadShotImage(shotId: string, event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  const prepared = await optimizeLocalImageDataUrl(file)
  emit('updateShot', shotId, { imageDataUrl: prepared.dataUrl })
}

onUnmounted(() => {
  if (previewTimer) clearTimeout(previewTimer)
})
</script>
