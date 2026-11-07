package service

import (
	"context"
	"database/sql"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/keyusage"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

func ProvideKeyUsageRuntime(db *sql.DB) *keyusage.Runtime {
	runtime := keyusage.New(db)
	if err := runtime.Load(context.Background()); err != nil {
		logger.LegacyPrintf("service.key_usage", "optional key usage rollup unavailable; using raw statistics: %v", err)
	}
	return runtime
}

func ProvideUsageService(repo UsageLogRepository, users UserRepository, client *dbent.Client, invalidator APIKeyAuthCacheInvalidator, runtime *keyusage.Runtime, keyDaily UsageKeyDailyRollupRepository) *UsageService {
	s := NewUsageService(repo, users, client, invalidator)
	s.keyUsage = runtime
	// keyDaily 同时满足 UsageKeyDailyReader（读）与 UsageKeyDailyRollupRepository
	// （写/回填）。这里只持有读侧接口：读路径不该有能力触发回填。
	if keyDaily != nil {
		s.keyDaily = keyDaily
	}
	return s
}
