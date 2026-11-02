package keyusage

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRollupCaptureWritePerformance(t *testing.T) {
	if os.Getenv("KEY_USAGE_BENCHMARK") != "1" {
		t.Skip("explicit write benchmark")
	}
	r, db := fixture(t)
	now := r.clock()
	for _, mode := range []string{"disabled", "enabled"} {
		if mode == "enabled" {
			enablePaused(t, r)
		}
		for i := 0; i < 5; i++ {
			started := time.Now()
			for j := 0; j < 100; j++ {
				_, err := db.Exec(`INSERT INTO usage_logs(api_key_id,created_at,actual_cost) SELECT 1,$1::timestamptz,0.0001 FROM generate_series(1,64)`, now)
				require.NoError(t, err)
			}
			data, err := json.Marshal(map[string]any{"mode": mode, "trial": i, "rows": 6400, "statements": 100, "ms": float64(time.Since(started).Microseconds()) / 1000})
			require.NoError(t, err)
			fmt.Printf("KEY_USAGE_WRITE %s\n", data)
		}
	}
	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM key_usage_changes`).Scan(&n))
	require.Equal(t, 500, n, "one signed delta per bucket per insert statement")
}
