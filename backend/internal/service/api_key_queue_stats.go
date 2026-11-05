package service

import "context"

// FillAPIKeyQueueStats preserves unavailable counts instead of reporting zero.
func (s *ConcurrencyService) FillAPIKeyQueueStats(ctx context.Context, keys []APIKey) {
	ids := make([]int64, 0, len(keys))
	for i := range keys {
		keys[i].WaitingCount = nil
		keys[i].QueueStatsAvailable = false
		ids = append(ids, keys[i].ID)
	}
	stats, err := s.GetAPIKeyQueueStatsBatch(ctx, ids)
	if err != nil {
		return
	}
	for i := range keys {
		if v, ok := stats[keys[i].ID]; ok {
			n := v.Waiting
			keys[i].WaitingCount = &n
			keys[i].CurrentConcurrency = v.Active
			keys[i].QueueStatsAvailable = true
		}
	}
}
