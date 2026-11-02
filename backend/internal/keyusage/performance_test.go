package keyusage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestRollupMillionRowPerformance(t *testing.T) {
	if os.Getenv("KEY_USAGE_BENCHMARK") != "1" {
		t.Skip("set KEY_USAGE_BENCHMARK=1 for explicit million-row benchmark")
	}
	r, db := fixture(t)
	ctx := context.Background()
	now := r.clock()
	start := now.AddDate(0, 0, -30)
	_, err := db.Exec(`ALTER TABLE usage_logs ADD COLUMN payload text`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO usage_logs(api_key_id,request_id,created_at,actual_cost,payload)
 SELECT CASE WHEN g%10<9 THEN 1 ELSE 2 END,'benchmark-'||g,
 $1::timestamptz+interval '1 second'*((g::bigint*7919)%2591999),((g%97)+1)::numeric/10000,repeat('sample-data-',24)
 FROM generate_series(1,1000000) g`, start)
	require.NoError(t, err)
	_, err = db.Exec(`VACUUM ANALYZE usage_logs`)
	require.NoError(t, err)
	enablePaused(t, r)
	cover(t, r, start, now)
	ids := []int64{1, 2, 3}
	sameAsRaw(t, r, ids, start, now)
	raw := `SELECT api_key_id,COALESCE(SUM(actual_cost) FILTER(WHERE created_at >= $2 AND created_at < $3),0),COALESCE(SUM(actual_cost) FILTER(WHERE created_at >= $4),0) FROM usage_logs WHERE api_key_id=ANY($1) AND created_at>=LEAST($2,$4) GROUP BY api_key_id`
	type measurement struct {
		Path  string          `json:"path"`
		Trial int             `json:"trial"`
		MS    float64         `json:"ms"`
		Plan  json.RawMessage `json:"plan"`
	}
	for i := 0; i < 5; i++ {
		for _, q := range []struct {
			name, sql string
			args      []any
		}{
			{"raw", raw, []any{pq.Array(ids), start, now, timezone.StartOfDay(now)}},
			{"rollup", readSQL, []any{pq.Array(ids), start, now, timezone.StartOfDay(now), SettingKey}},
		} {
			var plan []byte
			begin := time.Now()
			require.NoError(t, db.QueryRowContext(ctx, `EXPLAIN(ANALYZE,BUFFERS,FORMAT JSON) `+q.sql, q.args...).Scan(&plan))
			data, err := json.Marshal(measurement{q.name, i, float64(time.Since(begin).Microseconds()) / 1000, plan})
			require.NoError(t, err)
			fmt.Printf("KEY_USAGE_BENCH %s\n", data)
		}
	}
	var count int64
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM usage_logs`).Scan(&count))
	require.EqualValues(t, 1000000, count)
}
