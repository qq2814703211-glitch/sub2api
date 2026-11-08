<template>
  <!--
    生图密钥与 AI 改写密钥共处一张卡片。

    卡片默认只露用户真正要填的东西：两个密钥选择框、说明、校验令牌按钮、
    详细配置入口。模型 / 思考强度这些低频项全部收进弹窗，接口地址与协议
    不出现任何控件——地址写死、协议由密钥所属分组自动判定。
  -->
  <section class="card">
    <div class="card-body space-y-4">
      <div class="flex items-start justify-between gap-3">
        <div class="min-w-0">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('imagegen.keyCardTitle') }}</h3>
          <p class="mt-0.5 text-[11px] leading-relaxed text-gray-500 dark:text-dark-400">
            {{ t('imagegen.keyCardHint') }}
          </p>
        </div>
        <button
          type="button"
          class="btn btn-secondary btn-sm shrink-0"
          data-test="rewrite-advanced-open"
          @click="openAdvanced"
        >
          <Icon name="cog" size="sm" />
          {{ t('imagegen.rewriteAdvancedOpen') }}
        </button>
      </div>

      <!-- ==================== 生图密钥 ==================== -->
      <div class="rounded-lg border border-gray-200 px-3 py-2.5 dark:border-dark-700">
        <div class="flex items-center justify-between gap-2">
          <span class="text-xs font-semibold text-gray-900 dark:text-white">
            {{ t('imagegen.generationKeySection') }}
          </span>
          <span v-if="generationKeyStatus !== 'unknown'" class="badge shrink-0" :class="generationBadgeClass">
            {{ generationBadgeText }}
          </span>
        </div>

        <div class="mt-2 flex gap-2">
          <select
            :value="generationKeyId === null ? '' : String(generationKeyId)"
            class="input flex-1"
            data-test="generation-key-select"
            :aria-label="t('imagegen.generationKeySection')"
            @change="onGenerationKeyChange(($event.target as HTMLSelectElement).value)"
          >
            <option value="">{{ t('imagegen.keySelectPlaceholder') }}</option>
            <option v-for="item in keyOptions" :key="item.id" :value="String(item.id)">
              {{ item.label }}
            </option>
          </select>
          <button
            type="button"
            class="btn btn-secondary btn-sm shrink-0"
            data-test="generation-key-check"
            :disabled="checkingGenerationKey || !generationKeyValue"
            @click="checkGenerationKey"
          >
            {{ checkingGenerationKey ? t('imagegen.keyChecking') : t('imagegen.keyCheck') }}
          </button>
        </div>

        <p class="mt-1.5 text-[11px] leading-relaxed text-gray-500 dark:text-dark-400">
          {{ t('imagegen.generationKeyHint') }}
        </p>
      </div>

      <!-- ==================== AI 改写密钥 ==================== -->
      <div class="rounded-lg border border-gray-200 px-3 py-2.5 dark:border-dark-700">
        <div class="flex items-center justify-between gap-2">
          <span class="text-xs font-semibold text-gray-900 dark:text-white">
            {{ t('imagegen.rewriteKeySection') }}
          </span>
          <span v-if="rewriteBadgeText" class="badge shrink-0" :class="rewriteBadgeClass">
            {{ rewriteBadgeText }}
          </span>
        </div>

        <div class="mt-2 flex gap-2">
          <select
            :value="config.keyId === null ? '' : String(config.keyId)"
            class="input flex-1"
            data-test="rewrite-key-select"
            :aria-label="t('imagegen.rewriteKeySection')"
            @change="onRewriteKeyChange(($event.target as HTMLSelectElement).value)"
          >
            <option value="">{{ t('imagegen.rewriteKeySelectPlaceholder') }}</option>
            <option v-for="item in keyOptions" :key="item.id" :value="String(item.id)">
              {{ item.label }}
            </option>
          </select>
          <button
            type="button"
            class="btn btn-secondary btn-sm shrink-0"
            data-test="rewrite-key-check"
            :disabled="checkingRewriteKey || !rewriteKeyValue"
            @click="checkRewriteKey"
          >
            {{ checkingRewriteKey ? t('imagegen.keyChecking') : t('imagegen.keyCheck') }}
          </button>
        </div>

        <p class="mt-1.5 text-[11px] leading-relaxed text-gray-500 dark:text-dark-400">
          {{ rewriteKeyHint }}
        </p>

        <!-- 旧版手填令牌：只读展示，不给编辑入口 -->
        <p
          v-if="config.keyId === null && config.legacyApiKey"
          class="mt-1.5 text-[11px] leading-relaxed text-amber-600 dark:text-amber-400"
        >
          {{ t('imagegen.rewriteLegacyKeyNotice') }}
        </p>

        <p v-if="rewriteCheckMessage" class="mt-1.5 text-[11px] leading-relaxed" :class="rewriteCheckMessageClass">
          {{ rewriteCheckMessage }}
        </p>

        <!-- 协议是自动判定的，这里只做只读说明，绝不出现下拉 -->
        <p class="mt-1.5 text-[11px] leading-relaxed text-gray-500 dark:text-dark-400" data-test="rewrite-protocol-readonly">
          {{ protocolHint }}
        </p>
      </div>
    </div>

    <!-- ==================== 详细配置弹窗 ==================== -->
    <BaseDialog :show="advancedOpen" :title="t('imagegen.rewriteAdvancedTitle')" width="wide" @close="advancedOpen = false">
      <div class="space-y-4">
        <!-- 模型：由所选密钥的分组决定候选 -->
        <label class="block">
          <span class="input-label">{{ t('imagegen.rewriteModelLabel') }}</span>
          <select
            :value="config.model"
            class="input"
            data-test="rewrite-model-select"
            :disabled="modelOptions.length === 0"
            @change="emit('update:model', ($event.target as HTMLSelectElement).value)"
          >
            <option v-if="modelOptions.length === 0" value="">{{ t('imagegen.rewriteModelEmpty') }}</option>
            <option v-for="item in modelOptions" :key="item.id" :value="item.id">{{ item.id }}</option>
          </select>
          <span class="input-hint">{{ modelHint }}</span>
        </label>

        <!-- 思考强度：按模型能力收窄，拿不到元数据就给全量七档 -->
        <label class="block">
          <span class="input-label">{{ t('imagegen.rewriteEffortLabel') }}</span>
          <select
            :value="config.effort"
            class="input"
            data-test="rewrite-effort-select"
            @change="emit('update:effort', ($event.target as HTMLSelectElement).value as RewriteEffort)"
          >
            <option value="unset">{{ t('imagegen.rewriteEffort_unset') }}</option>
            <option v-for="item in effortOptions" :key="item" :value="item">
              {{ effortLabel(item) }}
            </option>
          </select>
          <span class="input-hint">{{ effortHint }}</span>
        </label>

        <!-- 地址：写死，只读展示 -->
        <div class="block">
          <span class="input-label">{{ t('imagegen.rewriteEndpointLabel') }}</span>
          <p
            class="break-all rounded-md bg-gray-50 px-2 py-1.5 font-mono text-[10px] leading-relaxed text-gray-600 dark:bg-dark-800 dark:text-dark-300"
            data-test="rewrite-endpoint-readonly"
          >
            POST {{ endpointPreview }}
          </p>
          <span class="input-hint">{{ t('imagegen.rewriteEndpointHint') }}</span>
        </div>
      </div>

      <template #footer>
        <button type="button" class="btn btn-primary btn-sm" data-test="rewrite-advanced-close" @click="advancedOpen = false">
          {{ t('imagegen.rewriteAdvancedDone') }}
        </button>
      </template>
    </BaseDialog>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { buildRewriteUrl, rewritePath } from './rewriteApi'
import { verifyRewriteToken, resolveKeyContext, type RewriteTokenCheck } from './rewriteCatalog'
import {
  REWRITE_API_BASE_URL,
  detectRewriteProtocol,
  effortOptionsForModel,
  defaultEffortForModel,
  type RewriteConfig,
  type RewriteEffort,
  type RewriteModelOption,
} from './rewriteConfig'
import type { ApiKey } from '@/types'

const props = defineProps<{
  config: RewriteConfig
  /** 当前用户自己的密钥列表，两个下拉共用。 */
  keys: ApiKey[]
  /** 生图密钥选中的 id（生图侧自己的状态，与改写配置无关）。 */
  generationKeyId: number | null
  /** 所选密钥分组内可用的文本模型。 */
  modelOptions: RewriteModelOption[]
  /** 生图密钥的校验状态。 */
  generationKeyStatus: 'unknown' | 'verified' | 'invalid'
  checkingGenerationKey: boolean
  /** 模型列表是否正在加载。 */
  modelsLoading: boolean
  /** 镜像 composable 的判定，保证徽标与真实请求一致。 */
  usesGenerationKey: boolean
}>()

const emit = defineEmits<{
  (e: 'update:model', value: string): void
  (e: 'update:effort', value: RewriteEffort): void
  (e: 'update:keyId', value: number | null): void
  (e: 'update:generationKeyId', value: number | null): void
  (e: 'check-generation-key'): void
  (e: 'key-changed'): void
}>()

const { t } = useI18n()
const advancedOpen = ref(false)

const checkingRewriteKey = ref(false)
const rewriteCheck = ref<RewriteTokenCheck | null>(null)
const rewriteCheckMessage = ref('')

/** 下拉选项：只显示密钥名与分组名，绝不渲染完整 key。 */
const keyOptions = computed(() =>
  props.keys.map((item) => ({
    id: item.id,
    label: item.group?.name ? `${item.name} · ${item.group.name}` : item.name,
  })),
)

const generationKeyValue = computed(
  () => resolveKeyContext(props.keys, props.generationKeyId).key,
)
const rewriteKeyValue = computed(() => resolveKeyContext(props.keys, props.config.keyId).key)

const generationBadgeText = computed(() =>
  props.generationKeyStatus === 'verified' ? t('imagegen.keyValid') : t('imagegen.keyInvalid'),
)
const generationBadgeClass = computed(() =>
  props.generationKeyStatus === 'verified' ? 'badge-success' : 'badge-danger',
)

const rewriteBadgeText = computed(() => {
  if (rewriteCheck.value) return rewriteCheck.value.ok ? t('imagegen.keyValid') : t('imagegen.keyInvalid')
  return props.usesGenerationKey ? t('imagegen.rewriteKeyFallbackShort') : t('imagegen.rewriteKeyOwnShort')
})
const rewriteBadgeClass = computed(() => {
  if (rewriteCheck.value) return rewriteCheck.value.ok ? 'badge-success' : 'badge-danger'
  return props.usesGenerationKey ? 'badge-warning' : 'badge-primary'
})
const rewriteCheckMessageClass = computed(() =>
  rewriteCheck.value?.ok ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400',
)

/** 协议判定结果：密钥分组平台 + 当前模型。只做展示，没有控件。 */
const resolvedProtocol = computed(() => {
  const platform = resolveKeyContext(props.keys, props.config.keyId).platform
  return detectRewriteProtocol(platform, props.config.model)
})

const protocolHint = computed(() => {
  const key = `imagegen.rewriteProtocolAuto_${resolvedProtocol.value.replace(/-/g, '_')}`
  return t(key, { url: REWRITE_API_BASE_URL })
})

const rewriteKeyHint = computed(() =>
  props.usesGenerationKey
    ? t('imagegen.rewriteKeyFallbackHint')
    : t('imagegen.rewriteKeyOwnHint'),
)

/** 模型提示：区分「还没选密钥」「加载中」「该分组没有可用模型」三种空态。 */
const modelHint = computed(() => {
  if (props.config.keyId === null) return t('imagegen.rewriteModelNeedKey')
  if (props.modelsLoading) return t('imagegen.rewriteModelLoading')
  if (props.modelOptions.length === 0) return t('imagegen.rewriteModelNone')
  return t('imagegen.rewriteModelHint')
})

const currentModelMeta = computed(
  () => props.modelOptions.find((item) => item.id === props.config.model) ?? null,
)

/** 档位：模型声明了清单就用清单，否则全量七档。 */
const effortOptions = computed(() => effortOptionsForModel(currentModelMeta.value, resolvedProtocol.value))

const effortHint = computed(() => {
  const declaredDefault = defaultEffortForModel(currentModelMeta.value)
  if (props.config.effort === 'unset') {
    return declaredDefault
      ? t('imagegen.rewriteEffortHintAutoDefault', { effort: declaredDefault })
      : t('imagegen.rewriteEffortHintUnset')
  }
  if (props.config.effort === 'off') return t('imagegen.rewriteEffortHintOff')
  if (resolvedProtocol.value === 'anthropic-messages') return t('imagegen.rewriteEffortHintAnthropic')
  return t('imagegen.rewriteEffortHintOpenAI')
})

/** 弹窗里展示的实际端点。地址是常量，外部无法改。 */
const endpointPreview = computed(() =>
  buildRewriteUrl(REWRITE_API_BASE_URL, rewritePath(resolvedProtocol.value)),
)

function effortLabel(effort: RewriteEffort): string {
  return t(`imagegen.rewriteEffort_${effort}`)
}

function openAdvanced(): void {
  advancedOpen.value = true
}

function onGenerationKeyChange(raw: string): void {
  const id = raw ? Number(raw) : null
  emit('update:generationKeyId', Number.isFinite(id) && id !== null && id > 0 ? id : null)
  // 换了生图密钥 → 分组可能变了 → 改写侧的模型候选与协议都要重算。
  emit('key-changed')
}

function onRewriteKeyChange(raw: string): void {
  const id = raw ? Number(raw) : null
  emit('update:keyId', Number.isFinite(id) && id !== null && id > 0 ? id : null)
  rewriteCheck.value = null
  rewriteCheckMessage.value = ''
  emit('key-changed')
}

function checkGenerationKey(): void {
  emit('check-generation-key')
}

/**
 * 校验改写令牌：真打一次 `GET {站内}/v1/models`。
 * 结果同时回填协议判定，让用户看到「程序认出这走哪套协议」。
 */
async function checkRewriteKey(): Promise<void> {
  if (!rewriteKeyValue.value) {
    rewriteCheckMessage.value = t('imagegen.rewriteCheckNeedKey')
    rewriteCheck.value = { ok: false, protocol: null, modelCount: 0, message: '' }
    return
  }
  checkingRewriteKey.value = true
  const controller = new AbortController()
  const timeout = window.setTimeout(() => controller.abort(), 15000)
  try {
    const result = await verifyRewriteToken(props.config.keyId, rewriteKeyValue.value, controller.signal)
    rewriteCheck.value = result
    rewriteCheckMessage.value = controller.signal.aborted
      ? t('imagegen.rewriteCheckTimeout')
      : result.message
  } finally {
    window.clearTimeout(timeout)
    checkingRewriteKey.value = false
  }
}

defineExpose({ advancedOpen, checkRewriteKey })
</script>
