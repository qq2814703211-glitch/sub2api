package service

import (
	"context"
	"sync"
)

type liveSlotSourceKey struct{}
type liveSlotSource struct {
	mu  sync.Mutex
	ids map[int64]string
}

func withLiveSlotSource(ctx context.Context) context.Context {
	return context.WithValue(ctx, liveSlotSourceKey{}, &liveSlotSource{ids: map[int64]string{}})
}
func recordLiveSlotSource(ctx context.Context, accountID int64, id string) {
	if s, ok := ctx.Value(liveSlotSourceKey{}).(*liveSlotSource); ok {
		s.mu.Lock()
		s.ids[accountID] = id
		s.mu.Unlock()
	}
}
func liveSlotSourceID(ctx context.Context, accountID int64) string {
	if s, ok := ctx.Value(liveSlotSourceKey{}).(*liveSlotSource); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.ids[accountID]
	}
	return ""
}
