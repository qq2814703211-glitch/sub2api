import { ref } from "vue";
import type { ProviderBalance } from "@toonflow/providers";

type BalanceSnapshot = { accountId: string; balance: ProviderBalance | null; revision: number };
type BalanceEntry = BalanceSnapshot & { id: string; label: string; configKey: string; loading: boolean; error: string };

export const providerBalances = ref<BalanceEntry[]>([]);

export function refreshProviderBalance(_providerId: string, _signal?: AbortSignal): Promise<void> {
  return Promise.resolve();
}

export function useProviderBalances() {
  providerBalances.value = [];
}
