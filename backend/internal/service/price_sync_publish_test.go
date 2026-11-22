package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type publishTestStore struct {
	mu   sync.Mutex
	raw  []byte
	fail bool
}

func (s *publishTestStore) WithPriceSyncHead(_ context.Context, f func([]byte) (string, []byte, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, next, e := f(s.raw)
	if e != nil {
		return e
	}
	if next != nil {
		if s.fail {
			return errors.New("injected commit failure")
		}
		s.raw = next
	}
	return nil
}

const publishModel = "kimi-k3.1-pro-highspeed"

type publishLegacyReader string

func (c publishLegacyReader) GetRawSettings(context.Context, []string) (map[string]string, error) {
	return map[string]string{SettingKeyPricingCNYOfficial: string(c)}, nil
}

func TestPricePublishLegacyMigration(t *testing.T) {
	manual := []byte(`{"enabled":true,"manual_overrides":{"legacy":{"input":1,"output":4,"cache_write":0}}}`)
	rows := []byte(`{"legacy":{"input_cost_per_token":0.000001,"output_cost_per_token":0.000004,"cny_official":true},"external":{"cache_creation_input_token_cost_30m":0,"metadata":"untouched"}}`)
	shadow := os.Getenv("PRICE_PUBLISH_TEST_SHADOW")
	if shadow != "" {
		var e error
		manual, e = os.ReadFile(filepath.Join(shadow, "manual.json"))
		require.NoError(t, e)
		rows, e = os.ReadFile(filepath.Join(shadow, "overrides.json"))
		require.NoError(t, e)
	}
	s, _ := publishFixture(t, &publishTestStore{})
	s.settings = publishLegacyReader(manual)
	require.NoError(t, os.WriteFile(s.cfg.LegacyOverrideFile, rows, 0600))
	before := publishConfig(t, s)
	var wantCfg CNYOfficialPricingSettings
	require.NoError(t, json.Unmarshal(manual, &wantCfg))
	var wantRows map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rows, &wantRows))
	if shadow != "" {
		require.Len(t, wantCfg.ManualOverrides, 23)
		require.Len(t, wantRows, 553)
	}
	require.Len(t, before.ManualOverrides, len(wantCfg.ManualOverrides))
	for k, p := range wantCfg.ManualOverrides {
		require.False(t, p.AutoRestore, k)
		require.JSONEq(t, string(wantRows[k]), string(s.active.Entries[k]), k)
		require.Equal(t, "manual", s.active.Models[k].Mode, k)
		require.Nil(t, s.active.Models[k].Auto, k)
	}
	s.settings = publishLegacyReader(`{"enabled":false,"manual_overrides":{}}`)
	require.NoError(t, os.WriteFile(s.cfg.LegacyOverrideFile, []byte(`{}`), 0600))
	reloaded := publishConfig(t, s)
	require.Equal(t, before.Revision, reloaded.Revision)
	require.Len(t, reloaded.ManualOverrides, len(wantCfg.ManualOverrides))
	t.Logf("protected %d manual cards, %d override rows", len(wantCfg.ManualOverrides), len(wantRows))
}

func TestPricePublishRejectsIncompleteManual(t *testing.T) {
	for _, raw := range []string{`{}`, `{"input":0}`, `{"billing_mode":"per_second"}`, `{"billing_mode":"per_image","image_output_per_image":null}`, `{"billing_mode":"per_request","per_request_price":-1}`, `{"input":1,"output":3,"cache_write_12h":-0.00001}`, `{"billing_mode":"per_second","per_second_price":1,"per_request_price":0}`} {
		s, _ := publishFixture(t, &publishTestStore{})
		c := publishConfig(t, s)
		c.ManualOverrides["x"] = publishManual(t, raw)
		_, e := s.SavePublishedSettings(context.Background(), c)
		require.ErrorIs(t, e, ErrPriceSyncInvalid, raw)
		require.Empty(t, publishConfig(t, s).ManualOverrides)
	}
}

func TestPricePublishSettingsOnlyAndWriteFailure(t *testing.T) {
	s, b := publishFixture(t, &publishTestStore{})
	v := publishConfig(t, s)
	v.ManualOverrides[publishModel] = publishManual(t, `{"input":13,"output":68,"cache_write_1h":26}`)
	v = publishSet(t, s, v)
	v = publishSet(t, s, &CNYOfficialPricingSettings{Revision: v.Revision, Enabled: true})
	require.Contains(t, v.ManualOverrides, publishModel)
	old := v.Revision
	file := s.cfg.OverrideFile
	require.NoError(t, os.Rename(file, file+".saved"))
	require.NoError(t, os.Mkdir(file, 0700))
	v.Enabled = false
	_, e := s.SavePublishedSettings(context.Background(), v)
	require.Error(t, e)
	publishMeasure(t, b, publishModel, 68, 26)
	require.Equal(t, old, s.active.Revision)
	require.NoError(t, os.Remove(file))
	require.NoError(t, os.Rename(file+".saved", file))
	require.Equal(t, old, publishConfig(t, s).Revision)
}
func publishFixture(t *testing.T, store PriceSyncPublishStore) (*ModelPricingSyncService, *BillingService) {
	t.Helper()
	c := &config.Config{}
	c.Pricing.DataDir = t.TempDir()
	c.Pricing.OverrideFile = filepath.Join(c.Pricing.DataDir, "projection.json")
	require.NoError(t, os.WriteFile(filepath.Join(c.Pricing.DataDir, "model_pricing.json"), []byte(`{"sentinel":{"input_cost_per_token":0.003},"kimi-k3.1-pro-highspeed":{"input_cost_per_token":0.03,"cache_creation_input_token_cost_above_1hr":0.8,"cny_official":true,"mode":"chat"}}`), 0600))
	p := NewPricingService(c, nil)
	svc := NewModelPricingSyncService(&ModelPricingSyncConfig{OverrideFile: c.Pricing.OverrideFile, LegacyOverrideFile: filepath.Join(c.Pricing.DataDir, "v1.json"), Store: store}, p, nil, nil)
	return svc, NewBillingService(c, p)
}
func publishQuote(model string, out, oneHour float64, evidence string) PriceSyncQuote {
	return PriceSyncQuote{Model: model, Currency: "CNY", Provider: "vendor", SourceURL: "https://vendor.invalid/cn", Evidence: evidence, VerifiedAt: time.Now().UTC(), BillingMode: "token", Rates: map[string]float64{"input_cost_per_token": 26e-6, "output_cost_per_token": out / 1e6, "cache_creation_input_token_cost": 26e-6, "cache_read_input_token_cost": 1.3e-6, "cache_creation_input_token_cost_above_1hr": oneHour / 1e6}}
}
func publishSupply(s *ModelPricingSyncService, q PriceSyncQuote) {
	s.cfg.Collector = func(context.Context, modelPricingSyncFetcher, string, *CNYOfficialPricingSettings) (PriceSyncCollection, error) {
		return PriceSyncCollection{Quotes: map[string]PriceSyncQuote{q.Model: q}, NativeModels: map[string]bool{q.Model: true}}, nil
	}
}
func publishConfig(t *testing.T, s *ModelPricingSyncService) *CNYOfficialPricingSettings {
	t.Helper()
	v, e := s.PublishedSettings(context.Background())
	require.NoError(t, e)
	return v
}
func publishManual(t *testing.T, v string) CNYOfficialManualPrice {
	t.Helper()
	var p CNYOfficialManualPrice
	require.NoError(t, json.Unmarshal([]byte(v), &p))
	return p
}
func publishSet(t *testing.T, s *ModelPricingSyncService, v *CNYOfficialPricingSettings) *CNYOfficialPricingSettings {
	t.Helper()
	r, e := s.SavePublishedSettings(context.Background(), v)
	require.NoError(t, e)
	require.NotEmpty(t, r.Revision)
	require.Equal(t, r.Revision, r.ActiveRevision)
	return r
}
func publishMeasure(t *testing.T, b *BillingService, model string, out, oneHour float64) {
	t.Helper()
	a, e := b.CalculateCost(model, UsageTokens{OutputTokens: 1_000_000}, 1)
	require.NoError(t, e)
	require.InDelta(t, out, a.ActualCost, 1e-9)
	a, e = b.CalculateCost(model, UsageTokens{CacheCreationTokens: 1_000_000, CacheCreation1hTokens: 1_000_000}, 1)
	require.NoError(t, e)
	require.InDelta(t, oneHour, a.ActualCost, 1e-9)
}

func TestPricePublishRestoreWholeQuote(t *testing.T) {
	s, bs := publishFixture(t, &publishTestStore{})
	ctx := context.Background()
	cfg := publishConfig(t, s)
	require.False(t, cfg.Enabled)
	cfg.Enabled = true
	publishSet(t, s, cfg)
	publishSupply(s, publishQuote(publishModel, 120, 52, "first"))
	_, e := s.Sync(ctx)
	require.NoError(t, e)
	publishMeasure(t, bs, publishModel, 120, 52)
	cfg = publishConfig(t, s)
	cfg.ManualOverrides[publishModel] = publishManual(t, `{"input":13,"output":68,"cache_read":1.3,"cache_write":13,"cache_write_1h":26}`)
	cfg = publishSet(t, s, cfg)
	publishMeasure(t, bs, publishModel, 68, 26)
	q := publishQuote(publishModel, 136, 52, "second")
	delete(q.Rates, "cache_creation_input_token_cost_above_1hr")
	publishSupply(s, q)
	_, e = s.Sync(ctx)
	require.NoError(t, e)
	cfg = publishConfig(t, s)
	delete(cfg.ManualOverrides, publishModel)
	_, e = s.SavePublishedSettings(ctx, cfg)
	require.ErrorContains(t, e, "above_1hr")
	publishMeasure(t, bs, publishModel, 68, 26)
	publishSupply(s, publishQuote(publishModel, 136, 52, "third"))
	_, e = s.Sync(ctx)
	require.NoError(t, e)
	publishMeasure(t, bs, publishModel, 68, 26)
	s.cfg.Collector = func(context.Context, modelPricingSyncFetcher, string, *CNYOfficialPricingSettings) (PriceSyncCollection, error) {
		panic("manual path contacted source")
	}
	cfg = publishConfig(t, s)
	delete(cfg.ManualOverrides, publishModel)
	publishSet(t, s, cfg)
	publishMeasure(t, bs, publishModel, 136, 52)
	require.Equal(t, "auto", s.active.Models[publishModel].Mode)
	restarted, newBilling := publishFixture(t, s.cfg.Store)
	publishConfig(t, restarted)
	publishMeasure(t, newBilling, publishModel, 136, 52)
}

func TestPricePublishEvidenceAndDeepSeek(t *testing.T) {
	const model = "deepseek-v4.1-flash"
	s, b := publishFixture(t, &publishTestStore{})
	cfg := publishConfig(t, s)
	cfg.Enabled = true
	publishSet(t, s, cfg)
	q := publishQuote(model, 11, 7, "stable")
	publishSupply(s, q)
	_, e := s.Sync(context.Background())
	require.NoError(t, e)
	cfg = publishConfig(t, s)
	cfg.ManualOverrides[model] = publishManual(t, `{"input":17,"output":49,"cache_write_1h":19,"auto_restore":true}`)
	publishSet(t, s, cfg)
	publishMeasure(t, b, model, 49, 19)
	require.Equal(t, "SITE", s.active.Models[model].Effective.Currency)
	require.True(t, s.active.Models[model].NativeCNY)
	for _, bad := range []string{"same", "expired", "future", "missing", "wrong-currency"} {
		q = publishQuote(model, 99, 22, "new")
		switch bad {
		case "same":
			q.Evidence = "stable"
		case "expired":
			q.VerifiedAt = time.Now().Add(-48 * time.Hour)
		case "future":
			q.VerifiedAt = time.Now().Add(time.Hour)
		case "missing":
			q.Evidence = ""
		case "wrong-currency":
			q.Currency = "USD"
		}
		publishSupply(s, q)
		_, e = s.Sync(context.Background())
		require.NoError(t, e)
		require.Contains(t, publishConfig(t, s).ManualOverrides, model, bad)
		publishMeasure(t, b, model, 49, 19)
	}
	q = publishQuote(model, 99, 22, "corrected")
	publishSupply(s, q)
	_, e = s.Sync(context.Background())
	require.NoError(t, e)
	require.NotContains(t, publishConfig(t, s).ManualOverrides, model)
	publishMeasure(t, b, model, 99, 22)
}

func TestPricePublishRestoreReplacesModeAndCurrency(t *testing.T) {
	ctx := context.Background()
	s, _ := publishFixture(t, &publishTestStore{})
	base := s.pricing.getPricingFilePath()
	require.NoError(t, os.WriteFile(base, []byte(`{"control":{"input_cost_per_token":1},"sku":{"input_cost_per_token":8,"cny_official":true,"output_cost_per_image":99,"mode":"image_generation"}}`), 0600))
	c := publishConfig(t, s)
	c.ManualOverrides["sku"] = publishManual(t, `{"input":7,"output":9,"cache_write_1h":13}`)
	publishSet(t, s, c)
	q := PriceSyncQuote{Model: "sku", Provider: "vendor", Currency: "USD", BillingMode: "per_image", Rates: map[string]float64{"output_cost_per_image": 0.17}, SourceURL: "https://vendor.invalid/sku", Evidence: "new-image-tariff", VerifiedAt: time.Now().UTC()}
	s.cfg.Collector = func(context.Context, modelPricingSyncFetcher, string, *CNYOfficialPricingSettings) (PriceSyncCollection, error) {
		return PriceSyncCollection{Quotes: map[string]PriceSyncQuote{"sku": q}}, nil
	}
	_, e := s.Sync(ctx)
	require.NoError(t, e)
	c = publishConfig(t, s)
	delete(c.ManualOverrides, "sku")
	publishSet(t, s, c)
	r := s.active.Models["sku"]
	require.Equal(t, "per_image", r.Effective.BillingMode)
	require.Equal(t, "USD", r.Effective.Currency)
	p := s.pricing.pricingData["sku"]
	require.True(t, p.TokenPricingAbsent)
	require.False(t, p.CNYOfficial)
	require.Zero(t, p.CacheCreationInputTokenCostAbove1hr)
	require.Equal(t, 0.17, p.OutputCostPerImage)
	require.Equal(t, "image_generation", p.Mode)
}

func TestPricePublishRollbackAndCAS(t *testing.T) {
	store := &publishTestStore{}
	s, b := publishFixture(t, store)
	cfg := publishConfig(t, s)
	cfg.ManualOverrides[publishModel] = publishManual(t, `{"input":13,"output":68,"cache_write_1h":26}`)
	cfg = publishSet(t, s, cfg)
	orig, e := os.ReadFile(s.cfg.OverrideFile)
	require.NoError(t, e)
	old := cfg.Revision
	cfg.ManualOverrides[publishModel] = publishManual(t, `{"input":13,"output":99,"cache_write_1h":44}`)
	store.fail = true
	_, e = s.SavePublishedSettings(context.Background(), cfg)
	require.ErrorContains(t, e, "commit failure")
	store.fail = false
	after, e := os.ReadFile(s.cfg.OverrideFile)
	require.NoError(t, e)
	require.Equal(t, orig, after)
	publishMeasure(t, b, publishModel, 68, 26)
	require.Equal(t, old, publishConfig(t, s).Revision)
	base := s.pricing.getPricingFilePath()
	require.NoError(t, os.Rename(base, base+".backup"))
	_, e = s.SavePublishedSettings(context.Background(), cfg)
	require.Error(t, e)
	publishMeasure(t, b, publishModel, 68, 26)
	after, e = os.ReadFile(s.cfg.OverrideFile)
	require.NoError(t, e)
	require.Equal(t, orig, after)
	require.NoError(t, os.Rename(base+".backup", base))
	cfg = publishSet(t, s, cfg)
	publishMeasure(t, b, publishModel, 99, 44)
	cfg.Revision = old
	cfg.Enabled = true
	_, e = s.SavePublishedSettings(context.Background(), cfg)
	require.ErrorIs(t, e, ErrPriceSyncConflict)
	cfg.Revision = ""
	_, e = s.SavePublishedSettings(context.Background(), cfg)
	require.ErrorIs(t, e, ErrPriceSyncConflict)
}

func TestPricePublishSlowDownloadAndLegacyWriter(t *testing.T) {
	s, _ := publishFixture(t, &publishTestStore{})
	peer, b := publishFixture(t, s.cfg.Store)
	cfg := publishConfig(t, s)
	cfg.Enabled = true
	publishSet(t, s, cfg)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	s.cfg.Collector = func(context.Context, modelPricingSyncFetcher, string, *CNYOfficialPricingSettings) (PriceSyncCollection, error) {
		close(entered)
		<-release
		q := publishQuote(publishModel, 7, 8, "late")
		return PriceSyncCollection{Quotes: map[string]PriceSyncQuote{publishModel: q}}, nil
	}
	go func() { _, e := s.Sync(context.Background()); done <- e }()
	<-entered
	cfg = publishConfig(t, peer)
	cfg.ManualOverrides[publishModel] = publishManual(t, `{"input":13,"output":68,"cache_write_1h":26}`)
	publishSet(t, peer, cfg)
	close(release)
	require.ErrorIs(t, <-done, ErrPriceSyncConflict)
	require.NoError(t, os.WriteFile(peer.cfg.LegacyOverrideFile, []byte(`{"kimi-k3.1-pro-highspeed":{"output_cost_per_token":999}}`), 0600))
	_, e := peer.PublishedSettings(context.Background())
	require.NoError(t, e)
	publishMeasure(t, b, publishModel, 68, 26)
}
