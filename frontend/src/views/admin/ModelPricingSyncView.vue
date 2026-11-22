<template>
  <AppLayout>
    <div class="w-full min-w-0 space-y-6 pb-8">
      <!-- 计价口径配置：usd < cny-official < manual，manual 是这张卡片唯一可写的部分。 -->
      <section class="card p-4 sm:p-6" data-testid="pricing-source-card">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div class="min-w-0">
            <h2 class="flex items-center gap-2 text-base font-semibold text-gray-900 dark:text-white">
              <span
                class="inline-flex h-7 w-7 items-center justify-center rounded-lg bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300"
              >
                <Icon name="cog" size="sm" />
              </span>
              {{ t('admin.modelPricingSync.pricingSource.title') }}
            </h2>
            <p class="mt-1.5 text-xs text-gray-500 dark:text-dark-400">
              {{ t('admin.modelPricingSync.pricingSource.description') }}
            </p>
          </div>
          <span
            v-if="dirty"
            class="inline-flex items-center rounded-full bg-amber-50 px-2.5 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-300"
            data-testid="pricing-source-dirty"
          >
            {{ t('admin.modelPricingSync.pricingSource.unsaved') }}
          </span>
        </div>

        <!-- 优先级条：一眼看出 manual 最高。 -->
        <div class="mt-4" data-testid="pricing-priority-strip">
          <div class="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-gray-400 dark:text-dark-500">
            {{ t('admin.modelPricingSync.pricingSource.priorityLabel') }}
          </div>
          <div class="flex flex-col gap-2 sm:flex-row sm:items-stretch">
            <div
              v-for="(tier, index) in priorityTiers"
              :key="tier.key"
              class="flex min-w-0 flex-1 flex-col justify-center rounded-xl border px-3 py-2"
              :class="tier.highest
                ? 'border-primary-300 bg-primary-50 dark:border-primary-700/60 dark:bg-primary-900/20'
                : 'border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-900'"
              :data-testid="`pricing-tier-${tier.key}`"
            >
              <div class="flex items-center gap-1.5">
                <span
                  class="text-sm font-semibold"
                  :class="tier.highest ? 'text-primary-700 dark:text-primary-300' : 'text-gray-700 dark:text-gray-200'"
                >
                  {{ index + 1 }}. {{ tier.label }}
                </span>
                <span
                  v-if="tier.highest"
                  class="inline-flex items-center rounded-full bg-primary-600 px-1.5 py-0.5 text-[10px] font-bold text-white"
                >
                  {{ t('admin.modelPricingSync.pricingSource.highestBadge') }}
                </span>
              </div>
              <span class="mt-0.5 truncate text-[11px] text-gray-500 dark:text-dark-400">{{ tier.note }}</span>
            </div>
          </div>
        </div>

        <!-- 加载 / 错误 -->
        <div
          v-if="configLoading"
          class="mt-5 flex min-h-[120px] items-center justify-center rounded-xl border border-dashed border-gray-200 text-sm text-gray-500 dark:border-dark-700 dark:text-dark-400"
          data-testid="pricing-source-loading"
        >
          {{ t('admin.modelPricingSync.pricingSource.loading') }}
        </div>

        <div
          v-else-if="configError"
          class="mt-5 rounded-xl border border-red-200 bg-red-50 px-4 py-3 dark:border-red-900/50 dark:bg-red-950/30"
          data-testid="pricing-source-load-error"
          role="alert"
        >
          <p class="text-sm font-medium text-red-700 dark:text-red-300">
            {{ routeMissing
              ? t('admin.modelPricingSync.pricingSource.endpointMissing', { path: OPEN_API_PATH })
              : t('admin.modelPricingSync.pricingSource.loadFailed') }}
          </p>
          <p class="mt-1 break-all font-mono text-xs text-red-600 dark:text-red-400">{{ configError }}</p>
          <p v-if="routeMissing" class="mt-2 text-xs text-gray-600 dark:text-gray-300">
            {{ t('admin.modelPricingSync.pricingSource.endpointMissingHint') }}
          </p>
          <button type="button" class="btn btn-secondary btn-sm mt-3" @click="loadConfig">
            {{ t('admin.modelPricingSync.pricingSource.retry') }}
          </button>
        </div>

        <template v-else-if="config">
          <!-- 三档状态区 -->
          <div class="mt-4 divide-y divide-gray-100 rounded-xl border border-gray-200 dark:divide-dark-700 dark:border-dark-700">
            <!-- USD 同步：只读 -->
            <div class="flex flex-wrap items-center justify-between gap-3 px-4 py-3">
              <div class="min-w-0">
                <strong class="text-sm font-medium text-gray-900 dark:text-white">
                  {{ t('admin.modelPricingSync.pricingSource.usdTitle') }}
                </strong>
                <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
                  {{ t('admin.modelPricingSync.pricingSource.usdHint') }}
                </p>
              </div>
              <span
                class="inline-flex items-center gap-1.5 whitespace-nowrap rounded-full px-2.5 py-0.5 text-xs font-semibold"
                :class="usdEnabled
                  ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
                  : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-300'"
                data-testid="pricing-usd-state"
              >
                <span class="h-1.5 w-1.5 rounded-full" :class="usdEnabled ? 'bg-emerald-500' : 'bg-gray-400'"></span>
                {{ usdEnabled
                  ? t('admin.modelPricingSync.pricingSource.usdEnabled')
                  : t('admin.modelPricingSync.pricingSource.usdDisabled') }}
              </span>
            </div>

            <!-- CNY 官方价：开关 + 数据源（只读） -->
            <div class="px-4 py-3">
              <div class="flex flex-wrap items-center justify-between gap-3">
                <div class="min-w-0">
                  <strong class="text-sm font-medium text-gray-900 dark:text-white">
                    {{ t('admin.modelPricingSync.pricingSource.cnyTitle') }}
                  </strong>
                  <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
                    {{ t('admin.modelPricingSync.pricingSource.cnyHint') }}
                  </p>
                </div>
                <Toggle v-model="config.enabled" :disabled="saving" data-testid="pricing-cny-toggle" />
              </div>
              <div class="mt-2 flex flex-wrap items-baseline gap-2">
                <span class="text-xs text-gray-500 dark:text-dark-400">
                  {{ t('admin.modelPricingSync.pricingSource.sourceUrl') }}
                </span>
                <span class="break-all font-mono text-xs text-gray-700 dark:text-gray-300" data-testid="pricing-source-url">
                  {{ config.source_url }}
                </span>
              </div>
            </div>
          </div>

          <div v-if="config.revision !== undefined || pricingStatus" class="mt-4 flex flex-wrap items-center gap-x-5 gap-y-2 text-xs text-gray-600 dark:text-gray-300" data-testid="pricing-revisions">
            <span>{{ text('storedRevision') }}: <code class="break-all">{{ config.revision || '—' }}</code></span>
            <span>{{ text('activeRevision') }}: <code class="break-all">{{ activeRevision || '—' }}</code></span>
            <button type="button" class="btn btn-ghost btn-sm" :disabled="saving || statusLoading" @click="refreshPricing">
              {{ text('refreshStatus') }}
            </button>
          </div>
          <p v-if="activationPending" class="mt-2 text-xs text-amber-700 dark:text-amber-300" role="status" data-testid="pricing-activation-pending">{{ text('activationPending') }}</p>
          <p v-if="pricingScope" class="mt-2 text-xs" :class="pricingScope.ok ? 'text-gray-500 dark:text-dark-400' : 'text-amber-700 dark:text-amber-300'" role="status" data-testid="pricing-scope">{{ pricingScope.text }}</p>
          <p v-if="statusError" class="mt-2 break-words text-xs text-red-700 dark:text-red-300" role="alert">{{ statusError }}</p>
          <details v-if="sourceErrors.length" class="mt-2 text-xs text-red-700 dark:text-red-300">
            <summary class="cursor-pointer">{{ text('sourceFailures') }} ({{ sourceErrors.length }})</summary>
            <ul class="mt-1 space-y-1">
              <li v-for="[source, error] in sourceErrors" :key="source" class="break-all">{{ source }}: {{ error }}</li>
            </ul>
          </details>

          <section v-if="config.revision !== undefined || inspectModels.length" class="mt-4 border-t border-gray-200 pt-4 dark:border-dark-700">
            <div class="flex flex-wrap items-end gap-2">
              <label class="block min-w-0 grow sm:max-w-sm">
                <span class="input-label">{{ text('inspectModel') }}</span>
                <input v-model.trim="inspectedModel" list="pricing-v2-models" :placeholder="inspectModels[0]" class="input w-full font-mono text-sm" data-testid="pricing-inspect-model" />
              </label>
              <datalist id="pricing-v2-models">
                <option v-for="model in inspectModels" :key="model" :value="model" />
              </datalist>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="saving || !selectedModel || selectedStatus?.external" @click="editSelectedModel">
                {{ text('editSelected') }}
              </button>
            </div>
            <div v-if="selectedModel" class="mt-3 grid gap-3 lg:grid-cols-3">
              <PricingSyncQuotePanel v-for="item in quotePanels" :key="item.kind" v-bind="item" />
            </div>
            <p v-if="selectedModel && selectedIssue" class="mt-2 break-words text-xs text-amber-700 dark:text-amber-300" data-testid="pricing-model-issue">{{ text('missingReason') }}: {{ selectedIssue }}</p>
          </section>

          <!-- 手动覆盖编辑器 -->
          <div class="mt-4">
            <div class="flex flex-wrap items-center justify-between gap-3">
              <div class="min-w-0">
                <strong class="text-sm font-medium text-gray-900 dark:text-white">
                  {{ t('admin.modelPricingSync.pricingSource.manualTitle') }}
                </strong>
                <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
                  {{ t('admin.modelPricingSync.pricingSource.manualHint') }}
                </p>
              </div>
              <div class="flex items-center gap-2">
                <template v-if="overrideRows.length > 0">
                  <button
                    type="button"
                    class="btn btn-ghost btn-sm"
                    data-testid="pricing-expand-all"
                    @click="toggleAllRows(true)"
                  >
                    {{ t('admin.modelPricingSync.pricingSource.expandAll') }}
                  </button>
                  <button
                    type="button"
                    class="btn btn-ghost btn-sm"
                    data-testid="pricing-collapse-all"
                    @click="toggleAllRows(false)"
                  >
                    {{ t('admin.modelPricingSync.pricingSource.collapseAll') }}
                  </button>
                </template>
                <button
                  type="button"
                  class="btn btn-secondary btn-sm"
                  data-testid="pricing-add-row"
                  :disabled="saving"
                  @click="addRow()"
                >
                  <Icon name="plus" size="sm" />
                  {{ t('admin.modelPricingSync.pricingSource.addRow') }}
                </button>
              </div>
            </div>

            <div
              v-if="overrideRows.length === 0"
              class="mt-3 flex flex-col items-center rounded-xl border border-dashed border-gray-200 px-4 py-8 text-center dark:border-dark-700"
              data-testid="pricing-manual-empty"
            >
              <Icon name="lightbulb" size="lg" class="mb-2 h-8 w-8 text-gray-300 dark:text-dark-600" />
              <p class="text-sm text-gray-500 dark:text-dark-400">
                {{ t('admin.modelPricingSync.pricingSource.manualEmpty') }}
              </p>
            </div>

            <div v-else class="mt-2 divide-y divide-gray-100 dark:divide-dark-700">
              <div
                v-for="row in overrideRows"
                :key="row.id"
                class="py-2"
                :data-testid="`pricing-row-${row.id}`"
                :data-expanded="row.expanded ? 'true' : 'false'"
              >
                <!-- 收起态：一行摘要。模型名 + 价格 + 阶梯标记，点整行展开。 -->
                <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
                  <button
                    type="button"
                    class="flex min-w-0 flex-1 items-center gap-2 text-left"
                    :aria-expanded="row.expanded"
                    :data-testid="`pricing-row-toggle-${row.id}`"
                    @click="toggleRow(row.id)"
                  >
                    <Icon
                      name="chevronRight"
                      size="sm"
                      class="h-4 w-4 shrink-0 text-gray-400 transition-transform"
                      :class="{ 'rotate-90': row.expanded }"
                    />
                    <code class="truncate font-mono text-sm text-gray-800 dark:text-gray-200">
                      {{ row.model || t('admin.modelPricingSync.pricingSource.colModelPlaceholder') }}
                    </code>
                  </button>

                  <code class="min-w-0 break-words font-mono text-[11px] text-gray-500 dark:text-dark-400">
                    {{ rowSummary(row) }}
                  </code>

                  <span
                    v-if="hasLongContext(row)"
                    class="shrink-0 rounded bg-primary-50 px-1.5 py-0.5 text-[10px] text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
                    :data-testid="`pricing-row-lc-badge-${row.id}`"
                  >
                    {{ t('admin.modelPricingSync.pricingSource.longContextBadge') }}
                  </span>

                  <button
                    v-if="row.persistedModel && config.revision !== undefined"
                    type="button"
                    class="btn btn-secondary btn-sm"
                    :disabled="saving || dirty || Boolean(conflictError)"
                    :title="dirty ? text('restoreCleanFirst') : text('restoreAuto')"
                    :data-testid="`pricing-restore-row-${row.id}`"
                    @click="restoreAuto(row.persistedModel)"
                  >
                    {{ text('restoreAuto') }}
                  </button>
                  <button
                    v-else
                    type="button"
                    class="btn btn-ghost btn-sm shrink-0 text-red-600 hover:text-red-700 dark:text-red-400"
                    :disabled="saving"
                    :title="t('admin.modelPricingSync.pricingSource.removeRow')"
                    :aria-label="t('admin.modelPricingSync.pricingSource.removeRow')"
                    :data-testid="`pricing-remove-row-${row.id}`"
                    @click="removeRow(row.id)"
                  >
                    <Icon name="trash" size="sm" />
                  </button>
                </div>

                <!-- 展开态：完整字段。 -->
                <fieldset v-show="row.expanded" :disabled="saving" class="mt-2 min-w-0">
                  <div class="grid gap-3 sm:grid-cols-12">
                    <label class="block sm:col-span-4">
                      <span class="text-[11px] font-medium text-gray-600 dark:text-dark-300">
                        {{ t('admin.modelPricingSync.pricingSource.colModel') }}
                      </span>
                      <input
                        v-model.trim="row.model"
                        :readonly="Boolean(row.persistedModel && config.revision !== undefined)"
                        type="text"
                        class="input mt-1 w-full font-mono text-sm"
                        :class="{ 'input-error': rowErrors[row.id]?.model }"
                        :placeholder="t('admin.modelPricingSync.pricingSource.colModelPlaceholder')"
                        :data-testid="`pricing-row-model-${row.id}`"
                      />
                      <span
                        v-if="rowErrors[row.id]?.model"
                        class="input-error-text mt-1 block"
                        :data-testid="`pricing-row-model-error-${row.id}`"
                      >
                        {{ rowErrors[row.id]?.model }}
                      </span>
                    </label>

                    <label class="block sm:col-span-4">
                      <span class="text-[11px] font-medium text-gray-600 dark:text-dark-300">{{ text('billingMode') }}</span>
                      <select v-model="row.mode" class="input mt-1 w-full text-sm" :data-testid="`pricing-row-mode-${row.id}`" @change="openExtraMenuId = null">
                        <option v-for="mode in BILLING_MODES" :key="mode" :value="mode">{{ text(`mode.${mode}`) }}</option>
                      </select>
                    </label>
                    <label class="flex items-center gap-2 text-xs text-gray-700 dark:text-gray-300 sm:col-span-4">
                      <input v-model="row.autoRestore" type="checkbox" class="rounded border-gray-300 text-primary-600 dark:border-dark-600 dark:bg-dark-800" :data-testid="`pricing-row-auto-restore-${row.id}`" />
                      {{ text('autoRestore') }}
                    </label>
                  </div>
                  <p class="mt-2 text-xs text-gray-500 dark:text-dark-400" :data-testid="`pricing-row-unit-${row.id}`">{{ siteUnit(row.mode) }}</p>
                  <div class="mt-1 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                    <label
                      v-for="field in priceFields(row.mode)"
                      :key="field"
                      class="block min-w-0"
                    >
                      <span class="text-[11px] font-medium text-gray-600 dark:text-dark-300">
                        {{ t(`admin.modelPricingSync.pricingSource.col.${field}`) }}
                      </span>
                      <input
                        v-model.trim="row[field]"
                        type="text"
                        inputmode="decimal"
                        class="input mt-1 w-full font-mono text-sm"
                        :placeholder="text(row.original?.[field] === null ? 'unknownRate' : 'unsetRate')"
                        :class="{ 'input-error': rowErrors[row.id]?.[field] }"
                        :data-testid="`pricing-row-${field}-${row.id}`"
                        @input="clearUnknown(row, field)"
                      />
                      <span
                        v-if="rowErrors[row.id]?.[field]"
                        class="input-error-text mt-1 block"
                        :data-testid="`pricing-row-${field}-error-${row.id}`"
                      >
                        {{ rowErrors[row.id]?.[field] }}
                      </span>
                    </label>

                    <div class="flex items-end">
                      <span
                        class="text-[11px] leading-tight text-primary-600 dark:text-primary-300"
                        data-testid="pricing-row-priority-note"
                      >
                        {{ row.autoRestore ? text('autoRestoreHint') : t('admin.modelPricingSync.pricingSource.manualPriorityNote') }}
                      </span>
                    </div>
                  </div>

                  <!-- 可选计费项：默认不显示，「添加计费项」目录按需添加，已添加的可删。 -->
                  <div v-if="row.mode === 'token'" class="mt-3" :data-testid="`pricing-row-extras-${row.id}`">
                    <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
                      <span class="text-[11px] font-medium text-gray-600 dark:text-dark-300">
                        {{ t('admin.modelPricingSync.pricingSource.extraTitle') }}
                      </span>
                      <span class="text-[11px] text-gray-400 dark:text-dark-500">
                        {{ t('admin.modelPricingSync.pricingSource.extraHint') }}
                      </span>
                      <div class="relative">
                        <button
                          type="button"
                          class="btn btn-ghost btn-sm gap-1 text-primary-600 hover:text-primary-700 dark:text-primary-300"
                          :data-testid="`pricing-row-add-extra-${row.id}`"
                          :aria-expanded="openExtraMenuId === row.id"
                          @click.stop="toggleExtraMenu(row.id)"
                        >
                          <Icon name="plus" size="xs" />
                          {{ t('admin.modelPricingSync.pricingSource.addExtra') }}
                        </button>
                        <!-- 目录：按分组列出，已添加的项置灰不可重复添加。 -->
                        <div
                          v-if="openExtraMenuId === row.id"
                          class="absolute left-0 z-20 mt-1 w-64 rounded-lg border border-gray-200 bg-white p-2 shadow-lg dark:border-dark-600 dark:bg-dark-800"
                          :data-testid="`pricing-row-extra-menu-${row.id}`"
                        >
                          <div
                            v-for="group in extraCatalogGroups"
                            :key="group.key"
                            class="mb-1 last:mb-0"
                          >
                            <div class="px-1 pb-1 text-[10px] font-semibold uppercase tracking-wide text-gray-400 dark:text-dark-500">
                              {{ t(`admin.modelPricingSync.pricingSource.extraGroup.${group.key}`) }}
                            </div>
                            <button
                              v-for="spec in group.fields"
                              :key="spec.key"
                              type="button"
                              class="flex w-full items-center justify-between gap-2 rounded px-1.5 py-1 text-left text-xs disabled:opacity-40"
                              :class="
                                hasExtra(row, spec.key)
                                  ? 'cursor-not-allowed text-gray-400 dark:text-dark-500'
                                  : 'text-gray-700 hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-dark-700'
                              "
                              :disabled="hasExtra(row, spec.key)"
                              :data-testid="`pricing-row-extra-option-${spec.key}-${row.id}`"
                              @click.stop="addExtra(row, spec.key)"
                            >
                              <span class="truncate">
                                {{ t(`admin.modelPricingSync.pricingSource.extra.${spec.key}`) }}
                              </span>
                              <span class="shrink-0 text-[10px] text-gray-400 dark:text-dark-500">
                                {{ extraUnitLabel(spec.unit) }}
                              </span>
                            </button>
                          </div>
                        </div>
                      </div>
                    </div>

                    <!-- 已添加的计费项：一行一个，右侧可删除。 -->
                    <div
                      v-if="row.extras.length > 0"
                      class="mt-1 grid gap-3 sm:grid-cols-12"
                      :data-testid="`pricing-row-extras-list-${row.id}`"
                    >
                      <div
                        v-for="key in row.extras"
                        :key="key"
                        class="sm:col-span-4"
                        :data-testid="`pricing-row-extra-field-${key}-${row.id}`"
                      >
                        <div class="flex items-baseline justify-between gap-1">
                          <span class="truncate text-[11px] font-medium text-gray-600 dark:text-dark-300">
                            {{ t(`admin.modelPricingSync.pricingSource.extra.${key}`) }}
                          </span>
                          <button
                            type="button"
                            class="shrink-0 text-red-500 hover:text-red-600 dark:text-red-400"
                            :title="t('admin.modelPricingSync.pricingSource.removeExtra')"
                            :aria-label="t('admin.modelPricingSync.pricingSource.removeExtra')"
                            :data-testid="`pricing-row-remove-extra-${key}-${row.id}`"
                            @click.stop="removeExtra(row, key)"
                          >
                            <Icon name="trash" size="xs" />
                          </button>
                        </div>
                        <input
                          v-model.trim="row[key]"
                          type="text"
                          inputmode="decimal"
                          class="input mt-1 w-full font-mono text-xs"
                          :class="{ 'input-error': rowErrors[row.id]?.[key] }"
                          :aria-label="t(`admin.modelPricingSync.pricingSource.extra.${key}`)"
                          :placeholder="row.original?.[key] === null ? text('unknownRate') : extraUnitLabel(extraUnitOf(key))"
                          :data-testid="`pricing-row-extra-input-${key}-${row.id}`"
                          @input="clearUnknown(row, key)"
                        />
                        <span
                          v-if="rowErrors[row.id]?.[key]"
                          class="input-error-text mt-1 block"
                          :data-testid="`pricing-row-extra-error-${key}-${row.id}`"
                        >
                          {{ rowErrors[row.id]?.[key] }}
                        </span>
                      </div>
                    </div>
                  </div>

                  <!-- 长上下文阶梯：留空即不启用，所以这三列共用一排、字号更小。 -->
                  <div v-if="row.mode === 'token'" class="mt-3" :data-testid="`pricing-row-long-context-${row.id}`">
                    <div class="flex flex-wrap items-baseline gap-x-2 gap-y-1">
                      <span class="text-[11px] font-medium text-gray-600 dark:text-dark-300">
                        {{ t('admin.modelPricingSync.pricingSource.longContextTitle') }}
                      </span>
                      <span class="text-[11px] text-gray-400 dark:text-dark-500">
                        {{ t('admin.modelPricingSync.pricingSource.longContextHint') }}
                      </span>
                    </div>
                    <div class="mt-1 grid gap-3 sm:grid-cols-12">
                      <label
                        v-for="field in LONG_CONTEXT_FIELDS"
                        :key="field"
                        class="block sm:col-span-3"
                      >
                        <span class="text-[11px] font-medium text-gray-600 dark:text-dark-300">
                          {{ t(`admin.modelPricingSync.pricingSource.col.${field}`) }}
                        </span>
                        <input
                          v-model.trim="row[field]"
                          type="text"
                          :inputmode="field === 'long_context_threshold' ? 'numeric' : 'decimal'"
                          class="input mt-1 w-full font-mono text-xs"
                          :class="{ 'input-error': rowErrors[row.id]?.[field] }"
                          :placeholder="t(`admin.modelPricingSync.pricingSource.colPlaceholder.${field}`)"
                          :data-testid="`pricing-row-${field}-${row.id}`"
                        />
                        <span
                          v-if="rowErrors[row.id]?.[field]"
                          class="input-error-text mt-1 block"
                          :data-testid="`pricing-row-${field}-error-${row.id}`"
                        >
                            {{ rowErrors[row.id]?.[field] }}
                        </span>
                      </label>
                    </div>
                  </div>
                </fieldset>
              </div>
            </div>
          </div>

          <!-- 国模厂商清单（只读折叠） -->
          <details class="mt-4 rounded-xl border border-gray-200 dark:border-dark-700">
            <summary class="cursor-pointer px-4 py-3 text-sm font-medium text-gray-800 dark:text-gray-200">
              {{ t('admin.modelPricingSync.pricingSource.vendorsTitle') }}
              <span class="ml-1 text-xs font-normal text-gray-500 dark:text-dark-400">
                {{ t('admin.modelPricingSync.pricingSource.vendorsHint', { count: cnVendors.length }) }}
              </span>
            </summary>
            <div class="flex flex-wrap gap-1.5 px-4 pb-4" data-testid="pricing-cn-vendors">
              <code
                v-for="vendor in cnVendors"
                :key="vendor"
                class="rounded-md bg-gray-100 px-1.5 py-0.5 font-mono text-[11px] text-gray-700 dark:bg-dark-700 dark:text-gray-300"
              >
                {{ vendor }}
              </code>
            </div>
          </details>

          <div v-if="conflictError" class="mt-4 rounded-lg border border-amber-300 p-3 text-sm text-amber-800 dark:border-amber-800 dark:text-amber-200" role="alert" data-testid="pricing-revision-conflict">
            <p>{{ text('revisionConflict') }}</p>
            <p class="break-words">{{ conflictError }}</p>
            <button type="button" class="btn btn-secondary btn-sm mt-2" data-testid="pricing-conflict-reload" @click="reloadConfig">
              {{ text('discardAndReload') }}
            </button>
          </div>
          <!-- 保存 -->
          <div class="mt-4 flex flex-wrap items-center justify-end gap-3">
            <p
              v-if="saveError"
              class="mr-auto break-all text-xs text-red-600 dark:text-red-400"
              data-testid="pricing-save-error"
              role="alert"
            >
              {{ saveError }}
            </p>
            <button
              type="button"
              class="btn btn-primary"
              data-testid="pricing-save"
              :disabled="saving || Boolean(conflictError)"
              @click="save"
            >
              <Icon name="check" size="sm" />
              {{ saving ? t('admin.modelPricingSync.pricingSource.saving') : t('admin.modelPricingSync.pricingSource.save') }}
            </button>
            <button v-if="dirty && !conflictError" type="button" class="btn btn-secondary" :disabled="saving" data-testid="pricing-discard-draft" @click="discardDraft">
              {{ text('discardDraft') }}
            </button>
          </div>
        </template>
      </section>

      <TablePageLayout>
        <!-- Filters -->
        <template #filters>
          <div class="card p-4 sm:p-6">
            <div class="flex flex-wrap items-end justify-between gap-4">
              <div class="flex flex-1 flex-wrap items-end gap-4">
                <div class="w-full sm:w-auto sm:min-w-[240px]">
                  <label class="input-label">{{ t('admin.modelPricingSync.filters.model') }}</label>
                  <div class="relative">
                    <Icon
                      name="search"
                      size="md"
                      class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-500"
                    />
                    <input
                      v-model.trim="filters.model"
                      type="text"
                      class="input pl-10"
                      :placeholder="t('admin.modelPricingSync.filters.modelPlaceholder')"
                      @keyup.enter="search"
                    />
                  </div>
                </div>

                <div class="w-full sm:w-auto sm:min-w-[200px]">
                  <label class="input-label">{{ t('admin.modelPricingSync.filters.field') }}</label>
                  <Select v-model="filters.field" :options="fieldOptions" @change="search" />
                </div>

                <div class="w-full sm:w-auto sm:min-w-[180px]">
                  <label class="input-label">{{ t('admin.modelPricingSync.filters.source') }}</label>
                  <Select v-model="filters.source" :options="sourceOptions" @change="search" />
                </div>
              </div>

              <div class="flex w-full flex-wrap items-center justify-end gap-3 sm:w-auto">
                <button type="button" class="btn btn-primary" :disabled="loading" @click="search">
                  {{ t('common.search') }}
                </button>
                <button type="button" class="btn btn-secondary" :disabled="loading" @click="resetFilters">
                  {{ t('common.reset') }}
                </button>
              </div>
            </div>

            <!-- Status strip -->
            <div
              class="mt-4 flex flex-wrap items-center gap-x-5 gap-y-2 rounded-xl bg-gray-50 px-4 py-3 text-xs text-gray-600 dark:bg-dark-900 dark:text-gray-300"
            >
              <span class="inline-flex items-center gap-1.5 font-medium">
                <span
                  class="h-1.5 w-1.5 rounded-full"
                  :class="status?.enabled ? 'bg-emerald-500' : 'bg-gray-400 dark:bg-dark-600'"
                ></span>
                {{ status?.enabled ? t('admin.modelPricingSync.status.enabled') : t('admin.modelPricingSync.status.disabled') }}
              </span>
              <span>
                {{ t('admin.modelPricingSync.status.lastRun') }}:
                <span class="font-mono">{{ status?.last_run ? formatTime(status.last_run) : t('admin.modelPricingSync.status.never') }}</span>
              </span>
              <span v-if="status?.interval_seconds">
                {{ t('admin.modelPricingSync.status.interval') }}: {{ formatInterval(status.interval_seconds) }}
              </span>
              <span v-if="typeof status?.last_applied === 'number' && status.last_applied > 0">
                {{ t('admin.modelPricingSync.status.lastApplied') }}:
                {{ t('admin.modelPricingSync.status.changes', { count: status.last_applied }) }}
              </span>
              <span v-if="status?.override_file" class="break-all font-mono text-gray-500 dark:text-gray-400">
                {{ t('admin.modelPricingSync.status.overrideFile') }}: {{ status.override_file }}
              </span>
              <span v-if="status?.last_error" class="break-all font-mono text-red-600 dark:text-red-400">
                {{ t('admin.modelPricingSync.status.lastError') }}: {{ status.last_error }}
              </span>
            </div>
            <p v-if="status && !status.enabled" class="mt-2 text-xs text-amber-600 dark:text-amber-400">
              {{ t('admin.modelPricingSync.disabledHint') }}
            </p>
          </div>
        </template>

        <!-- Table -->
        <template #table>
          <DataTable :columns="columns" :data="historyRows" :loading="loading" row-key="id">
            <template #cell-applied_at="{ value }">
              <span class="whitespace-nowrap text-gray-600 dark:text-gray-300">{{ formatTime(value) }}</span>
            </template>

            <template #cell-model="{ value }">
              <span class="break-all font-mono text-sm text-gray-800 dark:text-gray-200">{{ value }}</span>
            </template>

            <template #cell-field="{ value }">
              <span class="whitespace-nowrap text-gray-600 dark:text-gray-300">
                {{ fieldLabel(value) }}
              </span>
              <div class="mt-0.5 break-all font-mono text-xs text-gray-500">{{ value }}</div>
            </template>

            <template #cell-change="{ row }">
              <span class="inline-flex items-center gap-2 whitespace-nowrap font-mono text-sm">
                <span class="text-gray-500 dark:text-gray-400">{{ formatHistoryPrice(row.old_value, row.field) }}</span>
                <Icon name="arrowRight" size="sm" class="text-gray-400 dark:text-dark-500" />
                <span class="font-semibold text-gray-900 dark:text-white">{{ formatHistoryPrice(row.new_value, row.field) }}</span>
              </span>
            </template>

            <template #cell-reason="{ value }">
              <span class="text-gray-600 dark:text-gray-300">{{ value || '—' }}</span>
            </template>

            <template #cell-source="{ value }">
              <span
                class="inline-flex items-center rounded-full bg-primary-50 px-2.5 py-0.5 text-xs font-semibold text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
              >
                {{ value || '—' }}
              </span>
            </template>

            <template #empty>
              <div class="flex flex-col items-center py-8">
                <Icon name="chart" size="xl" class="mb-4 h-12 w-12 text-gray-300 dark:text-dark-600" />
                <p class="text-sm font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.modelPricingSync.empty') }}
                </p>
              </div>
            </template>
          </DataTable>
        </template>

        <!-- Pagination -->
        <template #pagination>
          <Pagination
            v-if="total > 0"
            :total="total"
            :page="page"
            :page-size="pageSize"
            @update:page="onPageChange"
            @update:pageSize="onPageSizeChange"
          />
        </template>
      </TablePageLayout>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  adminAPI,
  type ModelPricingSyncHistoryRow,
  type ModelPricingSyncStatus,
  type PricingCnyOfficialConfig,
  type PricingManualExtraFields,
  type PricingManualOverride
} from '@/api/admin'
import {
  BILLING_MODES, DEFAULT_CN_VENDORS, DEFAULT_CNY_SOURCE_URL, getRateUnit, MANUAL_EXTRA_FIELDS,
  MANUAL_RATE_KEYS, MANUAL_TOKEN_FIELDS, MANUAL_UNIT_FIELDS,
  type PricingBillingMode, type PricingCnyOfficialUpdate, type PricingQuoteRates
} from '@/api/admin/modelPricingSync'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import type { Column } from '@/components/common/types'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import PricingSyncQuotePanel from '@/components/admin/PricingSyncQuotePanel.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const text = (key: string, params?: Record<string, unknown>) => t(`admin.modelPricingSync.v2.${key}`, params ?? {})
const appStore = useAppStore()

const loading = ref(false)
const historyRows = ref<ModelPricingSyncHistoryRow[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const status = ref<ModelPricingSyncStatus | null>(null)

const filters = reactive({
  model: '',
  field: '',
  source: ''
})

const SYNC_FIELDS = Object.values(MANUAL_RATE_KEYS)

const columns = computed<Column[]>(() => [
  { key: 'applied_at', label: t('admin.modelPricingSync.columns.appliedAt') },
  { key: 'model', label: t('admin.modelPricingSync.columns.model') },
  { key: 'field', label: t('admin.modelPricingSync.columns.field') },
  { key: 'change', label: t('admin.modelPricingSync.columns.change') },
  { key: 'reason', label: t('admin.modelPricingSync.columns.reason') },
  { key: 'source', label: t('admin.modelPricingSync.columns.source') }
])

const fieldOptions = computed(() => [
  { value: '', label: t('admin.modelPricingSync.filters.allFields') },
  ...SYNC_FIELDS.map((field) => ({ value: field, label: fieldLabel(field) }))
])

const sourceOptions = computed(() => [
  { value: '', label: t('admin.modelPricingSync.filters.allSources') },
  { value: 'models.dev', label: 'models.dev' }
])

function fieldLabel(field: string): string {
  const key = `admin.modelPricingSync.fields.${field}`
  const label = t(key)
  // vue-i18n 找不到键时回退成键名本身，此时直接展示原始字段名更有用。
  if (label !== key) return label
  const extra = MANUAL_EXTRA_FIELDS.find(({ key }) => MANUAL_RATE_KEYS[key] === field)
  return extra ? t(`admin.modelPricingSync.pricingSource.extra.${extra.key}`) : field
}

function formatHistoryPrice(value: number | null | undefined, field: string): string {
  if (value === null) return text('unknownRate')
  if (value === undefined) return text('unsetRate')
  const unit = getRateUnit(field)
  const displayValue = unit === 'token' || unit === 'storage' ? value * 1e6 : value
  return unit ? `${Number(displayValue.toPrecision(15))} / ${text(`unit.${unit}`)}` : String(value)
}

function formatTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

function formatInterval(seconds: number): string {
  if (seconds % 3600 === 0) return `${seconds / 3600}h`
  if (seconds % 60 === 0) return `${seconds / 60}m`
  return `${seconds}s`
}

async function load() {
  loading.value = true
  try {
    const history = await adminAPI.modelPricingSync.listHistory({
        page: page.value,
        page_size: pageSize.value,
        model: filters.model || undefined,
        field: filters.field || undefined,
        source: filters.source || undefined
    })
    historyRows.value = history.items ?? []
    total.value = history.total ?? 0
  } catch (err) {
    appStore.showError(extractApiErrorMessage(err, t('admin.modelPricingSync.loadFailed')))
  } finally {
    loading.value = false
  }
}

/** 历史列表数据源；卡片里的手动覆盖行是另一份状态（overrideRows）。 */

function search() {
  page.value = 1
  void load()
}

function resetFilters() {
  filters.model = ''
  filters.field = ''
  filters.source = ''
  page.value = 1
  void load()
}

function onPageChange(next: number) {
  page.value = next
  void load()
}

function onPageSizeChange(next: number) {
  pageSize.value = next
  page.value = 1
  void load()
}

// ==================== 计价口径配置（pricing.cny_official） ====================

type PriceField = (typeof MANUAL_TOKEN_FIELDS)[number] | (typeof MANUAL_UNIT_FIELDS)[keyof typeof MANUAL_UNIT_FIELDS]

function priceFields(mode: PricingBillingMode): readonly PriceField[] {
  return mode === 'token' ? MANUAL_TOKEN_FIELDS : [MANUAL_UNIT_FIELDS[mode]]
}

function siteUnit(mode: PricingBillingMode) {
  return `${text('siteCurrency')} / ${text(`unit.${mode}`)}`
}

/**
 * 可选计费项按分组展示，顺序即目录顺序。
 *
 * 目录本身来自 `MANUAL_EXTRA_FIELDS`（与后端登记表一一对应），这里只负责分组。
 */
const EXTRA_GROUPS = ['priority', 'image', 'storage'] as const
type ExtraField = keyof PricingManualExtraFields & string

const extraCatalogGroups = computed(() =>
  EXTRA_GROUPS.map((key) => ({
    key,
    fields: MANUAL_EXTRA_FIELDS.filter((spec) => spec.group === key)
  })).filter((group) => group.fields.length > 0)
)

/** 该计费项在目录里的单位，用于占位提示。 */
function extraUnitOf(key: string): string {
  const spec = MANUAL_EXTRA_FIELDS.find((item) => item.key === key)
  return spec ? spec.unit : 'per_million_tokens'
}

function extraUnitLabel(unit: string): string {
  return t(`admin.modelPricingSync.pricingSource.extraUnit.${unit}`)
}

/**
 * 长上下文阶梯三列。与 `PRICE_FIELDS` 分开：这三项留空合法，价格列留空报错。
 * 顺序即界面顺序（阈值 → 输入倍率 → 输出倍率）。
 */
const LONG_CONTEXT_FIELDS = [
  'long_context_threshold',
  'long_context_input_multiplier',
  'long_context_output_multiplier'
] as const
type LongContextField = (typeof LONG_CONTEXT_FIELDS)[number]

/**
 * 编辑行。价格、可选计费项都用字符串存：`type="number"` 在清空时给空串，
 * 直接绑 number 会把 NaN 写进模型，保存时无从分辨「空」与「0」。
 *
 * `id` 只用于 v-for 的稳定 key 与错误定位 —— 模型键可被用户随便改，不能当 key。
 *
 * 索引签名只为让模板里 `row[spec.key]` 能双向绑定动态计费项；实际有值的键由
 * `extras` 约束（添加时初始化、删除时一并清掉）。
 */
type OverrideRow = Record<PriceField | LongContextField, string> & Partial<Record<ExtraField, string>> & {
  id: number
  model: string
  persistedModel?: string
  original?: PricingManualOverride
  mode: PricingBillingMode
  autoRestore: boolean
  /** 已添加的可选计费项（目录键），决定展开态里渲染哪些输入框。 */
  extras: ExtraField[]

  /** 行是否展开。默认收起 —— manual 一多，全展开会把卡片拉成几十屏。 */
  expanded: boolean

}

type RowErrors = { model?: string } & Partial<
  Record<PriceField | LongContextField | ExtraField, string>
>

const configLoading = ref(true)
const configError = ref('')
/** 专用配置端点还没在后端注册（404/405）。界面要如实说明，不能假装是加载失败。 */
const routeMissing = ref(false)
const config = ref<PricingCnyOfficialConfig | null>(null)
const overrideRows = ref<OverrideRow[]>([])
const rowErrors = ref<Record<number, RowErrors>>({})
const saving = ref(false)
const saveError = ref('')
const conflictError = ref('')
const statusError = ref('')
const statusLoading = ref(false)
const inspectedModel = ref('')
/** 保存成功后的基线快照，用来判断是否有未保存改动。 */
const savedSnapshot = ref('')
const savedEnabled = ref(true)

let nextRowId = 1
let statusRequest = 0

const priorityTiers = computed(() => [
  {
    key: 'usd',
    label: t('admin.modelPricingSync.pricingSource.tierUsd'),
    note: t('admin.modelPricingSync.pricingSource.tierUsdNote'),
    highest: false
  },
  {
    key: 'cny-official',
    label: t('admin.modelPricingSync.pricingSource.tierCny'),
    note: t('admin.modelPricingSync.pricingSource.tierCnyNote'),
    highest: false
  },
  {
    key: 'manual',
    label: t('admin.modelPricingSync.pricingSource.tierManual'),
    note: t('admin.modelPricingSync.pricingSource.tierManualNote'),
    highest: true
  }
])

/** USD 同步是后端既有任务，本卡片只读展示它是否在跑。 */
const usdEnabled = computed(() => status.value?.enabled === true)

/**
 * 国模厂商清单，优先级：
 *   1. 配置接口返回的 `cn_vendors`（后端写入时回传）
 *   2. `status.cny_official.national_vendor` —— 后端白名单的真实来源
 *   3. 内置清单（后端老版本没有该字段时的兜底，仅用于展示）
 *
 * 判定逻辑始终在后端，这里只是把「哪些厂商走 CNY 官方价」摆给运营看。
 */
const cnVendors = computed<string[]>(() => {
  const fromConfig = config.value?.cn_vendors
  if (fromConfig && fromConfig.length > 0) return fromConfig
  const fromStatus = status.value?.cny_official?.national_vendor
  if (fromStatus && fromStatus.length > 0) return fromStatus
  return [...DEFAULT_CN_VENDORS]
})

function makeRow(
  model = '',
  price?: PricingManualOverride
): OverrideRow {
  const optionalText = (value: number | null | undefined) => (value == null ? '' : String(value))
  const row: OverrideRow = {
    id: nextRowId++,
    model,
    persistedModel: price ? model : undefined,
    original: price,
    mode: price?.billing_mode ?? 'token',
    autoRestore: price?.auto_restore === true,
    input: optionalText(price?.input),
    output: optionalText(price?.output),
    cache_read: optionalText(price?.cache_read),
    cache_write: optionalText(price?.cache_write),
    cache_write_1h: optionalText(price?.cache_write_1h),
    cache_write_30m: optionalText(price?.cache_write_30m),
    cache_write_12h: optionalText(price?.cache_write_12h),
    per_request_price: optionalText(price?.per_request_price),
    per_second_price: optionalText(price?.per_second_price),
    image_output_per_image: optionalText(price?.image_output_per_image),
    extras: [],
    long_context_threshold: optionalText(price?.long_context_threshold),
    long_context_input_multiplier: optionalText(price?.long_context_input_multiplier),
    long_context_output_multiplier: optionalText(price?.long_context_output_multiplier),
    // 已有行默认收起；新增行由 addRow 展开（见那里），避免点「添加」后还得再点一次。
    expanded: false
  }
  if (price && row.mode === 'token') {
    for (const spec of MANUAL_EXTRA_FIELDS) {
      const value = price[spec.key]
      if (value !== undefined) {
        row.extras.push(spec.key)
        row[spec.key] = optionalText(value)
      }
    }
  }
  return row
}

function rowSummary(row: OverrideRow): string {
  if (row.mode !== 'token') return `${row[MANUAL_UNIT_FIELDS[row.mode]] || '—'} ${siteUnit(row.mode)}`
  const parts = MANUAL_TOKEN_FIELDS.slice(0, 4).map((field) => row[field] || '—')
  const summary = `${parts.join(' / ')} · ${siteUnit(row.mode)}`
  const extraCount = row.extras.filter((key) => String(row[key] ?? '').trim() !== '').length
  if (extraCount === 0) return summary
  return `${summary} +${extraCount}`
}

/** 收了长上下文阶梯才算有；只填阈值不填倍率等于没配，不亮标记。 */
function hasLongContext(row: OverrideRow): boolean {
  if (row.mode !== 'token' || !row.long_context_threshold) return false
  return Boolean(row.long_context_input_multiplier || row.long_context_output_multiplier)
}

function toggleRow(id: number) {
  const row = overrideRows.value.find((item) => item.id === id)
  if (row) row.expanded = !row.expanded
}

// ==================== 可选计费项（添加 / 删除） ====================

/**
 * 当前打开目录的行 id。同一时刻只开一个目录 —— 多个悬浮层叠在一起会互相遮挡，
 * 而且关掉一个再开另一个的交互本来就是「单选」语义。
 */
const openExtraMenuId = ref<number | null>(null)

function toggleExtraMenu(id: number) {
  openExtraMenuId.value = openExtraMenuId.value === id ? null : id
}

/** 该行是否已添加某计费项。已添加的在目录里置灰，避免重复添加。 */
function hasExtra(row: OverrideRow, key: ExtraField): boolean {
  return row.extras.includes(key)
}

/** 添加计费项：插到 extras 末尾并把值初始化成空串，等用户填。 */
function addExtra(row: OverrideRow, key: ExtraField) {
  if (row.extras.includes(key)) return
  row.extras.push(key)
  row[key] = ''
  openExtraMenuId.value = null
}

/**
 * 删除已添加的计费项：从 extras 移除并清掉值。
 *
 * 清值很关键 —— 同步是按「值 > 0 才写覆盖层」判定的，留着值只从 extras 移除会让
 * 页面上看不见、覆盖层里仍有旧价。后端另有 cnyOfficialManualPruneFields 兜底删除
 * 已写入的旧值。
 */
function removeExtra(row: OverrideRow, key: ExtraField) {
  row.extras = row.extras.filter((item) => item !== key)
  row[key] = ''
  clearUnknown(row, key)
  const errors = { ...rowErrors.value }
  if (errors[row.id]) {
    const next = { ...errors[row.id] }
    delete next[key]
    if (Object.keys(next).length > 0) {
      errors[row.id] = next
    } else {
      delete errors[row.id]
    }
    rowErrors.value = errors
  }
}

/** 展开所有行 / 收起所有行 —— manual 多的时候省得一个个点。 */
function toggleAllRows(expand: boolean) {
  for (const row of overrideRows.value) row.expanded = expand
}

function clearUnknown(row: OverrideRow, key: PriceField | ExtraField) {
  if (row.original?.[key] === null) row.original = { ...row.original, [key]: undefined }
}

function applyConfig(next: PricingCnyOfficialConfig) {
  config.value = next
  savedEnabled.value = next.enabled
  overrideRows.value = Object.entries(next.manual_overrides)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([model, price]) => makeRow(model, price))
  rowErrors.value = {}
  saveError.value = ''
  conflictError.value = ''
  openExtraMenuId.value = null
  savedSnapshot.value = snapshot()
}

function addRow(model = '', mode: PricingBillingMode = 'token') {
  if (saving.value) return
  const row = makeRow(model)
  row.mode = mode
  // 新增行直接展开：刚点完「添加」就是要填，再点一次展开是多余的。
  row.expanded = true
  overrideRows.value = [...overrideRows.value, row]
  // 新行默认聚焦到模型键，省一次点击。
  void nextTickFocus(`[data-testid="pricing-row-${model ? 'mode' : 'model'}-${row.id}"]`)
}

function nextTickFocus(selector: string) {
  return nextTick().then(() => {
    const el = document.querySelector<HTMLInputElement>(selector)
    el?.focus()
  })
}

function removeRow(id: number) {
  overrideRows.value = overrideRows.value.filter((row) => row.id !== id)
  const next = { ...rowErrors.value }
  delete next[id]
  rowErrors.value = next
}

/** 价格必须是有限非负数；空值单列一条，免得空输入被当成 0 静默落库。 */
function priceError(raw: string): string | undefined {
  const text = raw.trim()
  if (text === '') return t('admin.modelPricingSync.pricingSource.errPriceRequired')
  const parsed = Number(text)
  if (!Number.isFinite(parsed)) return t('admin.modelPricingSync.pricingSource.errPriceInvalid')
  if (parsed < 0) return t('admin.modelPricingSync.pricingSource.errPriceInvalid')
  return undefined
}

/** 阈值：留空合法；填了必须是 ≥0 的整数。 */
function thresholdError(raw: string): string | undefined {
  const text = raw.trim()
  if (text === '') return undefined
  const parsed = Number(text)
  if (!Number.isFinite(parsed)) return t('admin.modelPricingSync.pricingSource.errThresholdInvalid')
  if (!Number.isInteger(parsed) || parsed < 0) {
    return t('admin.modelPricingSync.pricingSource.errThresholdInvalid')
  }
  return undefined
}

/** 倍率：留空合法；填了必须是 ≥1 的有限数。 */
function multiplierError(raw: string): string | undefined {
  const text = raw.trim()
  if (text === '') return undefined
  const parsed = Number(text)
  if (!Number.isFinite(parsed)) return t('admin.modelPricingSync.pricingSource.errMultiplierInvalid')
  if (parsed < 1) return t('admin.modelPricingSync.pricingSource.errMultiplierInvalid')
  return undefined
}

/**
 * 校验单行的三个长上下文字段。
 *
 * 单独抽出来是因为有一条跨字段规则：倍率填了但阈值留空 → 倍率没有阈值不生效，
 * 必须拦。阈值为 0 时也确实不启用阶梯，所以填了倍率同样算错。
 */
function longContextErrors(row: OverrideRow): Partial<Record<LongContextField, string>> {
  const errors: Partial<Record<LongContextField, string>> = {}

  const thresholdMessage = thresholdError(row.long_context_threshold)
  if (thresholdMessage) errors.long_context_threshold = thresholdMessage

  const inputMessage = multiplierError(row.long_context_input_multiplier)
  if (inputMessage) errors.long_context_input_multiplier = inputMessage

  const outputMessage = multiplierError(row.long_context_output_multiplier)
  if (outputMessage) errors.long_context_output_multiplier = outputMessage

  // 阈值本身已经有错时不再叠加「缺阈值」，那只是同一个问题的两种说法。
  if (errors.long_context_threshold) return errors

  const thresholdText = row.long_context_threshold.trim()
  const threshold = thresholdText === '' ? 0 : Number(thresholdText)
  const hasMultiplier =
    row.long_context_input_multiplier.trim() !== '' || row.long_context_output_multiplier.trim() !== ''
  if (hasMultiplier && threshold <= 0) {
    errors.long_context_threshold = t('admin.modelPricingSync.pricingSource.errThresholdRequiredForMultiplier')
  }

  return errors
}

/** 校验全部行，返回 id → 错误。纯函数，不改组件状态。 */
function collectErrors(): Record<number, RowErrors> {
  const errors: Record<number, RowErrors> = {}
  const seen = new Map<string, number>()

  for (const row of overrideRows.value) {
    const rowError: RowErrors = {}
    const model = row.model.trim()

    if (model === '') {
      rowError.model = t('admin.modelPricingSync.pricingSource.errModelRequired')
    } else if (seen.has(model)) {
      rowError.model = t('admin.modelPricingSync.pricingSource.errModelDuplicate')
    } else {
      seen.set(model, row.id)
    }

    for (const field of priceFields(row.mode)) {
      const required = row.mode !== 'token' || field === 'input' || field === 'output'
      if (!required && row[field].trim() === '') continue
      const message = priceError(row[field])
      if (message) rowError[field] = message
    }

    // 已添加的可选计费项：留空合法（等价于不配置），填了必须是有限非负数。
    for (const key of row.mode === 'token' ? row.extras : []) {
      const raw = String(row[key] ?? '').trim()
      if (raw === '') continue
      const parsed = Number(raw)
      if (!Number.isFinite(parsed) || parsed < 0) {
        rowError[key] = t('admin.modelPricingSync.pricingSource.errPriceInvalid')
      }
    }

    if (row.mode === 'token') Object.assign(rowError, longContextErrors(row))

    if (Object.keys(rowError).length > 0) errors[row.id] = rowError
  }

  return errors
}

/** 把校验结果落到界面。只在保存时调用，不在 computed 里做。 */
function validate(): Record<number, RowErrors> {
  const errors = collectErrors()
  rowErrors.value = errors
  return errors
}

/**
 * 行 → 提交用的 override 条目。
 *
 * 长上下文字段留空就整键省略（不是写 0）：后端带 `omitempty`，省略才是
 * 「不启用阶梯」。快照与真实提交共用这一个函数，dirty 判断才不会与载荷漂移。
 */
function rowToOverride(row: OverrideRow): PricingManualOverride {
  const override: PricingManualOverride = {}
  if (row.mode !== 'token' || row.original?.billing_mode !== undefined) override.billing_mode = row.mode
  if (row.autoRestore || row.original?.auto_restore !== undefined) override.auto_restore = row.autoRestore
  const fields = row.mode === 'token' ? [...priceFields(row.mode), ...row.extras] : priceFields(row.mode)
  for (const field of fields) {
    const raw = row[field]?.trim()
    if (raw) override[field] = Number(raw)
    else if (row.original?.[field] === null) override[field] = null
  }
  if (row.mode === 'token') {
    for (const field of LONG_CONTEXT_FIELDS) {
      if (row[field].trim()) override[field] = Number(row[field])
    }
  }
  return override
}

/** 稳定的载荷快照，字段顺序固定，供 dirty 比较与测试断言。 */
function snapshot(): string {
  if (!config.value) return ''
  return JSON.stringify({
    enabled: config.value.enabled,
    rows: overrideRows.value.map((row) => [row.model, rowToOverride(row)])
  })
}

const dirty = computed(() => config.value !== null && snapshot() !== savedSnapshot.value)

const pricingStatus = computed(() => status.value?.pricing_v2)
const sourceErrors = computed(() => Object.entries(pricingStatus.value?.source_errors ?? {}))
const activeRevision = computed(() => pricingStatus.value?.active_revision ?? config.value?.active_revision)
const activationPending = computed(() => Boolean(config.value?.revision && activeRevision.value !== config.value.revision))
// The admin can only verify one process from here. Never render a single
// process's revision as if every node had loaded it.
const pricingScope = computed(() => {
  const v = pricingStatus.value
  if (!v || v.scope !== 'process') return null
  if (v.converged) return { ok: true, text: text('scopeConverged', { revision: v.active_revision }) }
  if (v.head_error) return { ok: false, text: text('scopeUnverified', { reason: v.head_error }) }
  if (v.stale) return { ok: false, text: text('scopeUnverified', { reason: text('scopeStale') }) }
  const age = typeof v.head_age_seconds === 'number' ? Math.round(v.head_age_seconds) : '—'
  return { ok: false, text: text('scopeLagging', { head: v.head_revision ?? '—', active: v.active_revision || '—', age }) }
})
const inspectModels = computed(() => [...new Set([
  ...Object.keys(config.value?.manual_overrides ?? {}), ...Object.keys(pricingStatus.value?.models ?? {})
])].sort((a, b) => a.localeCompare(b)))
const selectedModel = computed(() => inspectedModel.value || inspectModels.value[0] || '')
const selectedStatus = computed(() => {
  const models = pricingStatus.value?.models
  return models && Object.prototype.hasOwnProperty.call(models, selectedModel.value) ? models[selectedModel.value] : undefined
})
const selectedManual = computed(() => {
  const manual = config.value?.manual_overrides
  return manual && Object.prototype.hasOwnProperty.call(manual, selectedModel.value) ? manual[selectedModel.value] : undefined
})
const selectedIssue = computed(() => selectedStatus.value?.issue || (!selectedStatus.value?.auto ? text('noVerifiedQuote') : ''))
const quotePanels = computed(() => {
  const manual = selectedManual.value
  const modelStatus = selectedStatus.value
  const rates: PricingQuoteRates = {}
  if (manual) {
    const mode = manual.billing_mode ?? 'token'
    const fields = mode === 'token' ? [...MANUAL_TOKEN_FIELDS, ...MANUAL_EXTRA_FIELDS.map(({ key }) => key)] : [MANUAL_UNIT_FIELDS[mode]]
    for (const field of fields) {
      const value = manual[field]
      if (value !== undefined) rates[MANUAL_RATE_KEYS[field]] = value
    }
  }
  return [
    { kind: 'auto', quote: modelStatus?.auto },
    { kind: 'manual', quote: manual ? { currency: 'SITE' as const, billing_mode: manual.billing_mode ?? 'token', rates, source_url: 'settings:pricing.cny_official' } : undefined, manualPolicy: manual ? (manual.auto_restore ? 'autoRestore' : 'manualPinned') : undefined, manualUnits: true },
    { kind: 'effective', quote: modelStatus?.effective, effectiveMode: modelStatus?.mode, revision: pricingStatus.value?.active_revision }
  ] as const
})

function editSelectedModel() {
  if (saving.value || !selectedModel.value || selectedStatus.value?.external) return
  const row = overrideRows.value.find((item) => item.model === selectedModel.value)
  if (row) {
    row.expanded = true
    void nextTickFocus(`[data-testid="pricing-row-mode-${row.id}"]`)
  } else {
    addRow(selectedModel.value, selectedStatus.value?.auto?.billing_mode ?? selectedStatus.value?.effective?.billing_mode)
  }
}

function discardDraft() {
  if (config.value && !saving.value) applyConfig({ ...config.value, enabled: savedEnabled.value })
}

async function loadStatus() {
  const request = ++statusRequest
  statusLoading.value = true
  try {
    const next = await adminAPI.modelPricingSync.getStatus()
    if (request !== statusRequest) return
    status.value = next
    statusError.value = ''
  } catch (err) {
    if (request !== statusRequest) return
    statusError.value = extractApiErrorMessage(err, t('admin.modelPricingSync.statusLoadFailed'))
  } finally {
    if (request === statusRequest) statusLoading.value = false
  }
}

const OPEN_API_PATH = '/api/v1/admin/model-pricing-sync/cny-official'

function isRouteMissing(err: unknown): boolean {
  const status = (err as { status?: number } | null)?.status
  // 404/405 只可能是路由没注册：settings 服务本身的故障会走 500 分支。
  return status === 404 || status === 405
}

async function loadConfig() {
  configLoading.value = true
  configError.value = ''
  routeMissing.value = false
  try {
    const next = await adminAPI.modelPricingSync.getCnyOfficialConfig()
    applyConfig(next)
  } catch (err) {
    routeMissing.value = isRouteMissing(err)
    configError.value = routeMissing.value
      // 端点没注册时把路径写进界面：这是并行后端 lane 尚未落地的信号，
      // 报「加载失败」会让人以为是自己配坏了。
      ? t('admin.modelPricingSync.pricingSource.endpointMissing', { path: OPEN_API_PATH })
      : extractApiErrorMessage(err, t('admin.modelPricingSync.pricingSource.loadFailed'))
    config.value = null
    overrideRows.value = []
  } finally {
    configLoading.value = false
  }
}

async function reloadConfig() {
  if (saving.value) return
  saving.value = true
  try {
    applyConfig(await adminAPI.modelPricingSync.getCnyOfficialConfig())
    await loadStatus()
  } catch (err) {
    saveError.value = extractApiErrorMessage(err, t('admin.modelPricingSync.pricingSource.loadFailed'))
  } finally {
    saving.value = false
  }
}

async function refreshPricing() {
  if (dirty.value || conflictError.value) await loadStatus()
  else await reloadConfig()
}

function makeUpdate(current: PricingCnyOfficialConfig, manual: Record<string, PricingManualOverride>): PricingCnyOfficialUpdate {
  return {
    enabled: current.enabled,
    source_url: current.source_url || DEFAULT_CNY_SOURCE_URL,
    manual_overrides: manual,
    ...(current.revision !== undefined ? { revision: current.revision } : {})
  }
}

async function restoreAuto(model: string) {
  if (!config.value || config.value.revision === undefined || saving.value || dirty.value || conflictError.value) return
  const manual = { ...config.value.manual_overrides }
  delete manual[model]
  inspectedModel.value = model
  await submit(makeUpdate(config.value, manual), true)
}

async function save() {
  if (!config.value || saving.value || conflictError.value) return
  saveError.value = ''

  const errors = validate()
  if (Object.keys(errors).length > 0) {
    for (const row of overrideRows.value) if (errors[row.id]) row.expanded = true
    appStore.showError(t('admin.modelPricingSync.pricingSource.errFixFirst'))
    return
  }

  const manualOverrides = Object.fromEntries(overrideRows.value.map((row) => [row.model.trim(), rowToOverride(row)]))
  await submit(makeUpdate(config.value, manualOverrides), false)
}

async function submit(payload: PricingCnyOfficialUpdate, restoring: boolean) {
  saveError.value = ''
  saving.value = true
  try {
    const next = await adminAPI.modelPricingSync.updateCnyOfficialConfig(payload)
    if (status.value?.pricing_v2?.active_revision !== next.active_revision) {
      status.value = status.value ? { ...status.value, pricing_v2: undefined } : null
    }
    applyConfig(next)
    appStore.showSuccess(activationPending.value ? text('activationPending') : t('admin.modelPricingSync.pricingSource.saved'))
    await loadStatus()
  } catch (err) {
    const message = extractApiErrorMessage(err, restoring ? text('restoreFailed') : t('admin.modelPricingSync.pricingSource.saveFailed'))
    const code = extractApiErrorCode(err)
    // The restore endpoint also uses HTTP 409 for quote validation failures.
    const legacyConflict = '价格修订已变更，请重新读取后保存'
    const versionConflict = code === 'PRICE_SYNC_REVISION_CONFLICT'
      || message === legacyConflict
      || /^revision (?:changed|mismatch|conflict|stale)/i.test(message)
      || (!restoring && (err as { status?: number } | null)?.status === 409 && !message.startsWith(`${legacyConflict}:`))
    if (versionConflict) conflictError.value = message
    else saveError.value = message.replace(`${legacyConflict}: `, '')
    appStore.showError(message)
  } finally {
    saving.value = false
  }
}

onMounted(async () => {
  void load()
  await loadConfig()
  await loadStatus()
})
</script>
