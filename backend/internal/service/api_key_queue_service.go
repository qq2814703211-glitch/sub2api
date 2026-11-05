package service

import "context"

func (s *APIKeyService) APIKeyQueuePolicy() APIKeyQueuePolicy {
	if s == nil || s.concurrencyService == nil {
		return APIKeyQueuePolicy{}
	}
	return s.concurrencyService.APIKeyQueuePolicy()
}
func (s *APIKeyService) GetAPIKeyQueueStatsBatch(ctx context.Context, ids []int64) (map[int64]APIKeyQueueCounts, error) {
	if s == nil {
		return (*ConcurrencyService)(nil).GetAPIKeyQueueStatsBatch(ctx, ids)
	}
	return s.concurrencyService.GetAPIKeyQueueStatsBatch(ctx, ids)
}
