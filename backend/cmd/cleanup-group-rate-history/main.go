package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"time"

	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
)

// group_rate_history 只服务展示提示，并不参与计费。默认 dry-run，且必须
// 显式给出 RFC3339 截止时间，避免误删仍在 48h 展示窗口内的数据。
func main() {
	beforeRaw := flag.String("before", "", "required RFC3339 cutoff; only rows older than this are deleted")
	execute := flag.Bool("execute", false, "delete matched rows (default is dry-run)")
	batchSize := flag.Int("batch-size", 5000, "delete batch size (1-5000)")
	flag.Parse()

	if *beforeRaw == "" {
		log.Fatal("--before is required")
	}
	before, err := time.Parse(time.RFC3339, *beforeRaw)
	if err != nil {
		log.Fatalf("invalid --before: %v", err)
	}
	if *batchSize < 1 || *batchSize > 5000 {
		log.Fatal("--batch-size must be between 1 and 5000")
	}

	cfg, err := config.LoadForBootstrap()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	client, db, err := repository.InitEnt(cfg)
	if err != nil {
		log.Fatalf("initialize database: %v", err)
	}
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	matched, err := countBefore(ctx, db, before)
	if err != nil {
		log.Fatalf("count rows: %v", err)
	}
	deleted := int64(0)
	if *execute {
		deleted, err = deleteBefore(ctx, db, before, *batchSize)
		if err != nil {
			log.Fatalf("delete rows: %v", err)
		}
	}

	mode := "dry-run"
	if *execute {
		mode = "execute"
	}
	fmt.Printf("mode=%s before=%s matched=%d deleted=%d\n", mode, before.UTC().Format(time.RFC3339), matched, deleted)
	if *execute && deleted > 0 {
		fmt.Println("cleanup complete; schedule VACUUM (ANALYZE) group_rate_history during normal maintenance")
	}
}

func countBefore(ctx context.Context, db *sql.DB, before time.Time) (int64, error) {
	var count int64
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM group_rate_history WHERE changed_at < $1`, before.UTC()).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// deleteBefore 按 id 分批删除，避免一条长事务锁住整张历史表。每批最多
// batchSize 行，直到没有更旧的行。
func deleteBefore(ctx context.Context, db *sql.DB, before time.Time, batchSize int) (int64, error) {
	var total int64
	for {
		result, err := db.ExecContext(ctx, `
			DELETE FROM group_rate_history
			WHERE id IN (
				SELECT id FROM group_rate_history
				WHERE changed_at < $1
				ORDER BY id
				LIMIT $2
			)`, before.UTC(), batchSize)
		if err != nil {
			return total, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
		if n < int64(batchSize) {
			return total, nil
		}
	}
}
