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
      <!-- Image Generation Key (shared by Quick Image Studio & Canvas Image Generation Node) -->
      <section class="roleCard">
        <div class="roleHeader">
          <h3>{{ translate("图像生成") }}</h3>
          <span v-if="imageStatus !== 'idle'" class="statusIndicator" :class="imageStatus">
            <icon-circle-check v-if="imageStatus === 'ok'" :size="15" aria-hidden="true" />
            <icon-alert-circle v-else :size="15" aria-hidden="true" />
            <span>{{ imageStatusText }}</span>
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
          <el-button :loading="verifyingImage" :disabled="!selectedImageKeyId" @click="verifyKey('image')">
            {{ translate("校验") }}
          </el-button>
        </div>
      </section>

      <!-- Text / AI Assistant & Prompt Rewrite Key -->
      <section class="roleCard">
        <div class="roleHeader">
          <h3>{{ translate("文本 / AI 助手与改写") }}</h3>
          <span v-if="textStatus !== 'idle'" class="statusIndicator" :class="textStatus">
            <icon-circle-check v-if="textStatus === 'ok'" :size="15" aria-hidden="true" />
            <icon-alert-circle v-else :size="15" aria-hidden="true" />
            <span>{{ textStatusText }}</span>
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
          <el-button :loading="verifyingText" :disabled="!selectedTextKeyId" @click="verifyKey('text')">
            {{ translate("校验") }}
          </el-button>
        </div>
        <div class="subControls">
          <div class="subField">
            <label>{{ translate("改写模型") }}</label>
            <el-select
              :modelValue="rewriteModel"
              filterable
              allowCreate
              defaultFirstOption
              clearable
              :placeholder="textModelOptions[0]?.id || 'gpt-4o-mini'"
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

      <!-- Video Generation Key -->
      <section class="roleCard">
        <div class="roleHeader">
          <h3>{{ translate("视频生成") }}</h3>
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
        </div>
      </section>

      <!-- Audio Generation Key -->
      <section class="roleCard">
        <div class="roleHeader">
          <h3>{{ translate("音频生成") }}</h3>
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
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { ElMessage } from "element-plus";
import { IconAlertCircle, IconCircleCheck, IconExternalLink, IconRefresh } from "@tabler/icons-vue";
import { translate } from "@toonflow/i18n/vue";
import {
  getHostContext,
  requestOpenSiteKeys,
  requestRefreshKeysFromHost,
  subscribeHostContext,
  updateKeyBindingsFromStudio,
  type Sub2ApiHostContext,
} from "../runtime/sub2apiBridge";
import {
  detectRewriteProtocol,
  effortOptionsForModel,
  getDiscoveredTextModelMetas,
  verifySiteKey,
  type RewriteEffort,
} from "./quickImage/quickImageApi";

defineProps<{ visible?: boolean }>();

const hostCtx = ref<Sub2ApiHostContext>(getHostContext());
const refreshing = ref(false);
const verifyingImage = ref(false);
const verifyingText = ref(false);
const imageStatus = ref<"idle" | "ok" | "error">("idle");
const textStatus = ref<"idle" | "ok" | "error">("idle");
const imageStatusText = ref("");
const textStatusText = ref("");

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
const rewriteModel = computed(() => hostCtx.value.rewriteModel || "");
const rewriteEffort = computed<RewriteEffort>(() => (hostCtx.value.rewriteEffort as RewriteEffort) || "unset");

const textModelOptions = computed(() => getDiscoveredTextModelMetas(selectedTextKeyId.value));

const effortOptions = computed<RewriteEffort[]>(() => {
  const activeKey = hostKeys.value.find((k) => k.id === selectedTextKeyId.value);
  const protocol = detectRewriteProtocol(activeKey?.platform || "", rewriteModel.value);
  const meta = textModelOptions.value.find((m) => m.id === rewriteModel.value) ?? null;
  const options = effortOptionsForModel(meta, protocol);
  return options.includes("unset") ? options : ["unset", ...options];
});

function effortLabel(effort: RewriteEffort): string {
  if (effort === "unset") return translate("自动（不指定）");
  if (effort === "off") return translate("关闭思考");
  return effort;
}

function onSelectKey(field: "textKeyId" | "imageKeyId" | "videoKeyId" | "audioKeyId", value: number | null) {
  if (field === "imageKeyId") imageStatus.value = "idle";
  if (field === "textKeyId") textStatus.value = "idle";
  updateKeyBindingsFromStudio({ [field]: value });
}

function onUpdateRewriteModel(value: string) {
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

async function verifyKey(role: "image" | "text") {
  const keyId = role === "image" ? selectedImageKeyId.value : selectedTextKeyId.value;
  const keyObj = hostKeys.value.find((k) => k.id === keyId);
  if (!keyObj?.key) return;
  if (role === "image") verifyingImage.value = true;
  else verifyingText.value = true;
  const controller = new AbortController();
  const timer = window.setTimeout(() => controller.abort(), 15000);
  try {
    const res = await verifySiteKey(keyObj.key, role, controller.signal);
    if (role === "image") {
      imageStatus.value = res.ok ? "ok" : "error";
      imageStatusText.value = res.ok ? `${res.modelCount}` : translate("不可用");
    } else {
      textStatus.value = res.ok ? "ok" : "error";
      textStatusText.value = res.ok ? `${res.modelCount}` : translate("不可用");
    }
    if (!res.ok) {
      ElMessage.warning(role === "image" ? translate("该密钥未发现可用图像模型") : translate("该密钥未发现可用文本模型"));
    }
  } catch {
    if (role === "image") {
      imageStatus.value = "error";
      imageStatusText.value = translate("校验失败");
    } else {
      textStatus.value = "error";
      textStatusText.value = translate("校验失败");
    }
  } finally {
    window.clearTimeout(timer);
    if (role === "image") verifyingImage.value = false;
    else verifyingText.value = false;
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

  @media (max-width: 600px) {
    .roleCard .subControls {
      grid-template-columns: 1fr;
    }
  }
}
</style>
