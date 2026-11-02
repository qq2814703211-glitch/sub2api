//go:build unit

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Build the real scope from full identity, including proxy secrets. None of
// those inputs may be needed by, or copied into, candidate metadata.
func schedulerLiveGrokAccount(t *testing.T, tier, display, billingPlan string) service.Account {
	t.Helper()
	proxyID := int64(81)
	a := service.Account{ID: 8801, Platform: service.PlatformGrok, Type: service.AccountTypeOAuth,
		Status: service.StatusActive, Schedulable: true, ProxyID: &proxyID,
		Proxy:       &service.Proxy{Protocol: "http", Host: "proxy.example", Port: 8080, Username: "proxy-user-secret", Password: "proxy-password-secret"},
		Credentials: map[string]any{"access_token": "opaque-token-secret", "refresh_token": "refresh-secret", "subscription_tier": billingPlan},
		Extra:       map[string]any{"grok_billing_snapshot": map[string]any{"plan": billingPlan, "status_code": 200}},
	}
	data, err := json.Marshal([]any{a.ID, a.Platform, a.Type, a.GetGrokAccessToken(), "", a.GetGrokBaseURL(), []any{proxyID, a.Proxy.Protocol, a.Proxy.Host, a.Proxy.Port, a.Proxy.Username, a.Proxy.Password}, a.GetHeaderOverrides()})
	require.NoError(t, err)
	digest := sha256.Sum256(data)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	a.Extra["grok_subscription_snapshot"] = map[string]any{"tier": tier, "display_name": display, "source": "settings", "observed_at": stamp, "requested_at": stamp, "status_code": 200, "scope": hex.EncodeToString(digest[:])}
	require.True(t, service.GrokSubscriptionSnapshotValid(&a), "fixture must exercise verified full-account identity")
	return a
}

func TestSchedulerMetadataGrokLiveFreeBlocksStalePaid(t *testing.T) {
	a := schedulerLiveGrokAccount(t, "free", "Free", "supergrok_heavy")
	require.False(t, a.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilityGrokMediaGeneration))
	meta := buildSchedulerMetadataAccount(a)
	require.False(t, meta.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilityGrokMediaGeneration))
	require.NotNil(t, meta.SchedulerGrokSubscription)
	require.Equal(t, "free", meta.SchedulerGrokSubscription.Tier)
}

func TestSchedulerMetadataGrokInvalidScopeDoesNotProject(t *testing.T) {
	for _, change := range []string{"token", "proxy", "header", "account"} {
		t.Run(change, func(t *testing.T) {
			a := schedulerLiveGrokAccount(t, "supergrok_heavy", "SuperGrok Heavy", "free")
			switch change {
			case "token":
				a.Credentials["access_token"] = "different-authorization"
			case "proxy":
				a.Proxy.Password = "rotated-password"
			case "header":
				a.Credentials["header_override_enabled"] = true
				a.Credentials["header_overrides"] = map[string]any{"X-Test": "changed"}
			case "account":
				a.ID++
			}
			require.False(t, service.GrokSubscriptionSnapshotValid(&a))
			meta := buildSchedulerMetadataAccount(a)
			require.Nil(t, meta.SchedulerGrokSubscription)
			require.False(t, meta.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilityGrokMediaGeneration))
		})
	}
}

func TestSchedulerCacheGrokSubscriptionRoundTripAndDynamicUpdate(t *testing.T) {
	ctx := context.Background()
	cache := newSchedulerCacheUnit(t)
	a := schedulerLiveGrokAccount(t, "supergrok_heavy", "SuperGrok Heavy", "free")
	bucket := service.SchedulerBucket{GroupID: 17, Platform: service.PlatformGrok, Mode: service.SchedulerModeSingle}
	token, err := cache.CaptureBucketWriteToken(ctx, bucket)
	require.NoError(t, err)
	require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{a}))
	candidates, hit, err := cache.GetSnapshot(ctx, bucket)
	require.NoError(t, err)
	require.True(t, hit)
	require.Len(t, candidates, 1)
	require.True(t, candidates[0].SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilityGrokMediaGeneration))
	require.Empty(t, candidates[0].GetGrokAccessToken())
	require.Nil(t, candidates[0].Proxy)
	// A subscription refresh must update existing bucket metadata, including
	// already-warmed process snapshots, without changing membership or order.
	a = schedulerLiveGrokAccount(t, "free", "Free", "supergrok_heavy")
	require.NoError(t, cache.SetAccount(ctx, &a))
	candidates, hit, err = cache.GetSnapshot(ctx, bucket)
	require.NoError(t, err)
	require.True(t, hit)
	require.Len(t, candidates, 1)
	require.Equal(t, a.ID, candidates[0].ID)
	require.Equal(t, "free", candidates[0].SchedulerGrokSubscription.Tier)
	require.False(t, candidates[0].SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilityGrokMediaGeneration))
	// Reprojection of redacted cache state cannot establish new trust.
	reprojection := buildSchedulerMetadataAccount(*candidates[0])
	require.Nil(t, reprojection.SchedulerGrokSubscription)
}

func TestSchedulerMetadataGrokLiveHeavySurvivesCandidateFilter(t *testing.T) {
	a := schedulerLiveGrokAccount(t, "supergrok_heavy", "SuperGrok Heavy", "free")
	require.True(t, a.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilityGrokMediaGeneration))
	meta := buildSchedulerMetadataAccount(a)
	eligible, reason := meta.GrokMediaGenerationEligibility()
	require.True(t, meta.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilityGrokMediaGeneration), "expected=true actual=%v reason=%s", eligible, reason)
	payload, err := json.Marshal(meta)
	require.NoError(t, err)
	for _, secret := range []string{"opaque-token-secret", "refresh-secret", "proxy-user-secret", "proxy-password-secret", "grok_subscription_snapshot"} {
		require.NotContains(t, string(payload), secret)
	}
	var roundtrip service.Account
	require.NoError(t, json.Unmarshal(payload, &roundtrip))
	require.True(t, roundtrip.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilityGrokMediaGeneration), "JSON metadata roundtrip must preserve live identity")
}
