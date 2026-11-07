package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
)

func ProvideAccountOpsService(settings SettingRepository, repo AccountOpsRepository, email *EmailService, accounts AccountRepository, encryptor SecretEncryptor, cfg *config.Config, usageCache *UsageCache, usageLogs UsageLogRepository, quota *GeminiQuotaService, monitor *UpstreamMonitorService) *AccountOpsService {
	svc := NewAccountOpsService(settings, repo, email)
	timezone := "UTC"
	fixed := false
	if cfg != nil {
		timezone = cfg.Timezone
		fixed = cfg.Totp.EncryptionKeyConfigured
	}
	svc.SetNotificationDependencies(accounts, encryptor, fixed, timezone)
	svc.SetNotificationUsageCache(usageCache)
	svc.SetNotificationQuotaReaders(usageLogs, quota)
	svc.monitor = monitor
	if monitor != nil {
		monitor.enabled = func(ctx context.Context) bool {
			c, e := svc.GetConfig(ctx)
			return e == nil && c.Enabled && c.MonitorEnabled
		}
	}
	if BackgroundJobsEnabled() {
		svc.Start()
	}
	return svc
}
