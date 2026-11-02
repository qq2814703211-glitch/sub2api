//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Model the shared Redis SET NX / DEL protocol, not separate per-provider locks.
type subscriptionSharedRefreshLock struct {
	GrokTokenCache
	mu              sync.Mutex
	held            map[string]bool
	acquireErr      error
	keys            []string
	ttls            []time.Duration
	releases        int
	releaseCanceled bool
	releaseBounded  bool
}

func (c *subscriptionSharedRefreshLock) AcquireRefreshLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys = append(c.keys, key)
	c.ttls = append(c.ttls, ttl)
	if c.acquireErr != nil {
		return false, c.acquireErr
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if c.held[key] {
		return false, nil
	}
	if c.held == nil {
		c.held = make(map[string]bool)
	}
	c.held[key] = true
	return true, nil
}

func (c *subscriptionSharedRefreshLock) ReleaseRefreshLock(ctx context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.releases++
	c.releaseCanceled = ctx.Err() != nil
	deadline, ok := ctx.Deadline()
	c.releaseBounded = ok && time.Until(deadline) > 0 && time.Until(deadline) <= defaultRefreshLockReleaseTimeout
	delete(c.held, key)
	return ctx.Err()
}

func (c *subscriptionSharedRefreshLock) DeleteAccessToken(context.Context, string) error { return nil }

// Preserve the old complete-credentials CAS: a tier-only change rejects new RT.
type subscriptionLegacyCASRepo struct {
	AccountRepository
	mu      sync.Mutex
	account *Account
	writes  int
	persist func(context.Context) error
}

func (r *subscriptionLegacyCASRepo) GetByID(context.Context, int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a := *r.account
	a.Credentials = shallowCopyMap(r.account.Credentials)
	a.Extra = shallowCopyMap(r.account.Extra)
	return &a, nil
}

func (r *subscriptionLegacyCASRepo) UpdateGrokOAuthCredentialsIfUnchanged(_ context.Context, _ int64, expected map[string]any, _ *int64, credentials map[string]any) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !reflect.DeepEqual(r.account.Credentials, expected) {
		return false, nil
	}
	r.account.Credentials = shallowCopyMap(credentials)
	return true, nil
}

func (r *subscriptionLegacyCASRepo) UpdateGrokSubscriptionIfUnchanged(ctx context.Context, _ int64, expected map[string]any, _ *int64, snapshot map[string]any, _ *Proxy) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writes++
	if r.persist != nil {
		if err := r.persist(ctx); err != nil {
			return false, err
		}
	}
	if r.account.GetGrokAccessToken() != expected["access_token"] {
		return false, nil
	}
	r.account.Credentials["subscription_tier"] = snapshot["tier"]
	if r.account.Extra == nil {
		r.account.Extra = make(map[string]any)
	}
	r.account.Extra[grokSubscriptionExtraKey] = snapshot
	return true, nil
}

type subscriptionBlockedRotation struct {
	entered chan struct{}
	proceed chan struct{}
}

func (*subscriptionBlockedRotation) CacheKey(a *Account) string                { return GrokTokenCacheKey(a) }
func (*subscriptionBlockedRotation) CanRefresh(*Account) bool                  { return true }
func (*subscriptionBlockedRotation) NeedsRefresh(*Account, time.Duration) bool { return true }
func (e *subscriptionBlockedRotation) Refresh(ctx context.Context, a *Account) (map[string]any, error) {
	close(e.entered)
	select {
	case <-e.proceed:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	credentials := shallowCopyMap(a.Credentials)
	credentials["access_token"] = "new-at"
	credentials["refresh_token"] = "new-rt"
	return credentials, nil
}

func newSubscriptionLockFixture() (*GrokQuotaService, *subscriptionLegacyCASRepo, *subscriptionSharedRefreshLock, *Account) {
	a := healthyGrokQuotaOAuthAccount(7190)
	a.Credentials["access_token"] = "old-at"
	a.Credentials["refresh_token"] = "old-rt"
	a.Credentials["subscription_tier"] = "supergrok_heavy"
	a.Extra = map[string]any{grokSubscriptionExtraKey: map[string]any{"tier": "supergrok_heavy", "marker": "preserve"}}
	r := &subscriptionLegacyCASRepo{account: a}
	cache := &subscriptionSharedRefreshLock{}
	s := NewGrokQuotaService(r, nil, NewGrokTokenProvider(r, cache), &subscriptionRefreshUpstream{body: `{"subscription_tier_display":"Free"}`}, nil)
	return s, r, cache, a
}

func TestGrokSubscriptionRefreshLockProtectsLegacyRotation(t *testing.T) {
	s, repo, cache, account := newSubscriptionLockFixture()
	// OAuthRefreshAPI is byte-identical to the 0.0.38 implementation; the repo
	// deliberately retains its old complete-document comparison semantics.
	legacy := NewOAuthRefreshAPI(repo, cache)
	executor := &subscriptionBlockedRotation{entered: make(chan struct{}), proceed: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := legacy.RefreshIfNeeded(ctx, account, executor, time.Minute); done <- err }()
	select {
	case <-executor.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	observation, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	err = s.refreshGrokSubscription(ctx, observation, observation.GetGrokAccessToken(), "")
	assert.Error(t, err, "busy refresh lock must defer subscription persistence")
	assert.Equal(t, "supergrok_heavy", account.GetCredential("subscription_tier"))
	assert.Equal(t, "preserve", account.Extra[grokSubscriptionExtraKey].(map[string]any)["marker"])
	assert.Zero(t, repo.writes, "green must not touch credentials while blue holds refresh lock")
	close(executor.proceed)
	require.NoError(t, <-done)
	require.Equal(t, "new-rt", account.GetGrokRefreshToken(), "legacy full credentials CAS must save the rotated RT")
	latest, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.NoError(t, s.refreshGrokSubscription(ctx, latest, latest.GetGrokAccessToken(), ""))
	require.Equal(t, "free", account.GetCredential("subscription_tier"))
	require.Equal(t, "new-rt", account.GetGrokRefreshToken())
	require.Equal(t, []string{GrokTokenCacheKey(account), GrokTokenCacheKey(account), GrokTokenCacheKey(account)}, cache.keys)
	for _, ttl := range cache.ttls {
		require.Equal(t, defaultRefreshLockTTL, ttl)
	}
	require.Equal(t, 2, cache.releases, "busy observer must never release blue's lock")
}

// Used by lock-placement tests without changing the existing upstream fixture.
type subscriptionLockInspectUpstream struct {
	HTTPUpstream
	inspect func(*http.Request)
}

func (u *subscriptionLockInspectUpstream) Do(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	u.inspect(req)
	return (&subscriptionRefreshUpstream{body: `{"subscription_tier_display":"Free"}`}).Do(req, proxy, id, concurrency)
}

func TestGrokSubscriptionRefreshLockErrorPreservesObservation(t *testing.T) {
	for _, name := range []string{"redis_error", "busy", "canceled"} {
		t.Run(name, func(t *testing.T) {
			s, repo, cache, account := newSubscriptionLockFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch name {
			case "redis_error":
				cache.acquireErr = errors.New("redis offline")
			case "busy":
				cache.held = map[string]bool{GrokTokenCacheKey(account): true}
			case "canceled":
				cancel()
			}
			err := s.refreshGrokSubscription(ctx, account, account.GetGrokAccessToken(), "")
			require.Error(t, err)
			require.Zero(t, repo.writes)
			require.Zero(t, cache.releases, "never release a lock we did not acquire")
			require.Equal(t, "supergrok_heavy", account.GetCredential("subscription_tier"))
			require.Equal(t, "preserve", account.Extra[grokSubscriptionExtraKey].(map[string]any)["marker"])
		})
	}
}

func TestGrokSubscriptionRefreshLockReleaseAfterPersistenceFailure(t *testing.T) {
	for _, name := range []string{"persistence_error", "parent_canceled", "stale_observation"} {
		t.Run(name, func(t *testing.T) {
			s, repo, cache, account := newSubscriptionLockFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			observation, err := repo.GetByID(ctx, account.ID)
			require.NoError(t, err)
			repo.persist = func(ctx context.Context) error {
				require.True(t, cache.held[GrokTokenCacheKey(account)], "persistence must execute inside shared lock")
				if name == "parent_canceled" {
					cancel()
					return ctx.Err()
				}
				if name == "persistence_error" {
					return errors.New("database unavailable")
				}
				return nil
			}
			if name == "stale_observation" {
				account.Credentials["access_token"] = "new-generation"
			}
			err = s.refreshGrokSubscription(ctx, observation, observation.GetGrokAccessToken(), "")
			require.Error(t, err)
			require.Equal(t, 1, repo.writes)
			require.Equal(t, 1, cache.releases)
			require.False(t, cache.held[GrokTokenCacheKey(account)])
			require.False(t, cache.releaseCanceled, "release must detach from canceled request")
			require.True(t, cache.releaseBounded, "release must retain existing 2s bound")
			require.Equal(t, "supergrok_heavy", account.GetCredential("subscription_tier"))
		})
	}
}

func TestGrokSubscriptionRefreshLockDoesNotCoverSettingsNetwork(t *testing.T) {
	s, repo, cache, account := newSubscriptionLockFixture()
	called := false
	s.httpUpstream = &subscriptionLockInspectUpstream{inspect: func(req *http.Request) {
		called = true
		require.Empty(t, cache.keys, "subscription must not acquire before settings HTTP returns")
		acquired, err := cache.AcquireRefreshLock(req.Context(), GrokTokenCacheKey(account), defaultRefreshLockTTL)
		require.NoError(t, err)
		require.True(t, acquired, "OAuth refresh remains free to run during settings")
		require.NoError(t, cache.ReleaseRefreshLock(req.Context(), GrokTokenCacheKey(account)))
	}}
	repo.persist = func(context.Context) error {
		require.True(t, cache.held[GrokTokenCacheKey(account)])
		return nil
	}
	require.NoError(t, s.refreshGrokSubscription(context.Background(), account, account.GetGrokAccessToken(), ""))
	require.True(t, called)
	require.Equal(t, "free", account.GetCredential("subscription_tier"))
	require.Equal(t, 2, cache.releases)
	require.False(t, cache.held[GrokTokenCacheKey(account)])
}

func TestGrokSubscriptionRefreshLockCachelessCompatibility(t *testing.T) {
	s, repo, _, account := newSubscriptionLockFixture()
	s.tokenProvider = NewGrokTokenProvider(repo, nil)
	require.NoError(t, s.refreshGrokSubscription(context.Background(), account, account.GetGrokAccessToken(), ""))
	require.Equal(t, "free", account.GetCredential("subscription_tier"))
}
