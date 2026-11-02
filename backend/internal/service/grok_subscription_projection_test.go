//go:build unit

package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGrokSchedulerSubscriptionAppliesFreeSoftGate(t *testing.T) {
	a := healthyGrokQuotaOAuthAccount(9901)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	a.Extra = map[string]any{grokSubscriptionExtraKey: map[string]any{"tier": "free", "display_name": "Free", "source": "settings", "status_code": 200, "requested_at": stamp, "observed_at": stamp, "scope": grokSubscriptionScope(a)}}
	meta := &Account{ID: a.ID, Platform: a.Platform, Type: a.Type, SchedulerGrokSubscription: ProjectGrokSchedulerSubscription(a)}
	require.True(t, isExplicitGrokFreeOAuthAccount(meta), "expected=true actual=%v: live Free must survive scheduler soft-gate admission", isExplicitGrokFreeOAuthAccount(meta))
}

func TestGrokSchedulerSubscriptionCannotBeInjectedThroughExtra(t *testing.T) {
	a := healthyGrokQuotaOAuthAccount(9902)
	a.Credentials["subscription_tier"] = "free"
	a.Extra = map[string]any{"scheduler_grok_subscription": map[string]any{"account_id": a.ID, "tier": "supergrok_heavy"}, "SchedulerGrokSubscription": &GrokSchedulerSubscription{AccountID: a.ID, Tier: "supergrok_heavy"}}
	require.True(t, isKnownGrokFreeAccount(a))
	require.Nil(t, ProjectGrokSchedulerSubscription(a))
}

func TestGrokSchedulerSubscriptionRejectsCopiesAndFullAccountOverrides(t *testing.T) {
	a := &Account{ID: 9903, Platform: PlatformGrok, Type: AccountTypeOAuth, SchedulerGrokSubscription: &GrokSchedulerSubscription{AccountID: 9903, Tier: "free"}}
	require.True(t, isKnownGrokFreeAccount(a))
	copied := *a
	copied.ID++
	require.False(t, isKnownGrokFreeAccount(&copied), "copied observation belongs to another account")
	a.Credentials = map[string]any{"access_token": "new-authorization", "subscription_tier": "supergrok_heavy"}
	require.False(t, isKnownGrokFreeAccount(a), "full identity must ignore redacted-cache projection")
	require.Nil(t, ProjectGrokSchedulerSubscription(a), "reprojection must revalidate full identity")
}

func TestGrokSchedulerSubscriptionAdminInputsCannotCarryProjection(t *testing.T) {
	payload := []byte(`{"name":"injection","platform":"grok","type":"oauth","credentials":{"access_token":"new-authorization"},"scheduler_grok_subscription":{"account_id":9904,"tier":"supergrok_heavy"},"SchedulerGrokSubscription":{"account_id":9904,"tier":"free"}}`)
	var input CreateAccountInput
	require.NoError(t, json.Unmarshal(payload, &input))
	a, err := buildAccountForCreate(&input, input.Extra)
	require.NoError(t, err)
	require.Nil(t, a.SchedulerGrokSubscription)
	require.Nil(t, ProjectGrokSchedulerSubscription(a))
}
