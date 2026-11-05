<template>
 <AppLayout><div class="space-y-5">
 <AdminPageHeader :title="t('admin.accountOps.centerTitle')" :description="t('admin.accountOps.centerDescription')"><template #actions><button class="btn btn-secondary" :disabled="loading" @click="refresh">{{t('admin.accountOps.refresh')}}</button></template></AdminPageHeader>
 <p v-if="error" role="alert" class="rounded bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950 dark:text-red-300">{{error}}</p>
 <p v-if="loading" role="status" class="text-sm text-gray-500">{{t('admin.accountOps.loading')}}</p>
 <section class="grid gap-4 md:grid-cols-3">
 <article class="card p-5"><h2 class="text-sm text-gray-500">{{t('admin.accountOps.freshBalancesAccounts')}}</h2><p class="mt-2 text-2xl font-semibold">{{overview?.with_balance??'—'}} / {{overview?.total_accounts??'—'}}</p><p v-for="c in overview?.currencies??[]" :key="c.unit" class="mt-2 tabular-nums">{{c.total.toLocaleString(undefined,{maximumFractionDigits:4})}} {{c.unit||t('admin.accountOps.unspecifiedUnit')}}</p><p v-if="!overview?.currencies.length" class="mt-2 text-sm text-gray-500">{{t('admin.accountOps.noFreshBalancesHint')}}</p></article>
 <article class="card p-5"><h2 class="text-sm text-gray-500">{{t('admin.accountOps.failedStaleUnsupported')}}</h2><p class="mt-2 text-2xl font-semibold">{{overview?.failed??'—'}} / {{overview?.stale??'—'}} / {{overview?.unsupported??'—'}}</p></article>
 <article class="card p-5"><h2 class="text-sm text-gray-500">{{t('admin.accountOps.recentPendingOrFailedDeliveries')}}</h2><p class="mt-2 text-2xl font-semibold">{{pending.length}}</p><p class="mt-2 text-sm text-gray-500">{{t('admin.accountOps.module')}}: {{settings?settings.config.enabled?t('admin.accountOps.enabled'):t('admin.accountOps.disabled'):'—'}}</p></article>
 </section>
 <section class="grid gap-4 md:grid-cols-2"><RouterLink to="/admin/account-ops" class="card block p-5 hover:border-primary-400"><h2 class="font-semibold">{{t('admin.accountOps.accountOperationsAndNotifications')}}</h2><p class="mt-2 text-sm text-gray-500">{{t('admin.accountOps.configureAccountsHint')}}</p></RouterLink><RouterLink to="/admin/upstream-monitor" class="card block p-5 hover:border-primary-400"><h2 class="font-semibold">{{t('admin.accountOps.upstreamMonitorLabel')}}</h2><p class="mt-2 text-sm text-gray-500">{{t('admin.accountOps.inspectMonitorHint')}}</p></RouterLink></section>
 <section class="card p-5"><h2 class="font-semibold">{{t('admin.accountOps.needsAttention')}}</h2><ul class="mt-3 divide-y dark:divide-dark-700"><li v-for="a in overview?.attention??[]" :key="a.account_id" class="py-3 text-sm">{{a.name}} #{{a.account_id}} · {{a.balance_status}} <RouterLink class="ml-3 text-primary-600" to="/admin/upstream-monitor">{{t('admin.accountOps.inspect')}}</RouterLink></li><li v-for="e in pending" :key="e.id||`${e.account_id}:${e.kind}`" class="py-3 text-sm">{{e.account_name}} · {{e.kind}} · {{e.state}} <RouterLink class="ml-3 text-primary-600" to="/admin/account-ops">{{t('admin.accountOps.inspectReceipts')}}</RouterLink></li></ul><p v-if="!loading&&!pending.length&&!overview?.attention.length" class="py-5 text-center text-sm text-gray-500">{{t('admin.accountOps.noAttention')}}</p></section>
 </div></AppLayout>
</template>
<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import AppLayout from '@/components/layout/AppLayout.vue';
import AdminPageHeader from '@/components/admin/operations/OpsPageHeader.vue';
import { getOverview, type UpstreamMonitorOverview } from '@/api/admin/upstreamMonitor';
import { getAccountOpsSettings, getAccountOpsEvents, type AccountOpsSettings, type AccountOpsEvent } from '@/api/admin/accountOps';
import { extractApiErrorMessage } from '@/utils/apiError';
const { t } = useI18n();
const overview = ref<UpstreamMonitorOverview | null>(null), settings = ref<AccountOpsSettings | null>(null), events = ref<AccountOpsEvent[]>([]), error = ref(''), loading = ref(false);
const pending = computed(() => events.value.filter(e => ['pending', 'sending', 'failed'].includes(e.state)).slice(0, 8));
let alive = true, sequence = 0;
let controller: AbortController | null = null;
async function refresh() { const id = ++sequence; controller?.abort(); controller = new AbortController(); const signal = controller.signal; loading.value = true; error.value = ''; try {
    const [o, s, e] = await Promise.all([getOverview(signal), getAccountOpsSettings(signal), getAccountOpsEvents(0, signal)]);
    if (alive && id === sequence) {
        overview.value = o;
        settings.value = s;
        events.value = e.items;
    }
}
catch (e) {
    if (alive && id === sequence && !signal.aborted)
        error.value = extractApiErrorMessage(e, t('admin.accountOps.loadFailed'));
}
finally {
    if (alive && id === sequence)
        loading.value = false;
} }
onMounted(refresh);
onBeforeUnmount(() => { alive = false; sequence++; controller?.abort(); });
</script>
