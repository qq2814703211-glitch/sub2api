package service

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// A new service exposes the revision produced by PublishedSettings before the
// first network collection, including when background jobs are disabled.
func TestPriceSyncBootstrapCurrencyFromLastWrite(t *testing.T) {
	for _, tc := range []struct {
		name, model, source, reason            string
		wantPending, manual, changed, disabled bool
	}{
		{name: "exact-domestic-missing", model: "glm-4.5", source: ModelPricingSyncSourceModelsDev, reason: modelPricingSyncReasonMissing, wantPending: true},
		{name: "exact-domestic-drift", model: "glm-4.6", source: ModelPricingSyncSourceModelsDev, reason: modelPricingSyncReasonDrift, wantPending: true},
		{name: "explicit-native-id", model: "hy4", source: ModelPricingSyncSourceModelsDev, reason: modelPricingSyncReasonMissing, wantPending: true},
		{name: "foreign-dollar-policy", model: "foreign-price-control", source: ModelPricingSyncSourceModelsDev, reason: modelPricingSyncReasonMissing},
		{name: "namespaced-route", model: "glm-hosted/foreign-price-control", source: ModelPricingSyncSourceModelsDev, reason: modelPricingSyncReasonMissing},
		{name: "unknown-reason", model: "glm-4.5", source: ModelPricingSyncSourceModelsDev, reason: ""},
		{name: "edited-after-sync", model: "glm-4.5", source: ModelPricingSyncSourceModelsDev, reason: modelPricingSyncReasonMissing, changed: true},
		{name: "explicit-manual", model: "glm-4.6", source: ModelPricingSyncSourceModelsDev, reason: modelPricingSyncReasonMissing, manual: true},
		{name: "cny-writer-needs-source-witness", model: "glm-4.5", source: ModelPricingSyncSourceCNYOfficial, reason: cnyOfficialReason("zhipuai", 0)},
		{name: "cny-setting-disabled", model: "glm-4.5", source: ModelPricingSyncSourceModelsDev, reason: modelPricingSyncReasonMissing, disabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			history := &countingLatestHistory{}
			rates := map[string]float64{"input_cost_per_token": 6e-7, "output_cost_per_token": 2.2e-6, "cache_read_input_token_cost": 1.1e-7}
			stored := map[string]any{"cny_official": true}
			for field, rate := range rates {
				stored[field] = rate
				if tc.changed {
					rate *= 2
				}
				history.rows = append(history.rows, ModelPricingSyncHistory{Model: tc.model, Field: field, NewValue: rate, Source: tc.source, Reason: tc.reason})
			}
			cfg := map[string]any{"enabled": !tc.disabled}
			if tc.manual {
				cfg["manual_overrides"] = map[string]any{tc.model: map[string]float64{"input": 0.6, "output": 2.2, "cache_read": 0.11}}
			}
			cfgJSON, err := json.Marshal(cfg)
			require.NoError(t, err)
			sync, billing, _ := legacyFixture(t, history, string(cfgJSON))
			rowsJSON, err := json.Marshal(map[string]any{tc.model: stored})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(sync.cfg.LegacyOverrideFile, rowsJSON, 0600))
			ctx := context.Background()
			_, err = sync.PublishedSettings(ctx)
			require.NoError(t, err)
			m := sync.active.Models[tc.model]
			require.NotNil(t, m)
			if tc.wantPending {
				for _, used := range []UsageTokens{{OutputTokens: 1_000_000}, {InputTokens: 1_000_000}, {CacheReadTokens: 1_000_000}} {
					cost, err := billing.CalculateCost(tc.model, used, 1)
					if err == nil {
						t.Fatalf("first collection has not run: %s calculated %g from a confirmed USD reference", tc.model, cost.ActualCost)
					}
					require.True(t, isTariffPriceIssue(err), "%v", err)
					require.Nil(t, cost)
				}
				require.True(t, m.NativeCNY)
				require.NotNil(t, m.LegacyQuarantine)
				require.Contains(t, m.LegacyQuarantine.Evidence, "models.dev USD")
				require.Nil(t, m.Auto)
				return
			}
			cost, err := billing.CalculateCost(tc.model, UsageTokens{OutputTokens: 1_000_000}, 1)
			require.NoError(t, err)
			require.InDelta(t, 2.2, cost.ActualCost, 1e-10)
			require.Nil(t, m.LegacyQuarantine)
			before, err := json.Marshal(stored)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(sync.active.Entries[tc.model]))
		})
	}
}
