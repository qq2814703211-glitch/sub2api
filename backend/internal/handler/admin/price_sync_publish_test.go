package admin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type publishHTTPSource struct {
	output atomic.Int64
	calls  atomic.Int64
}

func (f *publishHTTPSource) FetchPricingJSON(_ context.Context, url string) ([]byte, error) {
	f.calls.Add(1)
	if url != "https://models.dev/api.json" {
		return nil, errors.New("fixture offline")
	}
	return []byte(fmt.Sprintf(`{"google":{"id":"google","models":{"sample-text":{"id":"sample-text","cost":{"input":13,"output":%d,"cache_write_30m":2,"cache_write_12h":4}}}}}`, f.output.Load())), nil
}
func (f *publishHTTPSource) FetchHashText(context.Context, string) (string, error) {
	return "", errors.New("fixture offline")
}

// A disposable Unix-socket PostgreSQL only, with a unique schema per run.
// Starts a new test process to prove recovery uses neither Go memory nor a
// surviving V2 projection. No supplier or customer traffic is involved.
func TestPricePublishAdminPostgres(t *testing.T) {
	socket := os.Getenv("PRICE_PUBLISH_TEST_SOCKET")
	if socket == "" {
		t.Skip("set PRICE_PUBLISH_TEST_SOCKET to an isolated PostgreSQL Unix socket")
	}
	require.True(t, filepath.IsAbs(socket))
	schema := os.Getenv("PRICE_PUBLISH_TEST_SCHEMA")
	child := schema != ""
	dsn := fmt.Sprintf("host=%s user=publish_test dbname=price_publish_test sslmode=disable", socket)
	if !child {
		schema = "publish_" + fmt.Sprintf("%x", uuid.New())
		root, e := sql.Open("postgres", dsn)
		require.NoError(t, e)
		defer root.Close()
		_, e = root.Exec(`CREATE SCHEMA ` + schema)
		require.NoError(t, e)
		defer root.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
	}
	db, e := sql.Open("postgres", dsn+" search_path="+schema)
	require.NoError(t, e)
	defer db.Close()
	if !child {
		for _, name := range []string{"916_model_pricing_sync_history.sql", "921_price_sync_revisions.sql"} {
			ddl, e := os.ReadFile(filepath.Join("../../../migrations", name))
			require.NoError(t, e)
			_, e = db.Exec(string(ddl))
			require.NoError(t, e)
		}
	}
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Pricing.DataDir = dir
	cfg.Pricing.OverrideFile = filepath.Join(dir, "rollback.json")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "model_pricing.json"), []byte(`{"control":{"input_cost_per_token":0.1}}`), 0600))
	legacy := []byte(`{"external":{"input_cost_per_token":0.071,"metadata":"retain"}}`)
	require.NoError(t, os.WriteFile(cfg.Pricing.OverrideFile, legacy, 0600))
	t.Setenv("BACKGROUND_JOBS_ENABLED", "false")
	p, e := service.ProvidePricingService(cfg, nil)
	require.NoError(t, e)
	defer p.Stop()
	history := repository.NewModelPricingSyncHistoryRepository(db)
	source := &publishHTTPSource{}
	source.output.Store(120)
	sync, e := service.ProvideModelPricingSyncService(cfg, p, history, source, nil, repository.NewPriceSyncPublishStore(db))
	require.NoError(t, e)
	h := NewModelPricingSyncHandler(sync, history)
	r := gin.New()
	const url = "/api/v1/admin/model-pricing-sync/cny-official"
	r.GET(url, h.GetCNYOfficialConfig)
	r.PUT(url, h.UpdateCNYOfficialConfig)
	r.GET("/status", h.Status)
	call := func(method, path, body string, status int) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		q := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		q.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, q)
		require.Equal(t, status, w.Code, w.Body.String())
		var v struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
		return v.Data
	}
	v := call("GET", url, "", 200)
	if child {
		require.Equal(t, os.Getenv("PRICE_PUBLISH_TEST_REV"), v["active_revision"])
		b := service.NewBillingService(cfg, p)
		cb, e := b.CalculateCost("sample-text", service.UsageTokens{OutputTokens: 1_000_000}, 1)
		require.NoError(t, e)
		require.Equal(t, float64(68), cb.ActualCost)
		return
	}
	rev := v["revision"].(string)
	require.NotEmpty(t, rev)
	payload := fmt.Sprintf(`{"revision":%q,"enabled":false,"manual_overrides":{"sample-text":{"input":13,"output":68,"cache_write_30m":0,"cache_write_12h":37,"auto_restore":false},"img":{"billing_mode":"per_image","image_output_per_image":0}}}`, rev)
	saved := call("PUT", url, payload, 200)
	revision := saved["revision"].(string)
	require.NotEqual(t, rev, revision)
	require.Equal(t, revision, saved["active_revision"])
	require.Equal(t, revision, call("PUT", url, payload, 200)["revision"])
	call("PUT", url, fmt.Sprintf(`{"revision":%q,"manual_overrides":{}}`, revision), 409)
	call("PUT", url, `{"enabled":true,"manual_overrides":{}}`, 409)
	var raw []byte
	var n int
	require.NoError(t, db.QueryRow(`SELECT snapshot FROM price_sync_revisions ORDER BY seq DESC LIMIT 1`).Scan(&raw))
	var head struct {
		Entries map[string]map[string]any `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(raw, &head))
	require.Equal(t, float64(0), head.Entries["sample-text"]["cache_creation_input_token_cost_30m"])
	require.Equal(t, 37e-6, head.Entries["sample-text"]["cache_creation_input_token_cost_12h"])
	require.Equal(t, float64(0), head.Entries["img"]["output_cost_per_image"])
	require.NotContains(t, head.Entries["img"], "input_cost_per_token")
	require.Equal(t, "retain", head.Entries["external"]["metadata"])
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM price_sync_revisions`).Scan(&n))
	require.Equal(t, 2, n)
	actual, e := os.ReadFile(cfg.Pricing.OverrideFile)
	require.NoError(t, e)
	require.Equal(t, legacy, actual)
	cmd := exec.Command(os.Args[0], "-test.run=^TestPricePublishAdminPostgres$", "-test.v")
	cmd.Env = append(os.Environ(), "PRICE_PUBLISH_TEST_SCHEMA="+schema, "PRICE_PUBLISH_TEST_REV="+revision)
	out, e := cmd.CombinedOutput()
	require.NoError(t, e, string(out))
	t.Logf("new process reloaded and billed revision %s", revision)
	require.Zero(t, source.calls.Load())
	_, e = sync.Sync(context.Background())
	require.NoError(t, e)
	source.output.Store(136)
	_, e = sync.Sync(context.Background())
	require.NoError(t, e)
	v = call("GET", url, "", 200)
	m := v["manual_overrides"].(map[string]any)
	delete(m, "sample-text")
	m["one-call"] = map[string]any{"billing_mode": "per_request", "per_request_price": 0}
	m["seconds"] = map[string]any{"billing_mode": "per_second", "per_second_price": 0.375}
	body, e := json.Marshal(v)
	require.NoError(t, e)
	calls := source.calls.Load()
	saved = call("PUT", url, string(body), 200)
	require.Equal(t, calls, source.calls.Load())
	state := call("GET", "/status", "", 200)["pricing_v2"].(map[string]any)
	require.Equal(t, saved["active_revision"], state["active_revision"])
	models := state["models"].(map[string]any)
	require.Equal(t, "auto", models["sample-text"].(map[string]any)["mode"])
	b := service.NewBillingService(cfg, p)
	bill, e := b.CalculateCost("sample-text", service.UsageTokens{OutputTokens: 1_000_000}, 1)
	require.NoError(t, e)
	require.Equal(t, 136.0, bill.ActualCost)
	var hist int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM model_pricing_sync_history WHERE model='sample-text' AND field='output_cost_per_token' AND source='v2-auto-usd' AND new_value=0.000136`).Scan(&hist))
	require.Equal(t, 1, hist)
	for model, field := range map[string]string{"one-call": "cost_per_request", "seconds": "cost_per_second"} {
		var rate float64
		require.NoError(t, db.QueryRow(`SELECT (snapshot->'entries'->$1->>$2)::float8 FROM price_sync_revisions ORDER BY seq DESC LIMIT 1`, model, field).Scan(&rate))
		if model == "one-call" {
			require.Zero(t, rate)
		} else {
			require.Equal(t, 0.375, rate)
		}
	}
	// Both instances edit from the same DB revision on different private files.
	peerPricing, e := service.ProvidePricingService(cfg, nil)
	require.NoError(t, e)
	defer peerPricing.Stop()
	peer, e := service.ProvideModelPricingSyncService(cfg, peerPricing, history, source, nil, repository.NewPriceSyncPublishStore(db))
	require.NoError(t, e)
	results := make(chan error, 2)
	start := make(chan struct{})
	for i, svc := range []*service.ModelPricingSyncService{peer, sync} {
		edit, e := svc.PublishedSettings(context.Background())
		require.NoError(t, e)
		edit.ManualOverrides[fmt.Sprintf("racing-%d", i)] = service.CNYOfficialManualPrice{Input: 3, Output: 4}
		go func() { <-start; _, e := svc.SavePublishedSettings(context.Background(), edit); results <- e }()
	}
	close(start)
	wins, conflicts := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			wins++
		} else if errors.Is(err, service.ErrPriceSyncConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	require.Equal(t, 1, wins)
	require.Equal(t, 1, conflicts)
}
