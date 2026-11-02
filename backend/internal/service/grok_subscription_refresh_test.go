//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
	"time"
)

func TestGrokLiveSubscriptionResolverOverridesLegacySources(t *testing.T) {
	for _, display := range []string{"Free", "SuperGrok Heavy", "Ultra", "Future Platinum"} {
		t.Run(display, func(t *testing.T) {
			a := healthyGrokQuotaOAuthAccount(7102)
			a.Credentials["access_token"] = makeGrokOAuthJWT(map[string]any{"tier": 5})
			a.Credentials["subscription_tier"] = "supergrok_heavy"
			stamp := time.Now().UTC().Format(time.RFC3339Nano)
			a.Extra = map[string]any{"subscription_tier": "free", "grok_subscription_snapshot": map[string]any{"tier": xai.NormalizeSubscriptionTier(display), "display_name": display, "source": "settings", "requested_at": stamp, "observed_at": stamp, "status_code": 200, "scope": grokSubscriptionScope(a)}}
			usage := (&GrokQuotaFetcher{}).BuildUsageInfo(a)
			require.Equal(t, xai.NormalizeSubscriptionTier(display), usage.SubscriptionTier)
			require.Equal(t, display, usage.SubscriptionTierRaw)
			require.Equal(t, display == "Free", isKnownGrokFreeAccount(a))
			require.Equal(t, display == "Free", isExplicitGrokFreeOAuthAccount(a))
		})
	}
}

func TestGrokSubscriptionPlanAloneIsNotPaidQuota(t *testing.T) {
	for _, plan := range []string{"free", "supergrok_heavy", "ultra"} {
		require.False(t, grokBillingHasAuthoritativeQuota(&xai.BillingSummary{Plan: plan}))
	}
}

// This regression compiles unchanged on the production baseline. A fresh token
// without a tier claim must not leave the old Heavy label after live settings.
func TestGrokLiveSubscriptionRefreshNoTierJWTReplacesStoredHeavy(t *testing.T) {
	account := healthyGrokQuotaOAuthAccount(7101)
	account.Credentials["access_token"] = makeGrokOAuthJWT(map[string]any{"sub": "subscription-regression"})
	account.Credentials["subscription_tier"] = "supergrok_heavy"
	repo := &subscriptionRefreshRepo{grokQuotaAccountRepo: &grokQuotaAccountRepo{mockAccountRepoForPlatform: &mockAccountRepoForPlatform{accountsByID: map[int64]*Account{account.ID: account}}}}
	upstream := &subscriptionRefreshUpstream{body: `{"subscription_tier_display":"Free"}`}
	svc := NewGrokQuotaService(repo, nil, NewGrokTokenProvider(repo, nil), upstream, nil)
	_, err := svc.ProbeBilling(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, "free", account.GetCredential("subscription_tier"), "live settings must replace stale Heavy even when the fresh JWT has no tier")
	raw, err := json.Marshal(account.Extra["grok_subscription_snapshot"])
	require.NoError(t, err)
	require.Contains(t, string(raw), `"display_name":"Free"`)
	require.Contains(t, string(raw), `"source":"settings"`)
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	require.Equal(t, 1, upstream.settingsCalls)
	require.Zero(t, upstream.posts)
}

func TestGrokLiveSubscriptionRefreshTokenCannotRestoreHeavy(t *testing.T) {
	a := healthyGrokQuotaOAuthAccount(7110)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	a.Extra = map[string]any{"grok_subscription_snapshot": map[string]any{"tier": "free", "display_name": "Free", "source": "settings", "requested_at": stamp, "observed_at": stamp, "status_code": 200, "scope": grokSubscriptionScope(a)}}
	a.Credentials["subscription_tier"] = "free"
	svc := NewGrokOAuthService(nil, &grokOAuthClientStub{refreshResponse: &xai.TokenResponse{AccessToken: makeGrokOAuthJWT(map[string]any{"tier": 5}), ExpiresIn: 3600}})
	defer svc.Stop()
	info, err := svc.RefreshAccountToken(context.Background(), a)
	require.NoError(t, err)
	require.Equal(t, "free", info.SubscriptionTier)
}

func TestGrokLiveSubscriptionFreshBillingStillRefreshesSubscription(t *testing.T) {
	a := healthyGrokQuotaOAuthAccount(7111)
	a.Credentials["subscription_tier"] = "supergrok_heavy"
	a.Extra = map[string]any{grokBillingExtraKey: &xai.BillingSummary{UpdatedAt: time.Now().UTC().Format(time.RFC3339), StatusCode: 200}}
	repo := &subscriptionRefreshRepo{grokQuotaAccountRepo: &grokQuotaAccountRepo{mockAccountRepoForPlatform: &mockAccountRepoForPlatform{accountsByID: map[int64]*Account{a.ID: a}}}}
	up := &subscriptionRefreshUpstream{body: `{"subscription_tier_display":"Free"}`}
	quota := NewGrokQuotaService(repo, nil, NewGrokTokenProvider(repo, nil), up, nil)
	svc := &AccountUsageService{accountRepo: repo, grokQuotaFetcher: NewGrokQuotaFetcher(), grokQuotaService: quota}
	usage, err := svc.getGrokUsage(context.Background(), a, false)
	require.NoError(t, err)
	require.Equal(t, "free", usage.SubscriptionTier)
}

func TestGrokLiveSubscriptionRefreshIndependentFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body            string
		status, billingStatus int
		want                  string
	}{
		{"heavy", `{"subscription_tier_display":"SuperGrok Heavy"}`, 200, 200, "supergrok_heavy"},
		{"ultra", `{"subscription_tier_display":"Ultra"}`, 200, 200, "ultra"},
		{"unknown", `{"subscription_tier_display":"Future Platinum"}`, 200, 200, "future_platinum"},
		{"billing_failed", `{"subscription_tier_display":"Free"}`, 200, 403, "free"},
		{"unauthorized", `{"subscription_tier_display":"Free"}`, 401, 200, "supergrok_heavy"},
		{"forbidden", `{"subscription_tier_display":"Free"}`, 403, 200, "supergrok_heavy"},
		{"missing", `{}`, 200, 200, "supergrok_heavy"},
		{"blank", `{"subscription_tier_display":"  "}`, 200, 200, "supergrok_heavy"},
		{"malformed", `{`, 200, 200, "supergrok_heavy"},
		{"timeout", ``, -1, 200, "supergrok_heavy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := healthyGrokQuotaOAuthAccount(7112)
			a.Credentials["subscription_tier"] = "supergrok_heavy"
			repo := &subscriptionRefreshRepo{grokQuotaAccountRepo: &grokQuotaAccountRepo{mockAccountRepoForPlatform: &mockAccountRepoForPlatform{accountsByID: map[int64]*Account{a.ID: a}}}}
			up := &subscriptionRefreshUpstream{body: tc.body, status: tc.status, billingStatus: tc.billingStatus}
			svc := NewGrokQuotaService(repo, nil, NewGrokTokenProvider(repo, nil), up, nil)
			_, err := svc.ProbeBilling(context.Background(), a.ID)
			if tc.billingStatus >= 400 {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.want, a.GetCredential("subscription_tier"))
			if tc.want != "supergrok_heavy" || tc.name == "heavy" {
				require.NotNil(t, a.Extra[grokSubscriptionExtraKey])
			} else {
				require.Nil(t, a.Extra[grokSubscriptionExtraKey])
			}
		})
	}
}

func TestGrokLiveSubscriptionFreeKeeps24hDespiteBillingUsage(t *testing.T) {
	a := healthyGrokQuotaOAuthAccount(7141)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	percent := 100.0
	a.Extra = map[string]any{grokSubscriptionExtraKey: map[string]any{"tier": "free", "display_name": "Free", "source": "settings", "requested_at": stamp, "observed_at": stamp, "status_code": 200, "scope": grokSubscriptionScope(a)}, grokBillingExtraKey: &xai.BillingSummary{UsagePercent: &percent, StatusCode: 200, UpdatedAt: stamp}}
	usageRepo := &grokQuotaUsageLogRepo{}
	svc := &AccountUsageService{grokQuotaFetcher: NewGrokQuotaFetcher(), usageLogRepo: usageRepo}
	usage, err := svc.getGrokUsage(context.Background(), a, false)
	require.NoError(t, err)
	require.Equal(t, "free", usage.SubscriptionTier)
	require.Len(t, usageRepo.startTimes, 1)
	require.WithinDuration(t, time.Now().UTC().Add(-24*time.Hour), usageRepo.startTimes[0], time.Second)
	require.Nil(t, usage.SevenDay)
}

func TestGrokLiveSubscriptionScopeIncludesEffectiveHeaders(t *testing.T) {
	a := healthyGrokQuotaOAuthAccount(7140)
	a.Credentials["header_overrides"] = map[string]any{"x-account": "other"}
	a.Credentials["header_override_enabled"] = false
	disabled := grokSubscriptionScope(a)
	a.Credentials["header_override_enabled"] = true
	require.NotEqual(t, disabled, grokSubscriptionScope(a))
}

func TestGrokLiveSubscriptionScopeRejectsReauthorization(t *testing.T) {
	a := healthyGrokQuotaOAuthAccount(7130)
	a.Credentials["access_token"] = makeGrokOAuthJWT(map[string]any{"sub": "owner-a", "iss": "issuer", "tier": 5})
	repo := &subscriptionRefreshRepo{grokQuotaAccountRepo: &grokQuotaAccountRepo{mockAccountRepoForPlatform: &mockAccountRepoForPlatform{accountsByID: map[int64]*Account{a.ID: a}}}}
	svc := NewGrokQuotaService(repo, nil, NewGrokTokenProvider(repo, nil), &subscriptionRefreshUpstream{body: `{"subscription_tier_display":"Free"}`}, nil)
	_, err := svc.ProbeBilling(context.Background(), a.ID)
	require.NoError(t, err)
	require.Equal(t, "free", (&GrokQuotaFetcher{}).BuildUsageInfo(a).SubscriptionTier)
	a.Credentials["access_token"] = makeGrokOAuthJWT(map[string]any{"sub": "owner-a", "iss": "issuer", "tier": 5, "iat": 2})
	require.Equal(t, "free", (&GrokQuotaFetcher{}).BuildUsageInfo(a).SubscriptionTier, "same subject token rotation preserves settings")
	a.Credentials["access_token"] = makeGrokOAuthJWT(map[string]any{"sub": "owner-b", "iss": "issuer", "tier": 5})
	require.Equal(t, "supergrok_heavy", (&GrokQuotaFetcher{}).BuildUsageInfo(a).SubscriptionTier, "old settings must not bind to reauthorized identity")
}

// Narrow persistence capability signature intentionally uses baseline types.
type subscriptionRefreshRepo struct {
	*grokQuotaAccountRepo
	mu sync.Mutex
}

func (r *subscriptionRefreshRepo) GetByID(ctx context.Context, id int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a := r.accountsByID[id]
	if a == nil {
		return nil, ErrAccountNotFound
	}
	data, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	var copy Account
	err = json.Unmarshal(data, &copy)
	return &copy, err
}
func (r *subscriptionRefreshRepo) UpdateExtra(ctx context.Context, id int64, values map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.grokQuotaAccountRepo.UpdateExtra(ctx, id, values)
}

func (r *subscriptionRefreshRepo) UpdateGrokSubscriptionIfUnchanged(ctx context.Context, id int64, credentials map[string]any, proxyID *int64, snapshot map[string]any, proxy *Proxy) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a := r.accountsByID[id]
	a.Credentials["subscription_tier"] = snapshot["tier"]
	if a.Extra == nil {
		a.Extra = map[string]any{}
	}
	a.Extra["subscription_tier"] = snapshot["tier"]
	a.Extra["grok_subscription_snapshot"] = snapshot
	return true, nil
}

type subscriptionRefreshUpstream struct {
	httpUpstreamRecorder
	mu                   sync.Mutex
	body                 string
	status               int
	billingStatus        int
	settingsCalls, posts int
}

func (u *subscriptionRefreshUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	body := `{"config":{"monthlyLimit":0}}`
	status := 200
	if u.billingStatus != 0 {
		status = u.billingStatus
	}
	if req.Method == http.MethodPost {
		u.posts++
	}
	if strings.HasSuffix(req.URL.Path, "/settings") {
		u.settingsCalls++
		body = u.body
		status = 200
		if u.status == -1 {
			return nil, context.DeadlineExceeded
		}
		if u.status != 0 {
			status = u.status
		}
	}
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
}
