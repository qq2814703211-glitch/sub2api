<template>
  <el-drawer
    v-model="visible"
    :title="translate('模板库')"
    size="min(640px, 94vw)"
    appendToBody
    destroyOnClose
  >
    <div class="templateDrawer">
      <el-input
        v-model="query"
        :prefixIcon="IconSearch"
        :placeholder="translate('搜索模板')"
        clearable
      />

      <section v-if="filteredCustom.length" class="templateSection">
        <h3>{{ translate("我的模板") }}</h3>
        <div class="customList">
          <div v-for="tpl in filteredCustom" :key="tpl.id" class="customRow">
            <button type="button" class="customInfo" @click="applyDirect(tpl)">
              <strong>{{ tpl.name }}</strong>
              <span>{{ tpl.prompt }}</span>
            </button>
            <el-button text type="danger" :icon="IconTrash" @click="emit('deleteCustom', tpl.id)" />
          </div>
        </div>
      </section>

      <div v-if="categories.length > 1" class="categoryBar">
        <button
          v-for="cat in categories"
          :key="cat.id"
          type="button"
          class="catButton"
          :class="{ active: activeCategory === cat.id }"
          @click="activeCategory = cat.id"
        >
          {{ translate(cat.id) }} ({{ cat.count }})
        </button>
      </div>

      <div v-if="loading && !builtinTemplates.length" class="statusBox">
        {{ translate("正在加载…") }}
      </div>
      <el-empty v-else-if="!filteredBuiltin.length && !filteredCustom.length" :description="translate('暂无匹配模板')" :imageSize="64" />
      <div v-else class="templateGrid">
        <div
          v-for="tpl in filteredBuiltin"
          :key="tpl.id"
          class="templateCard"
          @click="openPreview(tpl)"
        >
          <div class="coverWrap">
            <img v-if="tpl.preview && !tpl.preview.startsWith('linear-gradient')" :src="tpl.preview" :alt="tpl.name" loading="lazy" />
            <div v-else class="coverPlaceholder" :style="tpl.preview ? { background: tpl.preview } : undefined">
              <icon-photo :size="24" aria-hidden="true" />
            </div>
          </div>
          <div class="cardBody">
            <strong>{{ tpl.name }}</strong>
            <p>{{ tpl.prompt }}</p>
          </div>
        </div>
      </div>
    </div>
  </el-drawer>

  <el-dialog
    v-model="previewVisible"
    :title="previewTemplate?.name || ''"
    width="min(760px, 94vw)"
    alignCenter
    appendToBody
  >
    <div v-if="previewTemplate" class="previewDialog">
      <div v-if="previewTemplate.preview && !previewTemplate.preview.startsWith('linear-gradient')" class="previewImage">
        <img :src="previewTemplate.preview" :alt="previewTemplate.name" />
      </div>
      <div class="previewForm">
        <label class="fieldLabel">{{ translate("原提示词") }}</label>
        <el-input v-model="draftPrompt" type="textarea" :rows="5" resize="none" />

        <div class="rewriteRow">
          <el-input
            v-model="instruction"
            :placeholder="translate('改写指令')"
            @keydown.enter.prevent="runRewrite"
          />
          <el-button :loading="rewriting" :disabled="!instruction.trim()" @click="runRewrite">
            {{ translate("AI 改写") }}
          </el-button>
        </div>
      </div>
    </div>
    <template #footer>
      <el-button @click="previewVisible = false">{{ translate("取消") }}</el-button>
      <el-button type="primary" @click="applyPreview">{{ translate("应用模板") }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import { IconPhoto, IconSearch, IconTrash } from "@tabler/icons-vue";
import { translate } from "@toonflow/i18n/vue";
import { normalizeRetouchParams } from "@host/features/imagegen/retouch";
import { safeGet, safeSet } from "@host/features/imagegen/storage";
import type { ImageTemplate, RetouchParams } from "@host/features/imagegen/types";
import { getHostContext } from "../../runtime/sub2apiBridge";
import { requestSiteRewrite, siteGatewayUrl, type RewriteEffort } from "./quickImageApi";

interface RawTemplate {
  id?: string;
  name?: string;
  tag?: string;
  category?: string;
  desc?: string;
  prompt?: string;
  preview?: string;
  params?: {
    model?: string;
    size?: string;
    quality?: string;
    count?: number;
    n?: number;
    format?: string;
    output_format?: string;
    background?: string;
    moderation?: string;
    compression?: number;
    style?: string;
    retouch?: unknown;
  };
}

const TEMPLATE_CATALOG_PATH = "/api/v1/imagegen-assets/templates/templates.json";
const TEMPLATE_CATALOG_CACHE_KEY = "imgGenTemplateCatalogCache";

const props = defineProps<{
  customTemplates: ImageTemplate[];
}>();

const visible = defineModel<boolean>({ default: false });

const emit = defineEmits<{
  apply: [template: ImageTemplate, prompt?: string];
  deleteCustom: [id: string];
}>();

const query = ref("");
const activeCategory = ref("全部");
const builtinTemplates = ref<ImageTemplate[]>([]);
const loading = ref(false);
const loaded = ref(false);

const previewVisible = ref(false);
const previewTemplate = ref<ImageTemplate | null>(null);
const draftPrompt = ref("");
const instruction = ref("");
const rewriting = ref(false);

function catalogUrl(): string {
  return siteGatewayUrl(TEMPLATE_CATALOG_PATH);
}

function resolvePreview(value: string | undefined): string {
  if (!value) return "";
  if (value.startsWith("linear-gradient") || value.startsWith("data:") || /^https?:\/\//.test(value)) return value;
  try {
    return new URL(value, catalogUrl()).href;
  } catch {
    return value;
  }
}

function normalizeTemplate(raw: RawTemplate, index: number): ImageTemplate {
  const params = raw.params ?? {};
  const count = Number.isFinite(Number(params.count))
    ? Number(params.count)
    : Number.isFinite(Number(params.n))
      ? Number(params.n)
      : 1;
  const rawSize = String(params.size || "").trim();
  const normalized: ImageTemplate = {
    id: raw.id || `server-${index}`,
    name: raw.name || `模板 ${index + 1}`,
    tag: raw.tag || "",
    category: raw.category || raw.tag || "其他",
    desc: raw.desc || "",
    prompt: raw.prompt || "",
    preview: resolvePreview(raw.preview),
    params: {
      size: /^\d+x\d+$/.test(rawSize) ? rawSize : "1024x1024",
      quality: params.quality || "auto",
      count,
      format: params.format || params.output_format || "png",
      background: params.background || "auto",
      moderation: params.moderation || "auto",
      compression: Number.isFinite(Number(params.compression)) ? Number(params.compression) : 80,
      style: params.style || "",
      retouch: normalizeRetouchParams(params.retouch ?? {}) as unknown as RetouchParams,
    },
  };
  if (typeof params.model === "string" && params.model) normalized.params.model = params.model;
  return normalized;
}

async function loadCatalog() {
  if (loaded.value || loading.value) return;
  loading.value = true;
  try {
    const resp = await fetch(catalogUrl());
    if (!resp.ok) throw new Error(`${resp.status}`);
    const payload = (await resp.json()) as { templates?: RawTemplate[] } | RawTemplate[];
    const raw = Array.isArray(payload) ? payload : payload.templates ?? [];
    builtinTemplates.value = raw.map(normalizeTemplate);
    loaded.value = true;
    safeSet(
      TEMPLATE_CATALOG_CACHE_KEY,
      JSON.stringify({ version: 2, url: catalogUrl(), savedAt: Date.now(), templates: raw })
    );
  } catch {
    try {
      const cached = JSON.parse(safeGet(TEMPLATE_CATALOG_CACHE_KEY) || "null") as {
        templates?: RawTemplate[];
      } | null;
      if (Array.isArray(cached?.templates)) {
        builtinTemplates.value = cached.templates.map(normalizeTemplate);
        loaded.value = true;
      }
    } catch {
      // ignore invalid cache
    }
  } finally {
    loading.value = false;
  }
}

watch(visible, (open) => {
  if (open) void loadCatalog();
});

const categories = computed(() => {
  const seen = new Set<string>();
  for (const tpl of builtinTemplates.value) seen.add(tpl.category || "其他");
  return [
    { id: "全部", count: builtinTemplates.value.length },
    ...[...seen].map((id) => ({
      id,
      count: builtinTemplates.value.filter((t) => (t.category || "其他") === id).length,
    })),
  ];
});

function matchQuery(list: ImageTemplate[]): ImageTemplate[] {
  const q = query.value.trim().toLowerCase();
  if (!q) return list;
  return list.filter(
    (item) =>
      item.name.toLowerCase().includes(q) ||
      item.prompt.toLowerCase().includes(q) ||
      item.category.toLowerCase().includes(q)
  );
}

const filteredCustom = computed(() => matchQuery(props.customTemplates));
const filteredBuiltin = computed(() => {
  const byCat =
    activeCategory.value === "全部"
      ? builtinTemplates.value
      : builtinTemplates.value.filter((t) => (t.category || "其他") === activeCategory.value);
  return matchQuery(byCat);
});

function applyDirect(tpl: ImageTemplate) {
  emit("apply", tpl);
  visible.value = false;
}

function openPreview(tpl: ImageTemplate) {
  previewTemplate.value = tpl;
  draftPrompt.value = tpl.prompt;
  instruction.value = "";
  previewVisible.value = true;
}

function applyPreview() {
  if (!previewTemplate.value) return;
  emit("apply", previewTemplate.value, draftPrompt.value);
  previewVisible.value = false;
  visible.value = false;
}

async function runRewrite() {
  if (!draftPrompt.value.trim() || !instruction.value.trim() || rewriting.value) return;
  rewriting.value = true;
  const controller = new AbortController();
  const timer = window.setTimeout(() => controller.abort(), 90_000);
  try {
    const ctx = getHostContext();
    const rewritten = await requestSiteRewrite({
      original: draftPrompt.value,
      instruction: instruction.value.trim(),
      model: ctx.rewriteModel || "",
      effort: (ctx.rewriteEffort as RewriteEffort) || "unset",
      signal: controller.signal,
    });
    draftPrompt.value = rewritten;
    ElMessage.success(translate("提示词已修改"));
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : translate("改写失败"));
  } finally {
    window.clearTimeout(timer);
    rewriting.value = false;
  }
}
</script>

<style lang="scss" scoped>
.templateDrawer {
  display: flex;
  flex-direction: column;
  gap: 16px;

  .templateSection {
    h3 {
      margin: 0 0 8px;
      color: var(--el-text-color-secondary);
      font-size: 12px;
      font-weight: 500;
    }

    .customList {
      display: flex;
      flex-direction: column;
      gap: 6px;
    }

    .customRow {
      display: flex;
      align-items: center;
      gap: 8px;
      padding: 8px 10px;
      border: 1px solid var(--el-border-color-lighter);
      border-radius: var(--ui-radius);

      .customInfo {
        display: flex;
        flex: 1;
        flex-direction: column;
        gap: 2px;
        min-width: 0;
        padding: 0;
        border: 0;
        background: transparent;
        color: inherit;
        font: inherit;
        text-align: start;
        cursor: pointer;

        strong {
          overflow: hidden;
          font-size: 13px;
          text-overflow: ellipsis;
          white-space: nowrap;
        }

        span {
          overflow: hidden;
          color: var(--el-text-color-secondary);
          font-size: 12px;
          text-overflow: ellipsis;
          white-space: nowrap;
        }
      }
    }
  }

  .categoryBar {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;

    .catButton {
      padding: 6px 10px;
      border: 0;
      border-radius: var(--ui-radius-large);
      background: transparent;
      color: var(--el-text-color-secondary);
      font: inherit;
      font-size: 12px;
      cursor: pointer;

      &:hover { color: var(--el-text-color-primary); }
      &.active {
        background: var(--el-fill-color);
        color: var(--el-text-color-primary);
        font-weight: 600;
      }
    }
  }

  .statusBox {
    padding: 32px 0;
    color: var(--el-text-color-secondary);
    font-size: 13px;
    text-align: center;
  }

  .templateGrid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
  }

  .templateCard {
    overflow: hidden;
    border: 1px solid var(--el-border-color-lighter);
    border-radius: var(--ui-radius);
    background: var(--el-bg-color-overlay);
    cursor: pointer;
    transition: border-color 0.15s;

    &:hover {
      border-color: var(--el-color-primary);
    }

    .coverWrap {
      aspect-ratio: 16 / 10;
      overflow: hidden;
      background: var(--el-fill-color-light);

      img {
        display: block;
        width: 100%;
        height: 100%;
        object-fit: cover;
      }

      .coverPlaceholder {
        display: grid;
        place-items: center;
        width: 100%;
        height: 100%;
        color: var(--el-text-color-secondary);
      }
    }

    .cardBody {
      padding: 10px 12px;

      strong {
        display: block;
        overflow: hidden;
        font-size: 13px;
        font-weight: 500;
        text-overflow: ellipsis;
        white-space: nowrap;
      }

      p {
        margin: 4px 0 0;
        display: -webkit-box;
        -webkit-line-clamp: 2;
        -webkit-box-orient: vertical;
        overflow: hidden;
        color: var(--el-text-color-secondary);
        font-size: 12px;
        line-height: 1.5;
      }
    }
  }
}

.previewDialog {
  display: grid;
  grid-template-columns: minmax(0, 260px) minmax(0, 1fr);
  gap: 20px;

  .previewImage {
    overflow: hidden;
    border-radius: var(--ui-radius);
    background: var(--el-fill-color-light);

    img {
      display: block;
      width: 100%;
      height: auto;
      object-fit: contain;
    }
  }

  .previewForm {
    display: flex;
    flex-direction: column;
    gap: 10px;
    min-width: 0;

    .fieldLabel {
      color: var(--el-text-color-secondary);
      font-size: 12px;
    }

    .rewriteRow {
      display: flex;
      gap: 8px;
    }
  }

  @media (max-width: 680px) {
    grid-template-columns: 1fr;
  }
}
</style>
