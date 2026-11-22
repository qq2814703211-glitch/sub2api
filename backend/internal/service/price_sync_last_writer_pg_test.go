package service_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPriceSyncLastWriterPostgresReplay(t *testing.T) {
	socket, root, audit := os.Getenv("LW_PG_SOCKET"), os.Getenv("PRICE_SYNC_SESSION_ROOT"), os.Getenv("PRICE_SYNC_AUDIT_ROOT")
	if socket == "" || root == "" || audit == "" {
		t.Skip("local Unix-socket PostgreSQL and sanitized snapshots required")
	}
	require.True(t, filepath.IsAbs(socket))
	u := url.URL{Scheme: "postgres", Path: "/postgres", User: url.User("lwtest")}
	v := url.Values{"host": {socket}, "port": {"55439"}, "sslmode": {"disable"}}
	u.RawQuery = v.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	var dir, tcp string
	require.NoError(t, db.QueryRow(`SELECT current_setting('unix_socket_directories'),current_setting('listen_addresses')`).Scan(&dir, &tcp))
	require.Equal(t, socket, dir)
	require.Empty(t, tcp)
	_, err = db.Exec(`CREATE TEMP TABLE model_pricing_sync_history(id bigint PRIMARY KEY,model text,field text,new_value numeric(24,14),source text,applied_at timestamptz,reason text)`)
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(root, "receipts/rc3-prod-pricing-history-5y90niui/history.json"))
	require.NoError(t, err)
	require.Equal(t, "bf083addaa127ca43383d33172d4ffb2bd512e32d8633a6fd1fec9d8d1120b9a", fmt.Sprintf("%x", sha256.Sum256(raw)))
	var export struct {
		History struct {
			Rows []struct {
				ID                           int64
				Model, Field, Source, Vendor string
				Value                        json.Number `json:"new_value"`
				At                           time.Time   `json:"applied_at"`
				Class                        string      `json:"writer_reason"`
				Latest                       bool        `json:"is_latest"`
			}
		}
	}
	require.NoError(t, json.Unmarshal(raw, &export))
	want := map[int64]bool{}
	keys := map[string]bool{}
	ctx := context.Background()
	tx, err := db.Begin()
	require.NoError(t, err)
	defer tx.Rollback()
	for _, r := range export.History.Rows {
		var reason string
		switch r.Class {
		case "unclassified":
		case "manual":
			reason = "按运营手工配置的人民币价写入（manual 优先级最高）"
		case "vendor_auto":
			require.NotEmpty(t, r.Vendor)
			reason = fmt.Sprintf("无手动定价，按 %s 官方人民币牌价补入", r.Vendor)
		case "ladder_shared":
			reason = "按官方人民币牌价折算长上下文阶梯（脱敏：共享阶梯）"
		default:
			t.Fatalf("unexpected export class %q", r.Class)
		}
		_, err = tx.Exec(`INSERT INTO pg_temp.model_pricing_sync_history VALUES($1,$2,$3,$4,$5,$6,$7)`, r.ID, r.Model, r.Field, string(r.Value), r.Source, r.At, reason)
		require.NoError(t, err)
		if r.Latest {
			want[r.ID] = true
		}
		keys[r.Model] = true
	}
	require.NoError(t, tx.Commit())
	repo := repository.NewModelPricingSyncHistoryRepository(db)
	reader := repo.(interface {
		ListLatestPricingFields(context.Context, []string) ([]service.ModelPricingSyncHistory, error)
	})
	var ids []string
	for k := range keys {
		ids = append(ids, k)
	}
	rows, err := reader.ListLatestPricingFields(ctx, ids)
	require.NoError(t, err)
	got := map[int64]bool{}
	for _, r := range rows {
		got[r.ID] = true
	}
	require.Equal(t, want, got)
	t.Logf("export=%d actual_winners=%d", len(export.History.Rows), len(rows))
	_, err = db.Exec(`INSERT INTO pg_temp.model_pricing_sync_history VALUES(-1,'tie','input',1,'cny-official',now(),'manual'),(-2,'tie','input',1,'models.dev',now(),'auto')`)
	require.NoError(t, err)
	rows, err = reader.ListLatestPricingFields(ctx, []string{"tie"})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int64(-1), rows[0].ID)
	require.Equal(t, "manual", rows[0].Reason)
	service.ReplayLastWriter(t, repo, root, audit)
}
