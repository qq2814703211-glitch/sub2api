<template>
  <AppLayout><div class="space-y-5">
    <AdminPageHeader :title="text('账号运营告警','Account operations alerts')" :description="text('阈值事件、恢复通知与每通道持久回执。','Threshold episodes, recovery and durable per-channel receipts.')">
      <template #actions><RouterLink to="/admin/upstream-monitor" class="btn btn-secondary">{{ text('上游看板','Upstream monitor') }}</RouterLink><button class="btn btn-secondary" :disabled="loading||saving" @click="load">{{ text('刷新','Refresh') }}</button></template>
    </AdminPageHeader>
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950 dark:text-red-300">{{ error }}</p>
    <p v-if="saved" role="status" class="text-sm text-emerald-600">{{ text('已保存','Saved') }}</p>
    <p v-if="loading&&!remote" class="p-8 text-center text-gray-500">{{ text('加载中…','Loading…') }}</p>
    <template v-if="remote">
    <fieldset :disabled="saving || loading || !!pending" class="min-w-0 space-y-5">
    <form class="card space-y-4 p-5" @submit.prevent="saveSettings">
      <h2 class="text-base font-semibold">{{ text('运行与通知参数','Runtime and notification settings') }}</h2>
      <div class="grid gap-4 md:grid-cols-3">
        <label class="flex items-center gap-2"><input v-model="draft.enabled" data-testid="enabled" type="checkbox">{{ text('启用模块','Enable module') }}</label>
        <label class="flex items-center gap-2"><input v-model="draft.monitor_enabled" type="checkbox">{{ text('采样历史与上游异常','Sample history and upstream anomalies') }}</label>
        <label class="block text-sm">{{ text('历史采样间隔（分钟）','History interval (minutes)') }}<input v-model.number="draft.sample_interval_minutes" class="input mt-1 w-full" type="number" min="1" max="60" required></label>
        <label class="block text-sm">{{ text('收件人（使用系统 SMTP）','Recipient (system SMTP)') }}<input v-model="draft.recipient" data-testid="recipient" class="input mt-1 w-full" type="email" autocomplete="off"></label>
        <label class="block text-sm">{{ text('邮件通道名称','Email label') }}<input v-model="draft.email_name" class="input mt-1 w-full" maxlength="80"></label>
        <label class="block text-sm">{{ text('信号冷却（分钟）','Signal cooldown (minutes)') }}<input v-model.number="draft.cooldown_minutes" class="input mt-1 w-full" type="number" min="5" max="1440" required></label>
        <label class="flex items-center gap-2"><input v-model="draft.balance_low" type="checkbox">{{ text('转发余额不足信号','Forward insufficient-balance signals') }}</label>
        <label class="flex items-center gap-2"><input v-model="draft.weekly_quota" type="checkbox">{{ text('转发周额度耗尽信号','Forward exhausted weekly-quota signals') }}</label>
      </div>
      <p class="text-xs text-gray-500">{{ text('停用后不发起余额读取、阈值扫描或送达；已有快照及事件保留。邮件/机器人回执表示提供方接收，不表示收件人已读。','Disabling stops balance reads, threshold scans and delivery; snapshots and events remain. A receipt means provider acceptance, not recipient read.') }}</p>
      <p class="text-xs text-gray-500">SMTP: {{ remote.smtp_configured?text('已配置','Configured'):text('未配置','Not configured') }} · {{ text('存储失败','Storage failures') }} {{ remote.storage_failures }} · {{ text('丢弃信号','Dropped signals') }} {{ remote.dropped_signals }}</p>
      <button data-testid="save-settings" class="btn btn-primary" :disabled="saving" type="submit">{{ saving?text('保存中…','Saving…'):text('保存运行参数','Save runtime settings') }}</button>
      <button data-testid="save-config" class="btn btn-secondary ml-2" type="button" @click="saveConfig">{{ text('保存完整配置','Save complete configuration') }}</button>
      <p class="text-xs text-gray-500">{{ text('完整保存会将当前运行参数与已保存的通道、分组、规则一起提交，并要求安全验证。请先保存下方的分组和规则编辑。','Complete save submits runtime settings with the saved channels, groups and rules, and requires security verification. Save group and rule edits below first.') }}</p>
    </form>
    <form v-if="probeSettings" class="card space-y-3 p-5" @submit.prevent="saveProbeSettings">
      <h2 class="text-base font-semibold">{{ text('复用上游探测器','Shared upstream probe') }}</h2>
      <p class="text-xs text-gray-500">{{ text('与已有倍率读取共用调度、账号凭据和代理。余额仅在模块及规则/看板启用时读取；账号须单独选择加入。','Shares the existing billing scheduler, credentials and proxy. Balance requests require the module and a rule or monitor; each account must opt in.') }}</p>
      <div class="flex flex-wrap items-center gap-4"><label><input v-model="probeSettings.enabled" type="checkbox"> {{ text('探测调度启用','Probe scheduling enabled') }}</label><label>{{ text('读取间隔（分钟）','Read interval (minutes)') }} <input v-model.number="probeSettings.interval_minutes" class="input w-24" type="number" min="1" max="1440" required></label><button class="btn btn-secondary" :disabled="saving" type="submit">{{ text('保存探测参数','Save probe settings') }}</button></div>
    </form>
    <section class="card space-y-4 p-5">
      <h2 class="text-base font-semibold">{{ text('通知通道','Notification channels') }}</h2>
      <p class="text-xs text-gray-500">{{ text('URL 和签名密钥加密存储，不返回明文；留空保留原值。更换通道会产生新的送达身份。','URLs and signing secrets are encrypted and never returned; blank preserves the stored value. Changing a channel creates a new delivery identity.') }}</p>
      <ul class="divide-y dark:divide-dark-700"><li v-for="hook in remote.config.webhooks||[]" :key="hook.id" class="flex flex-wrap items-center gap-2 py-2 text-sm"><span class="mr-auto">{{ hook.name||hook.id }} · {{ hook.provider }} · {{ hook.enabled?text('启用','Enabled'):text('停用','Disabled') }} · {{ hook.url_configured?text('URL 已配置','URL configured'):text('无 URL','No URL') }}</span><button class="btn btn-secondary" :disabled="saving" @click="editHook(hook)">{{ text('编辑','Edit') }}</button><button class="btn btn-secondary" :disabled="saving" @click="testHook(hook.id)">{{ text('测试','Test') }}</button><button class="btn btn-secondary" :disabled="saving" @click="removeHook(hook.id)">{{ text('删除','Delete') }}</button></li></ul>
      <p v-if="!remote.encryption_key_configured" class="text-sm text-amber-700">{{ text('需先配置持久加密密钥才能保存机器人通道。','Configure a persistent encryption key before saving robot channels.') }}</p>
      <form class="grid gap-3 md:grid-cols-3" @submit.prevent="saveHook">
        <label class="text-sm">ID<input v-model="hookDraft.id" class="input mt-1 w-full" pattern="[A-Za-z0-9_-]{1,64}" required></label>
        <label class="text-sm">{{ text('名称','Name') }}<input v-model="hookDraft.name" class="input mt-1 w-full" maxlength="80"></label>
        <label class="text-sm">{{ text('提供方','Provider') }}<select v-model="hookDraft.provider" class="input mt-1 w-full"><option v-for="p in ['custom','bark','wecom','dingtalk','feishu']" :key="p" :value="p">{{ p }}</option></select></label>
        <label class="text-sm md:col-span-2">{{ text('HTTPS URL（留空保留）','HTTPS URL (blank preserves)') }}<input v-model="hookDraft.url" class="input mt-1 w-full" type="password" autocomplete="new-password"></label>
        <label class="text-sm">{{ text('签名密钥（留空保留）','Signing secret (blank preserves)') }}<input v-model="hookDraft.secret" class="input mt-1 w-full" type="password" autocomplete="new-password"></label>
        <label class="flex items-center gap-2 text-sm"><input v-model="hookDraft.enabled" type="checkbox">{{ text('启用通道','Enable channel') }}</label><label class="flex items-center gap-2 text-sm"><input v-model="hookDraft.clear_secret" type="checkbox">{{ text('清除签名密钥','Clear signing secret') }}</label>
        <label class="text-sm md:col-span-3">{{ text('自定义消息模板（可选）','Custom message template (optional)') }}<textarea v-model="hookDraft.message_template" class="input mt-1 w-full" rows="2" /></label>
        <button class="btn btn-secondary" type="submit" :disabled="saving||!remote.encryption_key_configured">{{ text('保存通道','Save channel') }}</button>
      </form>
    </section>
    <section class="card space-y-4 p-5" data-testid="group-section">
      <h2 class="text-base font-semibold">{{ text('分组映射','Group mapping') }}</h2>
      <p class="text-xs text-gray-500">{{ text('每个账号只能属于一个分组。移除成员后账号独立展示；删除或清空映射不会删除账号。保存会替换全部分组映射。','Each account can belong to one group. Removed members appear individually; removing mappings does not delete accounts. Saving replaces all group mappings.') }}</p>
      <p v-if="!groupDrafts.length" class="text-sm text-gray-500">{{ text('暂无手动分组。','No manual groups.') }}</p>
      <form data-testid="groups-form" class="space-y-4" @submit.prevent="saveGroups">
        <div v-for="(group, index) in groupDrafts" :key="group.id" class="grid gap-3 rounded-lg border p-3 md:grid-cols-2 dark:border-dark-700">
          <label class="text-sm">{{ text('名称','Name') }}<input v-model="group.name" :data-testid="`group-name-${index}`" class="input mt-1 w-full" maxlength="80"><small class="block text-gray-500">{{ group.id }}</small></label>
          <label class="text-sm">{{ text('提供方','Provider') }}<input v-model="group.provider" :data-testid="`group-provider-${index}`" class="input mt-1 w-full"></label>
          <label class="text-sm">{{ text('站点（可选）','Site (optional)') }}<input v-model="group.site" :data-testid="`group-site-${index}`" class="input mt-1 w-full" type="url"></label>
          <label class="text-sm">{{ text('成员账号（可多选）','Member accounts (multiple)') }}<select v-model="group.account_ids" :data-testid="`group-accounts-${index}`" class="input mt-1 w-full" multiple><option v-for="a in accounts" :key="a.account_id" :value="a.account_id">{{ a.account_name }} #{{ a.account_id }}</option><option v-for="id in missingMembers(group.account_ids)" :key="id" :value="id">#{{ id }} · {{ text('不在当前账号列表中','Not in the current account list') }}</option></select></label>
          <button class="btn btn-secondary justify-self-start" type="button" :data-testid="`remove-group-${index}`" @click="groupDrafts.splice(index,1)">{{ text('移除映射','Remove mapping') }}</button>
        </div>
        <div class="flex flex-wrap gap-2"><button data-testid="add-group" class="btn btn-secondary" type="button" :disabled="groupDrafts.length>=200" @click="addGroup">{{ text('添加分组','Add group') }}</button><button data-testid="save-groups" class="btn btn-primary" type="submit">{{ text('保存分组映射','Save group mapping') }}</button></div>
      </form>
      <h3 class="text-sm font-semibold">{{ text('当前有效分组','Current effective groups') }}</h3>
      <ul class="text-sm text-gray-500"><li v-for="g in groups" :key="g.id">{{ g.name || g.default_name }} · {{ g.provider }} · {{ g.site }} · {{ g.account_ids.join(', ') }}</li></ul>
      <p v-if="!groups.length" class="text-sm text-gray-500">{{ text('暂无分组或账号。','No groups or accounts.') }}</p>
    </section>
    <section class="card space-y-4 overflow-x-auto p-5">
      <h2 class="text-base font-semibold">{{ text('账号与阈值规则','Accounts and threshold rules') }}</h2>
      <form class="flex flex-wrap items-end gap-3" @submit.prevent="saveRule">
        <label class="text-sm">{{ text('账号','Account') }}<select v-model.number="rule.account_id" class="input mt-1 block" required><option :value="0" disabled>{{ text('选择账号','Select account') }}</option><option v-for="a in accounts" :key="a.account_id" :value="a.account_id">{{ a.account_name }} #{{ a.account_id }}</option></select></label>
        <label class="text-sm">{{ text('指标','Metric') }}<select v-model="rule.metric" class="input mt-1 block"><option value="balance">{{ text('余额低于或等于','Balance at or below') }}</option><option value="quota">{{ text('用量百分比达到','Used percentage reaches') }}</option></select></label>
        <label class="text-sm">{{ text('阈值','Threshold') }}<input v-model.number="rule.value" class="input mt-1 block w-28" type="number" step="any" required></label>
        <label v-if="rule.metric==='balance'" class="text-sm">{{ text('币种 / 单位','Currency / unit') }}<input v-model="rule.unit" class="input mt-1 block w-24" required></label>
        <label v-else class="text-sm">{{ text('窗口','Window') }}<input v-model="rule.window" class="input mt-1 block w-28" placeholder="any / 5h / 7d" required></label>
        <label class="text-sm"><input v-model="rule.enabled" type="checkbox"> {{ text('启用','Enabled') }}</label><label class="text-sm"><input v-model="rule.notify_alert" type="checkbox"> {{ text('告警通知','Alert notification') }}</label><label class="text-sm"><input v-model="rule.notify_recovery" type="checkbox"> {{ text('恢复通知','Recovery notification') }}</label>
        <button data-testid="save-rule" class="btn btn-primary" type="submit" :disabled="saving||!rule.account_id">{{ text('保存规则','Save rule') }}</button>
      </form>
      <table class="w-full min-w-[780px] text-left text-sm"><thead class="border-b text-xs text-gray-500"><tr><th class="p-2">{{ text('账号','Account') }}</th><th class="p-2">{{ text('新鲜快照','Fresh snapshot') }}</th><th class="p-2">{{ text('规则','Rule') }}</th><th class="p-2">{{ text('读取 / 配置','Read / configure') }}</th></tr></thead><tbody><tr v-for="a in accounts" :key="a.account_id" class="border-b dark:border-dark-700"><td class="p-2">{{ a.account_name }} #{{ a.account_id }}<small class="block text-gray-500">{{ a.platform }} · {{ a.type }}</small></td><td class="p-2"><span v-if="a.balance_status==='ok'&&a.balance!==null">{{ a.balance }} {{ a.unit }}</span><span v-else class="text-gray-500">{{ a.balance_status }}</span><small class="block text-gray-500">{{ date(a.received_at) }}</small></td><td class="p-2"><div v-for="r in rulesFor(a.account_id)" :key="r.metric" class="flex items-center gap-2"><span>{{ r.label }} · {{ r.enabled?text('启用','Enabled'):text('停用','Disabled') }}</span><button class="text-primary-600" @click="editRule(a.account_id,r.metric)">{{ text('编辑','Edit') }}</button><button class="text-red-600" :disabled="saving" @click="removeRule(a.account_id,r.metric)">{{ text('删除','Delete') }}</button></div><span v-if="!rulesFor(a.account_id).length" class="text-gray-500">—</span></td><td class="space-x-2 p-2"><template v-if="a.type==='apikey'"><button class="text-primary-600" :disabled="saving||!draft.enabled" @click="probe(a.account_id)">{{ text('读取一次','Read once') }}</button><button class="text-primary-600" :disabled="saving" @click="optIn(a.account_id,true)">{{ text('加入定时探测','Opt into scheduling') }}</button><button class="text-gray-600" :disabled="saving" @click="optIn(a.account_id,false)">{{ text('停止定时探测','Stop scheduling') }}</button></template></td></tr><tr v-if="!accounts.length"><td colspan="4" class="p-8 text-center text-gray-500">{{ text('没有支持的账号。','No supported accounts.') }}</td></tr></tbody></table>
    </section>
    <section class="card overflow-x-auto p-5"><h2 class="text-base font-semibold">{{ text('持久告警与送达回执','Durable alerts and delivery receipts') }}</h2><table class="mt-3 w-full min-w-[800px] text-left text-sm"><thead class="border-b text-xs text-gray-500"><tr><th class="p-2">{{ text('账号 / 指标','Account / metric') }}</th><th class="p-2">{{ text('阶段 / episode','Phase / episode') }}</th><th class="p-2">{{ text('状态 / 尝试','State / attempts') }}</th><th class="p-2">{{ text('观测时间','Observed') }}</th><th class="p-2">{{ text('独立通道回执','Per-channel receipts') }}</th></tr></thead><tbody><tr v-for="e in events" :key="e.id||`${e.account_id}:${e.kind}:${e.first_seen}`" class="border-b dark:border-dark-700"><td class="p-2">{{ e.account_name }} #{{ e.account_id }}<small class="block text-gray-500">{{ e.kind }}</small></td><td class="p-2">{{ e.phase||'signal' }}<small class="block max-w-40 truncate text-gray-500" :title="e.episode_id">{{ e.episode_id }}</small></td><td class="p-2">{{ e.state }} / {{ e.attempts }}</td><td class="p-2 text-xs">{{ date(e.details?.observed_at||e.first_seen) }}</td><td class="p-2 text-xs"><p v-for="(d,key) in e.deliveries||{}" :key="key">{{ d.name||d.provider }} · {{ d.status }} · {{ d.attempts }} · {{ date(d.last_sent_at) }}</p><span v-if="!Object.keys(e.deliveries||{}).length">—</span></td></tr><tr v-if="!events.length"><td colspan="5" class="p-8 text-center text-gray-500">{{ text('暂无告警事件。','No alert events.') }}</td></tr></tbody></table><button v-if="hasMore" class="btn btn-secondary mt-3" :disabled="loading" @click="more">{{ text('加载更多','Load more') }}</button></section>
    </fieldset>
    </template>
    <ConfirmDialog :show="!!pending" :title="text('确认配置变更','Confirm configuration change')" :message="pending?.message || ''" :loading="saving" @confirm="confirmChange" @cancel="pending=null"/>
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
const { locale } = useI18n();
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en;
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
const pending = ref<{ message: string; action: () => Promise<void> } | null>(null);
function resetGroups(c: ops.AccountOpsConfig) {
    groupDrafts.value = (c.groups ?? []).map(g => ({ id: g.id, name: g.name, provider: g.provider ?? '', site: g.site ?? '', account_ids: [...(g.account_ids ?? [])] }));
}
function missingMembers(ids: number[]) { return ids.filter(id => !accounts.value.some(a => a.account_id === id)); }
function addGroup() { groupDrafts.value.push({ id: `manual-${crypto.randomUUID()}`, name: '', provider: '', site: '', account_ids: [] }); }
function confirmChange() { const change = pending.value; pending.value = null; if (change) void change.action(); }
function validGroups(value: GroupDraft[]) {
    const ids = value.flatMap(g => g.account_ids);
    if (value.length > 200 || ids.some(id => !Number.isSafeInteger(id) || id <= 0) || new Set(ids).size !== ids.length || value.some(g => g.account_ids.length > 1000 || g.name.length > 80 || /[\r\n]/.test(g.name))) {
        error.value = text('分组名称最多80字，成员账号须为正整数且不能跨组重复。','Group names must be at most 80 characters; member accounts must be positive and unique across groups.');
        return false;
    }
    return true;
}
async function refreshMappings(signal: AbortSignal) {
    const [g, a] = await Promise.all([ops.getAccountOpsThresholdGroups(signal), ops.getAccountOpsThresholdAccounts(signal)]);
    if (!alive || signal.aborted) return;
    groups.value = g; accounts.value = a;
}
function saveGroups() {
    const value = groupDrafts.value.map(g => ({ ...g, name: g.name.trim(), provider: g.provider.trim(), site: g.site.trim(), account_ids: [...g.account_ids] }));
    if (!validGroups(value)) return;
    pending.value = { message: text('替换全部分组映射？移除成员将恢复为独立账号。','Replace all group mappings? Removed members will appear as individual accounts.'), action: () => mutate(async signal => {
        const c = await ops.saveAccountOpsGroups(value); accept(c); if (alive) resetGroups(c); await refreshMappings(signal);
    }) };
}
function saveConfig() {
    if (!remote.value) return;
    const { webhooks, ...config } = remote.value.config;
    const value: ops.AccountOpsConfigInput = { ...config, ...draft, webhooks: (webhooks ?? []).map(({ id, name, provider, enabled, message_template }) => ({ id, name, provider, enabled, message_template })) };
    pending.value = { message: text('将当前运行参数与已保存的通道、分组、规则作为完整配置提交？','Submit current runtime settings with the saved channels, groups and rules as the complete configuration?'), action: () => mutate(async signal => {
        accept(await stepUp.run(() => ops.saveAccountOpsSettings(value))); if (alive && remote.value) resetGroups(remote.value.config); await refreshMappings(signal);
    }) };
}
let alive = true, version = 0;
let controller: AbortController | null = null;
const date = (s?: string | null) => s ? new Date(s).toLocaleString() : '—';
function accept(c: ops.AccountOpsConfig) { if (!alive || !remote.value)
    return; remote.value = { ...remote.value, config: c }; Object.assign(draft, { enabled: c.enabled, recipient: c.recipient, cooldown_minutes: c.cooldown_minutes, balance_low: c.balance_low, weekly_quota: c.weekly_quota, monitor_enabled: c.monitor_enabled ?? false, sample_interval_minutes: c.sample_interval_minutes ?? 5, email_name: c.email_name ?? '' }); }
async function load() { if (saving.value)
    return; const v = ++version; controller?.abort(); controller = new AbortController(); loading.value = true; error.value = ''; const signal = controller.signal; try {
    const [s, e, a, p, g] = await Promise.all([ops.getAccountOpsSettings(signal), ops.getAccountOpsEvents(0, signal), ops.getAccountOpsThresholdAccounts(signal), getUpstreamBillingProbeSettings(signal), ops.getAccountOpsThresholdGroups(signal)]);
    if (!alive || v !== version)
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
        error.value = extractApiErrorMessage(e, text('加载失败', 'Load failed'));
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
    if (alive && !isStepUpCancelled(e))
        error.value = extractApiErrorMessage(e, text('操作失败', 'Operation failed'));
}
finally {
    if (alive)
        saving.value = false;
} }
async function saveSettings() { await mutate(async () => { const p: ops.AccountOpsNotificationSettings = { enabled: draft.enabled, monitor_enabled: draft.monitor_enabled, sample_interval_minutes: draft.sample_interval_minutes, email_name: draft.email_name, cooldown_minutes: draft.cooldown_minutes, balance_low: draft.balance_low, weekly_quota: draft.weekly_quota }; if (draft.recipient !== remote.value?.config.recipient)
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
async function saveRule() { await mutate(async () => accept(await ops.saveAccountOpsRule(rule.account_id, { metric: rule.metric, enabled: rule.enabled, notify_alert: rule.notify_alert, notify_recovery: rule.notify_recovery, ...(rule.metric === 'balance' ? { threshold: rule.value, unit: rule.unit } : { threshold_percent: rule.value, window: rule.window }) }))); }
async function removeRule(id: number, metric: 'balance' | 'quota') { await mutate(async () => accept(await ops.deleteAccountOpsRule(id, metric))); }
async function optIn(id: number, enabled: boolean) { await mutate(() => setUpstreamBillingProbeEnabled(id, enabled)); }
async function probe(id: number) { await mutate(async () => { await probeUpstreamBilling(id); const a = await ops.getAccountOpsThresholdAccounts(); if (alive)
    accounts.value = a; }); }
async function more() { const v = ++version; loading.value = true; try {
    const p = await ops.getAccountOpsEvents(events.value.length);
    if (alive && v === version) {
        events.value.push(...p.items);
        hasMore.value = p.has_more;
    }
}
catch (e) {
    if (alive && v === version)
        error.value = extractApiErrorMessage(e, text('加载失败', 'Load failed'));
}
finally {
    if (alive && v === version)
        loading.value = false;
} }
onMounted(load);
onBeforeUnmount(() => { alive = false; version++; controller?.abort(); hookDraft.url = ''; hookDraft.secret = ''; });
</script>
