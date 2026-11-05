<template>
 <section class="card space-y-4 p-5">
  <h2 class="font-semibold">{{text('历史、趋势与异常规则','History, trends and anomaly rules')}}</h2>
  <p v-if="error" role="alert" class="text-sm text-red-600">{{error}}</p>
  <div class="flex flex-wrap items-center gap-3"><label>{{text('天数','Days')}} <select v-model.number="days" class="input w-24" @change="load"><option :value="7">7</option><option :value="14">14</option><option :value="30">30</option></select></label><label>{{text('币种','Unit')}} <select v-model="unit" class="input w-28"><option v-for="u in units" :key="u" :value="u">{{u||'—'}}</option></select></label><button class="btn btn-secondary" :disabled="loading" @click="load">{{text('刷新趋势','Refresh trend')}}</button></div>
  <p class="text-xs text-gray-500">{{text('仅估算同账号同币种的余额下降；充值、共享钱包与采样缺口会影响结果。不是精确消耗或利润。日期按 UTC+08:00 汇总。','Estimates declines within each account and currency. Top-ups, shared wallets and observation gaps affect results. This is not exact usage or profit. Days use UTC+08:00.')}}</p>
  <p v-if="loading" class="text-sm text-gray-500">{{text('加载中…','Loading…')}}</p>
  <OperationsTrendChart v-if="trendRows.length" :labels="trendRows.map(p=>p.day.slice(0,10))" :values="trendRows.map(p=>p.spend)" :unit="unit" :label="text('估算下降','Estimated decline')"/>
  <p v-else class="p-5 text-center text-gray-500">{{text('暂无趋势数据','No trend data')}}</p>
  <label class="block text-sm">{{text('账号历史及异常规则（当前页账号）','Account history and anomaly rules (current page)')}}<select v-model.number="accountId" class="input mt-1 block" @change="selectAccount"><option :value="0">{{text('选择账号','Select account')}}</option><option v-for="a in accounts" :key="a.account_id" :value="a.account_id">{{a.name}} #{{a.account_id}}</option></select></label>
  <template v-if="accountId"><p v-if="historyLoading" class="text-sm text-gray-500">{{text('加载历史…','Loading history…')}}</p><OperationsTrendChart v-for="h in histories" :key="h.unit" :labels="h.points.map(p=>new Date(p.sampled_at).toLocaleString())" :values="h.points.map(p=>p.balance)" :unit="h.unit" :label="text('余额','Balance')"/><p v-if="!historyLoading&&!history.length" class="text-sm text-gray-500">{{text('暂无历史点','No history points')}}</p>
  <p class="text-xs text-gray-500">{{text('以下规则生成看板异常记录；外部通知及恢复在账号运营页面配置。','These rules create dashboard anomalies; configure external notifications and recovery in Account operations.')}}</p>
  <form class="flex flex-wrap items-end gap-3" @submit.prevent="save">
   <label class="text-sm">{{text('低余额','Low balance')}}<input v-model.number="rule.threshold" class="input mt-1 block w-24" type="number" min="0" step="any" required></label>
   <label class="text-sm">{{text('单位','Unit')}}<input v-model="rule.unit" class="input mt-1 block w-24" maxlength="16"></label>
   <label class="text-sm">{{text('倍率变动 %','Rate move %')}}<input v-model.number="rule.rate_min_pct" class="input mt-1 block w-24" type="number" min="0" max="100" required></label>
   <label class="text-sm">{{text('剩余额度 %','Quota left %')}}<input v-model.number="rule.usage_min_pct" class="input mt-1 block w-24" type="number" min="1" max="100" required></label>
   <label class="text-sm">{{text('到期天数','Expiry days')}}<input v-model.number="rule.expire_days" class="input mt-1 block w-24" type="number" min="1" max="365" required></label>
   <label class="text-sm"><input v-model="rule.enabled" type="checkbox"> {{text('启用','Enabled')}}</label>
   <button class="btn btn-primary" :disabled="saving" type="submit">{{text('保存规则','Save rule')}}</button><button class="btn btn-secondary" :disabled="saving" type="button" @click="remove">{{text('删除规则','Delete rule')}}</button>
  </form></template>
 </section>
</template>
<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import OperationsTrendChart from '@/components/charts/OperationsTrendChart.vue';
import { getTrend, getHistory, listAlertRules, saveAlertRule, deleteAlertRule, type UpstreamMonitorAccount, type UpstreamMonitorPoint, type UpstreamTrendPoint, type UpstreamAlertRule } from '@/api/admin/upstreamMonitor';
import { extractApiErrorMessage } from '@/utils/apiError';
const props = defineProps<{
    accounts: UpstreamMonitorAccount[];
}>();
const { locale } = useI18n();
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en;
const trend = ref<UpstreamTrendPoint[]>([]), history = ref<UpstreamMonitorPoint[]>([]), rules = ref<UpstreamAlertRule[]>([]), accountId = ref(0), days = ref(7), unit = ref(''), error = ref(''), loading = ref(false), saving = ref(false), historyLoading = ref(false);
const rule = reactive({ threshold: 0, unit: '', rate_min_pct: 10, usage_min_pct: 20, expire_days: 7, enabled: true });
const units = computed(() => [...new Set(trend.value.map(p => p.unit))]);
const trendRows = computed(() => trend.value.filter(p => p.unit === unit.value));
const histories = computed(() => [...new Set(history.value.map(p => p.unit))].map(unit => ({ unit, points: history.value.filter(p => p.unit === unit) })));
let alive = true, sequence = 0, detailSequence = 0;
let controller: AbortController | null = null;
let detail: AbortController | null = null;
async function load() { const id = ++sequence; controller?.abort(); controller = new AbortController(); const signal = controller.signal; loading.value = true; error.value = ''; try {
    const [t, r] = await Promise.all([getTrend(days.value, signal), listAlertRules(signal)]);
    if (!alive || id !== sequence)
        return;
    trend.value = t.points;
    rules.value = r.items;
    if (!units.value.includes(unit.value))
        unit.value = units.value[0] ?? '';
}
catch (e) {
    if (alive && id === sequence && !signal.aborted)
        error.value = extractApiErrorMessage(e, text('加载失败', 'Load failed'));
}
finally {
    if (alive && id === sequence)
        loading.value = false;
} }
async function selectAccount() { const id = ++detailSequence; detail?.abort(); history.value = []; const r = rules.value.find(r => r.account_id === accountId.value); Object.assign(rule, r || { threshold: 0, unit: props.accounts.find(a => a.account_id === accountId.value)?.unit || '', rate_min_pct: 10, usage_min_pct: 20, expire_days: 7, enabled: true }); if (!accountId.value)
    return; detail = new AbortController(); const signal = detail.signal; historyLoading.value = true; try {
    const h = await getHistory(accountId.value, 30, signal);
    if (alive && id === detailSequence)
        history.value = h.points;
}
catch (e) {
    if (alive && id === detailSequence && !signal.aborted)
        error.value = extractApiErrorMessage(e, text('历史加载失败', 'History load failed'));
}
finally {
    if (alive && id === detailSequence)
        historyLoading.value = false;
} }
async function save() { if (saving.value)
    return; saving.value = true; error.value = ''; try {
    const r = await saveAlertRule({ account_id: accountId.value, ...rule });
    if (alive)
        rules.value = [...rules.value.filter(x => x.account_id !== r.account_id), r];
}
catch (e) {
    if (alive)
        error.value = extractApiErrorMessage(e, text('保存失败', 'Save failed'));
}
finally {
    if (alive)
        saving.value = false;
} }
async function remove() { if (saving.value)
    return; saving.value = true; error.value = ''; const id = accountId.value; try {
    await deleteAlertRule(id);
    if (alive) {
        rules.value = rules.value.filter(r => r.account_id !== id);
        rule.enabled = false;
    }
}
catch (e) {
    if (alive)
        error.value = extractApiErrorMessage(e, text('删除失败', 'Delete failed'));
}
finally {
    if (alive)
        saving.value = false;
} }
onMounted(load);
onBeforeUnmount(() => { alive = false; sequence++; detailSequence++; controller?.abort(); detail?.abort(); });
</script>
