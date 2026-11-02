package service

import "github.com/Wei-Shaw/sub2api/internal/pkg/xai"

// GrokSchedulerSubscription is a minimal cache-only identity. It is not part
// of credentials, Extra, account persistence, admin inputs, or public DTOs.
// AccountID prevents a metadata copy from transferring the observation.
type GrokSchedulerSubscription struct {
	AccountID int64  `json:"account_id"`
	Tier      string `json:"tier"`
}

// ProjectGrokSchedulerSubscription validates against the FULL account before
// credentials/proxy inputs are removed. Never trust a prior cache projection
// when generating a new one. Cache invalidation/hydration remain unchanged.
func ProjectGrokSchedulerSubscription(a *Account) *GrokSchedulerSubscription {
	if live := liveGrokSubscription(a); live != nil {
		return &GrokSchedulerSubscription{AccountID: a.ID, Tier: live.Tier}
	}
	return nil
}

// resolvedGrokSubscriptionTier gives full-account truth priority. A projection
// is usable only on redacted scheduler records. HTTP admin/import inputs never
// bind this top-level Account field, so arbitrary Extra cannot manufacture it.
func resolvedGrokSubscriptionTier(a *Account) (string, bool) {
	if live := liveGrokSubscription(a); live != nil {
		return live.Tier, true
	}
	if a == nil || !a.IsGrokOAuth() || a.GetGrokAccessToken() != "" {
		return "", false
	}
	p := a.SchedulerGrokSubscription
	if p == nil || p.AccountID != a.ID || p.Tier == "" || p.Tier != xai.NormalizeSubscriptionTier(p.Tier) {
		return "", false
	}
	return p.Tier, true
}
