package service

import (
	"context"
	"errors"
)

// The legacy OAuth success CAS compares the entire credential document, so even
// a tier-only write must serialize with refresh until the old workers drain.
// Acquire only after settings completes; never hold this lock across that HTTP
// request. A lock failure must defer observation, not degrade to an unsafe write.
func (s *GrokQuotaService) acquireGrokSubscriptionRefreshLock(ctx context.Context, account *Account) (func(), error) {
	if s.tokenProvider == nil || s.tokenProvider.tokenCache == nil {
		// Preserve the existing explicitly cache-less mode used by standalone
		// services/tests. Production DI supplies the shared OAuth token cache.
		return func() {}, nil
	}
	cache := s.tokenProvider.tokenCache
	key := GrokTokenCacheKey(account)
	acquired, err := cache.AcquireRefreshLock(ctx, key, defaultRefreshLockTTL)
	if err != nil {
		return nil, errors.New("refresh_lock_unavailable")
	}
	if !acquired {
		return nil, errors.New("refresh_lock_busy")
	}
	// Reuse OAuth's detached, bounded release and its existing cache protocol.
	lock := &OAuthRefreshAPI{tokenCache: cache}
	return func() { lock.releaseRefreshLock(ctx, key) }, nil
}
