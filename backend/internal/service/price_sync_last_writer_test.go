package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPriceSyncLastWriterStableRevision(t *testing.T) {
	ctx := context.Background()
	s, _, _ := legacyFixture(t, legacyHistoryStub{bySource: map[string]map[string]map[string]float64{
		ModelPricingSyncSourceCNYOfficial: {"legacy-est": {"input_cost_per_token": 1.5e-7, "output_cost_per_token": 6e-7, "cache_read_input_token_cost": 3e-8, cnyOfficialFieldThreshold: 128000, cnyOfficialFieldInputMult: 2, cnyOfficialFieldOutputMult: 1.5}},
	}}, `{"enabled":true}`)
	s.cfg.Collector = func(context.Context, modelPricingSyncFetcher, string, *CNYOfficialPricingSettings) (PriceSyncCollection, error) {
		return legacyAutomaticSupply(), nil
	}
	_, err := s.Sync(ctx)
	require.NoError(t, err)
	first := s.active.Revision
	for i := 0; i < 3; i++ {
		_, err = s.Sync(ctx)
		require.NoError(t, err)
		require.Equal(t, first, s.active.Revision, "identical evidence and final diagnosis must not append a revision")
	}
}

type countingLatestHistory struct {
	legacyHistoryStub
	calls, full int
	last        []string
	rows        []ModelPricingSyncHistory
}

func (h *countingLatestHistory) ListLatestPricingFields(_ context.Context, keys []string) ([]ModelPricingSyncHistory, error) {
	h.calls++
	h.last = append([]string(nil), keys...)
	return h.rows, nil
}
func (h *countingLatestHistory) ListSyncedFields(context.Context, string) (map[string]map[string]float64, error) {
	h.full++
	return nil, nil
}

func TestPriceSyncLastWriterControls(t *testing.T) {
	for _, tc := range []struct {
		name, source, reason string
		amount               float64
		owned, blocked       bool
	}{
		{"late-same-value-manual", ModelPricingSyncSourceCNYOfficial, "按运营手工配置的人民币价写入（manual 优先级最高）", 1.5e-7, false, true},
		{"late-same-value-USD", ModelPricingSyncSourceModelsDev, modelPricingSyncReasonDrift, 1.5e-7, true, true},
		{"different-selected-vendor", ModelPricingSyncSourceCNYOfficial, cnyOfficialReason("other", 0), 1.5e-7, false, true},
		{"changed-amount", ModelPricingSyncSourceCNYOfficial, cnyOfficialReason("vendor", 0), 2e-7, false, true},
		{"unknown-reason", ModelPricingSyncSourceModelsDev, "", 1.5e-7, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h := &countingLatestHistory{}
			rows, _ := (legacyHistoryStub{bySource: map[string]map[string]map[string]float64{ModelPricingSyncSourceCNYOfficial: {"legacy-est": {"input_cost_per_token": 1.5e-7, "output_cost_per_token": 6e-7, "cache_read_input_token_cost": 3e-8, cnyOfficialFieldThreshold: 128000, cnyOfficialFieldInputMult: 2, cnyOfficialFieldOutputMult: 1.5}}}}).ListLatestPricingFields(ctx, []string{"legacy-est"})
			for i := range rows {
				if rows[i].Field == "input_cost_per_token" {
					rows[i].Source = tc.source
					rows[i].Reason = tc.reason
					rows[i].NewValue = tc.amount
				}
			}
			h.rows = rows
			s, b, _ := legacyFixture(t, h, `{"enabled":true,"manual_overrides":{"legacy-manual":{"input":13,"output":68}}}`)
			_, err := s.PublishedSettings(ctx)
			require.NoError(t, err)
			h.calls = 0
			s.cfg.Collector = func(context.Context, modelPricingSyncFetcher, string, *CNYOfficialPricingSettings) (PriceSyncCollection, error) {
				return legacyAutomaticSupply(), nil
			}
			_, err = s.Sync(ctx)
			require.NoError(t, err)
			require.Equal(t, 1, h.calls)
			require.Zero(t, h.full)
			require.NotContains(t, h.last, "legacy-manual")
			require.Equal(t, !tc.owned, s.active.Models["legacy-est"].External)
			_, err = b.CalculateCost("legacy-est", UsageTokens{OutputTokens: 1000}, 1)
			require.Equal(t, tc.blocked, isTariffPriceIssue(err))
			publishSupply(s, publishQuote("legacy-est", 40, 5, "valid-native"))
			_, err = s.Sync(ctx)
			require.NoError(t, err)
			_, err = b.CalculateCost("legacy-est", UsageTokens{OutputTokens: 1000}, 1)
			if tc.owned {
				require.NoError(t, err)
			} else {
				require.True(t, isTariffPriceIssue(err))
				require.Nil(t, s.active.Models["legacy-est"].Effective)
			}
		})
	}
}
