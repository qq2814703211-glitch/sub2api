<template>
  <AppLayout>
    <div class="space-y-5">
      <AdminPageHeader :title="text('上游运营看板', 'Upstream operations')" :description="text('分币种余额快照、额度窗口、观测新鲜度与告警。', 'Currency-separated balances, quota windows, observation freshness and alerts.')">
        <template #actions><button class="btn btn-secondary" :disabled="busy" @click="refresh">{{ busy ? text('加载中…', 'Loading…') : text('刷新', 'Refresh') }}</button><button class="btn btn-primary" :disabled="sampling" @click="sample">{{ sampling ? text('采样中…', 'Sampling…') : text('现在采样', 'Sample now') }}</button></template>
      </AdminPageHeader>
      <p v-if="error" role="alert" class="rounded border border-red-200 bg-red-50 p-3 text-sm text-red-700">{{ error }}</p>
      <section v-if="overview" class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <article class="card p-4"><p class="text-xs text-gray-500">{{ text('账号', 'Accounts') }}</p><p class="mt-2 text-2xl font-semibold">{{ overview.total_accounts }}</p></article>
        <article class="card p-4"><p class="text-xs text-gray-500">{{ text('有效余额快照', 'Fresh balance snapshots') }}</p><p class="mt-2 text-2xl font-semibold">{{ overview.with_balance }}</p></article>
        <article class="card p-4"><p class="text-xs text-gray-500">{{ text('异常 / 陈旧 / 不支持', 'Failed / stale / unsupported') }}</p><p class="mt-2 text-2xl font-semibold">{{ overview.failed }} / {{ overview.stale }} / {{ overview.unsupported }}</p></article>
        <article class="card p-4"><p class="text-xs text-gray-500">{{ text('未确认上游告警', 'Open upstream alerts') }}</p><p class="mt-2 text-2xl font-semibold">{{ overview.open_alerts }}</p></article>
      </section>
      <section class="card overflow-x-auto p-5"><h2 class="text-base font-semibold">{{ text('币种余额', 'Balances by currency') }}</h2><div class="mt-3 flex flex-wrap gap-3"><span v-for="c in overview?.currencies??[]" :key="c.unit" class="border-r pr-4"><strong class="tabular-nums">{{ money(c.total,c.unit) }}</strong> <small class="text-gray-500">{{ c.accounts }} {{ text('账号','accounts') }}</small></span><span v-if="!overview?.currencies.length" class="text-sm text-gray-500">{{ text('暂无新鲜余额', 'No fresh balances') }}</span></div></section>
      <section class="card overflow-x-auto"><div class="flex flex-wrap items-center gap-3 border-b p-4"><h2 class="mr-auto text-base font-semibold">{{ text('账号快照', 'Account snapshots') }}</h2><input v-model="search" class="input w-52" :placeholder="text('搜索账号','Search accounts')" /><select v-model="status" class="input w-36"><option value="all">{{ text('全部状态','All states') }}</option><option value="ok">{{ text('正常','Fresh') }}</option><option value="stale">{{ text('陈旧','Stale') }}</option><option value="failed">{{ text('失败','Failed') }}</option><option value="unknown">{{ text('未知','Unknown') }}</option></select></div>
        <table class="w-full min-w-[900px] text-left text-sm"><thead class="border-b bg-gray-50 text-xs text-gray-500"><tr><th class="p-3">{{ text('账号','Account') }}</th><th class="p-3">{{ text('平台','Platform') }}</th><th class="p-3">{{ text('余额','Balance') }}</th><th class="p-3">{{ text('额度窗口','Quota windows') }}</th><th class="p-3">{{ text('观测 / 有效期','Observed / fresh until') }}</th><th class="p-3">{{ text('倍率','Rate') }}</th></tr></thead><tbody><tr v-for="a in filtered" :key="a.account_id" class="border-b"><td class="p-3">{{ a.name }} <small class="text-gray-500">#{{ a.account_id }}</small></td><td class="p-3">{{ a.platform }}</td><td class="p-3"><template v-if="a.balance_status==='ok' && a.balance!==null">{{ money(a.balance,a.unit) }}</template><span v-else class="text-gray-500">{{ labelStatus(a.balance_status) }}</span></td><td class="p-3"><span v-if="a.usage?.windows?.length">{{ a.usage.windows.map(w=>`${w.window}: ${w.remaining_pct.toFixed(1)}%`).join(' · ') }}</span><span v-else class="text-gray-500">{{ a.usage?.status==='ok'?text('无额度窗口','No quota window'):labelStatus(a.balance_status) }}</span><small v-if="a.usage?.expires_at" class="block text-gray-500">{{ text('到期','Expires') }} {{ date(a.usage.expires_at) }}</small></td><td class="p-3 text-xs text-gray-500">{{ date(a.balance_read_at) }}<br>{{ date(a.balance_fresh_until) }}</td><td class="p-3">{{ a.rate_status==='ok'&&a.rate!==null?`× ${a.rate.toFixed(4)}`:labelStatus(a.rate_status||'unknown') }}</td></tr><tr v-if="!filtered.length"><td colspan="6" class="p-8 text-center text-sm text-gray-500">{{ busy?text('加载中…','Loading…'):text('没有匹配账号','No matching accounts') }}</td></tr></tbody></table>
        <div class="flex items-center justify-between border-t p-3 text-sm"><span>{{ total }} {{ text('个账号','accounts') }}</span><div class="flex items-center gap-2"><button class="btn btn-secondary" :disabled="page<=1||busy" @click="page--;loadAccounts()">{{ text('上一页','Previous') }}</button><span>{{ page }} / {{ pages }}</span><button class="btn btn-secondary" :disabled="page>=pages||busy" @click="page++;loadAccounts()">{{ text('下一页','Next') }}</button></div></div>
      </section>
      <section class="card overflow-x-auto p-5"><h2 class="text-base font-semibold">{{ text('近期上游告警','Recent upstream alerts') }}</h2><table class="mt-3 w-full min-w-[680px] text-left text-sm"><thead class="border-b text-xs text-gray-500"><tr><th class="p-2">{{ text('账号','Account') }}</th><th class="p-2">{{ text('类型','Kind') }}</th><th class="p-2">{{ text('级别','Level') }}</th><th class="p-2">{{ text('时间','Time') }}</th><th class="p-2"></th></tr></thead><tbody><tr v-for="a in alerts" :key="a.id" class="border-b"><td class="p-2">{{ a.name }} #{{ a.account_id }}</td><td class="p-2">{{ a.kind }}</td><td class="p-2">{{ a.level }}</td><td class="p-2 text-xs">{{ date(a.created_at) }}</td><td class="p-2 text-right"><button v-if="!a.acked_at" class="btn btn-secondary" @click="ack(a.id)">{{ text('确认','Acknowledge') }}</button><span v-else class="text-xs text-gray-500">{{ text('已确认','Acknowledged') }}</span></td></tr><tr v-if="!alerts.length"><td colspan="5" class="p-5 text-center text-sm text-gray-500">{{ text('暂无告警','No alerts') }}</td></tr></tbody></table></section>
      <section class="card overflow-x-auto p-5"><h2 class="text-base font-semibold">{{ text('倍率变动','Rate changes') }}</h2><table class="mt-3 w-full min-w-[500px] text-left text-sm"><tbody><tr v-for="r in rates" :key="r.id" class="border-b"><td class="p-2">{{ r.name }} #{{ r.account_id }}</td><td class="p-2">× {{ r.old_rate.toFixed(4) }} → × {{ r.new_rate.toFixed(4) }}</td><td class="p-2">{{ r.change_pct.toFixed(1) }}%</td><td class="p-2 text-xs">{{ date(r.changed_at) }}</td></tr><tr v-if="!rates.length"><td class="p-5 text-center text-sm text-gray-500">{{ text('暂无倍率变动','No rate changes') }}</td></tr></tbody></table></section>
      <UpstreamHistoryPanel :accounts="accounts"/>
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import AppLayout from '@/components/layout/AppLayout.vue';
import UpstreamHistoryPanel from '@/components/admin/operations/UpstreamHistoryPanel.vue';
import AdminPageHeader from '@/components/admin/operations/OpsPageHeader.vue';
import { ackAlert, getOverview, listAccounts, listAlerts, listRateChanges, sampleNow, type UpstreamMonitorAccount, type UpstreamMonitorOverview, type UpstreamAlert, type UpstreamRateChange } from '@/api/admin/upstreamMonitor';
const { locale } = useI18n();
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en;
const overview = ref<UpstreamMonitorOverview | null>(null), accounts = ref<UpstreamMonitorAccount[]>([]), alerts = ref<UpstreamAlert[]>([]), rates = ref<UpstreamRateChange[]>([]), error = ref(''), busy = ref(false), sampling = ref(false), search = ref(''), status = ref('all'), page = ref(1), total = ref(0), pages = ref(1);
let generation = 0;
let alive = true;
let controller: AbortController | null = null;
const filtered = computed(() => accounts.value.filter(a => (status.value === 'all' || a.balance_status === status.value) && (!search.value || `${a.name} ${a.account_id} ${a.platform}`.toLowerCase().includes(search.value.trim().toLowerCase()))));
const date = (v: string | null | undefined) => v ? new Date(v).toLocaleString() : '-';
const money = (n: number, u: string) => `${n.toLocaleString(undefined, { maximumFractionDigits: 4 })} ${u || text('未标明单位', 'Unspecified unit')}`;
const labelStatus = (s: string) => ({ ok: text('正常', 'Fresh'), stale: text('未知（陈旧）', 'Unknown (stale)'), failed: text('未知（读取失败）', 'Unknown (failed)'), unsupported: text('不支持', 'Unsupported'), unknown: text('未知', 'Unknown') }[s] || s);
async function loadAccounts() { await refresh(); }
async function refresh() { const id = ++generation; controller?.abort(); controller = new AbortController(); const signal = controller.signal; busy.value = true; error.value = ''; try {
    const [o, a, l, r] = await Promise.all([getOverview(signal), listAccounts({ page: page.value, pageSize: 20, search: search.value, balanceStatus: status.value === 'all' ? '' : status.value }, signal), listAlerts({ page: 1, pageSize: 10, status: 'open' }, signal), listRateChanges({ page: 1, pageSize: 10 }, signal)]);
    if (!alive || id !== generation)
        return;
    overview.value = o;
    accounts.value = a.items;
    total.value = a.total;
    pages.value = a.pages || 1;
    alerts.value = l.items;
    rates.value = r.items;
}
catch (e) {
    if (alive && id === generation)
        error.value = e instanceof Error ? e.message : text('看板加载失败', 'Could not load monitor');
}
finally {
    if (alive && id === generation)
        busy.value = false;
} }
async function sample() { sampling.value = true; error.value = ''; try {
    await sampleNow();
    if (alive)
        await refresh();
}
catch (e) {
    if (alive)
        error.value = e instanceof Error ? e.message : text('采样失败', 'Sampling failed');
}
finally {
    if (alive)
        sampling.value = false;
} }
async function ack(id: number) { try {
    await ackAlert(id);
    if (alive)
        alerts.value = alerts.value.map(a => a.id === id ? { ...a, acked_at: new Date().toISOString() } : a);
}
catch (e) {
    if (alive)
        error.value = e instanceof Error ? e.message : text('确认失败', 'Could not acknowledge alert');
} }
let filterTimer: ReturnType<typeof setTimeout> | undefined;
watch([search, status], () => { page.value = 1; clearTimeout(filterTimer); filterTimer = setTimeout(() => { if (alive)
    void refresh(); }, 250); });
onMounted(refresh);
onBeforeUnmount(() => { alive = false; generation++; controller?.abort(); clearTimeout(filterTimer); });
</script>
