<template>
  <AppLayout><div class="space-y-5">
    <AdminPageHeader :title="t('admin.accountOps.title')" :description="t('admin.accountOps.description')">
      <template #actions><RouterLink to="/admin/upstream-monitor" class="btn btn-secondary">{{ t('admin.accountOps.upstreamMonitor') }}</RouterLink><button class="btn btn-secondary" :disabled="saving||!!pending" @click="load">{{ t('admin.accountOps.refresh') }}</button></template>
    </AdminPageHeader>
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950 dark:text-red-300">{{ error }}</p>
    <p v-if="saved" role="status" class="text-sm text-emerald-600">{{ t('admin.accountOps.saved') }}</p>
    <p v-if="loading" role="status" class="p-3 text-sm text-gray-500">{{ t('admin.accountOps.loading') }}</p>
    <template v-if="remote">
    <fieldset :disabled="saving || loading || !!pending" class="min-w-0 space-y-5">
    <form class="card space-y-4 p-5" @submit.prevent="saveSettings">
      <h2 class="text-base font-semibold">{{ t('admin.accountOps.runtimeAndNotificationSettings') }}</h2>
      <div class="grid gap-4 md:grid-cols-3">
        <label class="flex items-center gap-2"><input v-model="draft.enabled" data-testid="enabled" type="checkbox">{{ t('admin.accountOps.enableModule') }}</label>
        <label class="flex items-center gap-2"><input v-model="draft.monitor_enabled" type="checkbox">{{ t('admin.accountOps.sampleHistoryAndUpstreamAnomalies') }}</label>
        <label class="block text-sm">{{ t('admin.accountOps.historyIntervalMinutes') }}<input v-model.number="draft.sample_interval_minutes" class="input mt-1 w-full" type="number" min="1" max="60" required></label>
        <label class="block text-sm">{{ t('admin.accountOps.recipientSystemSMTP') }}<input v-model="draft.recipient" data-testid="recipient" class="input mt-1 w-full" type="email" autocomplete="off"></label>
        <label class="block text-sm">{{ t('admin.accountOps.emailLabel') }}<input v-model="draft.email_name" class="input mt-1 w-full" maxlength="80"></label>
        <label class="block text-sm">{{ t('admin.accountOps.signalCooldownMinutes') }}<input v-model.number="draft.cooldown_minutes" class="input mt-1 w-full" type="number" min="5" max="1440" required></label>
        <label class="flex items-center gap-2"><input v-model="draft.balance_low" type="checkbox">{{ t('admin.accountOps.forwardInsufficientBalanceSignals') }}</label>
        <label class="flex items-center gap-2"><input v-model="draft.weekly_quota" type="checkbox">{{ t('admin.accountOps.forwardExhaustedWeeklyQuotaSignals') }}</label>
      </div>
      <p class="text-xs text-gray-500">{{ t('admin.accountOps.disableHint') }}</p>
      <p class="text-xs text-gray-500">SMTP: {{ remote.smtp_configured?t('admin.accountOps.configured'):t('admin.accountOps.notConfigured') }} · {{ t('admin.accountOps.storageFailures') }} {{ remote.storage_failures }} · {{ t('admin.accountOps.droppedSignals') }} {{ remote.dropped_signals }}</p>
      <button data-testid="save-settings" class="btn btn-primary" :disabled="saving" type="submit">{{ saving?t('admin.accountOps.saving'):t('admin.accountOps.saveRuntimeSettings') }}</button>
      <button data-testid="save-config" class="btn btn-secondary ml-2" type="button" @click="saveConfig">{{ t('admin.accountOps.saveCompleteConfiguration') }}</button>
      <p class="text-xs text-gray-500">{{ t('admin.accountOps.completeSaveHint') }}</p>
    </form>
    <form v-if="probeSettings" class="card space-y-3 p-5" @submit.prevent="saveProbeSettings">
      <h2 class="text-base font-semibold">{{ t('admin.accountOps.sharedUpstreamProbe') }}</h2>
      <p class="text-xs text-gray-500">{{ t('admin.accountOps.sharedProbeHint') }}</p>
      <div class="flex flex-wrap items-center gap-4"><label><input v-model="probeSettings.enabled" type="checkbox"> {{ t('admin.accountOps.probeSchedulingEnabled') }}</label><label>{{ t('admin.accountOps.readIntervalMinutes') }} <input v-model.number="probeSettings.interval_minutes" class="input w-24" type="number" min="1" max="1440" required></label><button class="btn btn-secondary" :disabled="saving" type="submit">{{ t('admin.accountOps.saveProbeSettings') }}</button></div>
    </form>
    <section class="card space-y-4 p-5">
      <h2 class="text-base font-semibold">{{ t('admin.accountOps.notificationChannels') }}</h2>
      <p class="text-xs text-gray-500">{{ t('admin.accountOps.channelSecurityHint') }}</p>
      <ul class="divide-y dark:divide-dark-700"><li v-for="hook in remote.config.webhooks||[]" :key="hook.id" class="flex flex-wrap items-center gap-2 py-2 text-sm"><span class="mr-auto">{{ hook.name||hook.id }} · {{ hook.provider }} · {{ hook.enabled?t('admin.accountOps.enabled'):t('admin.accountOps.disabled') }} · {{ hook.url_configured?t('admin.accountOps.urlConfigured'):t('admin.accountOps.noURL') }}</span><button class="btn btn-secondary" :disabled="saving" @click="editHook(hook)">{{ t('admin.accountOps.edit') }}</button><button class="btn btn-secondary" :disabled="saving" @click="testHook(hook.id)">{{ t('admin.accountOps.test') }}</button><button class="btn btn-secondary" :disabled="saving" @click="removeHook(hook.id)">{{ t('admin.accountOps.delete') }}</button></li></ul>
      <p v-if="!remote.encryption_key_configured" class="text-sm text-amber-700">{{ t('admin.accountOps.encryptionRequired') }}</p>
      <form class="grid gap-3 md:grid-cols-3" @submit.prevent="saveHook">
        <label class="text-sm">ID<input v-model="hookDraft.id" class="input mt-1 w-full" pattern="[A-Za-z0-9_-]{1,64}" required></label>
        <label class="text-sm">{{ t('admin.accountOps.name') }}<input v-model="hookDraft.name" class="input mt-1 w-full" maxlength="80"></label>
        <label class="text-sm">{{ t('admin.accountOps.provider') }}<select v-model="hookDraft.provider" class="input mt-1 w-full"><option v-for="p in ['custom','bark','wecom','dingtalk','feishu']" :key="p" :value="p">{{ p }}</option></select></label>
        <label class="text-sm md:col-span-2">{{ t('admin.accountOps.webhookUrl') }}<input v-model="hookDraft.url" class="input mt-1 w-full" type="password" autocomplete="new-password"></label>
        <label class="text-sm">{{ t('admin.accountOps.signingSecret') }}<input v-model="hookDraft.secret" class="input mt-1 w-full" type="password" autocomplete="new-password"></label>
        <label class="flex items-center gap-2 text-sm"><input v-model="hookDraft.enabled" type="checkbox">{{ t('admin.accountOps.enableChannel') }}</label><label class="flex items-center gap-2 text-sm"><input v-model="hookDraft.clear_secret" type="checkbox">{{ t('admin.accountOps.clearSigningSecret') }}</label>
        <label class="text-sm md:col-span-3">{{ t('admin.accountOps.customMessageTemplateOptional') }}<textarea v-model="hookDraft.message_template" class="input mt-1 w-full" rows="2" /></label>
        <button class="btn btn-secondary" type="submit" :disabled="saving||!remote.encryption_key_configured">{{ t('admin.accountOps.saveChannel') }}</button>
      </form>
    </section>
    <section class="card space-y-4 p-5" data-testid="group-section">
      <h2 class="text-base font-semibold">{{ t('admin.accountOps.groupMapping') }}</h2>
      <p class="text-xs text-gray-500">{{ t('admin.accountOps.groupMappingHint') }}</p>
      <p v-if="!groupDrafts.length" class="text-sm text-gray-500">{{ t('admin.accountOps.noManualGroups') }}</p>
      <form data-testid="groups-form" class="space-y-4" @submit.prevent="saveGroups">
        <div v-for="(group, index) in groupDrafts" :key="group.id" class="grid gap-3 rounded-lg border p-3 md:grid-cols-2 dark:border-dark-700">
          <label class="text-sm">{{ t('admin.accountOps.name') }}<input v-model="group.name" :data-testid="`group-name-${index}`" class="input mt-1 w-full" maxlength="80"><small class="block text-gray-500">{{ group.id }}</small></label>
          <label class="text-sm">{{ t('admin.accountOps.provider') }}<input v-model="group.provider" :data-testid="`group-provider-${index}`" class="input mt-1 w-full"></label>
          <label class="text-sm">{{ t('admin.accountOps.siteOptional') }}<input v-model="group.site" :data-testid="`group-site-${index}`" class="input mt-1 w-full" type="url"></label>
          <label class="text-sm">{{ t('admin.accountOps.memberAccountsMultiple') }}<select v-model="group.account_ids" :data-testid="`group-accounts-${index}`" class="input mt-1 w-full" multiple><option v-for="a in accounts" :key="a.account_id" :value="a.account_id">{{ a.account_name }} #{{ a.account_id }}</option><option v-for="id in missingMembers(group.account_ids)" :key="id" :value="id">#{{ id }} · {{ t('admin.accountOps.missingAccount') }}</option></select></label>
          <button class="btn btn-secondary justify-self-start" type="button" :data-testid="`remove-group-${index}`" @click="groupDrafts.splice(index,1)">{{ t('admin.accountOps.removeMapping') }}</button>
        </div>
        <div class="flex flex-wrap gap-2"><button data-testid="add-group" class="btn btn-secondary" type="button" :disabled="groupDrafts.length>=200" @click="addGroup">{{ t('admin.accountOps.addGroup') }}</button><button data-testid="save-groups" class="btn btn-primary" type="submit">{{ t('admin.accountOps.saveGroupMapping') }}</button></div>
      </form>
      <h3 class="text-sm font-semibold">{{ t('admin.accountOps.currentEffectiveGroups') }}</h3>
      <ul class="text-sm text-gray-500"><li v-for="g in groups" :key="g.id">{{ g.name || g.default_name }} · {{ g.provider }} · {{ g.site }} · {{ g.account_ids.join(', ') }}</li></ul>
      <p v-if="!groups.length" class="text-sm text-gray-500">{{ t('admin.accountOps.noGroupsOrAccounts') }}</p>
    </section>
    <section class="card space-y-4 p-5">
      <h2 class="text-base font-semibold">{{ t('admin.accountOps.batchThresholdRules') }}</h2>
      <p class="text-xs text-gray-500">{{ t('admin.accountOps.batchRulesHint') }}</p>
      <p v-if="!accounts.length" class="text-sm text-gray-500">{{ t('admin.accountOps.noSupportedAccounts') }}</p>
      <form data-testid="batch-form" class="space-y-4" @submit.prevent="saveBatch">
        <fieldset v-for="(b,index) in batches" :key="b.key" class="grid gap-3 rounded-lg border p-3 md:grid-cols-3 dark:border-dark-700">
          <legend class="px-1 text-sm font-semibold">{{ t('admin.accountOps.ruleGroup') }} {{ index+1 }}</legend>
          <label class="text-sm">{{ t('admin.accountOps.selectFromEffectiveGroups') }}<select v-model="b.group_id" :data-testid="`batch-group-${index}`" class="input mt-1 w-full" @change="selectBatchGroup(b)"><option value="">{{ t('admin.accountOps.selectAccountsManually') }}</option><option v-for="g in groups" :key="g.id" :value="g.id">{{ g.name || g.default_name }} ({{ g.account_ids.length }})</option></select></label>
          <label class="text-sm md:col-span-2">{{ t('admin.accountOps.memberAccountsMultiple') }}<select v-model="b.account_ids" :data-testid="`batch-accounts-${index}`" class="input mt-1 w-full" multiple required @change="b.group_id='' "><option v-for="a in accounts" :key="a.account_id" :value="a.account_id">{{ a.account_name }} #{{ a.account_id }} · {{ a.unit || '—' }} · {{ a.type }}</option><option v-for="id in missingMembers(b.account_ids)" :key="id" :value="id">#{{ id }} · {{ t('admin.accountOps.missingAccount') }}</option></select></label>
          <label class="text-sm">{{ t('admin.accountOps.metric') }}<select v-model="b.metric" :data-testid="`batch-metric-${index}`" class="input mt-1 w-full"><option value="balance">{{ t('admin.accountOps.balanceAtOrBelow') }}</option><option value="quota">{{ t('admin.accountOps.usedPercentageReaches') }}</option></select></label>
          <label class="flex items-center gap-2 text-sm"><input v-model="b.enabled_only" :data-testid="`batch-enabled-only-${index}`" type="checkbox">{{ t('admin.accountOps.enabledOnly') }}</label>
          <label class="flex items-center gap-2 text-sm"><input v-model="b.enabled" :data-testid="`batch-enabled-${index}`" type="checkbox">{{ t('admin.accountOps.enabled') }}</label>
          <template v-if="!b.enabled_only">
            <label class="text-sm">{{ t('admin.accountOps.threshold') }}<input v-model.number="b.value" :data-testid="`batch-value-${index}`" class="input mt-1 w-full" type="number" step="any" required min="0" :max="b.metric==='balance'?1e12:100"></label>
            <label v-if="b.metric==='balance'" class="text-sm">{{ t('admin.accountOps.currencyUnit') }}<input v-model="b.unit" :data-testid="`batch-unit-${index}`" class="input mt-1 w-full" maxlength="32" required></label>
            <label v-else class="text-sm">{{ t('admin.accountOps.window') }}<input v-model="b.window" :data-testid="`batch-window-${index}`" class="input mt-1 w-full" placeholder="any / 5h / 7d" maxlength="128" required></label>
            <div class="space-y-2 text-sm"><label class="flex items-center gap-2"><input v-model="b.notify_alert" :data-testid="`batch-alert-${index}`" type="checkbox">{{ t('admin.accountOps.alertNotification') }}</label><label class="flex items-center gap-2"><input v-model="b.notify_recovery" :data-testid="`batch-recovery-${index}`" type="checkbox">{{ t('admin.accountOps.recoveryNotification') }}</label></div>
          </template>
          <button v-if="batches.length>1" class="btn btn-secondary justify-self-start" type="button" :data-testid="`remove-batch-${index}`" @click="batches.splice(index,1)">{{ t('admin.accountOps.removeRuleGroup') }}</button>
        </fieldset>
        <div class="flex flex-wrap gap-2"><button data-testid="add-batch" class="btn btn-secondary" type="button" :disabled="batches.length>=2" @click="batches.push(newBatch())">{{ t('admin.accountOps.addRuleGroup') }}</button><button data-testid="save-batch" class="btn btn-primary" type="submit" :disabled="!accounts.length">{{ t('admin.accountOps.applyBatchRules') }}</button></div>
      </form>
    </section>
    <section class="card space-y-4 overflow-x-auto p-5">
      <h2 class="text-base font-semibold">{{ t('admin.accountOps.accountsAndThresholdRules') }}</h2>
      <form class="flex flex-wrap items-end gap-3" @submit.prevent="saveRule">
        <label class="text-sm">{{ t('admin.accountOps.account') }}<select v-model.number="rule.account_id" class="input mt-1 block" required><option :value="0" disabled>{{ t('admin.accountOps.selectAccount') }}</option><option v-for="a in accounts" :key="a.account_id" :value="a.account_id">{{ a.account_name }} #{{ a.account_id }}</option></select></label>
        <label class="text-sm">{{ t('admin.accountOps.metric') }}<select v-model="rule.metric" class="input mt-1 block"><option value="balance">{{ t('admin.accountOps.balanceAtOrBelow') }}</option><option value="quota">{{ t('admin.accountOps.usedPercentageReaches') }}</option></select></label>
        <label class="text-sm">{{ t('admin.accountOps.threshold') }}<input v-model.number="rule.value" class="input mt-1 block w-28" type="number" step="any" required></label>
        <label v-if="rule.metric==='balance'" class="text-sm">{{ t('admin.accountOps.currencyUnit') }}<input v-model="rule.unit" class="input mt-1 block w-24" required></label>
        <label v-else class="text-sm">{{ t('admin.accountOps.window') }}<input v-model="rule.window" class="input mt-1 block w-28" placeholder="any / 5h / 7d" required></label>
        <label class="text-sm"><input v-model="rule.enabled" type="checkbox"> {{ t('admin.accountOps.enabled') }}</label><label class="text-sm"><input v-model="rule.notify_alert" type="checkbox"> {{ t('admin.accountOps.alertNotification') }}</label><label class="text-sm"><input v-model="rule.notify_recovery" type="checkbox"> {{ t('admin.accountOps.recoveryNotification') }}</label>
        <button data-testid="save-rule" class="btn btn-primary" type="submit" :disabled="saving||!rule.account_id">{{ t('admin.accountOps.saveRule') }}</button>
      </form>
      <table class="w-full min-w-[780px] text-left text-sm"><thead class="border-b text-xs text-gray-500"><tr><th class="p-2">{{ t('admin.accountOps.account') }}</th><th class="p-2">{{ t('admin.accountOps.freshSnapshot') }}</th><th class="p-2">{{ t('admin.accountOps.rule') }}</th><th class="p-2">{{ t('admin.accountOps.readConfigure') }}</th></tr></thead><tbody><tr v-for="a in accounts" :key="a.account_id" class="border-b dark:border-dark-700"><td class="p-2">{{ a.account_name }} #{{ a.account_id }}<small class="block text-gray-500">{{ a.platform }} · {{ a.type }}</small></td><td class="p-2"><span v-if="a.balance_status==='ok'&&a.balance!==null">{{ a.balance }} {{ a.unit }}</span><span v-else class="text-gray-500">{{ a.balance_status }}</span><small class="block text-gray-500">{{ date(a.received_at) }}</small></td><td class="p-2"><div v-for="r in rulesFor(a.account_id)" :key="r.metric" class="flex items-center gap-2"><span>{{ r.label }} · {{ r.enabled?t('admin.accountOps.enabled'):t('admin.accountOps.disabled') }}</span><button class="text-primary-600" @click="editRule(a.account_id,r.metric)">{{ t('admin.accountOps.edit') }}</button><button class="text-red-600" :disabled="saving" @click="removeRule(a.account_id,r.metric)">{{ t('admin.accountOps.delete') }}</button></div><span v-if="!rulesFor(a.account_id).length" class="text-gray-500">—</span></td><td class="space-x-2 p-2"><template v-if="a.type==='apikey'"><button class="text-primary-600" :disabled="saving||!draft.enabled" @click="probe(a.account_id)">{{ t('admin.accountOps.readOnce') }}</button><button class="text-primary-600" :disabled="saving" @click="optIn(a.account_id,true)">{{ t('admin.accountOps.optIntoScheduling') }}</button><button class="text-gray-600" :disabled="saving" @click="optIn(a.account_id,false)">{{ t('admin.accountOps.stopScheduling') }}</button></template></td></tr><tr v-if="!accounts.length"><td colspan="4" class="p-8 text-center text-gray-500">{{ t('admin.accountOps.noSupportedAccounts') }}</td></tr></tbody></table>
    </section>
    <section class="card overflow-x-auto p-5"><h2 class="text-base font-semibold">{{ t('admin.accountOps.durableAlertsAndDeliveryReceipts') }}</h2><table class="mt-3 w-full min-w-[800px] text-left text-sm"><thead class="border-b text-xs text-gray-500"><tr><th class="p-2">{{ t('admin.accountOps.accountMetric') }}</th><th class="p-2">{{ t('admin.accountOps.phaseEpisode') }}</th><th class="p-2">{{ t('admin.accountOps.stateAttempts') }}</th><th class="p-2">{{ t('admin.accountOps.observed') }}</th><th class="p-2">{{ t('admin.accountOps.perChannelReceipts') }}</th></tr></thead><tbody><tr v-for="e in events" :key="e.id||`${e.account_id}:${e.kind}:${e.first_seen}`" class="border-b dark:border-dark-700"><td class="p-2">{{ e.account_name }} #{{ e.account_id }}<small class="block text-gray-500">{{ e.kind }}</small></td><td class="p-2">{{ e.phase||'signal' }}<small class="block max-w-40 truncate text-gray-500" :title="e.episode_id">{{ e.episode_id }}</small></td><td class="p-2">{{ e.state }} / {{ e.attempts }}</td><td class="p-2 text-xs">{{ date(e.details?.observed_at||e.first_seen) }}</td><td class="p-2 text-xs"><p v-for="(d,key) in e.deliveries||{}" :key="key">{{ d.name||d.provider }} · {{ d.status }} · {{ d.attempts }} · {{ date(d.last_sent_at) }}</p><span v-if="!Object.keys(e.deliveries||{}).length">—</span></td></tr><tr v-if="!events.length"><td colspan="5" class="p-8 text-center text-gray-500">{{ t('admin.accountOps.noAlertEvents') }}</td></tr></tbody></table><button v-if="hasMore" class="btn btn-secondary mt-3" :disabled="loading" @click="more">{{ t('admin.accountOps.loadMore') }}</button></section>
    </fieldset>
    </template>
    <ConfirmDialog :show="!!pending" :title="t('admin.accountOps.confirmConfigurationChange')" :message="pending ? t(pending.messageKey) : ''" :loading="saving" @confirm="confirmChange" @cancel="pending=null"/>
    <TotpStepUpDialog :controller="stepUp"/>
  </div></AppLayout>
</template>
<script setup lang="ts">
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import AppLayout from '@/components/layout/AppLayout.vue';
import AdminPageHeader from '@/components/admin/operations/OpsPageHeader.vue';
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue';
import ConfirmDialog from '@/components/common/ConfirmDialog.vue';
import { useStepUp, isStepUpCancelled } from '@/composables/useStepUp';
import { extractApiErrorMessage } from '@/utils/apiError';
import * as ops from '@/api/admin/accountOps';
import { getUpstreamBillingProbeSettings, updateUpstreamBillingProbeSettings, probeUpstreamBilling, setUpstreamBillingProbeEnabled } from '@/api/admin/accounts';
const { t } = useI18n();
const stepUp = useStepUp();
const remote = ref<ops.AccountOpsSettings | null>(null), events = ref<ops.AccountOpsEvent[]>([]), accounts = ref<ops.AccountOpsThresholdAccount[]>([]), hasMore = ref(false), loading = ref(false), saving = ref(false), saved = ref(false), error = ref('');
const probeSettings = ref<{
    enabled: boolean;
    interval_minutes: number;
} | null>(null);
const draft = reactive({ enabled: false, monitor_enabled: false, sample_interval_minutes: 5, recipient: '', email_name: '', cooldown_minutes: 60, balance_low: true, weekly_quota: true });
const hookDraft = reactive({ id: '', name: '', provider: 'custom' as ops.AccountOpsWebhookProvider, enabled: true, url: '', secret: '', clear_secret: false, message_template: '' });
const rule = reactive({ account_id: 0, metric: 'balance' as 'balance' | 'quota', value: 5, unit: 'USD', window: 'any', enabled: true, notify_alert: true, notify_recovery: true });
type GroupDraft = { id: string; name: string; provider: string; site: string; account_ids: number[] };
const groups = ref<ops.AccountOpsGroup[]>([]);
const groupDrafts = ref<GroupDraft[]>([]);
let batchKey = 0;
function newBatch() { return { key: ++batchKey, group_id: '', account_ids: [] as number[], metric: 'balance' as 'balance' | 'quota', enabled_only: false, enabled: true, value: 5, unit: 'USD', window: 'any', notify_alert: true, notify_recovery: true }; }
type BatchDraft = ReturnType<typeof newBatch>;
const batches = ref<BatchDraft[]>([newBatch()]);
function selectBatchGroup(b: BatchDraft) { b.account_ids = [...(groups.value.find(g => g.id === b.group_id)?.account_ids ?? [])]; }
function ruleInput(r: Pick<BatchDraft, 'metric' | 'enabled' | 'value' | 'unit' | 'window' | 'notify_alert' | 'notify_recovery'> & { enabled_only?: boolean }): ops.AccountOpsRuleInput {
    if (r.enabled_only) return { metric: r.metric, enabled: r.enabled, enabled_only: true };
    return { metric: r.metric, enabled: r.enabled, notify_alert: r.notify_alert, notify_recovery: r.notify_recovery, ...(r.metric === 'balance' ? { threshold: r.value, unit: r.unit.trim() } : { threshold_percent: r.value, window: r.window.trim() }) };
}
function validRule(r: ops.AccountOpsRuleInput) {
    if (r.enabled_only) return true;
    const valid = r.metric === 'balance'
        ? typeof r.threshold === 'number' && Number.isFinite(r.threshold) && r.threshold >= 0 && r.threshold <= 1e12 && !!r.unit && r.unit.length <= 32 && !/[\r\n]/.test(r.unit)
        : typeof r.threshold_percent === 'number' && Number.isFinite(r.threshold_percent) && r.threshold_percent > 0 && r.threshold_percent <= 100 && /^[A-Za-z0-9_.:/-]{1,128}$/.test(r.window ?? '');
    if (!valid) error.value = t('admin.accountOps.invalidRule');
    return valid;
}
function saveBatch() {
    const payload = batches.value.map(b => ({ account_ids: [...b.account_ids], rule: ruleInput(b) }));
    const ids = payload.flatMap(b => b.account_ids);
    if (payload.some(b => !b.account_ids.length) || ids.length > 1000 || ids.some(id => !Number.isSafeInteger(id) || id <= 0) || new Set(ids).size !== ids.length) {
        error.value = t('admin.accountOps.invalidBatchAccounts'); return;
    }
    if (!payload.every(b => validRule(b.rule))) return;
    pending.value = { messageKey: 'admin.accountOps.confirmBatch', action: () => mutate(async () => {
        const first = payload[0];
        accept(await (payload.length === 1 ? ops.saveAccountOpsRulesBatch(first.account_ids, first.rule) : ops.saveAccountOpsRuleGroups(payload)));
    }) };
}
const pending = ref<{ messageKey: string; action: () => Promise<void> } | null>(null);
function resetGroups(c: ops.AccountOpsConfig) {
    groupDrafts.value = (c.groups ?? []).map(g => ({ id: g.id, name: g.name, provider: g.provider ?? '', site: g.site ?? '', account_ids: [...(g.account_ids ?? [])] }));
}
function missingMembers(ids: number[]) { return ids.filter(id => !accounts.value.some(a => a.account_id === id)); }
function addGroup() { groupDrafts.value.push({ id: `manual-${crypto.randomUUID()}`, name: '', provider: '', site: '', account_ids: [] }); }
function confirmChange() { const change = pending.value; pending.value = null; if (change) void change.action(); }
function validGroups(value: GroupDraft[]) {
    const ids = value.flatMap(g => g.account_ids);
    if (value.length > 200 || ids.some(id => !Number.isSafeInteger(id) || id <= 0) || new Set(ids).size !== ids.length || value.some(g => g.account_ids.length > 1000 || [...g.name].length > 80 || /\p{Cc}/u.test(g.name))) {
        error.value = t('admin.accountOps.invalidGroups');
        return false;
    }
    return true;
}
async function refreshMappings(signal: AbortSignal) {
    if (!alive || signal.aborted) return;
    const [g, a] = await Promise.all([ops.getAccountOpsThresholdGroups(signal), ops.getAccountOpsThresholdAccounts(signal)]);
    if (!alive || signal.aborted) return;
    groups.value = g; accounts.value = a;
}
async function refreshAfterSave(signal: AbortSignal) {
    try { await refreshMappings(signal); }
    catch (e) {
        if (alive && !signal.aborted) error.value = `${t('admin.accountOps.savedRefreshFailed')} ${extractApiErrorMessage(e, t('admin.accountOps.loadFailed'))}`;
    }
}
function saveGroups() {
    const value = groupDrafts.value.map(g => ({ ...g, name: g.name.trim(), provider: g.provider.trim(), site: g.site.trim(), account_ids: [...g.account_ids] }));
    if (!validGroups(value)) return;
    pending.value = { messageKey: 'admin.accountOps.confirmGroups', action: () => mutate(async signal => {
        const c = await ops.saveAccountOpsGroups(value);
        accept(c);
        if (alive) resetGroups(c);
        await refreshAfterSave(signal);
    }) };
}
function saveConfig() {
    if (!remote.value || !validRuntime()) return;
    const { webhooks, ...config } = remote.value.config;
    const value: ops.AccountOpsConfigInput = { ...config, ...draft, webhooks: (webhooks ?? []).map(({ id, name, provider, enabled, message_template }) => ({ id, name, provider, enabled, message_template })) };
    pending.value = { messageKey: 'admin.accountOps.confirmConfig', action: () => mutate(async signal => {
        accept(await stepUp.run(() => { if (!alive) throw new DOMException('Aborted', 'AbortError'); return ops.saveAccountOpsSettings(value); }));
        if (alive && remote.value) resetGroups(remote.value.config);
        await refreshAfterSave(signal);
    }) };
}
let alive = true, version = 0;
let controller: AbortController | null = null;
const date = (s?: string | null) => s ? new Date(s).toLocaleString() : '—';
function accept(c: ops.AccountOpsConfig) { if (!alive || !remote.value)
    return; remote.value = { ...remote.value, config: c }; Object.assign(draft, { enabled: c.enabled, recipient: c.recipient, cooldown_minutes: c.cooldown_minutes, balance_low: c.balance_low, weekly_quota: c.weekly_quota, monitor_enabled: c.monitor_enabled ?? false, sample_interval_minutes: c.sample_interval_minutes ?? 5, email_name: c.email_name ?? '' }); }
async function load() { if (saving.value)
    return; const v = ++version; controller?.abort(); controller = new AbortController(); loading.value = true; saved.value = false; error.value = ''; const signal = controller.signal; try {
    const [s, e, a, p, g] = await Promise.all([ops.getAccountOpsSettings(signal), ops.getAccountOpsEvents(0, signal), ops.getAccountOpsThresholdAccounts(signal), getUpstreamBillingProbeSettings(signal), ops.getAccountOpsThresholdGroups(signal)]);
    if (!alive || v !== version || signal.aborted)
        return;
    remote.value = s;
    accept(s.config);
    resetGroups(s.config);
    groups.value = g;
    events.value = e.items;
    hasMore.value = e.has_more;
    accounts.value = a;
    probeSettings.value = p;
}
catch (e) {
    if (alive && v === version && !signal.aborted)
        error.value = extractApiErrorMessage(e, t('admin.accountOps.loadFailed'));
}
finally {
    if (alive && v === version)
        loading.value = false;
} }
async function mutate(action: (signal: AbortSignal) => Promise<unknown>) { if (saving.value)
    return; saving.value = true; loading.value = false; saved.value = false; error.value = ''; version++; controller?.abort(); controller = new AbortController(); const signal = controller.signal; try {
    await action(signal);
    if (alive)
        saved.value = true;
}
catch (e) {
    if (alive && !signal.aborted && !isStepUpCancelled(e))
        error.value = extractApiErrorMessage(e, t('admin.accountOps.operationFailed'));
}
finally {
    if (alive)
        saving.value = false;
} }
function validRuntime() {
    if (!Number.isInteger(draft.sample_interval_minutes) || draft.sample_interval_minutes < 1 || draft.sample_interval_minutes > 60 || !Number.isInteger(draft.cooldown_minutes) || draft.cooldown_minutes < 5 || draft.cooldown_minutes > 1440) {
        error.value = t('admin.accountOps.invalidRuntime'); return false;
    }
    return true;
}
async function saveSettings() { if (!validRuntime()) return; await mutate(async () => { const p: ops.AccountOpsNotificationSettings = { enabled: draft.enabled, monitor_enabled: draft.monitor_enabled, sample_interval_minutes: draft.sample_interval_minutes, email_name: draft.email_name, cooldown_minutes: draft.cooldown_minutes, balance_low: draft.balance_low, weekly_quota: draft.weekly_quota }; if (draft.recipient !== remote.value?.config.recipient)
    p.recipient = draft.recipient; accept(await stepUp.run(() => ops.saveAccountOpsNotificationSettings(p))); }); }
async function saveProbeSettings() { await mutate(async () => { if (probeSettings.value) {
    const p = await updateUpstreamBillingProbeSettings({ ...probeSettings.value });
    if (alive)
        probeSettings.value = p;
} }); }
function editHook(h: ops.AccountOpsWebhook) { Object.assign(hookDraft, { ...h, name: h.name || '', url: '', secret: '', clear_secret: false, message_template: h.message_template || '' }); }
async function saveHook() { await mutate(async () => { accept(await stepUp.run(() => ops.saveAccountOpsWebhook(hookDraft.id, { ...hookDraft }))); hookDraft.url = ''; hookDraft.secret = ''; }); }
async function removeHook(id: string) { await mutate(async () => accept(await stepUp.run(() => ops.deleteAccountOpsWebhook(id)))); }
async function testHook(id: string) { await mutate(() => stepUp.run(() => ops.testAccountOpsWebhook(id))); }
function rulesFor(id: number) { const c = remote.value?.config; return [...(c?.balance_thresholds ?? []).filter(r => r.account_id === id).map(r => ({ metric: 'balance' as const, label: `≤ ${r.threshold} ${r.unit}`, enabled: r.enabled })), ...(c?.quota_thresholds ?? []).filter(r => r.account_id === id).map(r => ({ metric: 'quota' as const, label: `≥ ${r.threshold_percent}% ${r.window}`, enabled: r.enabled }))]; }
function editRule(id: number, metric: 'balance' | 'quota') { const c = remote.value?.config; const r = metric === 'balance' ? c?.balance_thresholds?.find(x => x.account_id === id) : c?.quota_thresholds?.find(x => x.account_id === id); if (!r)
    return; Object.assign(rule, { account_id: id, metric, enabled: r.enabled, notify_alert: r.notify_alert ?? true, notify_recovery: r.notify_recovery ?? true }); if ('threshold' in r) {
    rule.value = r.threshold;
    rule.unit = r.unit;
}
else {
    rule.value = r.threshold_percent;
    rule.window = r.window;
} }
async function saveRule() { const value = ruleInput(rule); if (!rule.account_id || !validRule(value)) return; await mutate(async () => accept(await ops.saveAccountOpsRule(rule.account_id, value))); }
async function removeRule(id: number, metric: 'balance' | 'quota') { await mutate(async () => accept(await ops.deleteAccountOpsRule(id, metric))); }
async function optIn(id: number, enabled: boolean) { await mutate(() => setUpstreamBillingProbeEnabled(id, enabled)); }
async function probe(id: number) { await mutate(async signal => { await probeUpstreamBilling(id); const a = await ops.getAccountOpsThresholdAccounts(signal); if (alive && !signal.aborted)
    accounts.value = a; }); }
async function more() { if (loading.value || saving.value) return; const v = ++version; controller?.abort(); controller = new AbortController(); const signal = controller.signal; loading.value = true; error.value = ''; try {
    const p = await ops.getAccountOpsEvents(events.value.length, signal);
    if (alive && v === version && !signal.aborted) {
        events.value.push(...p.items);
        hasMore.value = p.has_more;
    }
}
catch (e) {
    if (alive && v === version && !signal.aborted)
        error.value = extractApiErrorMessage(e, t('admin.accountOps.loadFailed'));
}
finally {
    if (alive && v === version)
        loading.value = false;
} }
onMounted(load);
onBeforeUnmount(() => { alive = false; version++; controller?.abort(); stepUp.onCancel(); pending.value = null; hookDraft.url = ''; hookDraft.secret = ''; });
</script>
