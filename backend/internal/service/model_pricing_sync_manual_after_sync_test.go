//go:build unit

package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestModelPricingSync_ManualEditAfterPriorSyncSurvives covers an ordering that
// the other sync tests miss: a model was already synced once, and only afterwards
// did an operator set a price by hand.
//
// Every other test builds the service with an EMPTY synced-fields history, so a
// non-zero field is trivially classified as manual. In production the opposite
// order is the common one — the sync writes a model, weeks pass, then someone
// deliberately re-prices it. Under the old rule ("field name appears in sync
// history => sync owns it") the next tick silently overwrote that manual price.
//
// The repository now returns the last value the sync wrote, so ownership is
// decided by comparing values: equal means untouched by anyone else, different
// means an operator edited it and it must be left alone.
func TestModelPricingSync_ManualEditAfterPriorSyncSurvives(t *testing.T) {
	const model = "gpt-5.6-sol"

	// What the sync wrote last time (registry price for this model is 4e-06).
	const lastSynced = 4e-06
	// What an operator set by hand afterwards.
	const manuallySet = 9e-06

	overrideJSON := `{"` + model + `": {"input_cost_per_token": 9e-06}}`
	path := filepath.Join(t.TempDir(), "overrides.json")
	require.NoError(t, os.WriteFile(path, []byte(overrideJSON), 0o644))

	history := &stubModelPricingSyncHistory{
		syncedSets: map[string]map[string]float64{
			model: {"input_cost_per_token": lastSynced},
		},
	}

	svc := NewModelPricingSyncService(
		&ModelPricingSyncConfig{OverrideFile: path, Interval: 1, RegistryURL: "https://models.dev/api.json"},
		nil,
		history,
		stubModelPricingSyncFetcher{body: []byte(modelsDevRegistryJSON)},
	)

	_, err := syncLegacyForTest(svc, context.Background())
	require.NoError(t, err)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var entries map[string]map[string]float64
	require.NoError(t, json.Unmarshal(body, &entries))

	got := entries[model]["input_cost_per_token"]
	require.InDelta(t, manuallySet, got, 1e-15,
		"运营在同步写过的模型上手改的价必须保留：当前值 %v 与上次同步写入值 %v 不同，说明是人工改的", got, lastSynced)
}
