<template>
  <AppLayout>
    <div class="space-y-5">
      <AdminPageHeader :title="t('admin.accountOps.monitorTitle')" :description="t('admin.accountOps.monitorDescription')">
        <template #actions><button class="btn btn-secondary" :disabled="busy" @click="refresh">{{ busy ? t('admin.accountOps.loading') : t('admin.accountOps.refresh') }}</button><button class="btn btn-primary" :disabled="sampling" @click="sample">{{ sampling ? t('admin.accountOps.sampling') : t('admin.accountOps.sampleNow') }}</button></template>
      </AdminPageHeader>
      <p v-if="error" role="alert" class="rounded border border-red-200 bg-red-50 p-3 text-sm text-red-700">{{ error }}</p>
      <section v-if="overview" class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <article class="card p-4"><p class="text-xs text-gray-500">{{ t('admin.accountOps.accounts') }}</p><p class="mt-2 text-2xl font-semibold">{{ overview.total_accounts }}</p></article>
        <article class="card p-4"><p class="text-xs text-gray-500">{{ t('admin.accountOps.freshBalanceSnapshots') }}</p><p class="mt-2 text-2xl font-semibold">{{ overview.with_balance }}</p></article>
        <article class="card p-4"><p class="text-xs text-gray-500">{{ t('admin.accountOps.failedStaleUnsupportedLabel') }}</p><p class="mt-2 text-2xl font-semibold">{{ overview.failed }} / {{ overview.stale }} / {{ overview.unsupported }}</p></article>
        <article class="card p-4"><p class="text-xs text-gray-500">{{ t('admin.accountOps.openUpstreamAlerts') }}</p><p class="mt-2 text-2xl font-semibold">{{ overview.open_alerts }}</p></article>
      </section>
      <section class="card overflow-x-auto p-5"><h2 class="text-base font-semibold">{{ t('admin.accountOps.balancesByCurrency') }}</h2><div class="mt-3 flex flex-wrap gap-3"><span v-for="c in overview?.currencies??[]" :key="c.unit" class="border-r pr-4"><strong class="tabular-nums">{{ money(c.total,c.unit) }}</strong> <small class="text-gray-500">{{ c.accounts }} {{ t('admin.accountOps.accountsLabel') }}</small></span><span v-if="!overview?.currencies.length" class="text-sm text-gray-500">{{ t('admin.accountOps.noFreshBalances') }}</span></div></section>
      <section class="card overflow-x-auto"><div class="flex flex-wrap items-center gap-3 border-b p-4"><h2 class="mr-auto text-base font-semibold">{{ t('admin.accountOps.accountSnapshots') }}</h2><input v-model="search" class="input w-52" :placeholder="t('admin.accountOps.searchAccounts')" /><select v-model="status" class="input w-36"><option value="all">{{ t('admin.accountOps.allStates') }}</option><option value="ok">{{ t('admin.accountOps.fresh') }}</option><option value="stale">{{ t('admin.accountOps.stale') }}</option><option value="failed">{{ t('admin.accountOps.failed') }}</option><option value="unknown">{{ t('admin.accountOps.unknown') }}</option></select></div>
        <table class="w-full min-w-[900px] text-left text-sm"><thead class="border-b bg-gray-50 text-xs text-gray-500"><tr><th class="p-3">{{ t('admin.accountOps.account') }}</th><th class="p-3">{{ t('admin.accountOps.platform') }}</th><th class="p-3">{{ t('admin.accountOps.balance') }}</th><th class="p-3">{{ t('admin.accountOps.quotaWindows') }}</th><th class="p-3">{{ t('admin.accountOps.observedFreshUntil') }}</th><th class="p-3">{{ t('admin.accountOps.rate') }}</th></tr></thead><tbody><tr v-for="a in filtered" :key="a.account_id" class="border-b"><td class="p-3">{{ a.name }} <small class="text-gray-500">#{{ a.account_id }}</small></td><td class="p-3">{{ a.platform }}</td><td class="p-3"><template v-if="a.balance_status==='ok' && a.balance!==null">{{ money(a.balance,a.unit) }}</template><span v-else class="text-gray-500">{{ labelStatus(a.balance_status) }}</span></td><td class="p-3"><span v-if="a.usage?.windows?.length">{{ a.usage.windows.map(w=>`${w.window}: ${w.remaining_pct.toFixed(1)}%`).join(' · ') }}</span><span v-else class="text-gray-500">{{ a.usage?.status==='ok'?t('admin.accountOps.noQuotaWindow'):labelStatus(a.balance_status) }}</span><small v-if="a.usage?.expires_at" class="block text-gray-500">{{ t('admin.accountOps.expires') }} {{ date(a.usage.expires_at) }}</small></td><td class="p-3 text-xs text-gray-500">{{ date(a.balance_read_at) }}<br>{{ date(a.balance_fresh_until) }}</td><td class="p-3">{{ a.rate_status==='ok'&&a.rate!==null?`× ${a.rate.toFixed(4)}`:labelStatus(a.rate_status||'unknown') }}</td></tr><tr v-if="!filtered.length"><td colspan="6" class="p-8 text-center text-sm text-gray-500">{{ busy?t('admin.accountOps.loading'):t('admin.accountOps.noMatchingAccounts') }}</td></tr></tbody></table>
        <div class="flex items-center justify-between border-t p-3 text-sm"><span>{{ total }} {{ t('admin.accountOps.accountsCount') }}</span><div class="flex items-center gap-2"><button class="btn btn-secondary" :disabled="page<=1||busy" @click="page--;loadAccounts()">{{ t('admin.accountOps.previous') }}</button><span>{{ page }} / {{ pages }}</span><button class="btn btn-secondary" :disabled="page>=pages||busy" @click="page++;loadAccounts()">{{ t('admin.accountOps.next') }}</button></div></div>
      </section>
      <section class="card overflow-x-auto p-5"><h2 class="text-base font-semibold">{{ t('admin.accountOps.recentUpstreamAlerts') }}</h2><table class="mt-3 w-full min-w-[680px] text-left text-sm"><thead class="border-b text-xs text-gray-500"><tr><th class="p-2">{{ t('admin.accountOps.account') }}</th><th class="p-2">{{ t('admin.accountOps.kind') }}</th><th class="p-2">{{ t('admin.accountOps.level') }}</th><th class="p-2">{{ t('admin.accountOps.time') }}</th><th class="p-2"></th></tr></thead><tbody><tr v-for="a in alerts" :key="a.id" class="border-b"><td class="p-2">{{ a.name }} #{{ a.account_id }}</td><td class="p-2">{{ a.kind }}</td><td class="p-2">{{ a.level }}</td><td class="p-2 text-xs">{{ date(a.created_at) }}</td><td class="p-2 text-right"><button v-if="!a.acked_at" class="btn btn-secondary" @click="ack(a.id)">{{ t('admin.accountOps.acknowledge') }}</button><span v-else class="text-xs text-gray-500">{{ t('admin.accountOps.acknowledged') }}</span></td></tr><tr v-if="!alerts.length"><td colspan="5" class="p-5 text-center text-sm text-gray-500">{{ t('admin.accountOps.noAlerts') }}</td></tr></tbody></table></section>
      <section class="card overflow-x-auto p-5"><h2 class="text-base font-semibold">{{ t('admin.accountOps.rateChanges') }}</h2><table class="mt-3 w-full min-w-[500px] text-left text-sm"><tbody><tr v-for="r in rates" :key="r.id" class="border-b"><td class="p-2">{{ r.name }} #{{ r.account_id }}</td><td class="p-2">× {{ r.old_rate.toFixed(4) }} → × {{ r.new_rate.toFixed(4) }}</td><td class="p-2">{{ r.change_pct.toFixed(1) }}%</td><td class="p-2 text-xs">{{ date(r.changed_at) }}</td></tr><tr v-if="!rates.length"><td class="p-5 text-center text-sm text-gray-500">{{ t('admin.accountOps.noRateChanges') }}</td></tr></tbody></table></section>
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
const { t } = useI18n();
const overview = ref<UpstreamMonitorOverview | null>(null), accounts = ref<UpstreamMonitorAccount[]>([]), alerts = ref<UpstreamAlert[]>([]), rates = ref<UpstreamRateChange[]>([]), error = ref(''), busy = ref(false), sampling = ref(false), search = ref(''), status = ref('all'), page = ref(1), total = ref(0), pages = ref(1);
let generation = 0;
let alive = true;
let controller: AbortController | null = null;
const filtered = computed(() => accounts.value.filter(a => (status.value === 'all' || a.balance_status === status.value) && (!search.value || `${a.name} ${a.account_id} ${a.platform}`.toLowerCase().includes(search.value.trim().toLowerCase()))));
const date = (v: string | null | undefined) => v ? new Date(v).toLocaleString() : '-';
const money = (n: number, u: string) => `${n.toLocaleString(undefined, { maximumFractionDigits: 4 })} ${u || t('admin.accountOps.unspecifiedUnit')}`;
const labelStatus = (s: string) => ({ ok: t('admin.accountOps.fresh'), stale: t('admin.accountOps.unknownStale'), failed: t('admin.accountOps.unknownFailed'), unsupported: t('admin.accountOps.unsupported'), unknown: t('admin.accountOps.unknown') }[s] || s);
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
    if (alive && id === generation && !signal.aborted)
        error.value = e instanceof Error ? e.message : t('admin.accountOps.monitorLoadError');
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
        error.value = e instanceof Error ? e.message : t('admin.accountOps.samplingFailed');
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
        error.value = e instanceof Error ? e.message : t('admin.accountOps.acknowledgeError');
} }
let filterTimer: ReturnType<typeof setTimeout> | undefined;
watch([search, status], () => { page.value = 1; clearTimeout(filterTimer); filterTimer = setTimeout(() => { if (alive)
    void refresh(); }, 250); });
onMounted(refresh);
onBeforeUnmount(() => { alive = false; generation++; controller?.abort(); clearTimeout(filterTimer); });
</script>
