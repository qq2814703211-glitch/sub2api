package service

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type snapshotFetcher map[string][]byte

func (f snapshotFetcher) FetchPricingJSON(_ context.Context, url string) ([]byte, error) {
	if body, ok := f[url]; ok {
		return body, nil
	}
	return nil, fmt.Errorf("snapshot offline: %s", url)
}
func (f snapshotFetcher) FetchHashText(context.Context, string) (string, error) {
	return "", fmt.Errorf("snapshot offline")
}

func readJSONFile[T any](t *testing.T, path string) T {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err, path)
	var out T
	require.NoError(t, json.Unmarshal(body, &out), path)
	return out
}

// TestPriceSyncLegacySnapshotQuarantine is the full-inventory regression. It
// runs only when the frozen snapshots are provided (never in CI) and asserts
// that the quarantined set is exactly the provenance set, not a hardcoded list.
func TestPriceSyncLegacySnapshotQuarantine(t *testing.T) {
	root := os.Getenv("PRICE_SYNC_SESSION_ROOT")
	audit := os.Getenv("PRICE_SYNC_AUDIT_ROOT")
	out := os.Getenv("PRICE_SYNC_OUT")
	if root == "" || audit == "" {
		t.Skip("frozen price snapshots are not configured")
	}
	ctx := context.Background()
	overrides := readJSONFile[map[string]json.RawMessage](t, filepath.Join(root, "shadow/overrides.json"))
	manualRaw := readJSONFile[json.RawMessage](t, filepath.Join(root, "shadow/manual.json"))
	var manual CNYOfficialPricingSettings
	require.NoError(t, json.Unmarshal(manualRaw, &manual))
	groups := readJSONFile[[]struct {
		ID       int64                 `json:"id"`
		Name     string                `json:"name"`
		Platform string                `json:"platform"`
		Rate     float64               `json:"rate_multiplier"`
		Long     bool                  `json:"long_context_pricing_enabled"`
		Prices   []ChannelModelPricing `json:"model_pricing"`
	}](t, filepath.Join(root, "shadow/groups.json"))

	// The provenance set is data: exact source rows the audit matched to the
	// stored values. It is never embedded in production code.
	expected := map[string]bool{}
	f, err := os.Open(filepath.Join(audit, "cny-provenance-68.csv"))
	require.NoError(t, err)
	rows, err := csv.NewReader(f).ReadAll()
	require.NoError(t, err)
	_ = f.Close()
	header := rows[0]
	keyCol, matchCol, vendorCol := -1, -1, -1
	providers := map[string]string{}
	for i, h := range header {
		h = strings.TrimPrefix(h, "\ufeff")
		if h == "被生产规则选中的数据源厂商" {
			vendorCol = i
		}
		if h == "覆盖键" {
			keyCol = i
		}
		if h == "线上输入和输出均复现该源金额" {
			matchCol = i
		}
	}
	require.NotEqual(t, -1, keyCol)
	require.NotEqual(t, -1, matchCol)
	require.NotEqual(t, -1, vendorCol)
	for _, row := range rows[1:] {
		if row[matchCol] == "是" {
			expected[row[keyCol]] = true
			providers[row[keyCol]] = row[vendorCol]
		}
	}
	require.Len(t, expected, 66)

	// This fixture constructs vendor-owned history; it is not a production-history replay.
	history := legacyHistoryStub{providers: providers, bySource: map[string]map[string]map[string]float64{ModelPricingSyncSourceCNYOfficial: {}}}
	for key := range expected {
		fields := map[string]json.RawMessage{}
		require.NoError(t, json.Unmarshal(overrides[key], &fields), key)
		perField := map[string]float64{}
		for field, raw := range fields {
			var value float64
			if json.Unmarshal(raw, &value) == nil {
				perField[field] = value
			}
		}
		history.bySource[ModelPricingSyncSourceCNYOfficial][key] = perField
	}

	cfg := &config.Config{}
	cfg.Pricing.DataDir = t.TempDir()
	cfg.Pricing.OverrideFile = filepath.Join(cfg.Pricing.DataDir, "projection.json")
	legacyPath := filepath.Join(cfg.Pricing.DataDir, "v1.json")
	body, err := json.Marshal(overrides)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(legacyPath, body, 0600))
	base, err := os.ReadFile(filepath.Join(audit, "sources/base-mirror.json"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Pricing.DataDir, "model_pricing.json"), base, 0600))
	pricing := NewPricingService(cfg, nil)
	fetcher := snapshotFetcher{
		"https://models.dev/api.json": readFile(t, filepath.Join(audit, "sources/models-dev.json")),
		"https://raw.githubusercontent.com/basellm/llm-metadata/main/dist/api/all.json": readFile(t, filepath.Join(audit, "sources/basellm.json")),
	}
	store := &publishTestStore{}
	s := NewModelPricingSyncService(&ModelPricingSyncConfig{
		OverrideFile: cfg.Pricing.OverrideFile, LegacyOverrideFile: legacyPath, Store: store,
		SettingsReader: publishLegacyReader(string(manualRaw)), Collector: collectPriceSyncV2,
	}, pricing, history, fetcher)
	billing := NewBillingService(cfg, pricing)
	_, err = s.PublishedSettings(ctx)
	require.NoError(t, err)

	// Before the sync, the stored legacy value is charged as written. This is the
	// path the quarantine must remove only for the proven set.
	beforeCost := map[string]float64{}
	for key := range expected {
		entry := map[string]json.RawMessage{}
		require.NoError(t, json.Unmarshal(s.active.Entries[key], &entry), key)
		var output float64
		require.NoError(t, json.Unmarshal(entry["output_cost_per_token"], &output))
		cost, err := billing.CalculateCost(key, UsageTokens{OutputTokens: 1_000_000}, 1)
		require.NoError(t, err, key)
		require.InDelta(t, output*1_000_000, cost.ActualCost, 1e-9, key)
		beforeCost[key] = cost.ActualCost
	}

	applied, err := s.Sync(ctx)
	require.NoError(t, err)
	quarantined := map[string]bool{}
	issues := map[string]string{}
	for key, row := range s.active.Models {
		if row.LegacyQuarantine != nil && row.LegacyQuarantine.Status == priceSyncLegacyQuarantined {
			quarantined[key] = true
			issues[key] = row.LegacyQuarantine.Evidence
		}
	}
	require.Equal(t, len(expected), len(quarantined), "quarantined set must equal the provenance set")
	for key := range expected {
		require.True(t, quarantined[key], key)
		entry := map[string]json.RawMessage{}
		require.NoError(t, json.Unmarshal(s.active.Entries[key], &entry), key)
		_, flag := entry["cny_official"]
		require.False(t, flag, key)
		require.Contains(t, entry, priceSyncLegacyField, key)
		_, err := billing.CalculateCost(key, UsageTokens{OutputTokens: 1_000_000}, 1)
		require.Error(t, err, key)
		require.True(t, isTariffPriceIssue(err), key)
	}
	for key := range quarantined {
		require.True(t, expected[key], "unexpected quarantine: %s", key)
	}

	// Manual cards stay semantically identical and keep their operator rates.
	for key := range manual.ManualOverrides {
		require.JSONEq(t, string(overrides[key]), string(s.active.Entries[key]), key)
		price := manual.ManualOverrides[key].Output
		cost, err := billing.CalculateCost(key, UsageTokens{OutputTokens: 1_000_000}, 1)
		require.NoError(t, err, key)
		require.InDelta(t, price, cost.ActualCost, 1e-9, key)
	}

	// The Kimi group card remains a real operator price.
	var kimiGroup *Group
	for _, g := range groups {
		if g.ID == 132 {
			kimiGroup = &Group{ID: g.ID, Name: g.Name, Platform: g.Platform, RateMultiplier: g.Rate, ModelPricing: g.Prices, LongContextPricingEnabled: g.Long}
		}
	}
	require.NotNil(t, kimiGroup)
	resolver := NewModelPricingResolver(nil, billing)
	for _, key := range []string{"kimi-k3.1-pro", "kimi-k3.1-pro-highspeed"} {
		resolved := resolver.Resolve(ctx, PricingInput{Model: key, Group: kimiGroup})
		require.Equal(t, PricingSourceGroup, resolved.Source)
		cost, err := billing.CalculateCostUnified(CostInput{Ctx: ctx, Model: key, Group: kimiGroup, Tokens: UsageTokens{OutputTokens: 1_000_000}, RequestCount: 1, RateMultiplier: kimiGroup.RateMultiplier, Resolver: resolver, Resolved: resolved})
		require.NoError(t, err)
		card := matchGroupModelPricing(kimiGroup, key)
		require.NotNil(t, card)
		require.InDelta(t, *card.OutputPrice*1_000_000*kimiGroup.RateMultiplier, cost.ActualCost, 1e-9, key)
	}

	if out != "" {
		require.NoError(t, os.MkdirAll(out, 0o700))
		summary := map[string]any{
			"applied": applied, "expected": len(expected), "quarantined": len(quarantined),
			"manual_protected": len(manual.ManualOverrides), "before_cost_samples": beforeCost,
			"issue_checked": len(expected), "evidence": issues,
		}
		buf, _ := json.MarshalIndent(summary, "", "  ")
		require.NoError(t, os.WriteFile(filepath.Join(out, "snapshot-quarantine.json"), buf, 0600))
	}
	keys := make([]string, 0, len(quarantined))
	for key := range quarantined {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	t.Logf("quarantined %d of %d provenance rows; manual %d; applied %d", len(keys), len(expected), len(manual.ManualOverrides), applied)
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err, path)
	return body
}
