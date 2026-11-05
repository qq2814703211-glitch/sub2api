//go:build unit

package service

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAPIKeyAuthSnapshotRoundTripPreservesRoutesAndConcurrency(t *testing.T) {
	primary, backup := int64(11), int64(22)
	apiKey := &APIKey{
		ID: 7, UserID: 8, GroupID: &primary, RouteGroupIDs: []int64{primary, backup},
		ConcurrencyLimit: 4, User: &User{ID: 8, Status: StatusActive},
	}
	svc := &APIKeyService{}

	payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: svc.snapshotFromAPIKey(context.Background(), apiKey)})
	require.NoError(t, err)
	var cached APIKeyAuthCacheEntry
	require.NoError(t, json.Unmarshal(payload, &cached))

	materialized, accepted, err := svc.applyAuthCacheEntry(apiKey.Key, &cached)
	require.NoError(t, err)
	require.True(t, accepted)
	require.Equal(t, apiKeyAuthSnapshotVersion, cached.Snapshot.Version)
	require.Equal(t, []int64{primary, backup}, materialized.RouteGroupIDs)
	require.Equal(t, 4, materialized.ConcurrencyLimit)
}

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
