//go:build unit

package accountopstest

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type upstreamHTTP struct{ client *http.Client }

func (u upstreamHTTP) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.client.Do(r)
}
func (u upstreamHTTP) DoWithTLS(r *http.Request, p string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(r, p, id, n)
}

type smtpFixture struct {
	listener net.Listener
	messages atomic.Int32
	wg       sync.WaitGroup
}

func newSMTP(t *testing.T) *smtpFixture {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, e)
	f := &smtpFixture{listener: l}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			func() {
				defer c.Close()
				r := bufio.NewReader(c)
				fmt.Fprint(c, "220 synthetic ESMTP\r\n")
				for {
					line, e := r.ReadString('\n')
					if e != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
						fmt.Fprint(c, "250-synthetic\r\n250 AUTH PLAIN\r\n")
					case strings.HasPrefix(line, "AUTH"):
						fmt.Fprint(c, "235 authenticated\r\n")
					case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
						fmt.Fprint(c, "250 OK\r\n")
					case strings.HasPrefix(line, "DATA"):
						fmt.Fprint(c, "354 End with dot\r\n")
						for {
							s, e := r.ReadString('\n')
							if e != nil {
								return
							}
							if s == ".\r\n" {
								break
							}
						}
						f.messages.Add(1)
						fmt.Fprint(c, "250 accepted\r\n")
					case strings.HasPrefix(line, "QUIT"):
						fmt.Fprint(c, "221 bye\r\n")
						return
					default:
						fmt.Fprint(c, "250 OK\r\n")
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { l.Close(); f.wg.Wait() })
	return f
}

func TestAccountOpsProducerDatabaseDeliveryRecovery(t *testing.T) {
	dsn := os.Getenv("ACCOUNT_OPS_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated ACCOUNT_OPS_TEST_DSN")
	}
	ctx := context.Background()
	db, e := sql.Open("postgres", dsn)
	require.NoError(t, e)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, db.PingContext(ctx))
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	// A second migration pass proves filename/checksum skip, preserving existing data.
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	accounts := repository.NewAccountRepository(client, db, nil)
	settings := repository.NewSettingRepository(client)
	smtp := newSMTP(t)
	host, port, _ := net.SplitHostPort(smtp.listener.Addr().String())
	require.NoError(t, settings.SetMultiple(ctx, map[string]string{service.SettingKeySMTPHost: host, service.SettingKeySMTPPort: port, service.SettingKeySMTPFrom: "source@example.test", service.SettingKeySMTPUseTLS: "false"}))
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Totp.EncryptionKey = strings.Repeat("19", 32)
	cfg.Totp.EncryptionKeyConfigured = true
	enc, e := repository.NewAESEncryptor(cfg)
	require.NoError(t, e)
	var remaining atomic.Int64
	remaining.Store(4)
	var rateFailure atomic.Bool
	var unit atomic.Value
	unit.Store("USD")
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		require.Equal(t, "Bearer sk-synthetic", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "billing") {
			if rateFailure.Load() {
				w.WriteHeader(401)
				return
			}
			http.NotFound(w, r)
			return
		}
		require.Equal(t, "/v1/usage", r.URL.Path)
		fmt.Fprintf(w, `{"isValid":true,"remaining":%d,"unit":"%s","quota":{"limit":100,"used":96,"remaining":4}}`, remaining.Load(), unit.Load())
	}))
	defer upstream.Close()
	account := &service.Account{Name: "synthetic-account", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Concurrency: 1, Credentials: map[string]any{"api_key": "sk-synthetic", "base_url": upstream.URL}, Extra: map[string]any{service.UpstreamBillingProbeEnabledExtraKey: true}}
	require.NoError(t, accounts.Create(ctx, account))
	repo := repository.NewAccountOpsRepository(db)
	ops := service.NewAccountOpsService(settings, repo, service.NewEmailService(settings, nil))
	ops.SetNotificationDependencies(accounts, enc, true, "UTC")
	require.NoError(t, ops.SaveConfig(ctx, service.AccountOpsConfig{Enabled: true, Recipient: "operator@example.test", CooldownMinutes: 5, SampleIntervalMinutes: 1, BalanceThresholds: []service.AccountOpsBalanceRule{{AccountID: account.ID, Enabled: true, Threshold: 5, Unit: "USD"}}}))
	probe := service.NewUpstreamBillingProbeService(accounts, service.NewAccountTestService(accounts, nil, nil, nil, nil, upstreamHTTP{client: upstream.Client()}, cfg, nil), nil)
	probe.SetAccountOpsService(ops)
	snap, e := probe.ProbeAccount(ctx, account.ID)
	require.NoError(t, e)
	require.NotNil(t, snap.Balance)
	require.Equal(t, 4.0, snap.Balance.Data["remaining"])
	require.EqualValues(t, 2, upstreamCalls.Load())
	require.NoError(t, ops.RunOnce(ctx))
	history, e := repo.List(ctx, 0, 50)
	require.NoError(t, e)
	require.Len(t, history, 1)
	require.Equal(t, "sent", history[0].State)
	require.Equal(t, "alert", history[0].Phase)
	require.Len(t, history[0].Deliveries, 1)
	require.EqualValues(t, 1, smtp.messages.Load())
	// Restart: no replay of acknowledged channel, no second episode from unchanged sample.
	restarted := service.NewAccountOpsService(settings, repository.NewAccountOpsRepository(db), service.NewEmailService(settings, nil))
	restarted.SetNotificationDependencies(accounts, enc, true, "UTC")
	require.NoError(t, restarted.RunOnce(ctx))
	require.EqualValues(t, 1, smtp.messages.Load())
	// Real stale persisted sample must not produce a recovery.
	_, e = db.ExecContext(ctx, `UPDATE accounts SET extra=jsonb_set(extra,'{upstream_billing_probe,balance,fresh_until}',to_jsonb((NOW()-INTERVAL '1 minute')::text)) WHERE id=$1`, account.ID)
	require.NoError(t, e)
	require.NoError(t, restarted.RunOnce(ctx))
	history, e = repo.List(ctx, 0, 50)
	require.NoError(t, e)
	require.Len(t, history, 1)
	remaining.Store(20)
	snap, e = probe.ProbeAccount(ctx, account.ID)
	require.NoError(t, e)
	require.NoError(t, restarted.RunOnce(ctx))
	// Advance durable stability baseline instead of waiting wall clock.
	_, e = db.ExecContext(ctx, `UPDATE account_ops_threshold_monitors SET healthy_since=NOW()-INTERVAL '20 seconds',checked_at=NOW()-INTERVAL '1 second' WHERE account_id=$1`, account.ID)
	require.NoError(t, e)
	require.NoError(t, restarted.RunOnce(ctx))
	history, e = repo.List(ctx, 0, 50)
	require.NoError(t, e)
	require.Len(t, history, 2)
	require.Equal(t, "recovery", history[0].Phase)
	require.Equal(t, "sent", history[0].State)
	require.Equal(t, history[0].EpisodeID, history[1].EpisodeID)
	require.EqualValues(t, 2, smtp.messages.Load())
	// Reader, history, currency aggregate and original producer time are persisted.
	monitor := service.NewUpstreamMonitorService(repository.NewUpstreamMonitorRepository(db))
	sample, e := monitor.SampleNow(ctx)
	require.NoError(t, e)
	require.Equal(t, 1, sample.Sampled)
	points, e := monitor.History(ctx, account.ID, time.Now().Add(-time.Hour))
	require.NoError(t, e)
	require.Len(t, points, 1)
	require.WithinDuration(t, *snap.Balance.ReceivedAt, points[0].SampledAt, time.Microsecond)
	overview, e := monitor.Overview(ctx)
	require.NoError(t, e)
	require.Len(t, overview.Currencies, 1)
	require.Equal(t, "USD", overview.Currencies[0].Unit)
	require.Equal(t, 20.0, overview.Currencies[0].Total)
	// Simultaneous claimers must obtain one owner. Expired owner cannot overwrite receipts.
	remaining.Store(2)
	_, e = probe.ProbeAccount(ctx, account.ID)
	require.NoError(t, e)
	// Observe without delivering: directly use durable repository seam with a new adverse identity.
	event := history[1]
	event.Criteria = "lease-probe"
	event.Identity = "lease-probe"
	event.AccountID = account.ID
	event.GroupID = "account:" + strconv.FormatInt(account.ID, 10)
	observer := repo.(interface {
		ObserveThreshold(context.Context, service.AccountOpsEvent, string, time.Time, bool, bool) error
	})
	require.NoError(t, observer.ObserveThreshold(ctx, event, "invalid", time.Now().Add(time.Second), true, true))
	require.NoError(t, observer.ObserveThreshold(ctx, event, "active", time.Now().Add(2*time.Second), true, true))
	// New invalidated baseline intentionally does not notify; create a fresh monitor to exercise claims.
	_, e = db.ExecContext(ctx, `UPDATE account_ops_threshold_events SET state='suppressed' WHERE account_id=$1 AND state='pending'`, account.ID)
	require.NoError(t, e)
	_, e = db.ExecContext(ctx, `DELETE FROM account_ops_threshold_monitors WHERE account_id=$1`, account.ID)
	require.NoError(t, e)
	require.NoError(t, observer.ObserveThreshold(ctx, event, "active", time.Now(), true, true))
	var wg sync.WaitGroup
	var owners atomic.Int32
	var owned *service.AccountOpsEvent
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := repo.Claim(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			if v != nil {
				owners.Add(1)
				mu.Lock()
				owned = v
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, owners.Load())
	require.NotNil(t, owned)
	_, e = db.ExecContext(ctx, `UPDATE account_ops_threshold_events SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, owned.ID)
	require.NoError(t, e)
	claimed, e := repository.NewAccountOpsRepository(db).Claim(ctx)
	require.NoError(t, e)
	require.NotNil(t, claimed)
	require.NotEqual(t, owned.Lease, claimed.Lease)
	receiptRepo := repo.(interface {
		SaveDelivery(context.Context, *service.AccountOpsEvent, string, service.AccountOpsDelivery) (bool, error)
	})
	ok, e := receiptRepo.SaveDelivery(ctx, owned, "mail", service.AccountOpsDelivery{Status: "sent"})
	require.NoError(t, e)
	require.False(t, ok)
	ok, e = receiptRepo.SaveDelivery(ctx, claimed, "mail", service.AccountOpsDelivery{Provider: "email", Status: "sent", Attempts: 1})
	require.NoError(t, e)
	require.True(t, ok)
	// Crash after receipt before completion: restart retains receipt and can skip channel.
	_, e = db.ExecContext(ctx, `UPDATE account_ops_threshold_events SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, claimed.ID)
	require.NoError(t, e)
	third, e := repo.Claim(ctx)
	require.NoError(t, e)
	require.NotNil(t, third)
	require.Equal(t, "sent", third.Deliveries["mail"].Status)
	raw, _ := json.Marshal(history)
	require.NotContains(t, string(raw), "sk-synthetic")
	t.Logf("producer_requests=%d smtp_deliveries=%d concurrent_claim_owners=%d durable_recovery=true", upstreamCalls.Load(), smtp.messages.Load(), owners.Load())

	// Failed transport/rate prerequisite cannot renew the old balance observation.
	old, e := accounts.GetByID(ctx, account.ID)
	require.NoError(t, e)
	rateFailure.Store(true)
	failed, e := probe.ProbeAccount(ctx, account.ID)
	require.NoError(t, e)
	require.Equal(t, "failed", failed.Balance.Status)
	require.Equal(t, 2.0, failed.Balance.Data["remaining"])
	before, _ := json.Marshal(old.Extra[service.UpstreamBillingProbeExtraKey])
	var prior service.UpstreamBillingProbeSnapshot
	require.NoError(t, json.Unmarshal(before, &prior))
	require.Equal(t, prior.Balance.ReceivedAt, failed.Balance.ReceivedAt)
	require.Equal(t, prior.Balance.FreshUntil, failed.Balance.FreshUntil)
	rateFailure.Store(false)
	// Two current balances remain separate currencies; corrupt legacy time is unknown/stale.
	unit.Store("CNY")
	remaining.Store(100)
	_, e = probe.ProbeAccount(ctx, account.ID)
	require.NoError(t, e)
	_, e = monitor.SampleNow(ctx)
	require.NoError(t, e)
	second := &service.Account{Name: "synthetic-usd", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Concurrency: 1, Credentials: map[string]any{"api_key": "sk-synthetic", "base_url": upstream.URL}, Extra: map[string]any{service.UpstreamBillingProbeEnabledExtraKey: true}}
	require.NoError(t, accounts.Create(ctx, second))
	c, e := ops.GetConfig(ctx)
	require.NoError(t, e)
	c.MonitorEnabled = true
	require.NoError(t, ops.SaveConfig(ctx, c))
	unit.Store("USD")
	remaining.Store(12)
	_, e = probe.ProbeAccount(ctx, second.ID)
	require.NoError(t, e)
	overview, e = monitor.Overview(ctx)
	require.NoError(t, e)
	require.Len(t, overview.Currencies, 2)
	totals := map[string]float64{}
	for _, v := range overview.Currencies {
		totals[v.Unit] = v.Total
	}
	require.Equal(t, map[string]float64{"CNY": 100, "USD": 12}, totals)
	// Re-applying the same observation cannot duplicate the daily decline.
	_, e = monitor.SampleNow(ctx)
	require.NoError(t, e)
	_, e = monitor.SampleNow(ctx)
	require.NoError(t, e)
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM upstream_balance_samples WHERE account_id=$1 AND unit='USD'`, second.ID).Scan(&count))
	require.Equal(t, 1, count)
	_, e = db.ExecContext(ctx, `UPDATE accounts SET extra=jsonb_set(extra,'{upstream_billing_probe,balance,fresh_until}','"not-a-date"'::jsonb) WHERE id=$1`, account.ID)
	require.NoError(t, e)
	listed, n, e := monitor.List(ctx, service.UpstreamMonitorFilter{BalanceStatus: "stale", Page: 1, PageSize: 20})
	require.NoError(t, e)
	require.EqualValues(t, 1, n)
	require.Nil(t, listed[0].Balance)
	overview, e = monitor.Overview(ctx)
	require.NoError(t, e)
	require.Len(t, overview.Currencies, 1)
	require.Equal(t, "USD", overview.Currencies[0].Unit)
	// Cross-process disable is checked by producer, not only its cached config.
	off := service.NewAccountOpsService(settings, repository.NewAccountOpsRepository(db), nil)
	off.SetNotificationDependencies(accounts, enc, true, "UTC")
	c, e = off.GetConfig(ctx)
	require.NoError(t, e)
	c.Enabled = false
	require.NoError(t, off.SaveConfig(ctx, c))
	calls := upstreamCalls.Load()
	_, e = probe.ProbeAccount(ctx, second.ID)
	require.NoError(t, e)
	require.EqualValues(t, 1, upstreamCalls.Load()-calls, "existing rate request remains but module adds no balance call")
	require.NoError(t, ops.RunOnce(ctx))
	require.EqualValues(t, 2, smtp.messages.Load())
	_ = io.EOF
}
