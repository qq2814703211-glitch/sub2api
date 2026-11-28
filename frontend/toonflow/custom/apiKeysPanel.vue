<template>
  <div class="apiKeysPanel">
    <div class="panelToolbar">
      <div class="toolbarActions">
        <el-button :icon="IconRefresh" :loading="refreshing" @click="refreshKeys">
          {{ translate("刷新密钥") }}
        </el-button>
        <el-button type="primary" plain :icon="IconExternalLink" @click="openSiteKeys">
          {{ translate("管理站内密钥") }}
        </el-button>
      </div>
    </div>

    <div v-if="!hostKeys.length" class="emptyKeys">
      <el-empty :description="translate('暂无可用站内密钥')" :imageSize="72">
        <el-button type="primary" @click="openSiteKeys">{{ translate("前往创建密钥") }}</el-button>
      </el-empty>
    </div>

    <div v-else class="roleList">
      <!-- Image Generation Key & Default Model -->
      <section class="roleCard" data-role="image">
        <div class="roleHeader">
          <h3>{{ translate("图像生成") }}</h3>
          <span v-if="statusMap.image.state !== 'idle'" class="statusIndicator" :class="statusMap.image.state">
            <icon-circle-check v-if="statusMap.image.state === 'ok'" :size="15" aria-hidden="true" />
            <icon-alert-circle v-else :size="15" aria-hidden="true" />
            <span>{{ statusMap.image.text }}</span>
          </span>
        </div>
        <div class="roleControl">
          <el-select
            :modelValue="selectedImageKeyId"
            class="keySelect"
            :placeholder="translate('选择站内密钥')"
            @change="onSelectKey('imageKeyId', $event)"
          >
            <el-option
              v-for="item in keyOptions"
              :key="item.id"
              :label="item.label"
              :value="item.id"
            />
          </el-select>
          <el-button
            data-test="verify-image-key"
            :loading="verifyingMap.image"
            :disabled="!selectedImageKeyId"
            @click="verifyKey('image')"
          >
            {{ translate("校验") }}
          </el-button>
        </div>
        <div class="subControls singleColumn">
          <div class="subField">
            <label>{{ translate("默认模型") }}</label>
            <el-select
              :modelValue="defaultImageModel"
              filterable
              clearable
              :placeholder="imageModelOptions[0] || translate('请先校验密钥获取模型列表')"
              :noDataText="translate('当前密钥未返回可用模型，请先校验密钥')"
              @change="onUpdateDefaultModel('defaultImageModel', $event)"
            >
              <el-option
                v-for="m in imageModelOptions"
                :key="m"
                :label="m"
                :value="m"
              />
            </el-select>
          </div>
        </div>
      </section>

      <!-- Text / AI Assistant & Prompt Rewrite Key & Default Models -->
      <section class="roleCard" data-role="text">
        <div class="roleHeader">
          <h3>{{ translate("文本 / AI 助手与改写") }}</h3>
          <span v-if="statusMap.text.state !== 'idle'" class="statusIndicator" :class="statusMap.text.state">
            <icon-circle-check v-if="statusMap.text.state === 'ok'" :size="15" aria-hidden="true" />
            <icon-alert-circle v-else :size="15" aria-hidden="true" />
            <span>{{ statusMap.text.text }}</span>
          </span>
        </div>
        <div class="roleControl">
          <el-select
            :modelValue="selectedTextKeyId"
            class="keySelect"
            :placeholder="translate('选择站内密钥')"
            @change="onSelectKey('textKeyId', $event)"
          >
            <el-option
              v-for="item in keyOptions"
              :key="item.id"
              :label="item.label"
              :value="item.id"
            />
          </el-select>
          <el-button
            data-test="verify-text-key"
            :loading="verifyingMap.text"
            :disabled="!selectedTextKeyId"
            @click="verifyKey('text')"
          >
            {{ translate("校验") }}
          </el-button>
        </div>
        <div class="subControls threeColumns">
          <div class="subField">
            <label>{{ translate("默认模型") }}</label>
            <el-select
              :modelValue="defaultTextModel"
              filterable
              clearable
              :placeholder="textModelOptions[0]?.id || translate('请先校验密钥获取模型列表')"
              :noDataText="translate('当前密钥未返回可用模型，请先校验密钥')"
              @change="onUpdateDefaultModel('defaultTextModel', $event)"
            >
              <el-option
                v-for="m in textModelOptions"
                :key="m.id"
                :label="m.id"
                :value="m.id"
              />
            </el-select>
          </div>
          <div class="subField">
            <label>{{ translate("改写模型") }}</label>
            <el-select
              :modelValue="rewriteModel"
              filterable
              clearable
              :placeholder="defaultTextModel || textModelOptions[0]?.id || translate('跟随默认模型')"
              :noDataText="translate('当前密钥未返回可用模型，请先校验密钥')"
              @change="onUpdateRewriteModel($event)"
            >
              <el-option
                v-for="m in textModelOptions"
                :key="m.id"
                :label="m.id"
                :value="m.id"
              />
            </el-select>
          </div>
          <div class="subField">
            <label>{{ translate("思考强度") }}</label>
            <el-select :modelValue="rewriteEffort" @change="onUpdateRewriteEffort($event)">
              <el-option
                v-for="opt in effortOptions"
                :key="opt"
                :label="effortLabel(opt)"
                :value="opt"
              />
            </el-select>
          </div>
        </div>
      </section>

      <!-- Video Generation Key & Default Model -->
      <section class="roleCard" data-role="video">
        <div class="roleHeader">
          <h3>{{ translate("视频生成") }}</h3>
          <span v-if="statusMap.video.state !== 'idle'" class="statusIndicator" :class="statusMap.video.state">
            <icon-circle-check v-if="statusMap.video.state === 'ok'" :size="15" aria-hidden="true" />
            <icon-alert-circle v-else :size="15" aria-hidden="true" />
            <span>{{ statusMap.video.text }}</span>
          </span>
        </div>
        <div class="roleControl">
          <el-select
            :modelValue="selectedVideoKeyId"
            class="keySelect"
            :placeholder="translate('选择站内密钥')"
            @change="onSelectKey('videoKeyId', $event)"
          >
            <el-option
              v-for="item in keyOptions"
              :key="item.id"
              :label="item.label"
              :value="item.id"
            />
          </el-select>
          <el-button
            data-test="verify-video-key"
            :loading="verifyingMap.video"
            :disabled="!selectedVideoKeyId"
            @click="verifyKey('video')"
          >
            {{ translate("校验") }}
          </el-button>
        </div>
        <div class="subControls singleColumn">
          <div class="subField">
            <label>{{ translate("默认模型") }}</label>
            <el-select
              :modelValue="defaultVideoModel"
              filterable
              clearable
              :placeholder="videoModelOptions[0] || translate('请先校验密钥获取模型列表')"
              :noDataText="translate('当前密钥未返回可用模型，请先校验密钥')"
              @change="onUpdateDefaultModel('defaultVideoModel', $event)"
            >
              <el-option
                v-for="m in videoModelOptions"
                :key="m"
                :label="m"
                :value="m"
              />
            </el-select>
          </div>
        </div>
      </section>

      <!-- Audio Generation Key & Default Model -->
      <section class="roleCard" data-role="audio">
        <div class="roleHeader">
          <h3>{{ translate("音频生成") }}</h3>
          <span v-if="statusMap.audio.state !== 'idle'" class="statusIndicator" :class="statusMap.audio.state">
            <icon-circle-check v-if="statusMap.audio.state === 'ok'" :size="15" aria-hidden="true" />
            <icon-alert-circle v-else :size="15" aria-hidden="true" />
            <span>{{ statusMap.audio.text }}</span>
          </span>
        </div>
        <div class="roleControl">
          <el-select
            :modelValue="selectedAudioKeyId"
            class="keySelect"
            :placeholder="translate('选择站内密钥')"
            @change="onSelectKey('audioKeyId', $event)"
          >
            <el-option
              v-for="item in keyOptions"
              :key="item.id"
              :label="item.label"
              :value="item.id"
            />
          </el-select>
          <el-button
            data-test="verify-audio-key"
            :loading="verifyingMap.audio"
            :disabled="!selectedAudioKeyId"
            @click="verifyKey('audio')"
          >
            {{ translate("校验") }}
          </el-button>
        </div>
        <div class="subControls singleColumn">
          <div class="subField">
            <label>{{ translate("默认模型") }}</label>
            <el-select
              :modelValue="defaultAudioModel"
              filterable
              clearable
              :placeholder="audioModelOptions[0] || translate('请先校验密钥获取模型列表')"
              :noDataText="translate('当前密钥未返回可用模型，请先校验密钥')"
              @change="onUpdateDefaultModel('defaultAudioModel', $event)"
            >
              <el-option
                v-for="m in audioModelOptions"
                :key="m"
                :label="m"
                :value="m"
              />
            </el-select>
          </div>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from "vue";
import { ElMessage } from "element-plus";
import { IconAlertCircle, IconCircleCheck, IconExternalLink, IconRefresh } from "@tabler/icons-vue";
import { translate } from "@toonflow/i18n/vue";
import {
  getHostContext,
  requestOpenSiteKeys,
  requestRefreshKeysFromHost,
  subscribeHostContext,
  updateDiscoveredModelsFromStudio,
  updateKeyBindingsFromStudio,
  type StudioKeyBindingsPatch,
  type Sub2ApiHostContext,
} from "../runtime/sub2apiBridge";
import {
  detectRewriteProtocol,
  effortOptionsForModel,
  getDiscoveredAudioModels,
  getDiscoveredImageModels,
  getDiscoveredTextModelMetas,
  getDiscoveredVideoModels,
  verifySiteKey,
  type RewriteEffort,
  type SiteKeyRole,
} from "./quickImage/quickImageApi";

defineProps<{ visible?: boolean }>();

const hostCtx = ref<Sub2ApiHostContext>(getHostContext());
const refreshing = ref(false);
const verifyingMap = reactive<Record<SiteKeyRole, boolean>>({
  image: false,
  text: false,
  video: false,
  audio: false,
});
const statusMap = reactive<Record<SiteKeyRole, { state: "idle" | "ok" | "error"; text: string }>>({
  image: { state: "idle", text: "" },
  text: { state: "idle", text: "" },
  video: { state: "idle", text: "" },
  audio: { state: "idle", text: "" },
});

let unsubscribe: (() => void) | undefined;

onMounted(() => {
  hostCtx.value = getHostContext();
  unsubscribe = subscribeHostContext((next) => {
    hostCtx.value = next;
    refreshing.value = false;
  });
});

onBeforeUnmount(() => {
  unsubscribe?.();
});

const hostKeys = computed(() => hostCtx.value.keys);

const keyOptions = computed(() =>
  hostKeys.value.map((k) => ({
    id: k.id,
    label: k.groupName ? `${k.name} (${k.groupName})` : k.name,
  }))
);

const selectedImageKeyId = computed(() => hostCtx.value.selectedImageKeyId ?? hostKeys.value[0]?.id ?? null);
const selectedTextKeyId = computed(() => hostCtx.value.selectedTextKeyId ?? hostKeys.value[0]?.id ?? null);
const selectedVideoKeyId = computed(() => hostCtx.value.selectedVideoKeyId ?? hostKeys.value[0]?.id ?? null);
const selectedAudioKeyId = computed(() => hostCtx.value.selectedAudioKeyId ?? hostKeys.value[0]?.id ?? null);

const defaultImageModel = computed(() => hostCtx.value.defaultImageModel || "");
const defaultTextModel = computed(() => hostCtx.value.defaultTextModel || "");
const defaultVideoModel = computed(() => hostCtx.value.defaultVideoModel || "");
const defaultAudioModel = computed(() => hostCtx.value.defaultAudioModel || "");
const rewriteModel = computed(() => hostCtx.value.rewriteModel || "");
const rewriteEffort = computed<RewriteEffort>(() => (hostCtx.value.rewriteEffort as RewriteEffort) || "unset");

const imageModelOptions = computed(() => getDiscoveredImageModels(selectedImageKeyId.value));
const textModelOptions = computed(() => getDiscoveredTextModelMetas(selectedTextKeyId.value));
const videoModelOptions = computed(() => getDiscoveredVideoModels(selectedVideoKeyId.value));
const audioModelOptions = computed(() => getDiscoveredAudioModels(selectedAudioKeyId.value));

const effortOptions = computed<RewriteEffort[]>(() => {
  const activeKey = hostKeys.value.find((k) => k.id === selectedTextKeyId.value);
  const activeModelId = rewriteModel.value || defaultTextModel.value;
  const protocol = detectRewriteProtocol(activeKey?.platform || "", activeModelId);
  const meta = textModelOptions.value.find((m) => m.id === activeModelId) ?? null;
  const options = effortOptionsForModel(meta, protocol);
  return options.includes("unset") ? options : ["unset", ...options];
});

function effortLabel(effort: RewriteEffort): string {
  if (effort === "unset") return translate("自动（不指定）");
  if (effort === "off") return translate("关闭思考");
  return effort;
}

function onSelectKey(field: "textKeyId" | "imageKeyId" | "videoKeyId" | "audioKeyId", value: number | null) {
  const roleMap: Record<typeof field, SiteKeyRole> = {
    imageKeyId: "image",
    textKeyId: "text",
    videoKeyId: "video",
    audioKeyId: "audio",
  };
  const role = roleMap[field];
  statusMap[role].state = "idle";
  statusMap[role].text = "";

  const patch: StudioKeyBindingsPatch = { [field]: value };
  const nextModels = value ? hostCtx.value.discoveredModels[value] ?? [] : [];
  if (role === "image" && defaultImageModel.value && nextModels.length && !nextModels.includes(defaultImageModel.value)) {
    patch.defaultImageModel = nextModels[0] ?? "";
  }
  if (role === "video" && defaultVideoModel.value && nextModels.length && !nextModels.includes(defaultVideoModel.value)) {
    patch.defaultVideoModel = nextModels[0] ?? "";
  }
  if (role === "audio" && defaultAudioModel.value && nextModels.length && !nextModels.includes(defaultAudioModel.value)) {
    patch.defaultAudioModel = nextModels[0] ?? "";
  }
  if (role === "text" && nextModels.length) {
    if (defaultTextModel.value && !nextModels.includes(defaultTextModel.value)) {
      patch.defaultTextModel = nextModels[0] ?? "";
    }
    if (rewriteModel.value && !nextModels.includes(rewriteModel.value)) {
      patch.rewriteModel = "";
    }
  }
  updateKeyBindingsFromStudio(patch);
}

function onUpdateDefaultModel(
  field: "defaultImageModel" | "defaultTextModel" | "defaultVideoModel" | "defaultAudioModel",
  value: string | undefined
) {
  const clean = String(value || "").trim();
  updateKeyBindingsFromStudio({ [field]: clean });
}

function onUpdateRewriteModel(value: string | undefined) {
  updateKeyBindingsFromStudio({ rewriteModel: String(value || "").trim() });
}

function onUpdateRewriteEffort(value: string) {
  updateKeyBindingsFromStudio({ rewriteEffort: value || "unset" });
}

function refreshKeys() {
  refreshing.value = true;
  requestRefreshKeysFromHost();
  window.setTimeout(() => {
    refreshing.value = false;
  }, 4000);
}

function openSiteKeys() {
  requestOpenSiteKeys();
}

function getKeyIdForRole(role: SiteKeyRole): number | null {
  if (role === "image") return selectedImageKeyId.value;
  if (role === "text") return selectedTextKeyId.value;
  if (role === "video") return selectedVideoKeyId.value;
  return selectedAudioKeyId.value;
}

async function verifyKey(role: SiteKeyRole) {
  const keyId = getKeyIdForRole(role);
  const keyObj = hostKeys.value.find((k) => k.id === keyId);
  if (!keyId || !keyObj?.key) return;
  verifyingMap[role] = true;
  const controller = new AbortController();
  const timer = window.setTimeout(() => controller.abort(), 15000);
  try {
    const res = await verifySiteKey(keyObj.key, role, controller.signal);
    updateDiscoveredModelsFromStudio(keyId, res.models, res.meta);
    statusMap[role].state = res.ok ? "ok" : "error";
    statusMap[role].text = res.ok ? `${res.modelCount} ${translate("个模型")}` : translate("不可用");

    if (res.ok) {
      const patch: StudioKeyBindingsPatch = {};
      if (role === "image" && (!defaultImageModel.value || !res.models.includes(defaultImageModel.value))) {
        patch.defaultImageModel = res.models[0] ?? "";
      } else if (role === "text") {
        if (!defaultTextModel.value || !res.models.includes(defaultTextModel.value)) {
          patch.defaultTextModel = res.models[0] ?? "";
        }
        if (rewriteModel.value && !res.models.includes(rewriteModel.value)) {
          patch.rewriteModel = "";
        }
      } else if (role === "video" && (!defaultVideoModel.value || !res.models.includes(defaultVideoModel.value))) {
        patch.defaultVideoModel = res.models[0] ?? "";
      } else if (role === "audio" && (!defaultAudioModel.value || !res.models.includes(defaultAudioModel.value))) {
        patch.defaultAudioModel = res.models[0] ?? "";
      }
      if (Object.keys(patch).length) {
        updateKeyBindingsFromStudio(patch);
      }
    } else {
      ElMessage.warning(translate("该密钥未从 /models 返回可用模型"));
    }
  } catch {
    statusMap[role].state = "error";
    statusMap[role].text = translate("校验失败");
  } finally {
    window.clearTimeout(timer);
    verifyingMap[role] = false;
  }
}
</script>

<style lang="scss" scoped>
.apiKeysPanel {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 0 4px 8px;

  .panelToolbar {
    display: flex;
    align-items: center;
    justify-content: flex-end;

    .toolbarActions {
      display: flex;
      align-items: center;
      flex-wrap: wrap;
      gap: 8px;
    }
  }

  .emptyKeys {
    padding: 24px 0;
  }

  .roleList {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .roleCard {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 14px 16px;
    border: 1px solid var(--el-border-color-lighter);
    border-radius: var(--ui-radius);
    background: var(--el-fill-color-extra-light);

    .roleHeader {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;

      h3 {
        margin: 0;
        color: var(--el-text-color-primary);
        font-size: 14px;
        font-weight: 600;
      }

      .statusIndicator {
        display: inline-flex;
        align-items: center;
        gap: 4px;
        font-size: 12px;
        font-variant-numeric: tabular-nums;

        &.ok { color: var(--el-color-success); }
        &.error { color: var(--el-color-danger); }
      }
    }

    .roleControl {
      display: flex;
      align-items: center;
      gap: 8px;

      .keySelect {
        flex: 1;
        min-width: 0;
      }
    }

    .subControls {
      display: grid;
      grid-template-columns: minmax(0, 1fr) minmax(140px, 200px);
      gap: 10px;
      padding-top: 4px;

      &.singleColumn {
        grid-template-columns: minmax(0, 1fr);
      }

      &.threeColumns {
        grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) minmax(130px, 180px);
      }

      .subField {
        display: flex;
        flex-direction: column;
        gap: 6px;
        min-width: 0;

        label {
          color: var(--el-text-color-secondary);
          font-size: 12px;
        }
      }
    }
  }

  @media (max-width: 680px) {
    .roleCard .subControls,
    .roleCard .subControls.threeColumns {
      grid-template-columns: 1fr;
    }
  }
}
</style>
