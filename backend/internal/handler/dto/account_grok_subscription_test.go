package dto

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAccountFromServiceDropsMismatchedGrokSubscription(t *testing.T) {
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	a := &service.Account{ID: 1, Platform: "grok", Type: "oauth", Credentials: map[string]any{"access_token": "new-identity", "subscription_tier": "free"}, Extra: map[string]any{"keep": true, "subscription_tier": "free", "grok_subscription_snapshot": map[string]any{"tier": "free", "display_name": "Free", "source": "settings", "status_code": 200, "requested_at": stamp, "observed_at": stamp, "scope": "old-identity"}}}
	got := AccountFromServiceShallow(a)
	require.NotContains(t, got.Extra, "grok_subscription_snapshot")
	require.NotContains(t, got.Extra, "subscription_tier")
	require.NotContains(t, got.Credentials, "subscription_tier")
	require.Equal(t, true, got.Extra["keep"])
	require.Contains(t, a.Extra, "grok_subscription_snapshot", "DTO must not mutate durable account")
}
