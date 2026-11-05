package service

import (
	"context"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type AdminAPIKeyConcurrencyManager interface {
	AdminSetAPIKeyConcurrency(context.Context, int64, int) (*APIKey, error)
}

func (s *adminServiceImpl) AdminSetAPIKeyConcurrency(ctx context.Context, keyID int64, limit int) (*APIKey, error) {
	if limit < 0 || limit > 2147483647 {
		return nil, infraerrors.BadRequest("API_KEY_LIMIT_INVALID", "Key concurrency must be non-negative")
	}
	key, err := s.apiKeyRepo.GetByID(ctx, keyID)
	if err != nil {
		return nil, err
	}
	key.ConcurrencyLimit = limit
	if err = s.apiKeyRepo.Update(ctx, key, APIKeyUpdateFields{ConcurrencyLimit: true}); err != nil {
		return nil, err
	}
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByKey(ctx, key.Key)
	}
	return key, nil
}
