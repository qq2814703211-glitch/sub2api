export interface GrokSubscriptionSnapshot {
  tier: string
  display_name: string
  source: 'settings'
  observed_at: string
  requested_at: string
  status_code: 200
}

// Only a successful, explicit settings observation may override legacy identity.
export function getGrokSubscriptionSnapshot(extra: Record<string, unknown> | null | undefined): GrokSubscriptionSnapshot | undefined {
  const value = extra?.grok_subscription_snapshot
  if (!value || typeof value !== 'object') return undefined
  const snapshot = value as Record<string, unknown>
  if (snapshot.source !== 'settings' || snapshot.status_code !== 200) return undefined
  for (const field of ['tier', 'display_name', 'observed_at', 'requested_at']) {
    if (typeof snapshot[field] !== 'string' || !snapshot[field].trim()) return undefined
  }
  if (!Number.isFinite(Date.parse(snapshot.observed_at as string)) ||
      !Number.isFinite(Date.parse(snapshot.requested_at as string))) return undefined
  return snapshot as unknown as GrokSubscriptionSnapshot
}
