//go:build unit

package service

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAPIKeyConcurrencyAuthCacheRoundTrip(t *testing.T) {
	for _, limit := range []int{0, 1, 500} {
		svc := &APIKeyService{}
		snapshot := svc.snapshotFromAPIKey(context.Background(), &APIKey{ID: 7, ConcurrencyLimit: limit, User: &User{ID: 8, Status: StatusActive, Concurrency: 0}})
		raw, err := json.Marshal(APIKeyAuthCacheEntry{Snapshot: snapshot})
		require.NoError(t, err)
		var entry APIKeyAuthCacheEntry
		require.NoError(t, json.Unmarshal(raw, &entry))
		key, accepted, err := svc.applyAuthCacheEntry("synthetic", &entry)
		require.NoError(t, err)
		require.True(t, accepted)
		require.Equal(t, limit, key.ConcurrencyLimit)
		require.Zero(t, key.User.Concurrency)
		entry.Snapshot.Version = 25
		_, accepted, err = svc.applyAuthCacheEntry("synthetic", &entry)
		require.NoError(t, err)
		require.False(t, accepted)
	}
}
