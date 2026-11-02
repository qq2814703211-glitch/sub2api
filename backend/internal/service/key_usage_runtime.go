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

func ProvideUsageService(repo UsageLogRepository, users UserRepository, client *dbent.Client, invalidator APIKeyAuthCacheInvalidator, runtime *keyusage.Runtime) *UsageService {
	s := NewUsageService(repo, users, client, invalidator)
	s.keyUsage = runtime
	return s
}
