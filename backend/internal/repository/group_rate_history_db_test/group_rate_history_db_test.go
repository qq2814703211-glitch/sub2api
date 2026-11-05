//go:build dbtest

// Package groupratehistorydbtest 是针对独立 PostgreSQL 实例的黑盒验证：
// 真实触发器写入、真实仓储读取和真实服务信号。它使用独立构建标签，避免
// 常规单测依赖数据库；DSN 由 GROUP_RATE_HISTORY_TEST_POSTGRES_DSN 提供。
package groupratehistorydbtest

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/group"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

type recordedRateChange struct {
	oldRate float64
	newRate float64
}

func openTestDB(t *testing.T) (*sql.DB, *dbent.Client) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("GROUP_RATE_HISTORY_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("GROUP_RATE_HISTORY_TEST_POSTGRES_DSN is not set; skipping real-PostgreSQL verification")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	require.NoError(t, db.Ping())
	require.NoError(t, repository.ApplyMigrations(context.Background(), db))

	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() {
		_ = client.Close()
		_ = db.Close()
	})
	return db, client
}

func asHistoryReader(t *testing.T, repo service.GroupRepository) service.GroupRateHistoryReader {
	t.Helper()
	reader, ok := repo.(service.GroupRateHistoryReader)
	require.True(t, ok, "group repository must implement GroupRateHistoryReader")
	return reader
}

func createGroup(t *testing.T, db *sql.DB, client *dbent.Client, name string, initialRate float64) *dbent.Group {
	t.Helper()
	g, err := client.Group.Create().
		SetName(name).
		SetPlatform(service.PlatformOpenAI).
		SetStatus(service.StatusActive).
		SetSubscriptionType(service.SubscriptionTypeStandard).
		SetRateMultiplier(initialRate).
		Save(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM group_rate_history WHERE group_id = $1", g.ID)
		_ = client.Group.DeleteOneID(g.ID).Exec(context.Background())
	})
	return g
}

func readHistory(t *testing.T, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, groupID int64) []recordedRateChange {
	t.Helper()
	rows, err := q.QueryContext(context.Background(),
		"SELECT old_rate, new_rate FROM group_rate_history WHERE group_id = $1 ORDER BY id", groupID)
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()

	var out []recordedRateChange
	for rows.Next() {
		var row recordedRateChange
		require.NoError(t, rows.Scan(&row.oldRate, &row.newRate))
		out = append(out, row)
	}
	require.NoError(t, rows.Err())
	return out
}

func TestGroupRateHistoryTriggerRecordsEveryWriterInTheSameTransaction(t *testing.T) {
	db, client := openTestDB(t)
	ctx := context.Background()
	repo := repository.NewGroupRepository(client, db)

	created := createGroup(t, db, client, "rate-history-writers", 1)
	other := createGroup(t, db, client, "rate-history-writers-other", 1)
	require.Empty(t, readHistory(t, db, created.ID), "创建分组不是调价")

	// 后台保存路径：Update 会回写所有列，未变化的倍率不得留下记录。
	loaded, err := repo.GetByIDLite(ctx, created.ID)
	require.NoError(t, err)
	require.NoError(t, repo.Update(ctx, loaded))
	require.Empty(t, readHistory(t, db, created.ID), "保存相同倍率不记录")

	loaded.RateMultiplier = 0.5
	require.NoError(t, repo.Update(ctx, loaded))
	require.Equal(t, []recordedRateChange{{1, 0.5}}, readHistory(t, db, created.ID))

	// 单行 ent 更新。
	_, err = client.Group.UpdateOneID(created.ID).SetRateMultiplier(0.75).Save(ctx)
	require.NoError(t, err)
	require.Equal(t, []recordedRateChange{{1, 0.5}, {0.5, 0.75}}, readHistory(t, db, created.ID))

	// 批量更新：一条语句覆盖多个分组。
	affected, err := client.Group.Update().
		Where(group.IDIn(created.ID, other.ID)).
		SetRateMultiplier(3).
		Save(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, affected)
	require.Equal(t, []recordedRateChange{{1, 0.5}, {0.5, 0.75}, {0.75, 3}}, readHistory(t, db, created.ID))
	require.Equal(t, []recordedRateChange{{1, 3}}, readHistory(t, db, other.ID))

	// 运维手写 SQL。
	_, err = db.ExecContext(ctx, "UPDATE groups SET rate_multiplier = $1 WHERE id = $2", 4, created.ID)
	require.NoError(t, err)
	require.Equal(t, []recordedRateChange{{1, 0.5}, {0.5, 0.75}, {0.75, 3}, {3, 4}}, readHistory(t, db, created.ID))

	// 任何形式写入相同值都不追加。
	_, err = db.ExecContext(ctx, "UPDATE groups SET rate_multiplier = rate_multiplier WHERE id = $1", created.ID)
	require.NoError(t, err)
	_, err = client.Group.UpdateOneID(created.ID).SetRateMultiplier(4).Save(ctx)
	require.NoError(t, err)
	require.Len(t, readHistory(t, db, created.ID), 4)

	// 峰时/媒体倍率是不同开关，不进入基础倍率历史。
	_, err = client.Group.UpdateOneID(created.ID).SetPeakRateMultiplier(2).SetImageRateMultiplier(3).Save(ctx)
	require.NoError(t, err)
	require.Len(t, readHistory(t, db, created.ID), 4)
}

func TestGroupRateHistoryRollsBackWithTheEdit(t *testing.T) {
	db, client := openTestDB(t)
	ctx := context.Background()
	g := createGroup(t, db, client, "rate-history-rollback", 1)

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "SAVEPOINT before_rate_edit")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "UPDATE groups SET rate_multiplier = 2 WHERE id = $1", g.ID)
	require.NoError(t, err)
	require.Len(t, readHistory(t, tx, g.ID), 1, "编辑事务内可见历史")

	_, err = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT before_rate_edit")
	require.NoError(t, err)
	require.Empty(t, readHistory(t, tx, g.ID), "回滚的编辑不留下历史")
	require.NoError(t, tx.Commit())

	var rate float64
	require.NoError(t, db.QueryRowContext(ctx, "SELECT rate_multiplier FROM groups WHERE id = $1", g.ID).Scan(&rate))
	require.InDelta(t, 1, rate, 1e-9)
}

func TestListGroupRateChangesSinceReadsAnOrderedWindow(t *testing.T) {
	db, client := openTestDB(t)
	ctx := context.Background()
	repo := repository.NewGroupRepository(client, db)

	a := createGroup(t, db, client, "rate-history-window-a", 1)
	b := createGroup(t, db, client, "rate-history-window-b", 1)
	c := createGroup(t, db, client, "rate-history-window-c", 1)

	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	insert := func(groupID int64, oldRate, newRate float64, at time.Time) {
		_, err := db.ExecContext(ctx,
			"INSERT INTO group_rate_history (group_id, old_rate, new_rate, changed_at) VALUES ($1, $2, $3, $4)",
			groupID, oldRate, newRate, at)
		require.NoError(t, err)
	}
	insert(b.ID, 1, 2, base.Add(3*time.Hour))
	insert(a.ID, 2, 3, base.Add(2*time.Hour))
	insert(a.ID, 1, 2, base.Add(1*time.Hour))
	insert(a.ID, 0.5, 1, base) // 截止时间之前
	insert(c.ID, 1, 9, base.Add(5*time.Hour))

	got, err := asHistoryReader(t, repo).ListGroupRateChangesSince(ctx, []int64{a.ID, b.ID}, base.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, got, 3, "c 未请求，a 的最旧一行早于截止时间")
	require.Equal(t, a.ID, got[0].GroupID)
	require.Equal(t, base.Add(time.Hour).UTC(), got[0].ChangedAt.UTC(), "包含截止时间本身")
	require.Equal(t, a.ID, got[1].GroupID)
	require.InDelta(t, 2, got[1].OldRate, 1e-9)
	require.InDelta(t, 3, got[1].NewRate, 1e-9)
	require.Equal(t, base.Add(2*time.Hour).UTC(), got[1].ChangedAt.UTC(), "同一分组按时间从旧到新")
	require.Equal(t, b.ID, got[2].GroupID, "分组按 id 顺序返回")

	none, err := asHistoryReader(t, repo).ListGroupRateChangesSince(ctx, []int64{a.ID}, base.Add(24*time.Hour))
	require.NoError(t, err)
	require.Empty(t, none)
}

// 整条链路对真实触发器：后台调价 -> 历史行 -> 服务提示。
func TestGroupSignalsFollowRealRateEdits(t *testing.T) {
	db, client := openTestDB(t)
	ctx := context.Background()
	repo := repository.NewGroupRepository(client, db)
	svc := service.NewAPIKeyService(nil, nil, repo, nil, nil, nil, nil)

	established := createGroup(t, db, client, "signals-established", 0.06)
	fresh := createGroup(t, db, client, "signals-fresh", 0.06)
	quiet := createGroup(t, db, client, "signals-quiet", 0.06)

	for _, edit := range []struct {
		id   int64
		rate float64
	}{{established.ID, 0.08}, {fresh.ID, 0.07}} {
		g, getErr := repo.GetByIDLite(ctx, edit.id)
		require.NoError(t, getErr)
		g.RateMultiplier = edit.rate
		require.NoError(t, repo.Update(ctx, g))
	}
	// 让 established/quiet 成为旧分组；fresh 保持刚刚创建。
	_, err := db.ExecContext(ctx, "UPDATE groups SET created_at = NOW() - INTERVAL '30 days' WHERE id = ANY($1)",
		pq.Array([]int64{established.ID, quiet.ID}))
	require.NoError(t, err)

	groups := make([]service.Group, 0, 3)
	for _, id := range []int64{established.ID, fresh.ID, quiet.ID} {
		g, getErr := repo.GetByIDLite(ctx, id)
		require.NoError(t, getErr)
		groups = append(groups, *g)
	}
	signals := svc.GetGroupSignals(ctx, 1, groups, time.Now())

	require.NotContains(t, signals, quiet.ID, "没有调价的旧分组没有提示")

	up := signals[established.ID]
	require.NotNil(t, up.RecentRateChange)
	require.Equal(t, service.GroupRateUp, up.RecentRateChange.Direction)
	require.InDelta(t, 0.02, up.RecentRateChange.Delta, 1e-9)
	require.InDelta(t, 0.06, up.RecentRateChange.PreviousRate, 1e-9)
	require.False(t, up.IsNew)

	newAndChanged := signals[fresh.ID]
	require.True(t, newAndChanged.IsNew)
	require.NotNil(t, newAndChanged.LaunchRate)
	require.InDelta(t, 0.06, *newAndChanged.LaunchRate, 1e-9, "初始倍率是首次调价前的值")
	require.NotNil(t, newAndChanged.RecentRateChange)
	require.InDelta(t, 0.06, newAndChanged.RecentRateChange.PreviousRate, 1e-9)
}
