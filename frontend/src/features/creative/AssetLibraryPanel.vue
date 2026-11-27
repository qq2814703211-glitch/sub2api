<template>
  <div class="space-y-4" data-test="creative-asset-library">
    <!-- Top filter & local upload bar -->
    <div class="card px-4 py-3">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex flex-wrap items-center gap-1.5">
          <button
            v-for="cat in categories"
            :key="cat.id"
            type="button"
            class="rounded-md border px-2.5 py-1 text-xs transition-colors"
            :class="
              activeCategory === cat.id
                ? 'border-primary-500 bg-primary-50 font-medium text-primary-700 dark:border-primary-500 dark:bg-primary-900/30 dark:text-primary-300'
                : 'border-gray-200 text-gray-600 hover:border-gray-300 dark:border-dark-700 dark:text-dark-300'
            "
            @click="activeCategory = cat.id"
          >
            {{ t(cat.labelKey) }}
            <span class="ml-1 opacity-60">{{ countFor(cat.id) }}</span>
          </button>
        </div>

        <div class="flex flex-wrap items-center gap-2">
          <div class="relative">
            <Icon
              name="search"
              size="xs"
              class="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-gray-400"
            />
            <input
              v-model="query"
              type="search"
              class="input h-8 w-44 pl-7 text-xs"
              :placeholder="t('creative.assets.searchPlaceholder')"
            />
          </div>

          <select v-model="uploadCategory" class="input h-8 w-28 text-xs">
            <option value="character">{{ t('creative.assets.catCharacter') }}</option>
            <option value="scene">{{ t('creative.assets.catScene') }}</option>
            <option value="prop">{{ t('creative.assets.catProp') }}</option>
            <option value="generated">{{ t('creative.assets.catGenerated') }}</option>
          </select>

          <label class="btn btn-primary btn-sm cursor-pointer">
            <Icon name="upload" size="xs" />
            {{ t('creative.assets.uploadLocal') }}
            <input
              type="file"
              multiple
              accept="image/png,image/jpeg,image/webp,video/*"
              class="hidden"
              @change="onFilePick"
            />
          </label>

          <button
            v-if="assets.length > 0"
            type="button"
            class="btn btn-secondary btn-sm text-red-600 dark:text-red-400"
            @click="emit('clearAssets')"
          >
            {{ t('creative.assets.clearAll') }}
          </button>
        </div>
      </div>

      <p class="mt-2 flex items-center gap-1.5 text-[11px] text-gray-500 dark:text-dark-400">
        <Icon name="database" size="xs" class="shrink-0 text-emerald-500" />
        <span>{{ t('creative.assets.localOnlyNotice', { usage: localUsageSummary }) }}</span>
      </p>
    </div>

    <!-- Empty state -->
    <div v-if="filteredAssets.length === 0" class="card px-6 py-12 text-center">
      <Icon name="cube" size="lg" class="mx-auto text-gray-400 dark:text-dark-500" />
      <p class="mt-2 text-sm font-medium text-gray-700 dark:text-dark-200">
        {{ t('creative.assets.emptyTitle') }}
      </p>
      <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
        {{ t('creative.assets.emptyHint') }}
      </p>
    </div>

    <!-- Asset cards grid -->
    <div v-else class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
      <article
        v-for="asset in filteredAssets"
        :key="asset.id"
        class="card flex flex-col overflow-hidden"
      >
        <div class="relative flex h-44 items-center justify-center bg-gray-100 dark:bg-dark-800">
          <video
            v-if="asset.kind === 'video'"
            :src="asset.dataUrl"
            controls
            playsinline
            class="h-full w-full object-contain bg-black"
          />
          <img
            v-else
            :src="asset.dataUrl"
            :alt="asset.name"
            class="h-full w-full object-cover"
          />
          <span class="badge badge-gray absolute left-2 top-2 bg-white/90 text-[10px] dark:bg-dark-900/90">
            {{ categoryLabel(asset.category) }}
          </span>
        </div>

        <div class="card-body flex flex-1 flex-col justify-between gap-2.5 p-3">
          <div class="min-w-0">
            <p class="truncate text-xs font-semibold text-gray-900 dark:text-white">
              {{ asset.name }}
            </p>
            <p class="mt-0.5 text-[10px] text-gray-500 dark:text-dark-400">
              <span v-if="asset.width && asset.height">{{ asset.width }}×{{ asset.height }} · </span>
              {{ formatTime(asset.createdAt) }}
            </p>
          </div>

          <div class="flex flex-wrap items-center gap-1.5">
            <button
              type="button"
              class="btn btn-primary btn-sm flex-1 text-[11px]"
              @click="emit('insertToCanvas', asset)"
            >
              {{ t('creative.assets.insertCanvas') }}
            </button>
            <button
              v-if="asset.kind === 'image'"
              type="button"
              class="btn btn-secondary btn-sm text-[11px]"
              @click="emit('sendToQuickStudio', { prompt: asset.prompt || '', imageDataUrl: asset.dataUrl, title: asset.name })"
            >
              {{ t('creative.assets.useAsRef') }}
            </button>
            <button
              type="button"
              class="btn btn-ghost btn-icon"
              :title="t('imagegen.download')"
              @click="downloadAsset(asset)"
            >
              <Icon name="download" size="xs" />
            </button>
            <button
              type="button"
              class="btn btn-ghost btn-icon text-red-600 dark:text-red-400"
              :title="t('common.delete')"
              @click="emit('removeAsset', asset.id)"
            >
              <Icon name="trash" size="xs" />
            </button>
          </div>
        </div>
      </article>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { downloadUrl } from '@/features/imagegen/api'
import type { AssetCategory, CreativeAsset, QuickImageBridgePayload } from './types'

const props = defineProps<{
  assets: CreativeAsset[]
  localUsageSummary: string
}>()

const emit = defineEmits<{
  uploadFiles: [files: File[], category: AssetCategory]
  insertToCanvas: [asset: CreativeAsset]
  sendToQuickStudio: [payload: QuickImageBridgePayload]
  removeAsset: [assetId: string]
  clearAssets: []
}>()

const { t } = useI18n()

type FilterCat = 'all' | AssetCategory

const activeCategory = ref<FilterCat>('all')
const uploadCategory = ref<AssetCategory>('character')
const query = ref('')

const categories: Array<{ id: FilterCat; labelKey: string }> = [
  { id: 'all', labelKey: 'creative.assets.catAll' },
  { id: 'character', labelKey: 'creative.assets.catCharacter' },
  { id: 'scene', labelKey: 'creative.assets.catScene' },
  { id: 'prop', labelKey: 'creative.assets.catProp' },
  { id: 'generated', labelKey: 'creative.assets.catGenerated' },
  { id: 'video', labelKey: 'creative.assets.catVideo' },
]

const filteredAssets = computed(() => {
  const q = query.value.trim().toLowerCase()
  return props.assets.filter((asset) => {
    if (activeCategory.value !== 'all' && asset.category !== activeCategory.value) return false
    if (!q) return true
    return (
      asset.name.toLowerCase().includes(q) ||
      String(asset.prompt || '').toLowerCase().includes(q)
    )
  })
})

function countFor(cat: FilterCat): number {
  if (cat === 'all') return props.assets.length
  return props.assets.filter((a) => a.category === cat).length
}

function categoryLabel(cat: AssetCategory): string {
  const map: Record<AssetCategory, string> = {
    character: t('creative.assets.catCharacter'),
    scene: t('creative.assets.catScene'),
    prop: t('creative.assets.catProp'),
    generated: t('creative.assets.catGenerated'),
    video: t('creative.assets.catVideo'),
  }
  return map[cat]
}

function onFilePick(event: Event): void {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  input.value = ''
  if (!files.length) return
  emit('uploadFiles', files, uploadCategory.value)
}

function downloadAsset(asset: CreativeAsset): void {
  const ext = asset.kind === 'video' ? 'mp4' : 'png'
  downloadUrl(asset.dataUrl, asset.name.includes('.') ? asset.name : `${asset.name}.${ext}`)
}

function formatTime(iso: string): string {
  try {
    return new Date(iso).toLocaleString()
  } catch {
    return iso
  }
}
</script>
