<template>
  <section class="quickImage" aria-labelledby="quickImageTitle">
    <div class="pageToolbar">
      <h2 id="quickImageTitle">{{ translate("快捷生图") }}</h2>
      <div class="toolbarActions">
        <el-button :icon="IconSparkles" @click="templateDrawerVisible = true">
          {{ translate("模板库") }}
        </el-button>
        <el-button
          :type="hasImageKey ? 'default' : 'warning'"
          :plain="!hasImageKey"
          :icon="IconKey"
          data-test="quick-image-open-keys"
          @click="openKeySettings"
        >
          {{ activeImageKeyLabel }}
        </el-button>
      </div>
    </div>

    <div class="studioGrid">
      <!-- Left: Prompt, Retouch, References, Parameters -->
      <div class="controlColumn">
        <el-card class="panelCard" shadow="never">
          <div class="fieldStack">
            <el-input
              v-model="prompt"
              type="textarea"
              :rows="5"
              resize="none"
              :placeholder="translate('描述想生成的画面...')"
            />

            <!-- Retouch Presets -->
            <div class="collapsibleBox">
              <button type="button" class="collapseTrigger" @click="retouchOpen = !retouchOpen">
                <span class="triggerLeft">
                  <icon-adjustments-horizontal :size="16" aria-hidden="true" />
                  <span>{{ translate("修图预设") }}</span>
                  <el-tag v-if="retouchSelections.length" size="small" type="primary" effect="plain">
                    {{ retouchSelections.length }}
                  </el-tag>
                </span>
                <component :is="retouchOpen ? IconChevronUp : IconChevronDown" :size="15" aria-hidden="true" />
              </button>
              <retouchPanel
                v-if="retouchOpen"
                :modelValue="retouchParams"
                :activeCategory="activeRetouchCategory"
                :activeGroup="activeRetouchGroup"
                :selectedPreset="selectedRetouchId"
                :selections="retouchSelections"
                @update:intensity="retouchIntensity = $event"
                @update:subject="retouchSubject = $event"
                @update:activeCategory="activeRetouchCategory = $event"
                @update:activeGroup="activeRetouchGroup = $event"
                @update:selectedPreset="selectedRetouchId = $event"
                @toggle="onToggleRetouch"
                @clear="clearRetouch"
              />
            </div>

            <!-- References + Optional Mask -->
            <div class="referenceBlock">
              <div
                class="dropZone"
                :class="{ dragging: dragActive }"
                role="button"
                tabindex="0"
                @click="refInput?.click()"
                @keydown.enter.prevent="refInput?.click()"
                @dragenter.prevent="dragActive = true"
                @dragover.prevent="dragActive = true"
                @dragleave.prevent="dragActive = false"
                @drop.prevent="onDropRefs"
              >
                <icon-photo-plus :size="18" aria-hidden="true" />
                <span>{{ translate("上传参考图") }}</span>
                <span v-if="references.length" class="refCount">{{ references.length }}/{{ MAX_REFERENCE_COUNT }}</span>
                <input
                  ref="refInput"
                  type="file"
                  :accept="ACCEPTED_IMAGE_TYPES.join(',')"
                  multiple
                  hidden
                  @change="onPickRefs"
                />
              </div>

              <div v-if="references.length" class="thumbStrip">
                <div v-for="(item, idx) in references" :key="item.id" class="thumbItem">
                  <img :src="item.preview" :alt="item.file.name" />
                  <span class="thumbIndex">{{ idx + 1 }}</span>
                  <div class="thumbActions">
                    <button type="button" :disabled="idx === 0" @click="moveReference(idx, idx - 1)">←</button>
                    <button type="button" :disabled="idx === references.length - 1" @click="moveReference(idx, idx + 1)">→</button>
                    <button type="button" class="danger" @click="removeReference(item.id)">×</button>
                  </div>
                </div>
              </div>

              <div class="assetTools">
                <el-button size="small" text :icon="IconMask" @click="maskInput?.click()">
                  {{ maskFile ? translate("更换 Mask") : translate("上传 Mask") }}
                </el-button>
                <el-button v-if="maskFile" size="small" text type="danger" @click="clearMask">
                  {{ translate("移除 Mask") }}
                </el-button>
                <img v-if="maskPreview" :src="maskPreview" alt="mask" class="maskThumb" />
                <el-button v-if="references.length" size="small" text class="clearRefsBtn" @click="clearReferences">
                  {{ translate("清空参考图") }}
                </el-button>
                <input
                  ref="maskInput"
                  type="file"
                  :accept="ACCEPTED_IMAGE_TYPES.join(',')"
                  hidden
                  @change="onPickMask"
                />
              </div>
            </div>
          </div>
        </el-card>

        <el-card class="panelCard" shadow="never">
          <div class="paramsGrid">
            <div class="paramField full">
              <label>{{ translate("模型") }}</label>
              <el-select v-model="model" filterable allowCreate defaultFirstOption>
                <el-option v-for="m in modelOptions" :key="m" :label="m" :value="m" />
              </el-select>
            </div>

            <div class="paramField full">
              <label>{{ translate("尺寸") }}</label>
              <el-select v-model="size">
                <el-option-group v-for="group in SIZE_GROUPS" :key="group.label" :label="group.label">
                  <el-option
                    v-for="opt in group.options"
                    :key="opt.value"
                    :label="opt.label"
                    :value="opt.value"
                  />
                </el-option-group>
                <el-option value="custom" :label="translate('自定义')" />
              </el-select>
            </div>

            <template v-if="size === 'custom'">
              <div class="paramField">
                <label>{{ translate("宽") }}</label>
                <el-input-number v-model="customWidth" :min="256" :max="4096" :step="64" controlsPosition="right" />
              </div>
              <div class="paramField">
                <label>{{ translate("高") }}</label>
                <el-input-number v-model="customHeight" :min="256" :max="4096" :step="64" controlsPosition="right" />
              </div>
            </template>

            <div class="paramField">
              <label>{{ translate("质量") }}</label>
              <el-select v-model="quality">
                <el-option value="auto" label="auto" />
                <el-option value="low" label="low" />
                <el-option value="medium" label="medium" />
                <el-option value="high" label="high" />
              </el-select>
            </div>

            <div class="paramField">
              <label>{{ translate("数量") }}</label>
              <el-input-number v-model="count" :min="1" :max="4" controlsPosition="right" />
            </div>

            <div class="paramField">
              <label>{{ translate("格式") }}</label>
              <el-select v-model="format">
                <el-option value="png" label="PNG" />
                <el-option value="jpeg" label="JPEG" />
                <el-option value="webp" label="WEBP" />
              </el-select>
            </div>

            <div class="paramField">
              <label>{{ translate("风格") }}</label>
              <el-select v-model="style">
                <el-option
                  v-for="opt in STYLE_OPTIONS"
                  :key="opt.value"
                  :label="translate(opt.label)"
                  :value="opt.value"
                />
              </el-select>
            </div>
          </div>

          <div class="advancedToggle">
            <button type="button" class="collapseTrigger" @click="advancedOpen = !advancedOpen">
              <span>{{ translate("高级参数") }}</span>
              <component :is="advancedOpen ? IconChevronUp : IconChevronDown" :size="15" aria-hidden="true" />
            </button>
            <div v-if="advancedOpen" class="paramsGrid advancedGrid">
              <div class="paramField">
                <label>{{ translate("背景") }}</label>
                <el-select v-model="background">
                  <el-option value="auto" label="auto" />
                  <el-option value="transparent" label="transparent" />
                  <el-option value="opaque" label="opaque" />
                </el-select>
              </div>
              <div class="paramField">
                <label>{{ translate("审核") }}</label>
                <el-select v-model="moderation">
                  <el-option value="auto" label="auto" />
                  <el-option value="low" label="low" />
                </el-select>
              </div>
              <div v-if="format !== 'png'" class="paramField full">
                <label>{{ translate("压缩") }} ({{ compression }}%)</label>
                <el-slider v-model="compression" :min="1" :max="100" />
              </div>
            </div>
          </div>

          <div class="actionRow">
            <el-button
              type="primary"
              class="generateBtn"
              :loading="generating"
              :icon="IconSparkles"
              data-test="quick-image-generate"
              @click="submit"
            >
              {{ generating ? `${translate("生成中")} ${elapsedSeconds}s` : translate("生成图片") }}
            </el-button>
            <el-button :icon="IconListCheck" @click="enqueue">{{ translate("加入队列") }}</el-button>
            <el-button text :icon="IconBookmark" :title="translate('保存模板')" @click="saveTemplate" />
            <el-button text :icon="IconRotate" :title="translate('重置')" @click="resetParams" />
          </div>
        </el-card>
      </div>

      <!-- Right: Results / Queue / History -->
      <div class="stageColumn">
        <div class="stageHeader">
          <el-segmented v-model="activeStageTab" :options="stageOptions" />
          <div v-if="activeStageTab === 'results' && results.length" class="stageTools">
            <el-slider v-model="previewSize" :min="60" :max="100" class="sizeSlider" />
            <el-button size="small" :icon="IconDownload" @click="downloadAll">{{ translate("下载全部") }}</el-button>
          </div>
          <div v-else-if="activeStageTab === 'queue'" class="stageTools">
            <el-input-number v-model="queueConcurrency" :min="1" :max="4" size="small" controlsPosition="right" class="concurrencyInput" />
            <el-button size="small" type="primary" :loading="queueRunning" :disabled="!queue.length" @click="runQueue">
              {{ translate("运行队列") }}
            </el-button>
            <el-button size="small" text :disabled="!queue.length || queueRunning" @click="queue = []">
              {{ translate("清空") }}
            </el-button>
          </div>
          <div v-else-if="activeStageTab === 'history'" class="stageTools">
            <el-button size="small" :type="favoriteOnly ? 'primary' : 'default'" plain @click="favoriteOnly = !favoriteOnly">
              {{ translate("仅收藏") }}
            </el-button>
            <el-button size="small" text :disabled="!history.length" @click="clearAllHistory">
              {{ translate("清空") }}
            </el-button>
          </div>
        </div>

        <!-- Results Stage -->
        <div v-if="activeStageTab === 'results'" class="stageBody">
          <el-alert v-if="errorText" :title="errorText" type="error" :closable="false" showIcon class="errorAlert" />
          <div v-if="results.length" class="resultGrid" :style="{ '--cardMin': `${Math.round(previewSize * 2.8)}px` }">
            <div v-for="img in results" :key="`${img.index}-${img.src.slice(0, 24)}`" class="resultCard">
              <div class="imageBox">
                <img :src="img.src" :alt="prompt" />
              </div>
              <div class="resultActions">
                <el-button size="small" text :icon="IconDownload" :title="translate('下载')" @click="downloadImage(img)" />
                <el-button size="small" text :icon="IconCopy" :title="translate('复制')" @click="copyResult(img)" />
                <el-button size="small" text :icon="IconPhotoEdit" :title="translate('继续修改')" @click="reuseAsReference(img)" />
                <el-button size="small" text :icon="IconFolderPlus" :title="translate('存入素材库')" @click="saveToAssets(img)">
                  {{ translate("存入素材库") }}
                </el-button>
                <el-button size="small" type="primary" text :icon="IconVector" @click="sendToCanvas(img)">
                  {{ translate("发到画布") }}
                </el-button>
              </div>
            </div>
          </div>
          <el-empty v-else :description="translate('生成结果')" :imageSize="80" />
        </div>

        <!-- Queue Stage -->
        <div v-else-if="activeStageTab === 'queue'" class="stageBody">
          <el-empty v-if="!queue.length" :description="translate('暂无任务')" :imageSize="72" />
          <div v-else class="queueList">
            <div v-for="(task, idx) in queue" :key="task.id" class="queueRow">
              <div class="queueMeta">
                <el-tag size="small" :type="queueTagType(task.state)">{{ task.state }}</el-tag>
                <span class="queuePrompt">{{ task.prompt }}</span>
                <span class="queueSub">{{ task.model }} · {{ task.size }}</span>
              </div>
              <div class="queueActions">
                <el-button size="small" text :disabled="idx === 0 || queueRunning" @click="moveQueue(idx, idx - 1)">↑</el-button>
                <el-button size="small" text :disabled="idx === queue.length - 1 || queueRunning" @click="moveQueue(idx, idx + 1)">↓</el-button>
                <el-button size="small" text type="danger" :disabled="task.state === 'running'" :icon="IconTrash" @click="removeQueue(task.id)" />
              </div>
            </div>
          </div>
        </div>

        <!-- History Stage -->
        <div v-else class="stageBody">
          <el-empty v-if="!visibleHistory.length" :description="translate('暂无记录')" :imageSize="72" />
          <div v-else class="historyGrid">
            <div v-for="item in visibleHistory" :key="item.id" class="historyCard">
              <div class="historyThumb" @click="applyHistory(item)">
                <img v-if="item.images[0]" :src="item.images[0]" :alt="item.prompt" loading="lazy" />
              </div>
              <div class="historyInfo">
                <p class="historyPrompt" @click="applyHistory(item)">{{ item.prompt }}</p>
                <div class="historyFoot">
                  <span class="historyModel">{{ item.model }}</span>
                  <div class="historyBtns">
                    <el-button size="small" text :icon="item.favorite ? IconStarFilled : IconStar" @click="toggleFavorite(item.id)" />
                    <el-button size="small" text :icon="IconRefresh" @click="rerunHistory(item)" />
                    <el-button size="small" text type="danger" :icon="IconTrash" @click="deleteHistory(item.id)" />
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <templateDrawer
      v-model="templateDrawerVisible"
      :customTemplates="customTemplates"
      @apply="onApplyTemplate"
      @deleteCustom="onDeleteCustomTemplate"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, inject, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRouter } from "vue-router";
import { ElMessage } from "element-plus";
import {
  IconAdjustmentsHorizontal,
  IconBookmark,
  IconChevronDown,
  IconChevronUp,
  IconCopy,
  IconDownload,
  IconFolderPlus,
  IconKey,
  IconListCheck,
  IconMask,
  IconPhotoEdit,
  IconPhotoPlus,
  IconRefresh,
  IconRotate,
  IconSparkles,
  IconStar,
  IconStarFilled,
  IconTrash,
  IconVector,
} from "@tabler/icons-vue";
import { translate } from "@toonflow/i18n/vue";
import { useWorkspaceStore } from "@/stores/workspace";
import {
  DEFAULT_RETOUCH_CATEGORY,
  DEFAULT_RETOUCH_GROUP,
  DEFAULT_RETOUCH_INTENSITY,
  DEFAULT_RETOUCH_PRESET,
  DEFAULT_RETOUCH_SUBJECT,
  buildPromptPayload,
  normalizeRetouchParams,
} from "@host/features/imagegen/retouch";
import {
  HISTORY_LIMIT,
  clearHistoryImages,
  deleteHistoryImages,
  loadHistoryImages,
  loadPreviewSize,
  newId,
  pruneHistoryImages,
  readRawHistory,
  safeGet,
  safeSet,
  saveHistoryImages,
  savePreviewSize,
  writeRawHistory,
} from "@host/features/imagegen/storage";
import type {
  GeneratedImage,
  HistoryItem,
  ImageRequestPayload,
  ImageTemplate,
  QueueState,
  ReferenceImage,
  RetouchParams,
  RetouchPreset,
} from "@host/features/imagegen/types";

interface StudioQueueTask {
  id: string;
  prompt: string;
  model: string;
  size: string;
  quality: string;
  count: number;
  format: string;
  background: string;
  moderation: string;
  compression: number;
  style: string;
  retouch: RetouchParams;
  state: QueueState;
  error: string;
}
import { getHostContext, subscribeHostContext, type Sub2ApiHostContext } from "../../runtime/sub2apiBridge";
import {
  getActiveImageKey,
  getDiscoveredImageModels,
  requestSiteImages,
  saveImageToStudioAssets,
  sendImageToWorkspaceCanvas,
} from "./quickImageApi";
import retouchPanel from "./retouchPanel.vue";
import templateDrawer from "./templateDrawer.vue";

const ACCEPTED_IMAGE_TYPES = ["image/png", "image/jpeg", "image/webp"] as const;
const MAX_REFERENCE_COUNT = 16;
const MAX_REFERENCE_BYTES = 50 * 1024 * 1024;
const CUSTOM_TEMPLATES_STORAGE_KEY = "imgGenCustomTemplates";

const SIZE_GROUPS = [
  {
    label: "1K",
    options: [
      { value: "1024x1024", label: "1024×1024 (1:1)" },
      { value: "1536x1024", label: "1536×1024 (3:2)" },
      { value: "1024x1536", label: "1024×1536 (2:3)" },
      { value: "1600x1200", label: "1600×1200 (4:3)" },
      { value: "1200x1600", label: "1200×1600 (3:4)" },
      { value: "1792x1024", label: "1792×1024 (7:4)" },
      { value: "1024x1792", label: "1024×1792 (4:7)" },
    ],
  },
  {
    label: "2K",
    options: [
      { value: "2048x2048", label: "2048×2048 (1:1)" },
      { value: "2048x1152", label: "2048×1152 (16:9)" },
      { value: "1152x2048", label: "1152×2048 (9:16)" },
      { value: "2048x1536", label: "2048×1536 (4:3)" },
      { value: "1536x2048", label: "1536×2048 (3:4)" },
    ],
  },
  {
    label: "4K",
    options: [
      { value: "2880x2880", label: "2880×2880 (1:1)" },
      { value: "3840x2160", label: "3840×2160 (16:9)" },
      { value: "2160x3840", label: "2160×3840 (9:16)" },
    ],
  },
];

const STYLE_OPTIONS = [
  { value: "", label: "无" },
  { value: "photorealistic, 8k, detailed", label: "写实" },
  { value: "anime style, vibrant colors", label: "动漫" },
  { value: "oil painting, artistic", label: "油画" },
  { value: "watercolor, soft tones", label: "水彩" },
  { value: "3d render, cinema lighting", label: "3D 渲染" },
  { value: "cyberpunk, neon lights", label: "赛博朋克" },
  { value: "minimalist, clean design", label: "极简" },
];

const openSettingsPanel = inject<(id: string) => void>("openSettingsPanel");
const router = useRouter();
const workspaceStore = useWorkspaceStore();

const hostCtx = ref<Sub2ApiHostContext>(getHostContext());
let unsubscribeHost: (() => void) | undefined;

const prompt = ref("");
const model = ref("gpt-image-1");
const size = ref("1024x1024");
const customWidth = ref(1024);
const customHeight = ref(1024);
const quality = ref("auto");
const count = ref(1);
const format = ref("png");
const style = ref("");
const background = ref("auto");
const moderation = ref("auto");
const compression = ref(80);
const advancedOpen = ref(false);

const retouchOpen = ref(false);
const retouchSelections = ref<{ category: string; group: string; preset: string }[]>([]);
const retouchIntensity = ref(DEFAULT_RETOUCH_INTENSITY);
const retouchSubject = ref(DEFAULT_RETOUCH_SUBJECT);
const activeRetouchCategory = ref(DEFAULT_RETOUCH_CATEGORY);
const activeRetouchGroup = ref(DEFAULT_RETOUCH_GROUP);
const selectedRetouchId = ref(DEFAULT_RETOUCH_PRESET);

const retouchParams = computed<RetouchParams>(() => ({
  enabled: retouchSelections.value.length > 0,
  category: activeRetouchCategory.value,
  group: activeRetouchGroup.value,
  preset: selectedRetouchId.value,
  intensity: retouchIntensity.value,
  subject: retouchSubject.value,
  selections: retouchSelections.value,
}));

const references = ref<ReferenceImage[]>([]);
const maskFile = ref<File | null>(null);
const maskPreview = ref("");
const refInput = ref<HTMLInputElement | null>(null);
const maskInput = ref<HTMLInputElement | null>(null);
const dragActive = ref(false);

const activeStageTab = ref<"results" | "queue" | "history">("results");
const results = ref<GeneratedImage[]>([]);
const previewSize = ref(loadPreviewSize());
const generating = ref(false);
const elapsedSeconds = ref(0);
const errorText = ref("");

const queue = ref<StudioQueueTask[]>([]);
const queueRunning = ref(false);
const queueConcurrency = ref(1);

const history = ref<HistoryItem[]>([]);
const favoriteOnly = ref(false);

const templateDrawerVisible = ref(false);
const customTemplates = ref<ImageTemplate[]>([]);

const hasImageKey = computed(() => Boolean(getActiveImageKey()?.key));
const activeImageKeyLabel = computed(() => {
  const key = getActiveImageKey();
  return key?.name || translate("密钥配置");
});

const modelOptions = computed(() => {
  const discovered = getDiscoveredImageModels(hostCtx.value.selectedImageKeyId);
  const defaults = ["gpt-image-1", "gemini-2.5-flash-image-preview", "flux-pro"];
  const merged = [...new Set([...discovered, ...defaults])];
  return merged;
});

watch(
  modelOptions,
  (opts) => {
    if (opts.length && !opts.includes(model.value)) {
      model.value = opts[0]!;
    }
  },
  { immediate: true }
);

watch(previewSize, (val) => savePreviewSize(val));

const stageOptions = computed(() => [
  { label: `${translate("生成结果")}${results.value.length ? ` (${results.value.length})` : ""}`, value: "results" },
  { label: `${translate("任务队列")}${queue.value.length ? ` (${queue.value.length})` : ""}`, value: "queue" },
  { label: `${translate("历史记录")}${history.value.length ? ` (${history.value.length})` : ""}`, value: "history" },
]);

const visibleHistory = computed(() =>
  favoriteOnly.value ? history.value.filter((item) => item.favorite) : history.value
);

function openKeySettings() {
  openSettingsPanel?.("apiKeys");
}

function onToggleRetouch(preset: RetouchPreset) {
  const idx = retouchSelections.value.findIndex(
    (item) =>
      item.category === activeRetouchCategory.value &&
      item.group === activeRetouchGroup.value &&
      item.preset === preset.id
  );
  if (idx >= 0) {
    retouchSelections.value.splice(idx, 1);
  } else {
    retouchSelections.value.push({
      category: activeRetouchCategory.value,
      group: activeRetouchGroup.value,
      preset: preset.id,
    });
  }
}

function clearRetouch() {
  retouchSelections.value = [];
}

function addReferences(files: File[]) {
  for (const file of files) {
    if (!ACCEPTED_IMAGE_TYPES.includes(file.type as (typeof ACCEPTED_IMAGE_TYPES)[number])) {
      ElMessage.warning(translate("仅支持 PNG / JPG / WEBP"));
      continue;
    }
    if (file.size > MAX_REFERENCE_BYTES) {
      ElMessage.warning(translate("单张图片不能超过 50MB"));
      continue;
    }
    if (references.value.length >= MAX_REFERENCE_COUNT) {
      ElMessage.warning(translate("最多 16 张参考图"));
      break;
    }
    references.value.push({ id: newId(), file, preview: URL.createObjectURL(file) });
  }
}

function onPickRefs(e: Event) {
  const input = e.target as HTMLInputElement;
  addReferences(Array.from(input.files ?? []));
  input.value = "";
}

function onDropRefs(e: DragEvent) {
  dragActive.value = false;
  addReferences(Array.from(e.dataTransfer?.files ?? []));
}

function removeReference(id: string) {
  const idx = references.value.findIndex((r) => r.id === id);
  if (idx < 0) return;
  URL.revokeObjectURL(references.value[idx]!.preview);
  references.value.splice(idx, 1);
}

function moveReference(from: number, to: number) {
  if (to < 0 || to >= references.value.length) return;
  const [item] = references.value.splice(from, 1);
  if (item) references.value.splice(to, 0, item);
}

function clearReferences() {
  for (const r of references.value) URL.revokeObjectURL(r.preview);
  references.value = [];
}

function onPickMask(e: Event) {
  const input = e.target as HTMLInputElement;
  const file = input.files?.[0] ?? null;
  input.value = "";
  clearMask();
  if (!file) return;
  maskFile.value = file;
  maskPreview.value = URL.createObjectURL(file);
}

function clearMask() {
  if (maskPreview.value) URL.revokeObjectURL(maskPreview.value);
  maskFile.value = null;
  maskPreview.value = "";
}

function resolvedSize(): string {
  return size.value === "custom" ? `${customWidth.value}x${customHeight.value}` : size.value;
}

function buildPayload(rawPrompt: string): ImageRequestPayload {
  const finalPrompt = buildPromptPayload(rawPrompt, style.value, retouchParams.value);
  const payload: ImageRequestPayload = {
    model: model.value.trim() || "gpt-image-1",
    prompt: finalPrompt,
    n: count.value,
    size: resolvedSize(),
    quality: quality.value,
    output_format: format.value,
  };
  if (background.value !== "auto") payload.background = background.value;
  if (moderation.value !== "auto") payload.moderation = moderation.value;
  if (format.value !== "png") payload.output_compression = compression.value;
  return payload;
}

async function persistHistoryEntry(rawPrompt: string, usedModel: string, usedSize: string, urls: string[]) {
  const entry: HistoryItem = {
    id: newId(),
    prompt: rawPrompt,
    model: usedModel,
    size: usedSize,
    quality: quality.value,
    count: urls.length,
    imageCount: urls.length,
    format: format.value,
    background: background.value,
    moderation: moderation.value,
    compression: compression.value,
    style: style.value,
    retouch: { ...retouchParams.value },
    images: urls,
    favorite: false,
    time: new Date().toLocaleString(),
  };
  history.value.unshift(entry);
  if (history.value.length > HISTORY_LIMIT) {
    history.value = history.value.slice(0, HISTORY_LIMIT);
  }
  await saveHistoryImages(entry.id, urls);
  const meta = history.value.map(({ images: _imgs, ...rest }) => rest);
  writeRawHistory(meta);
  void pruneHistoryImages(history.value.map((h) => h.id));
}

async function submit() {
  if (generating.value) return;
  const keyInfo = getActiveImageKey();
  if (!keyInfo?.key) {
    ElMessage.warning(translate("请先在「密钥与模型」中选择站内 API 密钥"));
    openKeySettings();
    return;
  }
  const rawPrompt = prompt.value.trim();
  if (!rawPrompt) {
    ElMessage.warning(translate("请输入提示词"));
    return;
  }
  if (maskFile.value && !references.value.length) {
    ElMessage.warning(translate("使用 Mask 需先上传参考图"));
    return;
  }

  generating.value = true;
  errorText.value = "";
  elapsedSeconds.value = 0;
  activeStageTab.value = "results";
  const timer = window.setInterval(() => {
    elapsedSeconds.value += 1;
  }, 1000);
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), 20 * 60 * 1000);

  try {
    const payload = buildPayload(rawPrompt);
    const urls = await requestSiteImages({
      key: keyInfo.key,
      payload,
      references: references.value.map((r) => r.file),
      mask: maskFile.value,
      format: format.value,
      signal: controller.signal,
    });
    results.value = urls.map((src, index) => ({ src, format: format.value, index }));
    await persistHistoryEntry(rawPrompt, payload.model, payload.size, urls);
    ElMessage.success(translate("生成完成"));
  } catch (err) {
    errorText.value = err instanceof Error ? err.message : translate("生成失败");
  } finally {
    window.clearInterval(timer);
    window.clearTimeout(timeout);
    generating.value = false;
  }
}

function enqueue() {
  const rawPrompt = prompt.value.trim();
  if (!rawPrompt) {
    ElMessage.warning(translate("请输入提示词"));
    return;
  }
  queue.value.push({
    id: newId(),
    prompt: rawPrompt,
    model: model.value.trim() || "gpt-image-1",
    size: resolvedSize(),
    quality: quality.value,
    count: count.value,
    format: format.value,
    background: background.value,
    moderation: moderation.value,
    compression: compression.value,
    style: style.value,
    retouch: { ...retouchParams.value },
    state: "pending",
    error: "",
  });
  activeStageTab.value = "queue";
}

function moveQueue(from: number, to: number) {
  if (to < 0 || to >= queue.value.length) return;
  const [item] = queue.value.splice(from, 1);
  if (item) queue.value.splice(to, 0, item);
}

function removeQueue(id: string) {
  queue.value = queue.value.filter((t) => t.id !== id);
}

function queueTagType(state: QueueState): "info" | "warning" | "success" | "danger" {
  if (state === "running") return "warning";
  if (state === "success") return "success";
  if (state === "failed") return "danger";
  return "info";
}

async function runQueue() {
  if (queueRunning.value) return;
  const keyInfo = getActiveImageKey();
  if (!keyInfo?.key) {
    ElMessage.warning(translate("请先在「密钥与模型」中选择站内 API 密钥"));
    openKeySettings();
    return;
  }
  queueRunning.value = true;
  const pending = queue.value.filter((t) => t.state === "pending" || t.state === "failed");
  let cursor = 0;

  const worker = async () => {
    while (cursor < pending.length) {
      const task = pending[cursor++]!;
      task.state = "running";
      task.error = "";
      const controller = new AbortController();
      try {
        const payload: ImageRequestPayload = {
          model: task.model,
          prompt: buildPromptPayload(task.prompt, task.style, task.retouch),
          n: task.count,
          size: task.size,
          quality: task.quality,
          output_format: task.format,
        };
        const urls = await requestSiteImages({
          key: keyInfo.key,
          payload,
          references: [],
          mask: null,
          format: task.format,
          signal: controller.signal,
        });
        task.state = "success";
        results.value = urls.map((src, index) => ({ src, format: task.format, index }));
        await persistHistoryEntry(task.prompt, task.model, task.size, urls);
      } catch (err) {
        task.state = "failed";
        task.error = err instanceof Error ? err.message : translate("生成失败");
      }
    }
  };

  try {
    await Promise.all(Array.from({ length: Math.min(queueConcurrency.value, Math.max(1, pending.length)) }, () => worker()));
  } finally {
    queueRunning.value = false;
  }
}

function downloadImage(img: GeneratedImage) {
  const link = document.createElement("a");
  link.href = img.src;
  link.download = `image-${Date.now()}-${img.index + 1}.${img.format}`;
  document.body.appendChild(link);
  link.click();
  link.remove();
}

function downloadAll() {
  for (const img of results.value) downloadImage(img);
}

async function copyResult(img: GeneratedImage) {
  try {
    const resp = await fetch(img.src);
    const blob = await resp.blob();
    if (navigator.clipboard && window.ClipboardItem) {
      await navigator.clipboard.write([new window.ClipboardItem({ [blob.type || "image/png"]: blob })]);
      ElMessage.success(translate("已复制"));
      return;
    }
    await navigator.clipboard.writeText(img.src);
    ElMessage.success(translate("已复制"));
  } catch {
    ElMessage.error(translate("复制失败"));
  }
}

async function reuseAsReference(img: GeneratedImage) {
  try {
    const resp = await fetch(img.src);
    const blob = await resp.blob();
    const file = new File([blob], `ref-${Date.now()}.${img.format}`, { type: blob.type || "image/png" });
    addReferences([file]);
    ElMessage.success(translate("已加入参考图"));
  } catch {
    ElMessage.error(translate("操作失败"));
  }
}

async function saveToAssets(img: GeneratedImage) {
  try {
    await saveImageToStudioAssets(img.src, prompt.value.slice(0, 24) || "快捷生图");
    ElMessage.success(translate("已存入素材库"));
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : translate("保存失败"));
  }
}

async function sendToCanvas(img: GeneratedImage) {
  try {
    const targetDir = workspaceStore.project?.directory || workspaceStore.projectList[0]?.directory;
    const saved = await sendImageToWorkspaceCanvas(img.src, {
      directory: targetDir,
      title: prompt.value.slice(0, 24) || "快捷生图",
      prompt: prompt.value,
    });
    await workspaceStore.openProject(saved.directory);
    await router.push("/workspace");
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : translate("发送失败"));
  }
}

function saveTemplate() {
  const rawPrompt = prompt.value.trim();
  if (!rawPrompt) {
    ElMessage.warning(translate("请输入提示词"));
    return;
  }
  const tpl: ImageTemplate = {
    id: `custom-${newId()}`,
    name: rawPrompt.slice(0, 20),
    tag: "",
    category: "我的模板",
    desc: "",
    prompt: rawPrompt,
    preview: "",
    params: {
      model: model.value,
      size: resolvedSize(),
      quality: quality.value,
      count: count.value,
      format: format.value,
      background: background.value,
      moderation: moderation.value,
      compression: compression.value,
      style: style.value,
      retouch: { ...retouchParams.value },
    },
  };
  customTemplates.value.unshift(tpl);
  safeSet(CUSTOM_TEMPLATES_STORAGE_KEY, JSON.stringify(customTemplates.value));
  ElMessage.success(translate("已保存到我的模板"));
}

function onApplyTemplate(tpl: ImageTemplate, overridePrompt?: string) {
  prompt.value = overridePrompt ?? tpl.prompt;
  if (tpl.params.model) model.value = tpl.params.model;
  if (tpl.params.size) size.value = tpl.params.size;
  if (tpl.params.quality) quality.value = tpl.params.quality;
  if (tpl.params.count) count.value = tpl.params.count;
  if (tpl.params.format) format.value = tpl.params.format;
  if (tpl.params.style !== undefined) style.value = tpl.params.style;
  if (tpl.params.retouch) {
    const norm = normalizeRetouchParams(tpl.params.retouch);
    retouchSelections.value = [...norm.selections];
    retouchIntensity.value = norm.intensity;
    retouchSubject.value = norm.subject;
  }
}

function onDeleteCustomTemplate(id: string) {
  customTemplates.value = customTemplates.value.filter((t) => t.id !== id);
  safeSet(CUSTOM_TEMPLATES_STORAGE_KEY, JSON.stringify(customTemplates.value));
}

function applyHistory(item: HistoryItem) {
  prompt.value = item.prompt;
  if (item.model) model.value = item.model;
  if (item.size) size.value = item.size;
  if (item.images.length) {
    results.value = item.images.map((src, index) => ({ src, format: item.format || "png", index }));
    activeStageTab.value = "results";
  }
}

function rerunHistory(item: HistoryItem) {
  prompt.value = item.prompt;
  if (item.model) model.value = item.model;
  void submit();
}

function toggleFavorite(id: string) {
  const target = history.value.find((h) => h.id === id);
  if (!target) return;
  target.favorite = !target.favorite;
  writeRawHistory(history.value.map(({ images: _imgs, ...rest }) => rest));
}

async function deleteHistory(id: string) {
  history.value = history.value.filter((h) => h.id !== id);
  await deleteHistoryImages(id);
  writeRawHistory(history.value.map(({ images: _imgs, ...rest }) => rest));
}

async function clearAllHistory() {
  history.value = [];
  favoriteOnly.value = false;
  await clearHistoryImages();
  writeRawHistory([]);
}

function resetParams() {
  size.value = "1024x1024";
  quality.value = "auto";
  count.value = 1;
  format.value = "png";
  style.value = "";
  background.value = "auto";
  moderation.value = "auto";
  compression.value = 80;
  clearRetouch();
}

function onPaste(event: ClipboardEvent) {
  const items = Array.from(event.clipboardData?.items ?? []);
  const files = items
    .filter((item) => item.type.startsWith("image/"))
    .map((item) => item.getAsFile())
    .filter((f): f is File => Boolean(f));
  if (files.length) addReferences(files);
}

async function loadInitialData() {
  try {
    const parsed = JSON.parse(safeGet(CUSTOM_TEMPLATES_STORAGE_KEY) || "[]") as ImageTemplate[];
    if (Array.isArray(parsed)) customTemplates.value = parsed;
  } catch {
    customTemplates.value = [];
  }
  const rawList = readRawHistory() as Partial<HistoryItem>[];
  const hydrated: HistoryItem[] = [];
  for (const row of rawList.slice(0, HISTORY_LIMIT)) {
    if (!row?.id || !row.prompt) continue;
    const imgs = await loadHistoryImages(row.id);
    hydrated.push({
      id: row.id,
      prompt: row.prompt,
      model: row.model || "gpt-image-1",
      size: row.size || "1024x1024",
      quality: row.quality || "auto",
      count: row.count || imgs.length || 1,
      imageCount: row.imageCount ?? imgs.length,
      format: row.format || "png",
      background: row.background || "auto",
      moderation: row.moderation || "auto",
      compression: row.compression ?? 80,
      style: row.style || "",
      retouch: normalizeRetouchParams(row.retouch ?? {}) as unknown as RetouchParams,
      images: imgs,
      favorite: Boolean(row.favorite),
      time: row.time || "",
    });
  }
  history.value = hydrated;
}

onMounted(() => {
  hostCtx.value = getHostContext();
  unsubscribeHost = subscribeHostContext((next) => {
    hostCtx.value = next;
  });
  window.addEventListener("paste", onPaste);
  void loadInitialData();
});

onBeforeUnmount(() => {
  unsubscribeHost?.();
  window.removeEventListener("paste", onPaste);
  clearReferences();
  clearMask();
});
</script>

<style lang="scss" scoped>
.quickImage {
  max-width: 1280px;
  margin: 0 auto;

  .pageToolbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 12px 16px;
    margin-bottom: 20px;

    h2 {
      margin: 0;
      font-size: 22px;
      font-weight: 600;
      line-height: 1.5;
    }

    .toolbarActions {
      display: flex;
      align-items: center;
      gap: 8px;
    }
  }

  .studioGrid {
    display: grid;
    grid-template-columns: minmax(0, 420px) minmax(0, 1fr);
    gap: 20px;
    align-items: start;
  }

  .controlColumn {
    display: flex;
    flex-direction: column;
    gap: 16px;
  }

  .panelCard {
    border: 1px solid var(--el-border-color-lighter);
    border-radius: var(--ui-radius-large);
    background: var(--el-bg-color-overlay);

    :deep(.el-card__body) {
      padding: 16px;
    }
  }

  .fieldStack {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .collapsibleBox {
    padding: 8px 12px;
    border: 1px solid var(--el-border-color-lighter);
    border-radius: var(--ui-radius);
    background: var(--el-fill-color-extra-light);
  }

  .collapseTrigger {
    display: flex;
    align-items: center;
    justify-content: space-between;
    width: 100%;
    padding: 2px 0;
    border: 0;
    background: transparent;
    color: var(--el-text-color-regular);
    font: inherit;
    font-size: 13px;
    font-weight: 500;
    cursor: pointer;

    .triggerLeft {
      display: inline-flex;
      align-items: center;
      gap: 8px;
    }
  }

  .referenceBlock {
    display: flex;
    flex-direction: column;
    gap: 8px;

    .dropZone {
      display: flex;
      align-items: center;
      justify-content: center;
      gap: 8px;
      padding: 14px;
      border: 1px dashed var(--el-border-color);
      border-radius: var(--ui-radius);
      color: var(--el-text-color-secondary);
      font-size: 13px;
      cursor: pointer;
      transition: border-color 0.15s, background-color 0.15s;

      &:hover,
      &.dragging {
        border-color: var(--el-color-primary);
        background: var(--el-color-primary-light-9);
        color: var(--el-color-primary);
      }

      .refCount {
        font-size: 12px;
        font-variant-numeric: tabular-nums;
      }
    }

    .thumbStrip {
      display: flex;
      flex-wrap: wrap;
      gap: 8px;

      .thumbItem {
        position: relative;
        width: 68px;
        height: 68px;
        overflow: hidden;
        border: 1px solid var(--el-border-color-lighter);
        border-radius: var(--ui-radius-small);

        img {
          display: block;
          width: 100%;
          height: 100%;
          object-fit: cover;
        }

        .thumbIndex {
          position: absolute;
          top: 2px;
          left: 2px;
          padding: 0 4px;
          border-radius: 4px;
          background: rgb(0 0 0 / 65%);
          color: #fff;
          font-size: 10px;
        }

        .thumbActions {
          position: absolute;
          inset: auto 0 0;
          display: flex;
          background: rgb(0 0 0 / 70%);
          opacity: 0;
          transition: opacity 0.15s;

          button {
            flex: 1;
            padding: 2px 0;
            border: 0;
            background: transparent;
            color: #fff;
            font-size: 11px;
            cursor: pointer;

            &:disabled { opacity: 0.35; }
            &.danger { color: #f87171; }
          }
        }

        &:hover .thumbActions {
          opacity: 1;
        }
      }
    }

    .assetTools {
      display: flex;
      align-items: center;
      flex-wrap: wrap;
      gap: 6px;

      .maskThumb {
        width: 28px;
        height: 28px;
        border-radius: 4px;
        object-fit: cover;
      }

      .clearRefsBtn {
        margin-inline-start: auto;
      }
    }
  }

  .paramsGrid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;

    .paramField {
      display: flex;
      flex-direction: column;
      gap: 6px;
      min-width: 0;

      &.full {
        grid-column: 1 / -1;
      }

      label {
        color: var(--el-text-color-secondary);
        font-size: 12px;
      }

      .el-select,
      .el-input-number {
        width: 100%;
      }
    }
  }

  .advancedToggle {
    margin-top: 14px;
    padding-top: 12px;
    border-top: 1px solid var(--el-border-color-lighter);

    .advancedGrid {
      margin-top: 12px;
    }
  }

  .actionRow {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 16px;

    .generateBtn {
      flex: 1;
      min-width: 140px;
    }
  }

  .stageColumn {
    display: flex;
    flex-direction: column;
    gap: 16px;
    min-width: 0;
    padding: 16px;
    border: 1px solid var(--el-border-color-lighter);
    border-radius: var(--ui-radius-large);
    background: var(--el-bg-color-overlay);

    .stageHeader {
      display: flex;
      align-items: center;
      justify-content: space-between;
      flex-wrap: wrap;
      gap: 12px;

      .stageTools {
        display: flex;
        align-items: center;
        gap: 10px;

        .sizeSlider { width: 100px; }
        .concurrencyInput { width: 88px; }
      }
    }

    .errorAlert {
      margin-bottom: 12px;
    }

    .resultGrid {
      display: grid;
      grid-template-columns: repeat(auto-fill, minmax(min(100%, var(--cardMin, 240px)), 1fr));
      gap: 14px;
    }

    .resultCard {
      overflow: hidden;
      border: 1px solid var(--el-border-color-lighter);
      border-radius: var(--ui-radius);
      background: var(--el-fill-color-extra-light);

      .imageBox {
        background: var(--el-fill-color-light);

        img {
          display: block;
          width: 100%;
          height: auto;
          object-fit: contain;
        }
      }

      .resultActions {
        display: flex;
        align-items: center;
        flex-wrap: wrap;
        gap: 2px;
        padding: 6px 8px;
        border-top: 1px solid var(--el-border-color-lighter);

        .el-button { margin: 0; }
      }
    }

    .queueList {
      display: flex;
      flex-direction: column;
      gap: 8px;

      .queueRow {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 12px;
        padding: 10px 12px;
        border: 1px solid var(--el-border-color-lighter);
        border-radius: var(--ui-radius);

        .queueMeta {
          display: flex;
          align-items: center;
          gap: 8px;
          min-width: 0;
          flex: 1;

          .queuePrompt {
            overflow: hidden;
            font-size: 13px;
            text-overflow: ellipsis;
            white-space: nowrap;
          }

          .queueSub {
            flex-shrink: 0;
            color: var(--el-text-color-secondary);
            font-size: 12px;
          }
        }

        .queueActions {
          display: flex;
          align-items: center;
          flex-shrink: 0;
        }
      }
    }

    .historyGrid {
      display: grid;
      grid-template-columns: repeat(auto-fill, minmax(min(100%, 200px), 1fr));
      gap: 12px;

      .historyCard {
        overflow: hidden;
        border: 1px solid var(--el-border-color-lighter);
        border-radius: var(--ui-radius);
        background: var(--el-fill-color-extra-light);

        .historyThumb {
          aspect-ratio: 1;
          overflow: hidden;
          background: var(--el-fill-color-light);
          cursor: pointer;

          img {
            display: block;
            width: 100%;
            height: 100%;
            object-fit: cover;
          }
        }

        .historyInfo {
          padding: 8px 10px;

          .historyPrompt {
            margin: 0;
            display: -webkit-box;
            -webkit-line-clamp: 2;
            -webkit-box-orient: vertical;
            overflow: hidden;
            font-size: 12px;
            line-height: 1.5;
            cursor: pointer;
          }

          .historyFoot {
            display: flex;
            align-items: center;
            justify-content: space-between;
            gap: 6px;
            margin-top: 6px;

            .historyModel {
              overflow: hidden;
              color: var(--el-text-color-secondary);
              font-size: 11px;
              text-overflow: ellipsis;
              white-space: nowrap;
            }

            .historyBtns {
              display: flex;
              flex-shrink: 0;

              .el-button {
                margin: 0;
                padding: 4px;
              }
            }
          }
        }
      }
    }
  }

  @media (max-width: 960px) {
    .studioGrid {
      grid-template-columns: 1fr;
    }
  }
}
</style>
