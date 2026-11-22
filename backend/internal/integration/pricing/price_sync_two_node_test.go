package pricing_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Two processes, one PostgreSQL, two private projections. The only mutation is
// the admin PUT on the commit node; the follower is never asked to re-read, its
// cache is never cleared and nothing polls it. What is measured is the price a
// brand new request on the follower is billed, and the price an already
// resolved request keeps.
const priceFollowCatalog = `{
 "ctl":{"input_cost_per_token":0.1},
 "tok":{"input_cost_per_token":0.000001,"output_cost_per_token":0.000002},
 "req":{"glory_billing_mode":"per_request","cost_per_request":0.5},
 "sec":{"glory_billing_mode":"per_second","cost_per_second":0.25},
 "img":{"glory_billing_mode":"per_image","output_cost_per_image":2}
}`

type priceFollowSource struct {
	output atomic.Int64
	calls  atomic.Int64
}

func (f *priceFollowSource) FetchPricingJSON(_ context.Context, url string) ([]byte, error) {
	f.calls.Add(1)
	if url != "https://models.dev/api.json" {
		return nil, fmt.Errorf("fixture offline")
	}
	return []byte(fmt.Sprintf(`{"google":{"id":"google","models":{"tok":{"id":"tok","cost":{"input":1,"output":%d}}}}}`, f.output.Load())), nil
}

func (f *priceFollowSource) FetchHashText(context.Context, string) (string, error) {
	return "", fmt.Errorf("fixture offline")
}

// pricePollOnlyStore hides the optional listener so the service falls back to
// the bounded head check. This is the pre-change follower: a head poll alone.
type pricePollOnlyStore struct {
	service.PriceSyncPublishStore
}

type priceFollowNode struct {
	name    string
	cfg     *config.Config
	pricing *service.PricingService
	sync    *service.ModelPricingSyncService
	billing *service.BillingService
	router  *gin.Engine
	saves   *atomic.Int64
}

func newPriceFollowNode(t *testing.T, db *sql.DB, socket string, source *priceFollowSource, name string, listener bool, backstop time.Duration) *priceFollowNode {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Pricing.DataDir = dir
	cfg.Pricing.OverrideFile = filepath.Join(dir, "projection.json")
	cfg.Pricing.PublishFollowBackstopSeconds = int(backstop.Seconds())
	// Only the LISTEN connection uses this: the pool is injected separately.
	cfg.Database = config.DatabaseConfig{Host: socket, Port: 5432, User: "publish_test", DBName: "price_publish_test", SSLMode: "disable"}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "model_pricing.json"), []byte(priceFollowCatalog), 0600))
	t.Setenv("BACKGROUND_JOBS_ENABLED", "false")
	pricing, err := service.ProvidePricingService(cfg, nil)
	require.NoError(t, err)
	history := repository.NewModelPricingSyncHistoryRepository(db)
	store := repository.ProvidePriceSyncPublishStore(db, cfg)
	if !listener {
		store = pricePollOnlyStore{store}
	}
	sync, err := service.ProvideModelPricingSyncService(cfg, pricing, history, source, nil, store)
	require.NoError(t, err)
	n := &priceFollowNode{name: name, cfg: cfg, pricing: pricing, sync: sync, billing: service.NewBillingService(cfg, pricing), saves: &atomic.Int64{}}
	h := admin.NewModelPricingSyncHandler(sync, history)
	r := gin.New()
	const url = "/api/v1/admin/model-pricing-sync/cny-official"
	r.GET(url, h.GetCNYOfficialConfig)
	r.PUT(url, h.UpdateCNYOfficialConfig)
	r.GET("/status", h.Status)
	n.router = r
	return n
}

func (n *priceFollowNode) stop() { n.pricing.Stop() }

func (n *priceFollowNode) call(t *testing.T, method, path, body string, status int) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	q := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	q.Header.Set("Content-Type", "application/json")
	n.router.ServeHTTP(w, q)
	require.Equal(t, status, w.Code, n.name+": "+w.Body.String())
	var v struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
	return v.Data
}

func (n *priceFollowNode) settings(t *testing.T) map[string]any {
	return n.call(t, "GET", "/api/v1/admin/model-pricing-sync/cny-official", "", 200)
}

// save sends only what the admin save sends, so no echoed `active_revision`
// can make an unrelated field differ and turn the save into a no-op.
func (n *priceFollowNode) save(t *testing.T, prev map[string]any, overrides map[string]any) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"revision":         prev["revision"],
		"enabled":          prev["enabled"],
		"source_url":       prev["source_url"],
		"manual_overrides": overrides,
	})
	require.NoError(t, err)
	n.saves.Add(1)
	return n.call(t, "PUT", "/api/v1/admin/model-pricing-sync/cny-official", string(body), 200)
}

func (n *priceFollowNode) status(t *testing.T) map[string]any {
	return requireJSONValue[map[string]any](t, n.call(t, "GET", "/status", "", 200)["pricing_v2"])
}

// resolveAt is what a request does at entry: snapshot the live catalog row.
func (n *priceFollowNode) resolveAt(model string) *service.ResolvedPricing {
	return service.NewModelPricingResolver(nil, n.billing).Resolve(context.Background(), service.PricingInput{Model: model})
}

func (n *priceFollowNode) billToken(t *testing.T, resolved *service.ResolvedPricing, model string) float64 {
	t.Helper()
	cost, err := n.billing.CalculateCostUnified(service.CostInput{
		Ctx: context.Background(), Model: model, Resolved: resolved,
		Resolver: service.NewModelPricingResolver(nil, n.billing),
		Tokens:   service.UsageTokens{OutputTokens: 1_000_000}, RateMultiplier: 1,
	})
	require.NoError(t, err)
	return cost.ActualCost
}

func (n *priceFollowNode) billUnit(t *testing.T, resolved *service.ResolvedPricing, model string, requests, images int, seconds float64) (float64, string) {
	t.Helper()
	cost, err := n.billing.CalculateCostUnified(service.CostInput{
		Ctx: context.Background(), Model: model, Resolved: resolved,
		RequestCount: requests, UsageUnits: seconds,
		MediaUsage: &service.MediaUsage{Count: images}, RateMultiplier: 1,
	})
	require.NoError(t, err)
	return cost.ActualCost, cost.BillingMode
}

// observeRevision waits for the follower to install the revision while only
// reading its in-memory status: no GET on the price API, no cache clear, no
// synthetic traffic.
func (n *priceFollowNode) observeRevision(t *testing.T, want string, limit time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	for time.Since(start) < limit {
		if n.status(t)["active_revision"] == want {
			return time.Since(start)
		}
		time.Sleep(2 * time.Millisecond)
	}
	return time.Since(start)
}

func priceFollowSchema(t *testing.T, socket string) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("host=%s user=publish_test dbname=price_publish_test sslmode=disable", socket)
	return priceSchema(t, dsn)
}

func priceSchema(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	schema := "follow_" + fmt.Sprintf("%x", uuid.New())
	root, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	_, err = root.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := root.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		require.NoError(t, err)
	})
	db, err := sql.Open("postgres", dsn+" search_path="+schema)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	for _, name := range []string{"916_model_pricing_sync_history.sql", "921_price_sync_revisions.sql"} {
		ddl, err := os.ReadFile(filepath.Join("../../../migrations", name))
		require.NoError(t, err)
		_, err = db.Exec(string(ddl))
		require.NoError(t, err)
	}
	return db
}

// A fake source only seeds the automatic quote that the undo round restores.
// Manual saves and the follower must never contact it.
func seedPriceFollowAuto(t *testing.T, a *priceFollowNode, source *priceFollowSource) string {
	t.Helper()
	source.output.Store(3)
	_, err := a.sync.Sync(context.Background())
	require.NoError(t, err)
	require.NotZero(t, source.calls.Load())
	return requireJSONValue[string](t, a.settings(t)["revision"])
}

func TestPricePublishTwoNodeFollowPostgres(t *testing.T) {
	socket := os.Getenv("PRICE_PUBLISH_TEST_SOCKET")
	if socket == "" {
		t.Skip("set PRICE_PUBLISH_TEST_SOCKET to an isolated PostgreSQL Unix socket")
	}
	// PRICE_PUBLISH_TEST_FOLLOW=poll reproduces the pre-change follower: the
	// same test, but the only way it learns about a revision is a head poll.
	follow := os.Getenv("PRICE_PUBLISH_TEST_FOLLOW")
	pollOnly := follow == "poll"
	backstop, bound, limit := 30*time.Second, time.Second, 12*time.Second
	if pollOnly {
		backstop = 5 * time.Second
	}
	db := priceFollowSchema(t, socket)
	source := &priceFollowSource{}
	a := newPriceFollowNode(t, db, socket, source, "commit", !pollOnly, backstop)
	defer a.stop()
	seedPriceFollowAuto(t, a, source)
	// The follower is started after the automatic quote exists, like a process
	// that restarts onto the committed head.
	b := newPriceFollowNode(t, db, socket, source, "follower", !pollOnly, backstop)
	defer b.stop()
	seeded := requireJSONValue[string](t, a.settings(t)["revision"])
	require.Equal(t, seeded, b.status(t)["active_revision"])
	calls := source.calls.Load()

	type round struct {
		name    string
		model   string
		manual  map[string]any
		probe   func(n *priceFollowNode, r *service.ResolvedPricing) float64
		wantNew float64
		wantOld float64
	}
	rounds := []round{
		{name: "token", model: "tok",
			manual:  map[string]any{"input": 1, "output": 68},
			probe:   func(n *priceFollowNode, r *service.ResolvedPricing) float64 { return n.billToken(t, r, "tok") },
			wantNew: 68, wantOld: 3},
		{name: "per_request", model: "req",
			manual: map[string]any{"billing_mode": "per_request", "per_request_price": 0.75},
			probe: func(n *priceFollowNode, r *service.ResolvedPricing) float64 {
				c, _ := n.billUnit(t, r, "req", 1, 0, 0)
				return c
			},
			wantNew: 0.75, wantOld: 0.5},
		{name: "per_image", model: "img",
			manual: map[string]any{"billing_mode": "per_image", "image_output_per_image": 3},
			probe: func(n *priceFollowNode, r *service.ResolvedPricing) float64 {
				c, _ := n.billUnit(t, r, "img", 0, 2, 0)
				return c
			},
			wantNew: 6, wantOld: 4},
		{name: "per_second", model: "sec",
			manual: map[string]any{"billing_mode": "per_second", "per_second_price": 0.5},
			probe: func(n *priceFollowNode, r *service.ResolvedPricing) float64 {
				c, _ := n.billUnit(t, r, "sec", 0, 0, 4)
				return c
			},
			wantNew: 2, wantOld: 1},
	}
	var slow []string
	for _, rc := range rounds {
		inflight := b.resolveAt(rc.model)
		commitInflight := a.resolveAt(rc.model)
		require.NotNil(t, inflight, rc.name)
		cur := a.settings(t)
		overrides := map[string]any{}
		for k, v := range requireJSONValue[map[string]any](t, cur["manual_overrides"]) {
			overrides[k] = v
		}
		overrides[rc.model] = rc.manual
		var before, after int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM price_sync_revisions`).Scan(&before))
		saved := a.save(t, cur, overrides)
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM price_sync_revisions`).Scan(&after))
		// One save, one revision: a follower's install must never append.
		require.Equal(t, before+1, after, rc.name+": revisions appended by the save")
		rev := requireJSONValue[string](t, saved["revision"])
		require.NotEmpty(t, rev)
		// The commit node is synchronous: the very next request bills the new price.
		require.InDelta(t, rc.wantNew, rc.probe(a, a.resolveAt(rc.model)), 1e-9, rc.name+": commit node")
		require.InDelta(t, rc.wantOld, rc.probe(a, commitInflight), 1e-9, rc.name+": in-flight on commit node")
		// The follower is not poked. This is the first request it would serve
		// after the save, recorded before it has necessarily caught up.
		first := rc.probe(b, b.resolveAt(rc.model))
		waited := b.observeRevision(t, rev, limit)
		if waited >= bound {
			slow = append(slow, fmt.Sprintf("%s: follower applied in %v (bound %v)", rc.name, waited, bound))
		}
		require.InDelta(t, rc.wantNew, rc.probe(b, b.resolveAt(rc.model)), 1e-9, rc.name+": new request after apply")
		// The in-flight request keeps the revision it resolved.
		require.InDelta(t, rc.wantOld, rc.probe(b, inflight), 1e-9, rc.name+": in-flight")
		t.Logf("%s: follower applied in %v, first request after save %.4f, in-flight %.4f", rc.name, waited, first, rc.wantOld)
	}
	// Four manual saves and their in-flight requests never contacted the source.
	require.Equal(t, calls, source.calls.Load())
	// Undo: dropping the manual card needs a newer trusted automatic quote.
	// That quote comes from the collector; the undo itself must not fetch.
	source.output.Store(4)
	_, err := a.sync.Sync(context.Background())
	require.NoError(t, err)
	syncCalls := source.calls.Load()
	cur := a.settings(t)
	overrides := map[string]any{}
	for k, v := range requireJSONValue[map[string]any](t, cur["manual_overrides"]) {
		if k != "tok" {
			overrides[k] = v
		}
	}
	inflight := b.resolveAt("tok")
	saved := a.save(t, cur, overrides)
	require.InDelta(t, 4.0, a.billToken(t, a.resolveAt("tok"), "tok"), 1e-9)
	waited := b.observeRevision(t, requireJSONValue[string](t, saved["revision"]), limit)
	if waited >= bound {
		slow = append(slow, fmt.Sprintf("undo: follower applied in %v (bound %v)", waited, bound))
	}
	require.InDelta(t, 4.0, b.billToken(t, b.resolveAt("tok"), "tok"), 1e-9)
	require.InDelta(t, 68.0, b.billToken(t, inflight, "tok"), 1e-9)
	t.Logf("undo: follower applied in %v, in-flight %.4f", waited, 68.0)
	require.Equal(t, syncCalls, source.calls.Load())
	// The admin view states the scope per process, and both agree here.
	status := b.status(t)
	require.Equal(t, "process", status["scope"])
	require.True(t, requireJSONValue[bool](t, status["converged"]), fmt.Sprintf("%v", status))
	require.Equal(t, saved["revision"], status["active_revision"])
	require.Equal(t, saved["revision"], status["head_revision"])
	want := "notify"
	if pollOnly {
		want = "poll"
	}
	require.Equal(t, want, requireJSONValue[map[string]any](t, status["follow"])["mode"])
	require.Empty(t, slow)
}

// A follower whose notification channel is gone must still converge, and must
// not claim the committed revision before it has loaded it.
func TestPricePublishFollowerWithoutNotificationsPostgres(t *testing.T) {
	socket := os.Getenv("PRICE_PUBLISH_TEST_SOCKET")
	if socket == "" {
		t.Skip("set PRICE_PUBLISH_TEST_SOCKET to an isolated PostgreSQL Unix socket")
	}
	db := priceFollowSchema(t, socket)
	source := &priceFollowSource{}
	a := newPriceFollowNode(t, db, socket, source, "commit", true, 30*time.Second)
	defer a.stop()
	seedPriceFollowAuto(t, a, source)
	b := newPriceFollowNode(t, db, socket, source, "poll", false, time.Second)
	defer b.stop()
	seeded := requireJSONValue[string](t, a.settings(t)["revision"])
	require.Equal(t, seeded, b.status(t)["active_revision"])
	cur := a.settings(t)
	overrides := map[string]any{}
	for k, v := range requireJSONValue[map[string]any](t, cur["manual_overrides"]) {
		overrides[k] = v
	}
	overrides["tok"] = map[string]any{"input": 1, "output": 68}
	saved := a.save(t, cur, overrides)
	rev := requireJSONValue[string](t, saved["revision"])
	// Nothing notifies this process. Until the head check runs it may only
	// report the revision it actually loaded; once the newer head is known it
	// must stop claiming convergence.
	deadline := time.Now().Add(6 * time.Second)
	sawLag := false
	for time.Now().Before(deadline) {
		st := b.status(t)
		head := requireJSONValue[string](t, st["head_revision"])
		active := requireJSONValue[string](t, st["active_revision"])
		if head == rev && active != rev {
			require.False(t, requireJSONValue[bool](t, st["converged"]), "a lagging process must not report converged")
			sawLag = true
		}
		if active == rev {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.Equal(t, rev, b.status(t)["active_revision"], "backstop must converge without notifications")
	require.InDelta(t, 68.0, b.billToken(t, b.resolveAt("tok"), "tok"), 1e-9)
	st := b.status(t)
	require.Equal(t, "poll", requireJSONValue[map[string]any](t, st["follow"])["mode"])
	require.True(t, requireJSONValue[bool](t, st["converged"]))
	t.Logf("poll-only follower converged; observed a lagging head before apply: %v", sawLag)
}

// A dropped LISTEN connection must not leave a follower stale: pq reconnects,
// reports that notifications may have been missed, and the follower re-reads
// the head. Nothing else is triggered.
func TestPricePublishListenerReconnectPostgres(t *testing.T) {
	socket := os.Getenv("PRICE_PUBLISH_TEST_SOCKET")
	if socket == "" {
		t.Skip("set PRICE_PUBLISH_TEST_SOCKET to an isolated PostgreSQL Unix socket")
	}
	db := priceFollowSchema(t, socket)
	source := &priceFollowSource{}
	a := newPriceFollowNode(t, db, socket, source, "commit", true, 30*time.Second)
	defer a.stop()
	seedPriceFollowAuto(t, a, source)
	b := newPriceFollowNode(t, db, socket, source, "follower", true, 30*time.Second)
	defer b.stop()
	require.Equal(t, a.settings(t)["revision"], b.status(t)["active_revision"])
	// LISTEN is issued asynchronously by the provider: wait until both
	// processes have registered the channel, then drop both backends.
	listening := func() int {
		var n int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND pid <> pg_backend_pid()
			AND query LIKE 'LISTEN%sub2api_price_sync_head%'`).Scan(&n))
		return n
	}
	require.Eventually(t, func() bool { return listening() >= 2 }, 5*time.Second, 5*time.Millisecond,
		"both instances must be listening before the connection is dropped")
	var killed int
	require.NoError(t, db.QueryRow(`SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity
		WHERE datname = current_database() AND pid <> pg_backend_pid()
		AND query LIKE 'LISTEN%sub2api_price_sync_head%'`).Scan(&killed))
	require.GreaterOrEqual(t, killed, 2)
	cur := a.settings(t)
	overrides := map[string]any{}
	for k, v := range requireJSONValue[map[string]any](t, cur["manual_overrides"]) {
		overrides[k] = v
	}
	overrides["tok"] = map[string]any{"input": 1, "output": 68}
	saved := a.save(t, cur, overrides)
	rev := requireJSONValue[string](t, saved["revision"])
	waited := b.observeRevision(t, rev, 10*time.Second)
	require.Less(t, waited, 10*time.Second, "a reconnected listener must re-read the head")
	require.InDelta(t, 68.0, b.billToken(t, b.resolveAt("tok"), "tok"), 1e-9)
	follow := requireJSONValue[map[string]any](t, b.status(t)["follow"])
	require.GreaterOrEqual(t, requireJSONValue[float64](t, follow["reconnects"]), 1.0)
	t.Logf("listener killed (%d backends), follower applied %s in %v, reconnects=%v", killed, rev, waited, follow["reconnects"])
}
