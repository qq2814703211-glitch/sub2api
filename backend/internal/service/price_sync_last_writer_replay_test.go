package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// ReplayLastWriter uses the production reader supplied by the external-package test.
func ReplayLastWriter(t *testing.T, history ModelPricingSyncHistoryRepository, root, audit string) {
	t.Helper()
	ctx := context.Background()
	var export struct {
		Overrides map[string]json.RawMessage `json:"overrides"`
	}
	require.NoError(t, json.Unmarshal(readFile(t, filepath.Join(root, "receipts/rc3-prod-pricing-history-5y90niui/history.json")), &export))
	var independent struct {
		Decisions []struct{ Model, Decision string }
	}
	require.NoError(t, json.Unmarshal(readFile(t, filepath.Join(root, "receipts/rc3-prod-pricing-history-5y90niui/independent-last-writer.json")), &independent))
	s, b, _ := legacyFixture(t, history, string(readFile(t, filepath.Join(root, "shadow/manual.json"))))
	data, err := json.Marshal(export.Overrides)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(s.cfg.LegacyOverrideFile, data, 0600))
	s.fetcher = snapshotFetcher{modelPricingSyncRegistryURL: readFile(t, filepath.Join(audit, "sources/models-dev.json")), cnyOfficialDefaultSourceURL: readFile(t, filepath.Join(audit, "sources/basellm.json"))}
	s.cfg.Collector = collectPriceSyncV2
	_, err = s.Sync(ctx)
	require.NoError(t, err)
	var blocked, pending int
	for _, d := range independent.Decisions {
		m := s.active.Models[d.Model]
		require.NotNil(t, m, d.Model)
		_, err = b.CalculateCost(d.Model, UsageTokens{OutputTokens: 1000}, 1)
		switch d.Model {
		case "glm-4.5", "glm-4.6":
			require.NoError(t, err, d.Model)
			require.True(t, m.External)
			require.NotEqual(t, "manual", m.Mode)
			require.Contains(t, m.Issue, "未核实")
			require.JSONEq(t, string(export.Overrides[d.Model]), string(s.active.Entries[d.Model]))
			pending++
		default:
			require.True(t, isTariffPriceIssue(err), d.Model)
			require.NotNil(t, m.LegacyQuarantine)
			blocked++
		}
		if d.Decision == "other_writer" {
			t.Logf("%s protected=%t blocked=%t", d.Model, m.External, isTariffPriceIssue(err))
		}
	}
	require.Equal(t, 64, blocked)
	require.Equal(t, 2, pending)
	require.Len(t, s.active.Settings.ManualOverrides, 23)
	for k := range s.active.Settings.ManualOverrides {
		require.JSONEq(t, string(export.Overrides[k]), string(s.active.Entries[k]), k)
	}
	rev := s.active.Revision
	for i := 0; i < 3; i++ {
		old := s.active
		_, err = s.Sync(ctx)
		require.NoError(t, err)
		if rev != s.active.Revision {
			n := 0
			for k, m := range s.active.Models {
				if !priceSyncEqual(m, old.Models[k]) {
					if n < 10 {
						t.Logf("changed model %s mode=%s issue_equal=%t auto_equal=%t marker_equal=%t", k, m.Mode, m.Issue == old.Models[k].Issue, priceSyncEqual(m.Auto, old.Models[k].Auto), priceSyncEqual(m.LegacyQuarantine, old.Models[k].LegacyQuarantine))
					}
					n++
				}
			}
			t.Logf("changed models=%d entries_equal=%t source_errors_equal=%t", n, priceSyncEqual(s.active.Entries, old.Entries), priceSyncEqual(s.active.SourceErrors, old.SourceErrors))
		}
		require.Equal(t, rev, s.active.Revision)
	}
	t.Logf("risk=%d blocked=%d unclassified_preserved=%d manual_preserved=%d stable_repeats=3", len(independent.Decisions), blocked, pending, len(s.active.Settings.ManualOverrides))
}
