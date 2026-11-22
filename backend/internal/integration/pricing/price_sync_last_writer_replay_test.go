package pricing_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type replaySettings struct {
	service.SettingRepository
	manual string
}

func (s replaySettings) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{service.SettingKeyPricingCNYOfficial: s.manual}, nil
}

type replaySource map[string][]byte

func (s replaySource) FetchPricingJSON(_ context.Context, url string) ([]byte, error) {
	if data, ok := s[url]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("snapshot offline: %s", url)
}
func (replaySource) FetchHashText(context.Context, string) (string, error) {
	return "", errors.New("snapshot offline")
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}

func replayLastWriter(t *testing.T, history service.ModelPricingSyncHistoryRepository, storeDB *sql.DB, root, audit string) {
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
	cfg := &config.Config{}
	cfg.Pricing.DataDir = t.TempDir()
	cfg.Pricing.OverrideFile = filepath.Join(cfg.Pricing.DataDir, "v1.json")
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Pricing.DataDir, "model_pricing.json"), []byte(`{"sentinel":{"input_cost_per_token":0.003}}`), 0600))
	data, err := json.Marshal(export.Overrides)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfg.Pricing.OverrideFile, data, 0600))
	t.Setenv("BACKGROUND_JOBS_ENABLED", "false")
	p, err := service.ProvidePricingService(cfg, nil)
	require.NoError(t, err)
	defer p.Stop()

	source := replaySource{"https://models.dev/api.json": readFile(t, filepath.Join(audit, "sources/models-dev.json")), "https://raw.githubusercontent.com/basellm/llm-metadata/main/dist/api/all.json": readFile(t, filepath.Join(audit, "sources/basellm.json"))}
	settings := service.NewSettingService(replaySettings{manual: string(readFile(t, filepath.Join(root, "shadow/manual.json")))}, cfg)
	s, err := service.ProvideModelPricingSyncService(cfg, p, history, source, settings, repository.NewPriceSyncPublishStore(storeDB))
	require.NoError(t, err)
	b := service.NewBillingService(cfg, p)
	type revision struct {
		Revision string
		Settings service.CNYOfficialPricingSettings
		Entries  map[string]json.RawMessage
		Models   map[string]*struct {
			Mode, Issue      string
			External         bool
			LegacyQuarantine *json.RawMessage `json:"legacy_quarantine"`
		}
	}
	head := func() revision {
		var raw []byte
		require.NoError(t, storeDB.QueryRow(`SELECT snapshot FROM price_sync_revisions ORDER BY seq DESC LIMIT 1`).Scan(&raw))
		var h revision
		require.NoError(t, json.Unmarshal(raw, &h))
		return h
	}
	_, err = s.Sync(ctx)
	require.NoError(t, err)
	active := head()
	var blocked, pending int
	for _, d := range independent.Decisions {
		m := active.Models[d.Model]
		require.NotNil(t, m, d.Model)
		_, err = b.CalculateCost(d.Model, service.UsageTokens{OutputTokens: 1000}, 1)
		var issue *service.TariffPriceIssue
		switch d.Model {
		case "glm-4.5", "glm-4.6":
			require.NoError(t, err, d.Model)
			require.True(t, m.External)
			require.NotEqual(t, "manual", m.Mode)
			require.Contains(t, m.Issue, "未核实")
			require.JSONEq(t, string(export.Overrides[d.Model]), string(active.Entries[d.Model]))
			pending++
		default:
			require.ErrorAs(t, err, &issue, d.Model)
			require.NotNil(t, m.LegacyQuarantine)
			blocked++
		}
		if d.Decision == "other_writer" {
			t.Logf("%s protected=%t blocked=%t", d.Model, m.External, errors.As(err, &issue))
		}
	}
	require.Equal(t, 64, blocked)
	require.Equal(t, 2, pending)
	require.Len(t, active.Settings.ManualOverrides, 23)
	for k := range active.Settings.ManualOverrides {
		require.JSONEq(t, string(export.Overrides[k]), string(active.Entries[k]), k)
	}
	rev := active.Revision
	for i := 0; i < 3; i++ {
		_, err = s.Sync(ctx)
		require.NoError(t, err)
		require.Equal(t, rev, head().Revision)
		current, err := s.PublishedSettings(ctx)
		require.NoError(t, err)
		require.Equal(t, rev, current.ActiveRevision)
	}
	t.Logf("risk=%d blocked=%d unclassified_preserved=%d manual_preserved=%d stable_repeats=3", len(independent.Decisions), blocked, pending, len(active.Settings.ManualOverrides))
}
