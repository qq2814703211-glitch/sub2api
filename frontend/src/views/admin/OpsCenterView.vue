<template>
 <AppLayout><div class="space-y-5">
 <AdminPageHeader :title="text('运营中心','Operations center')" :description="text('上游资产、观测质量与通知送达。','Upstream assets, observation quality and delivery status.')"><template #actions><button class="btn btn-secondary" :disabled="loading" @click="refresh">{{text('刷新','Refresh')}}</button></template></AdminPageHeader>
 <p v-if="error" role="alert" class="rounded bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950 dark:text-red-300">{{error}}</p>
 <p v-if="loading" role="status" class="text-sm text-gray-500">{{text('加载中…','Loading…')}}</p>
 <section class="grid gap-4 md:grid-cols-3">
 <article class="card p-5"><h2 class="text-sm text-gray-500">{{text('新鲜余额 / 账号','Fresh balances / accounts')}}</h2><p class="mt-2 text-2xl font-semibold">{{overview?.with_balance??'—'}} / {{overview?.total_accounts??'—'}}</p><p v-for="c in overview?.currencies??[]" :key="c.unit" class="mt-2 tabular-nums">{{c.total.toLocaleString(undefined,{maximumFractionDigits:4})}} {{c.unit||text('未标明单位','Unspecified unit')}}</p><p v-if="!overview?.currencies.length" class="mt-2 text-sm text-gray-500">{{text('暂无新鲜余额，需先在账号运营选择账号及规则。','No fresh balances. Opt accounts into probes in Account operations.')}}</p></article>
 <article class="card p-5"><h2 class="text-sm text-gray-500">{{text('失败 / 陈旧 / 不支持','Failed / stale / unsupported')}}</h2><p class="mt-2 text-2xl font-semibold">{{overview?.failed??'—'}} / {{overview?.stale??'—'}} / {{overview?.unsupported??'—'}}</p></article>
 <article class="card p-5"><h2 class="text-sm text-gray-500">{{text('最近待送达或失败事件','Recent pending or failed deliveries')}}</h2><p class="mt-2 text-2xl font-semibold">{{pending.length}}</p><p class="mt-2 text-sm text-gray-500">{{text('模块','Module')}}: {{settings?settings.config.enabled?text('启用','Enabled'):text('停用','Disabled'):'—'}}</p></article>
 </section>
 <section class="grid gap-4 md:grid-cols-2"><RouterLink to="/admin/account-ops" class="card block p-5 hover:border-primary-400"><h2 class="font-semibold">{{text('账号运营与通知','Account operations and notifications')}}</h2><p class="mt-2 text-sm text-gray-500">{{text('配置读取账号、阈值规则、通知通道，查看恢复与独立回执。','Configure opted-in accounts, thresholds and channels; inspect recovery and per-channel receipts.')}}</p></RouterLink><RouterLink to="/admin/upstream-monitor" class="card block p-5 hover:border-primary-400"><h2 class="font-semibold">{{text('上游运营看板','Upstream monitor')}}</h2><p class="mt-2 text-sm text-gray-500">{{text('按币种查看余额、额度、到期、趋势和异常。','Inspect balances, quota, expiry, trends and anomalies by currency.')}}</p></RouterLink></section>
 <section class="card p-5"><h2 class="font-semibold">{{text('需要处理','Needs attention')}}</h2><ul class="mt-3 divide-y dark:divide-dark-700"><li v-for="a in overview?.attention??[]" :key="a.account_id" class="py-3 text-sm">{{a.name}} #{{a.account_id}} · {{a.balance_status}} <RouterLink class="ml-3 text-primary-600" to="/admin/upstream-monitor">{{text('查看','Inspect')}}</RouterLink></li><li v-for="e in pending" :key="e.id||`${e.account_id}:${e.kind}`" class="py-3 text-sm">{{e.account_name}} · {{e.kind}} · {{e.state}} <RouterLink class="ml-3 text-primary-600" to="/admin/account-ops">{{text('查看回执','Inspect receipts')}}</RouterLink></li></ul><p v-if="!loading&&!pending.length&&!overview?.attention.length" class="py-5 text-center text-sm text-gray-500">{{text('当前没有需要处理的记录。','No records need attention.')}}</p></section>
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
const { locale } = useI18n();
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en;
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
        error.value = extractApiErrorMessage(e, text('加载失败', 'Load failed'));
}
finally {
    if (alive && id === sequence)
        loading.value = false;
} }
onMounted(refresh);
onBeforeUnmount(() => { alive = false; sequence++; controller?.abort(); });
</script>
