package keyusage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cleanup"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/lib/pq"
)

const SettingKey = "api_key_usage_rollup"
const lockID int64 = 729314902041

// Config is persisted only when an administrator explicitly changes it.
type Config struct {
	Enabled         bool `json:"enabled"`
	IntervalSeconds int  `json:"interval_seconds"`
}

func (c Config) normalized() (Config, error) {
	if c.IntervalSeconds == 0 {
		c.IntervalSeconds = 10
	}
	if c.IntervalSeconds < 1 || c.IntervalSeconds > 3600 {
		return c, fmt.Errorf("interval_seconds must be between 1 and 3600")
	}
	return c, nil
}

type Status struct {
	Config         Config `json:"config"`
	Running        bool   `json:"running"`
	CoveredHours   int64  `json:"covered_hours"`
	PendingChanges int64  `json:"pending_changes"`
	LastError      string `json:"last_error,omitempty"`
}

// Runtime owns only the optional rollup. A disabled runtime has no worker or
// write trigger; the request hot path is one atomic load and the legacy reader.
type Runtime struct {
	db              *sql.DB
	enabled         atomic.Bool
	gate            sync.RWMutex
	mu              sync.Mutex
	cfg             Config
	cancel          context.CancelFunc
	done            chan struct{}
	lastError       string
	errorMu         sync.Mutex
	lastWorkerError string
	clock           func() time.Time
	dayStart        func(time.Time) time.Time
}

func New(db *sql.DB) *Runtime {
	return &Runtime{db: db, clock: time.Now, dayStart: timezone.StartOfDay, cfg: Config{IntervalSeconds: 10}}
}

func (r *Runtime) Load(ctx context.Context) error {
	if r == nil || r.db == nil {
		return nil
	}
	var raw string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, SettingKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var c Config
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return err
	}
	c, err = c.normalized()
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c.Enabled {
		if c, err = r.install(ctx, c, false); err != nil {
			return err
		}
	}
	r.cfg = c
	r.enabled.Store(c.Enabled)
	if c.Enabled {
		r.startLocked(c)
	}
	return nil
}

// Configure changes schema and durable configuration atomically. Disabling
// removes capture before returning; enabling after a disabled period rebuilds
// coverage so changes made while off can never be mistaken for covered data.
func (r *Runtime) Configure(ctx context.Context, c Config) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("key usage runtime unavailable")
	}
	var err error
	c, err = c.normalized()
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked()
	r.gate.Lock()
	defer r.gate.Unlock()
	r.enabled.Store(false)
	if c, err = r.install(ctx, c, true); err != nil {
		r.lastError = err.Error()
		// Durable state remains unchanged if the transaction failed.
		if r.cfg.Enabled {
			r.enabled.Store(true)
			r.startLocked(r.cfg)
		}
		return err
	}
	r.cfg = c
	r.lastError = ""
	r.enabled.Store(c.Enabled)
	if c.Enabled {
		r.startLocked(c)
	}
	return nil
}

func (r *Runtime) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked()
	r.enabled.Store(false)
}
func (r *Runtime) stopLocked() {
	if r.cancel != nil {
		r.cancel()
		<-r.done
		r.cancel = nil
		r.done = nil
	}
}
func (r *Runtime) startLocked(c Config) {
	if r.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.done = make(chan struct{})
	go func() {
		defer close(r.done)
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			tickCtx, stop := context.WithTimeout(ctx, 30*time.Second)
			err := r.Step(tickCtx)
			stop()
			if err != nil && ctx.Err() == nil {
				r.recordError(err)
			}
			timer.Reset(time.Duration(c.IntervalSeconds) * time.Second)
		}
	}()
}

// Error storage uses a separate lock: shutdown may wait for the worker while
// holding the lifecycle mutex.
func (r *Runtime) recordError(err error) {
	r.errorMu.Lock()
	r.lastWorkerError = err.Error()
	r.errorMu.Unlock()
}

func (r *Runtime) Status(ctx context.Context) (status Status, retErr error) {
	r.mu.Lock()
	s := Status{Config: r.cfg, Running: r.cancel != nil, LastError: r.lastError}
	r.mu.Unlock()
	r.errorMu.Lock()
	if s.LastError == "" {
		s.LastError = r.lastWorkerError
	}
	r.errorMu.Unlock()
	if !r.enabled.Load() {
		return s, nil
	}
	r.gate.RLock()
	defer r.gate.RUnlock()
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return s, err
	}
	defer cleanup.Rollback(&retErr, tx)
	if _, err = tx.ExecContext(ctx, `LOCK TABLE usage_logs IN ACCESS SHARE MODE`); err != nil {
		return s, err
	}
	err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM key_usage_covered),(SELECT count(*) FROM key_usage_changes)`).Scan(&s.CoveredHours, &s.PendingChanges)
	return s, err
}

func (r *Runtime) Purge(ctx context.Context) (err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked()
	r.gate.Lock()
	defer r.gate.Unlock()
	r.enabled.Store(false)
	defer func() {
		if err != nil {
			r.lastError = err.Error()
			if r.cfg.Enabled {
				r.enabled.Store(true)
				r.startLocked(r.cfg)
			}
		}
	}()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer cleanup.Rollback(&err, tx)
	if _, err = tx.ExecContext(ctx, `SET LOCAL lock_timeout='3s'; SET LOCAL statement_timeout='15s'`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, lockID); err != nil {
		return err
	}
	schema, err := schemaName(ctx, tx)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `LOCK TABLE usage_logs IN ACCESS EXCLUSIVE MODE`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, dropCaptureSQL(schema)+dropTablesSQL(schema)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM settings WHERE key=$1`, SettingKey); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	r.cfg = Config{IntervalSeconds: 10}
	r.lastError = ""
	return nil
}

// TryRead keeps the caller's existing fallback and HTTP response unchanged.
// The handler must perform ownership checks before this method is reached.
func (r *Runtime) TryRead(ctx context.Context, ids []int64, start, end time.Time) (stats map[int64]*usagestats.BatchAPIKeyUsageStats, handled bool, retErr error) {
	if r == nil || !r.enabled.Load() {
		return nil, false, nil
	}
	r.gate.RLock()
	defer r.gate.RUnlock()
	if !r.enabled.Load() {
		return nil, false, nil
	}
	now := r.clock()
	if start.IsZero() {
		start = now.AddDate(0, 0, -30)
	}
	if end.IsZero() {
		end = now
	}
	today := r.dayStart(now)
	normalized := make([]int64, 0, len(ids))
	seen := make(map[int64]bool)
	result := make(map[int64]*usagestats.BatchAPIKeyUsageStats)
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			normalized = append(normalized, id)
			result[id] = &usagestats.BatchAPIKeyUsageStats{APIKeyID: id}
		}
	}
	if len(normalized) == 0 {
		return result, true, nil
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, true, err
	}
	defer cleanup.Rollback(&retErr, tx)
	if _, err = tx.ExecContext(ctx, `LOCK TABLE usage_logs IN ACCESS SHARE MODE`); err != nil {
		return nil, true, err
	}
	rows, err := tx.QueryContext(ctx, readSQL, pq.Array(normalized), start, end, today, SettingKey)
	if err != nil {
		var pe *pq.Error
		if errors.As(err, &pe) && (pe.Code == "42P01" || pe.Code == "42883") {
			return nil, false, nil
		}
		return nil, true, err
	}
	defer cleanup.Close(&retErr, rows)
	active := false
	for rows.Next() {
		var id sql.NullInt64
		var total, todayCost float64
		var enabled bool
		if err = rows.Scan(&id, &total, &todayCost, &enabled); err != nil {
			return nil, true, err
		}
		active = enabled
		if id.Valid {
			if s := result[id.Int64]; s != nil {
				s.TotalActualCost = total
				s.TodayActualCost = todayCost
			}
		}
	}
	if err = rows.Err(); err != nil {
		return nil, true, err
	}
	if !active {
		return nil, false, nil
	}
	return result, true, nil
}
