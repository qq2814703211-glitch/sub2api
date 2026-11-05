//go:build videodb

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/Wei-Shaw/sub2api/migrations"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

var videoDBOnce sync.Once

func videoDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("VIDEO_BILLING_TEST_DSN")
	if dsn == "" {
		t.Fatal("VIDEO_BILLING_TEST_DSN must point to a disposable database")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	videoDBOnce.Do(func() {
		baseline := fstest.MapFS{}
		names, e := fs.Glob(migrations.FS, "*.sql")
		require.NoError(t, e)
		for _, name := range names {
			if name != "914_video_billing_jobs.sql" {
				data, e := fs.ReadFile(migrations.FS, name)
				require.NoError(t, e)
				baseline[name] = &fstest.MapFile{Data: data}
			}
		}
		require.NoError(t, applyMigrationsFS(context.Background(), db, baseline))
		var legacyID int64
		require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES($1,'legacy',12.3456789) RETURNING id`, uuid.NewString()+"@legacy.test").Scan(&legacyID))
		require.NoError(t, ApplyMigrations(context.Background(), db))
		var balance float64
		require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, legacyID).Scan(&balance))
		require.Equal(t, 12.3456789, balance)
		t.Log("baseline migrations -> existing balance -> migration 914: preserved")
	})
	return db
}

type videoDBFixture struct {
	db                        *sql.DB
	user, key, account, group int64
	provider, model           string
}

func newVideoDBFixture(t *testing.T, provider string) *videoDBFixture {
	t.Helper()
	db := videoDB(t)
	_, err := db.Exec(`DELETE FROM video_billing_jobs`)
	require.NoError(t, err)
	f := &videoDBFixture{db: db, provider: provider, model: "grok-imagine-video-1.5"}
	if provider == "seedance" {
		f.model = "seedance-test"
	}
	suffix := uuid.NewString()
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES($1,'hash',100) RETURNING id`, suffix+"@example.test").Scan(&f.user))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform,rate_multiplier) VALUES($1,'composite',1) RETURNING id`, suffix).Scan(&f.group))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id,quota,rate_limit_5h) VALUES($1,$2,'video',$3,100,100) RETURNING id`, f.user, "sk-"+suffix, f.group).Scan(&f.key))
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type) VALUES($1,$2,'apikey') RETURNING id`, suffix, map[string]string{"seedance": "openai", "grok": "grok"}[provider]).Scan(&f.account))
	return f
}

type videoDBAccountRepo struct {
	service.AccountRepository
	account *service.Account
}

func (r *videoDBAccountRepo) GetByID(context.Context, int64) (*service.Account, error) {
	if r.account == nil {
		return nil, service.ErrAccountNotFound
	}
	return r.account, nil
}

type videoDBQuota struct{}

func (videoDBQuota) UpdateQuotaUsed(context.Context, int64, float64) error      { return nil }
func (videoDBQuota) UpdateRateLimitUsage(context.Context, int64, float64) error { return nil }

type videoDBUpstream struct {
	mu      sync.Mutex
	body    string
	status  int
	calls   int
	methods []string
}

func (u *videoDBUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls++
	u.methods = append(u.methods, req.Method)
	status := u.status
	if status == 0 {
		status = 200
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(u.body))}, nil
}
func (u *videoDBUpstream) DoWithTLS(req *http.Request, proxy string, id int64, c int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, c)
}
func (f *videoDBFixture) service(t *testing.T, body string) (*service.OpenAIGatewayService, *service.OpenAIRecordUsageInput, *videoDBUpstream) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	bs := service.NewBillingService(cfg, nil)
	resolver := service.NewModelPricingResolver(nil, bs)
	account := &service.Account{ID: f.account, Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Status: service.StatusActive, GroupIDs: []int64{f.group}, Credentials: map[string]any{"api_key": "synthetic", "base_url": "https://provider.example"}}
	if f.provider == "seedance" {
		account.Platform = service.PlatformOpenAI
		account.Credentials["openai_capabilities"] = []string{"seedance"}
	}
	upstream := &videoDBUpstream{body: body}
	svc := service.NewOpenAIGatewayService(&videoDBAccountRepo{account: account}, NewUsageLogRepository(nil, f.db), NewUsageBillingRepository(nil, f.db), nil, nil, nil, nil, cfg, nil, nil, bs, nil, nil, upstream, nil, nil, nil, resolver, nil, nil, service.NewSettingService(&videoDBSettings{db: f.db}, cfg), nil)
	svc.SetVideoBillingJobs(NewVideoBillingJobRepository(f.db))
	out := 0.0000025
	group := &service.Group{ID: f.group, Platform: service.PlatformComposite, RateMultiplier: 1, VideoModelPrices: map[string]map[string]float64{f.model: {"720p": 0.025}}}
	if f.provider == "seedance" {
		group.ModelPricing = []service.ChannelModelPricing{{Platform: service.PlatformComposite, Models: []string{f.model}, BillingMode: service.BillingModeToken, OutputPrice: &out}}
	}
	user := &service.User{ID: f.user}
	key := &service.APIKey{ID: f.key, UserID: f.user, User: user, GroupID: &f.group, Group: group, Quota: 100, RateLimit5h: 100}
	in := &service.OpenAIRecordUsageInput{Result: &service.OpenAIForwardResult{Model: f.model, BillingModel: f.model}, APIKey: key, User: user, Account: account, APIKeyService: videoDBQuota{}, QuotaPlatform: account.Platform}
	return svc, in, upstream
}
func (f *videoDBFixture) begin(t *testing.T, svc *service.OpenAIGatewayService, in *service.OpenAIRecordUsageInput, task string) *service.VideoBillingJob {
	t.Helper()
	j, err := svc.BeginVideoBillingJob(context.Background(), in, service.GrokVideoPendingBilling{Model: f.model, BillingModel: f.model, VideoResolution: "720p"}, f.provider)
	require.NoError(t, err)
	require.NoError(t, svc.SubmitVideoBillingJob(context.Background(), j, task))
	j.TaskID = task
	return j
}
func (f *videoDBFixture) assertBill(t *testing.T, expected float64) {
	t.Helper()
	var balance, quota, rate float64
	require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.user).Scan(&balance))
	require.NoError(t, f.db.QueryRow(`SELECT quota_used,usage_5h FROM api_keys WHERE id=$1`, f.key).Scan(&quota, &rate))
	require.InDelta(t, 100-expected, balance, 1e-8)
	require.InDelta(t, expected, quota, 1e-8)
	require.InDelta(t, expected, rate, 1e-8)
	var count int
	require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM usage_billing_dedup WHERE api_key_id=$1`, f.key).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM usage_logs WHERE api_key_id=$1`, f.key).Scan(&count))
	require.Equal(t, 1, count)
	var cost float64
	require.NoError(t, f.db.QueryRow(`SELECT actual_cost FROM usage_logs WHERE api_key_id=$1`, f.key).Scan(&cost))
	require.InDelta(t, expected, cost, 1e-8)
}

// A separate OS process reloads SQL only, starts the real lifecycle worker and
// completes the task without any client poll or the original pricing service.
func TestVideoDBRestartNoClientPoll(t *testing.T) {
	if os.Getenv("VIDEO_BILLING_RESTART_FIXTURE") != "" {
		runVideoDBRestartChild(t)
		return
	}
	f := newVideoDBFixture(t, "seedance")
	svc, in, _ := f.service(t, "")
	j := f.begin(t, svc, in, "seedance:restart-task")
	// Persisted price is $2.5/M; the restarted process has a conflicting $99/M card.
	values := fmt.Sprintf("%d,%d,%d,%d", f.user, f.key, f.account, f.group)
	command := exec.Command(os.Args[0], "-test.run=^TestVideoDBRestartNoClientPoll$", "-test.v")
	command.Env = append(os.Environ(), "VIDEO_BILLING_RESTART_FIXTURE="+values)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Contains(t, string(output), "PASS")
	row, err := NewVideoBillingJobRepository(f.db).GetForOwner(context.Background(), j.TaskID, f.user, f.key, f.group)
	require.NoError(t, err)
	require.Equal(t, "done", row.State)
	f.assertBill(t, 0.0308625)
	// Client polling after worker completion cannot produce another debit.
	require.NoError(t, svc.ObserveVideoBilling(context.Background(), j, &service.OpenAIForwardResult{VideoStatus: "succeeded", VideoUsageKnown: true, Usage: service.OpenAIUsage{OutputTokens: 12345}}))
	f.assertBill(t, 0.0308625)
}
func runVideoDBRestartChild(t *testing.T) {
	values := os.Getenv("VIDEO_BILLING_RESTART_FIXTURE")
	require.NotEmpty(t, values)
	f := &videoDBFixture{db: videoDB(t), provider: "seedance", model: "seedance-test"}
	_, err := fmt.Sscanf(values, "%d,%d,%d,%d", &f.user, &f.key, &f.account, &f.group)
	require.NoError(t, err)
	svc, in, u := f.service(t, `{"status":"succeeded","usage":{"completion_tokens":12345}}`)
	*in.APIKey.Group.ModelPricing[0].OutputPrice = 0.000099
	svc.StartVideoBillingWorker()
	defer svc.StopVideoBillingWorker()
	require.Eventually(t, func() bool {
		var state string
		e := f.db.QueryRow(`SELECT state FROM video_billing_jobs WHERE api_key_id=$1`, f.key).Scan(&state)
		return e == nil && state == "done"
	}, 10*time.Second, 20*time.Millisecond)
	u.mu.Lock()
	defer u.mu.Unlock()
	require.Equal(t, []string{"GET"}, u.methods)
}

func TestVideoDBConcurrentCustomerWorkerAndFrozenPrices(t *testing.T) {
	f := newVideoDBFixture(t, "grok")
	svc, in, u := f.service(t, `{"status":"done","video":{"url":"https://example.test/v.mp4","duration":10}}`)
	j := f.begin(t, svc, in, "grok-concurrent")
	in.APIKey.Group.VideoModelPrices[f.model]["720p"] = 99
	result := &service.OpenAIForwardResult{VideoCount: 1, VideoStatus: "done", VideoDurationSeconds: 10}
	var wg sync.WaitGroup
	errs := make(chan error, 17)
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- svc.ObserveVideoBilling(context.Background(), j, result) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); errs <- svc.RunVideoBillingRecovery(context.Background(), 4) }()
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	f.assertBill(t, 0.25)
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, m := range u.methods {
		require.Equal(t, "GET", m)
	}
}

func TestVideoDBLeaseFenceAndCrashBeforeDebit(t *testing.T) {
	f := newVideoDBFixture(t, "grok")
	svc, in, _ := f.service(t, `{"status":"done","video":{"url":"https://example.test/v.mp4","duration":6}}`)
	j := f.begin(t, svc, in, "lease-crash")
	repo := NewVideoBillingJobRepository(f.db)
	old, err := repo.Claim(context.Background(), j.ID, time.Minute)
	require.NoError(t, err)
	_, err = f.db.Exec(`UPDATE video_billing_jobs SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, j.ID)
	require.NoError(t, err)
	successor, err := repo.Claim(context.Background(), j.ID, time.Minute)
	require.NoError(t, err)
	require.NotEqual(t, old.LeaseToken, successor.LeaseToken)
	require.ErrorIs(t, repo.Finish(context.Background(), old, "submitted", "", "", 0), service.ErrVideoJobLeaseLost)
	_, err = repo.Apply(context.Background(), old)
	require.ErrorIs(t, err, service.ErrVideoJobLeaseLost)
	require.NoError(t, repo.Finish(context.Background(), successor, "submitted", "", "", 0))
	require.NoError(t, svc.RunVideoBillingRecovery(context.Background(), 3))
	f.assertBill(t, 0.15)
}

func TestVideoDBCrashAfterLedgerBeforeUsage(t *testing.T) {
	f := newVideoDBFixture(t, "grok")
	svc, in, _ := f.service(t, `{"status":"done","video":{"url":"https://example.test/v.mp4","duration":8}}`)
	j := f.begin(t, svc, in, "after-ledger")
	// Fail the production usage repository after the debit has committed.
	_, err := f.db.Exec(`CREATE FUNCTION video_usage_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic usage failure'; END $$;
 CREATE TRIGGER video_usage_fail BEFORE INSERT ON usage_logs FOR EACH ROW EXECUTE FUNCTION video_usage_fail()`)
	require.NoError(t, err)
	require.NoError(t, svc.RunVideoBillingRecovery(context.Background(), 1))
	var state string
	require.NoError(t, f.db.QueryRow(`SELECT state FROM video_billing_jobs WHERE id=$1`, j.ID).Scan(&state))
	require.Equal(t, "billed", state)
	_, err = f.db.Exec(`DROP TRIGGER video_usage_fail ON usage_logs;DROP FUNCTION video_usage_fail()`)
	require.NoError(t, err)
	require.NoError(t, svc.RetryVideoBillingJob(context.Background(), j.ID))
	fresh, _, _ := f.service(t, `{"status":"pending"}`)
	require.NoError(t, fresh.RunVideoBillingRecovery(context.Background(), 3))
	f.assertBill(t, 0.2)
}

func TestVideoDBLedgerTransactionRollback(t *testing.T) {
	f := newVideoDBFixture(t, "grok")
	svc, in, _ := f.service(t, `{"status":"done","video":{"url":"https://example.test/v.mp4","duration":8}}`)
	j := f.begin(t, svc, in, "rollback-ledger")
	_, err := f.db.Exec(`CREATE FUNCTION video_quota_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic quota failure'; END $$;
 CREATE TRIGGER video_quota_fail BEFORE UPDATE OF quota_used ON api_keys FOR EACH ROW EXECUTE FUNCTION video_quota_fail()`)
	require.NoError(t, err)
	require.NoError(t, svc.RunVideoBillingRecovery(context.Background(), 1))
	var balance float64
	require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.user).Scan(&balance))
	require.Equal(t, 100.0, balance)
	var count int
	require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM usage_billing_dedup WHERE api_key_id=$1`, f.key).Scan(&count))
	require.Zero(t, count)
	_, err = f.db.Exec(`DROP TRIGGER video_quota_fail ON api_keys;DROP FUNCTION video_quota_fail()`)
	require.NoError(t, err)
	require.NoError(t, svc.RetryVideoBillingJob(context.Background(), j.ID))
	require.NoError(t, svc.RunVideoBillingRecovery(context.Background(), 3))
	f.assertBill(t, 0.2)
}

func TestVideoDBOwnerRevocationAndMissingUsage(t *testing.T) {
	f := newVideoDBFixture(t, "seedance")
	svc, in, u := f.service(t, `{"status":"succeeded"}`)
	j := f.begin(t, svc, in, "seedance:missing-usage")
	repo := NewVideoBillingJobRepository(f.db)
	for _, ids := range [][3]int64{{f.user + 1, f.key, f.group}, {f.user, f.key + 1, f.group}, {f.user, f.key, f.group + 1}} {
		_, err := repo.GetForOwner(context.Background(), j.TaskID, ids[0], ids[1], ids[2])
		require.ErrorIs(t, err, service.ErrVideoJobNotFound)
	}
	require.NoError(t, svc.RunVideoBillingRecovery(context.Background(), 1))
	row, err := repo.GetForOwner(context.Background(), j.TaskID, f.user, f.key, f.group)
	require.NoError(t, err)
	require.Equal(t, "reconciliation", row.State)
	require.Equal(t, "completion_usage_missing", row.LastError)
	var balance float64
	require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.user).Scan(&balance))
	require.Equal(t, 100.0, balance)
	in.Account.Status = service.StatusDisabled
	require.NoError(t, svc.RetryVideoBillingJob(context.Background(), j.ID))
	require.NoError(t, svc.RunVideoBillingRecovery(context.Background(), 1))
	row, err = repo.GetForOwner(context.Background(), j.TaskID, f.user, f.key, f.group)
	require.NoError(t, err)
	require.Equal(t, "account_revoked", row.LastError)
	u.mu.Lock()
	require.Equal(t, 1, u.calls)
	u.body = `{"status":"succeeded","usage":{"completion_tokens":10}}`
	u.mu.Unlock()
	in.Account.Status = service.StatusActive
	require.NoError(t, svc.RetryVideoBillingJob(context.Background(), j.ID))
	require.NoError(t, svc.RunVideoBillingRecovery(context.Background(), 2))
	f.assertBill(t, 0.000025)
}

func TestVideoDBUnknownCreateNeverResentAndUpgradePreservesData(t *testing.T) {
	f := newVideoDBFixture(t, "grok")
	svc, in, u := f.service(t, "")
	j, err := svc.BeginVideoBillingJob(context.Background(), in, service.GrokVideoPendingBilling{Model: f.model, VideoResolution: "720p"}, "grok")
	require.NoError(t, err)
	_, err = f.db.Exec(`UPDATE video_billing_jobs SET available_at=NOW() WHERE id=$1`, j.ID)
	require.NoError(t, err)
	require.NoError(t, svc.RunVideoBillingRecovery(context.Background(), 2))
	jobs, err := repoListVideoTest(f.db)
	require.NoError(t, err)
	require.Equal(t, "reconciliation", jobs[0].State)
	require.Equal(t, "create_outcome_unknown_no_replay", jobs[0].LastError)
	require.Error(t, svc.RetryVideoBillingJob(context.Background(), j.ID))
	require.Zero(t, u.calls)
	require.NoError(t, ApplyMigrations(context.Background(), f.db))
	var balance float64
	require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.user).Scan(&balance))
	require.Equal(t, 100.0, balance)
}
func repoListVideoTest(db *sql.DB) ([]*service.VideoBillingJob, error) {
	return NewVideoBillingJobRepository(db).List(context.Background(), "", "", 50)
}

func TestVideoDBRejectedCreateCannotSubmitOrCharge(t *testing.T) {
	f := newVideoDBFixture(t, "grok")
	r := &videoBillingJobRepository{db: f.db}
	j := &service.VideoBillingJob{ID: uuid.NewString(), UserID: f.user, APIKeyID: f.key, AccountID: f.account, GroupID: f.group, Provider: "grok"}
	require.NoError(t, r.Create(context.Background(), j))
	require.NoError(t, r.RejectCreate(context.Background(), j.ID))
	require.ErrorIs(t, r.Submit(context.Background(), j.ID, "must-not-publish"), service.ErrVideoJobLeaseLost)
	var state string
	require.NoError(t, f.db.QueryRow(`SELECT state FROM video_billing_jobs WHERE id=$1`, j.ID).Scan(&state))
	require.Equal(t, "failed", state)
	var balance float64
	require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.user).Scan(&balance))
	require.Equal(t, 100.0, balance)
}
