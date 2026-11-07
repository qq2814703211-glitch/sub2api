//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// End-to-end timing for the keys-page paths, measured through the repository
// implementation against the production-shaped dataset (17.55M usage_logs rows).
//
// This exists because a single EXPLAIN is not the same thing as the latency a user
// sees: the fast path issues ONE statement that merges a rollup branch with a
// raw-logs tail, while the old path re-derives every day from raw rows. Timing the
// repository call is the only honest before/after.
//
//	PERF_DATABASE_URL='postgres://...' go test -tags integration -run TestPerfKeysPage -v ./internal/repository/

func perfDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("PERF_DATABASE_URL")
	if dsn == "" {
		t.Skip("PERF_DATABASE_URL not set")
	}
	if err := timezone.Init("Asia/Shanghai"); err != nil {
		t.Fatalf("timezone.Init: %v", err)
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	// Match the production DSN, which pins the session timezone. The legacy query
	// path depends on it while the rollup uses its own explicit zone, so a mismatch
	// here would make the comparison meaningless.
	if _, err := db.Exec("SET timezone='Asia/Shanghai'"); err != nil {
		t.Fatalf("set timezone: %v", err)
	}
	return db
}

func perfHotKeys(t *testing.T, db *sql.DB, n int) []int64 {
	t.Helper()
	rows, err := db.Query(`
		SELECT api_key_id FROM usage_logs
		GROUP BY api_key_id ORDER BY count(*) DESC LIMIT $1`, n)
	if err != nil {
		t.Fatalf("hot keys: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

// perfBest returns the fastest and median of n runs, so a single scheduler hiccup
// cannot masquerade as a regression.
func perfBest(n int, fn func() error) (fastest, median time.Duration, err error) {
	durations := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		start := time.Now()
		if err = fn(); err != nil {
			return 0, 0, err
		}
		durations = append(durations, time.Since(start))
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	return durations[0], durations[len(durations)/2], nil
}

func TestPerfKeysPageThroughRepository(t *testing.T) {
	db := perfDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	repo := newUsageKeyDailyRollupRepositoryWithSQL(db)
	reader := service.UsageKeyDailyReader(repo)
	now := time.Now()

	usable, err := reader.RollupUsable(ctx)
	if err != nil {
		t.Fatalf("RollupUsable: %v", err)
	}
	t.Logf("rollup usable = %v", usable)

	keys := perfHotKeys(t, db, 100)
	top := keys
	if len(top) > 5 {
		top = top[:5]
	}
	t.Logf("hottest keys (by row count): %v", top)

	// The window the keys page asks for: local midnight 30 days back through today.
	end := service.GroupUsageTodayStart(now).AddDate(0, 0, 1)
	start := end.AddDate(0, 0, -31)

	t.Run("FAST/DailyPoints single hot key", func(t *testing.T) {
		var pts []service.UsageKeyDailyPoint
		fastest, median, err := perfBest(5, func() error {
			p, err := reader.DailyPoints(ctx, keys[0], start, end, now)
			pts = p
			return err
		})
		if err != nil {
			t.Fatalf("DailyPoints: %v", err)
		}
		t.Logf("key=%d days=%d  fastest=%v median=%v", keys[0], len(pts), fastest, median)
	})

	t.Run("FAST/TotalsForKeys 100 keys", func(t *testing.T) {
		var got map[int64]service.UsageKeyDailyPoint
		fastest, median, err := perfBest(5, func() error {
			m, err := reader.TotalsForKeys(ctx, keys, start, end, now)
			got = m
			return err
		})
		if err != nil {
			t.Fatalf("TotalsForKeys: %v", err)
		}
		t.Logf("keys=%d returned=%d  fastest=%v median=%v", len(keys), len(got), fastest, median)
	})

	// The BEFORE shape: what the service used to run, straight off usage_logs.
	t.Run("BEFORE/DailyPoints raw 30d", func(t *testing.T) {
		var rows int
		fastest, median, err := perfBest(3, func() error {
			r, err := db.QueryContext(ctx, `
				SELECT TO_CHAR(date_trunc('day', created_at), 'YYYY-MM-DD'), COUNT(*),
				       COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
				       COALESCE(SUM(cache_creation_tokens),0), COALESCE(SUM(cache_read_tokens),0),
				       COALESCE(SUM(total_cost),0), COALESCE(SUM(actual_cost),0)
				FROM usage_logs
				WHERE created_at >= $1 AND created_at < $2 AND api_key_id = $3
				GROUP BY date_trunc('day', created_at)
				ORDER BY date_trunc('day', created_at)`, start, end, keys[0])
			if err != nil {
				return err
			}
			defer func() { _ = r.Close() }()
			rows = 0
			for r.Next() {
				rows++
			}
			return r.Err()
		})
		if err != nil {
			t.Fatalf("raw daily: %v", err)
		}
		t.Logf("key=%d days=%d  fastest=%v median=%v", keys[0], rows, fastest, median)
	})

	t.Run("BEFORE/TotalsForKeys 100 keys raw 30d", func(t *testing.T) {
		var rows int
		fastest, median, err := perfBest(3, func() error {
			r, err := db.QueryContext(ctx, `
				SELECT api_key_id, COUNT(*),
				       COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
				       COALESCE(SUM(cache_creation_tokens),0), COALESCE(SUM(cache_read_tokens),0),
				       COALESCE(SUM(total_cost),0), COALESCE(SUM(actual_cost),0),
				       COALESCE(SUM(COALESCE(duration_ms,0)),0), COUNT(*)
				FROM usage_logs
				WHERE created_at >= $1 AND created_at < $2 AND api_key_id = ANY($3)
				GROUP BY api_key_id`, start, end, pq.Array(keys))
			if err != nil {
				return err
			}
			defer func() { _ = r.Close() }()
			rows = 0
			for r.Next() {
				rows++
			}
			return r.Err()
		})
		if err != nil {
			t.Fatalf("raw totals: %v", err)
		}
		t.Logf("keys=%d returned=%d  fastest=%v median=%v", len(keys), rows, fastest, median)
	})
}

// TestPerfKeysPageEquivalence asserts the fast path and the old query produce
// identical day buckets for hot keys. Timing without this would be worthless: a
// fast wrong answer is not an improvement.
func TestPerfKeysPageEquivalence(t *testing.T) {
	db := perfDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	repo := newUsageKeyDailyRollupRepositoryWithSQL(db)
	reader := service.UsageKeyDailyReader(repo)
	now := time.Now()

	usable, err := reader.RollupUsable(ctx)
	if err != nil {
		t.Fatalf("RollupUsable: %v", err)
	}
	if !usable {
		t.Skip("rollup not usable; nothing to compare")
	}

	end := service.GroupUsageTodayStart(now).AddDate(0, 0, 1)
	start := end.AddDate(0, 0, -31)

	for _, key := range perfHotKeys(t, db, 5) {
		key := key
		t.Run(fmt.Sprintf("key_%d", key), func(t *testing.T) {
			fast, err := reader.DailyPoints(ctx, key, start, end, now)
			if err != nil {
				t.Fatalf("DailyPoints: %v", err)
			}

			r, err := db.QueryContext(ctx, `
				SELECT (created_at AT TIME ZONE 'Asia/Shanghai')::date, COUNT(*),
				       COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
				       COALESCE(SUM(cache_creation_tokens),0), COALESCE(SUM(cache_read_tokens),0),
				       COALESCE(SUM(total_cost),0), COALESCE(SUM(actual_cost),0)
				FROM usage_logs
				WHERE api_key_id = $1 AND created_at >= $2 AND created_at < $3
				GROUP BY 1 ORDER BY 1`, key, start, end)
			if err != nil {
				t.Fatalf("raw: %v", err)
			}
			defer func() { _ = r.Close() }()

			type dayRow struct {
				requests              int64
				in, out, cc, cr       int64
				totalCost, actualCost float64
			}
			want := map[string]dayRow{}
			for r.Next() {
				var d time.Time
				var v dayRow
				if err := r.Scan(&d, &v.requests, &v.in, &v.out, &v.cc, &v.cr, &v.totalCost, &v.actualCost); err != nil {
					t.Fatalf("scan: %v", err)
				}
				want[d.Format("2006-01-02")] = v
			}
			if err := r.Err(); err != nil {
				t.Fatalf("rows: %v", err)
			}

			if len(fast) != len(want) {
				t.Fatalf("day count: fast=%d raw=%d", len(fast), len(want))
			}
			for _, p := range fast {
				dayKey := p.Day.Format("2006-01-02")
				w, ok := want[dayKey]
				if !ok {
					t.Fatalf("fast path returned day %s absent from raw", dayKey)
				}
				if p.Requests != w.requests {
					t.Errorf("%s requests: fast=%d raw=%d", dayKey, p.Requests, w.requests)
				}
				if p.InputTokens != w.in || p.OutputTokens != w.out ||
					p.CacheCreationTokens != w.cc || p.CacheReadTokens != w.cr {
					t.Errorf("%s tokens mismatch: fast=[%d %d %d %d] raw=[%d %d %d %d]",
						dayKey, p.InputTokens, p.OutputTokens, p.CacheCreationTokens, p.CacheReadTokens,
						w.in, w.out, w.cc, w.cr)
				}
				if diff := p.TotalCost - w.totalCost; diff > 1e-9 || diff < -1e-9 {
					t.Errorf("%s total_cost: fast=%.10f raw=%.10f", dayKey, p.TotalCost, w.totalCost)
				}
				if diff := p.ActualCost - w.actualCost; diff > 1e-9 || diff < -1e-9 {
					t.Errorf("%s actual_cost: fast=%.10f raw=%.10f", dayKey, p.ActualCost, w.actualCost)
				}
			}
			t.Logf("key=%d days=%d all fields equal", key, len(fast))
		})
	}
}
