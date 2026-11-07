package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
)

const (
	defaultDashboardAggregationTimeout         = 2 * time.Minute
	defaultDashboardAggregationBackfillTimeout = 30 * time.Minute
	dashboardAggregationRetentionInterval      = 6 * time.Hour

	// dashboardAggregationLeaderLockKey 保证多副本部署中每个周期只有一个实例执行聚合。
	dashboardAggregationLeaderLockKey = "dashboard:aggregation:leader"
	// TTL 必须覆盖 dashboard 聚合与分组日汇总两个有界阶段，避免任务中途失锁。
	dashboardAggregationLeaderLockTTL = 5 * time.Minute

	// 启动回填耗时可能远长于周期聚合，因此使用独立锁并让 TTL 严格大于回填超时。
	dashboardAggregationGroupUsageBackfillLeaderLockKey = "dashboard:aggregation:group-usage-backfill:leader"
	dashboardAggregationGroupUsageBackfillLeaderLockTTL = defaultDashboardAggregationBackfillTimeout + time.Minute

	dashboardAggregationKeyUsageDailyBackfillLeaderLockKey = "dashboard:aggregation:key-usage-daily-backfill:leader"
	dashboardAggregationKeyUsageDailyBackfillLeaderLockTTL = defaultDashboardAggregationBackfillTimeout + time.Minute
	// 分组日汇总用独立锁：一个维度的同步失败不该拖住另一个。
	dashboardAggregationGroupDailyBackfillLeaderLockKey = "dashboard:aggregation:group-daily-backfill:leader"
	dashboardAggregationGroupDailyBackfillLeaderLockTTL = defaultDashboardAggregationBackfillTimeout + time.Minute
)

var (
	// ErrDashboardBackfillDisabled 当配置禁用回填时返回。
	ErrDashboardBackfillDisabled = errors.New("仪表盘聚合回填已禁用")
	// ErrDashboardBackfillTooLarge 当回填跨度超过限制时返回。
	ErrDashboardBackfillTooLarge   = errors.New("回填时间跨度过大")
	errDashboardAggregationRunning = errors.New("聚合作业正在运行")
)

// DashboardAggregationRepository 定义仪表盘预聚合仓储接口。
type DashboardAggregationRepository interface {
	AggregateRange(ctx context.Context, start, end time.Time) error
	// RecomputeRange 重新计算指定时间范围内的聚合数据（包含活跃用户等派生表）。
	// 设计目的：当 usage_logs 被批量删除/回滚后，确保聚合表可恢复一致性。
	RecomputeRange(ctx context.Context, start, end time.Time) error
	GetAggregationWatermark(ctx context.Context) (time.Time, error)
	UpdateAggregationWatermark(ctx context.Context, aggregatedAt time.Time) error
	CleanupAggregates(ctx context.Context, hourlyCutoff, dailyCutoff time.Time) error
	CleanupUsageLogs(ctx context.Context, cutoff time.Time) error
	CleanupUsageBillingDedup(ctx context.Context, cutoff time.Time) error
	EnsureUsageLogsPartitions(ctx context.Context, now time.Time) error
}

// DashboardAggregationService 负责定时聚合与回填。
type DashboardAggregationService struct {
	repo                 DashboardAggregationRepository
	timingWheel          *TimingWheelService
	cfg                  config.DashboardAggregationConfig
	settingRepo          SettingRepository
	running              int32
	lastRetentionCleanup atomic.Value // time.Time

	lockCache  LeaderLockCache
	db         *sql.DB
	instanceID string
}

// NewDashboardAggregationService 创建聚合服务。
func NewDashboardAggregationService(repo DashboardAggregationRepository, timingWheel *TimingWheelService, cfg *config.Config) *DashboardAggregationService {
	var aggCfg config.DashboardAggregationConfig
	if cfg != nil {
		aggCfg = cfg.DashboardAgg
	}
	return &DashboardAggregationService{
		repo:        repo,
		timingWheel: timingWheel,
		cfg:         aggCfg,
		instanceID:  uuid.NewString(),
	}
}

// SetLeaderLock injects the leader-lock cache and DB used to elect a single
// instance for the periodic scheduled aggregation. When both are nil the job runs
// ungated (single-instance / test behavior).
func (s *DashboardAggregationService) SetLeaderLock(lockCache LeaderLockCache, db *sql.DB) {
	if s == nil {
		return
	}
	s.lockCache = lockCache
	s.db = db
}

// Start 启动定时聚合作业（重启生效配置）。
func (s *DashboardAggregationService) Start() {
	if s == nil || s.repo == nil || s.timingWheel == nil {
		return
	}
	if !s.cfg.Enabled {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 聚合作业已禁用")
		// Explicit runtime retention remains available without dashboard aggregation.
		s.timingWheel.ScheduleRecurring("dashboard:retention", time.Minute, s.runScheduledRetention)
		return
	}
	go s.runStartupGroupUsageSync()
	go s.runStartupKeyUsageDailySync()

	interval := time.Duration(s.cfg.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = time.Minute
	}

	if s.cfg.RecomputeDays > 0 {
		go s.recomputeRecentDays()
	}

	s.timingWheel.ScheduleRecurring("dashboard:aggregation", interval, func() {
		s.runScheduledAggregation()
	})
	// 键日汇总与仪表盘聚合分开调度：它只是把「昨天」重算一遍（稳态一行
	// DELETE + 一行 upsert），没必要跟着回填窗口一起跑，也不该因为仪表盘
	// 聚合失败而停更。间隔跟聚合间隔取同一个值，语义上都是「数据新鲜度」。
	s.timingWheel.ScheduleRecurring("dashboard:key-usage-daily", interval, func() {
		s.runScheduledKeyUsageDailySync()
	})
	// 分组日汇总与键日汇总同一套语义、同一个间隔，但各自独立的 leader lock：
	// 一个维度的同步失败不该拖住另一个。它服务 monitor V3 的「30 天缓存率均值」，
	// 那条查询在 1763 万行的生产上直接扫全库要 19.7 秒。
	s.timingWheel.ScheduleRecurring("dashboard:group-usage-daily", interval, func() {
		s.runScheduledGroupDailySync()
	})
	go s.runStartupGroupDailySync()
	logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 聚合作业启动 (interval=%v, lookback=%ds)", interval, s.cfg.LookbackSeconds)
	if !s.cfg.BackfillEnabled {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 回填已禁用，如需补齐保留窗口以外历史数据请手动回填")
	}
}

// TriggerBackfill 触发回填（异步）。
func (s *DashboardAggregationService) TriggerBackfill(start, end time.Time) error {
	if s == nil || s.repo == nil {
		return errors.New("聚合服务未初始化")
	}
	if !s.cfg.BackfillEnabled {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 回填被拒绝: backfill_enabled=false")
		return ErrDashboardBackfillDisabled
	}
	if !end.After(start) {
		return errors.New("回填时间范围无效")
	}
	if s.cfg.BackfillMaxDays > 0 {
		maxRange := time.Duration(s.cfg.BackfillMaxDays) * 24 * time.Hour
		if end.Sub(start) > maxRange {
			return ErrDashboardBackfillTooLarge
		}
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), defaultDashboardAggregationBackfillTimeout)
		defer cancel()
		if err := s.backfillRange(ctx, start, end); err != nil {
			logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 回填失败: %v", err)
		}
	}()
	return nil
}

// TriggerRecomputeRange 触发指定范围的重新计算（异步）。
// 与 TriggerBackfill 不同：
// - 不依赖 backfill_enabled（这是内部一致性修复）
// - 不更新 watermark（避免影响正常增量聚合游标）
func (s *DashboardAggregationService) TriggerRecomputeRange(start, end time.Time) error {
	if s == nil || s.repo == nil {
		return errors.New("聚合服务未初始化")
	}
	if !s.cfg.Enabled {
		return errors.New("聚合服务已禁用")
	}
	if !end.After(start) {
		return errors.New("重新计算时间范围无效")
	}

	go func() {
		const maxRetries = 3
		for i := 0; i < maxRetries; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), defaultDashboardAggregationBackfillTimeout)
			err := s.recomputeRange(ctx, start, end)
			cancel()
			if err == nil {
				return
			}
			if !errors.Is(err, errDashboardAggregationRunning) {
				logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 重新计算失败: %v", err)
				return
			}
			time.Sleep(5 * time.Second)
		}
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 重新计算放弃: 聚合作业持续占用")
	}()
	return nil
}

func (s *DashboardAggregationService) recomputeRecentDays() {
	days := s.cfg.RecomputeDays
	if days <= 0 {
		return
	}
	now := time.Now().UTC()
	start := now.AddDate(0, 0, -days)

	ctx, cancel := context.WithTimeout(context.Background(), defaultDashboardAggregationBackfillTimeout)
	defer cancel()
	if err := s.backfillRange(ctx, start, now); err != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 启动重算失败: %v", err)
		return
	}
}

func (s *DashboardAggregationService) recomputeRange(ctx context.Context, start, end time.Time) error {
	if !atomic.CompareAndSwapInt32(&s.running, 0, 1) {
		return errDashboardAggregationRunning
	}
	defer atomic.StoreInt32(&s.running, 0)

	jobStart := time.Now().UTC()
	if err := s.repo.RecomputeRange(ctx, start, end); err != nil {
		return err
	}
	logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 重新计算完成 (start=%s end=%s duration=%s)",
		start.UTC().Format(time.RFC3339),
		end.UTC().Format(time.RFC3339),
		time.Since(jobStart).String(),
	)
	return nil
}

func (s *DashboardAggregationService) runScheduledAggregation() {
	if !atomic.CompareAndSwapInt32(&s.running, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&s.running, 0)

	jobStart := time.Now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), defaultDashboardAggregationTimeout)
	defer cancel()

	// Multi-instance guard: only the leader runs the periodic aggregation; peers
	// skip this cycle to avoid N× redundant GROUP BY queries and watermark races.
	release, ok := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, dashboardAggregationLeaderLockKey, s.instanceID, dashboardAggregationLeaderLockTTL)
	if !ok {
		return
	}
	defer release()
	defer s.runScheduledGroupUsageSync()

	now := time.Now().UTC()
	last, err := s.repo.GetAggregationWatermark(ctx)
	if err != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 读取水位失败: %v", err)
		last = time.Unix(0, 0).UTC()
	}

	lookback := time.Duration(s.cfg.LookbackSeconds) * time.Second
	epoch := time.Unix(0, 0).UTC()
	start := last.Add(-lookback)
	if !last.After(epoch) {
		retentionDays := s.cfg.Retention.UsageLogsDays
		if retentionDays <= 0 {
			retentionDays = 1
		}
		start = truncateToDayUTC(now.AddDate(0, 0, -retentionDays))
	} else if start.After(now) {
		start = now.Add(-lookback)
	}

	if err := s.aggregateRange(ctx, start, now); err != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 聚合失败: %v", err)
		return
	}

	updateErr := s.repo.UpdateAggregationWatermark(ctx, now)
	if updateErr != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 更新水位失败: %v", updateErr)
	}
	slog.Debug("[DashboardAggregation] 聚合完成",
		"start", start.Format(time.RFC3339),
		"end", now.Format(time.RFC3339),
		"duration", time.Since(jobStart).String(),
		"watermark_updated", updateErr == nil,
	)

	s.maybeCleanupRetention(ctx, now)
}

func (s *DashboardAggregationService) runScheduledGroupUsageSync() {
	ctx, cancel := context.WithTimeout(context.Background(), defaultDashboardAggregationTimeout)
	defer cancel()
	if err := s.syncGroupUsageRollups(ctx, time.Now().UTC()); err != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 分组用量日汇总失败: %v", err)
	}
}

// runScheduledKeyUsageDailySync 周期性把「昨天」发布进 usage_key_daily。
//
// 关掉开关后这里会立刻空转（Sync 读 state 就返回），不需要重启进程；读侧同时
// 立即回落到 usage_logs。也就是说「关开关」是秒级生效的降级手段。
func (s *DashboardAggregationService) runScheduledKeyUsageDailySync() {
	ctx, cancel := context.WithTimeout(context.Background(), defaultDashboardAggregationTimeout)
	defer cancel()
	if err := s.syncKeyUsageDailyRollups(ctx, time.Now().UTC()); err != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 键日汇总同步失败: %v", err)
	}
}

func (s *DashboardAggregationService) runStartupGroupUsageSync() {
	ctx, cancel := context.WithTimeout(context.Background(), defaultDashboardAggregationBackfillTimeout)
	defer cancel()
	release, ok := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, dashboardAggregationGroupUsageBackfillLeaderLockKey, s.instanceID, dashboardAggregationGroupUsageBackfillLeaderLockTTL)
	if !ok {
		return
	}
	defer release()
	if err := s.syncGroupUsageRollups(ctx, time.Now().UTC()); err != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 启动分组用量回填失败: %v", err)
	}
}

func (s *DashboardAggregationService) syncGroupUsageRollups(ctx context.Context, now time.Time) error {
	repo, ok := s.repo.(GroupUsageRollupRepository)
	if !ok {
		return nil
	}
	return repo.SyncGroupUsageRollups(ctx, GroupUsageTodayStart(now))
}

// syncKeyUsageDailyRollups 把已关闭的自然日发布进 usage_key_daily。
//
// 与分组日汇总同一套语义：只发布「今天以前」的日，今天由读侧用 usage_logs
// 现算。因此这里最坏情况只重算昨天一天，成本与历史深度无关。
//
// 用独立的 leader lock：键日汇总出错不应该拖住分组汇总，反之亦然。
func (s *DashboardAggregationService) syncKeyUsageDailyRollups(ctx context.Context, now time.Time) error {
	repo, ok := s.repo.(UsageKeyDailyRollupRepository)
	if !ok {
		return nil
	}
	return repo.Sync(ctx, now)
}

func (s *DashboardAggregationService) runStartupKeyUsageDailySync() {
	ctx, cancel := context.WithTimeout(context.Background(), defaultDashboardAggregationBackfillTimeout)
	defer cancel()
	release, ok := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, dashboardAggregationKeyUsageDailyBackfillLeaderLockKey, s.instanceID, dashboardAggregationKeyUsageDailyBackfillLeaderLockTTL)
	if !ok {
		return
	}
	defer release()
	if err := s.syncKeyUsageDailyRollups(ctx, time.Now().UTC()); err != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 启动键日汇总同步失败: %v", err)
	}
}

// runScheduledGroupDailySync 周期性把「昨天」发布进 usage_group_daily。
//
// 关掉开关后这里会立刻空转（Sync 读 state 就返回），不需要重启进程；读侧同时
// 立即回落到直接扫 usage_logs。也就是说「关开关」是秒级生效的降级手段。
func (s *DashboardAggregationService) runScheduledGroupDailySync() {
	ctx, cancel := context.WithTimeout(context.Background(), defaultDashboardAggregationTimeout)
	defer cancel()
	if err := s.syncGroupDailyRollups(ctx, time.Now().UTC()); err != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 分组日汇总同步失败: %v", err)
	}
}

// syncGroupDailyRollups 把已关闭的自然日发布进 usage_group_daily。
//
// 与键日汇总同一套语义：只发布「今天以前」的日，今天由读侧用 usage_logs 现算。
func (s *DashboardAggregationService) syncGroupDailyRollups(ctx context.Context, now time.Time) error {
	repo, ok := s.repo.(UsageGroupDailyRollupRepository)
	if !ok {
		return nil
	}
	return repo.Sync(ctx, now)
}

func (s *DashboardAggregationService) runStartupGroupDailySync() {
	ctx, cancel := context.WithTimeout(context.Background(), defaultDashboardAggregationBackfillTimeout)
	defer cancel()
	release, ok := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, dashboardAggregationGroupDailyBackfillLeaderLockKey, s.instanceID, dashboardAggregationGroupDailyBackfillLeaderLockTTL)
	if !ok {
		return
	}
	defer release()
	if err := s.syncGroupDailyRollups(ctx, time.Now().UTC()); err != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 启动分组日汇总同步失败: %v", err)
	}
}

func (s *DashboardAggregationService) backfillRange(ctx context.Context, start, end time.Time) error {
	if !atomic.CompareAndSwapInt32(&s.running, 0, 1) {
		return errDashboardAggregationRunning
	}
	defer atomic.StoreInt32(&s.running, 0)

	jobStart := time.Now().UTC()
	startUTC := start.UTC()
	endUTC := end.UTC()
	if !endUTC.After(startUTC) {
		return errors.New("回填时间范围无效")
	}

	cursor := truncateToDayUTC(startUTC)
	for cursor.Before(endUTC) {
		windowEnd := cursor.Add(24 * time.Hour)
		if windowEnd.After(endUTC) {
			windowEnd = endUTC
		}
		if err := s.aggregateRange(ctx, cursor, windowEnd); err != nil {
			return err
		}
		cursor = windowEnd
	}

	updateErr := s.repo.UpdateAggregationWatermark(ctx, endUTC)
	if updateErr != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 更新水位失败: %v", updateErr)
	}
	logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 回填聚合完成 (start=%s end=%s duration=%s watermark_updated=%t)",
		startUTC.Format(time.RFC3339),
		endUTC.Format(time.RFC3339),
		time.Since(jobStart).String(),
		updateErr == nil,
	)

	s.maybeCleanupRetention(ctx, endUTC)
	return nil
}

func (s *DashboardAggregationService) aggregateRange(ctx context.Context, start, end time.Time) error {
	if !end.After(start) {
		return nil
	}
	if err := s.repo.EnsureUsageLogsPartitions(ctx, end); err != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 分区检查失败: %v", err)
	}
	return s.repo.AggregateRange(ctx, start, end)
}

func (s *DashboardAggregationService) maybeCleanupRetention(ctx context.Context, now time.Time) {
	lastAny := s.lastRetentionCleanup.Load()
	if lastAny != nil {
		if last, ok := lastAny.(time.Time); ok && now.Sub(last) < dashboardAggregationRetentionInterval {
			return
		}
	}

	usageDays, err := s.requestRetentionDays(ctx)
	if err != nil {
		// Never fall back to a shorter window after a settings read failure.
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 读取日志保留设置失败，跳过清理: %v", err)
		return
	}
	hourlyCutoff := now.AddDate(0, 0, -s.cfg.Retention.HourlyDays)
	dailyCutoff := now.AddDate(0, 0, -s.cfg.Retention.DailyDays)
	usageCutoff := now.AddDate(0, 0, -usageDays)
	dedupDays := s.cfg.Retention.UsageBillingDedupDays
	if dedupDays <= 0 {
		dedupDays = 365
	}
	dedupDays = max(dedupDays, usageDays)
	dedupCutoff := now.AddDate(0, 0, -dedupDays)

	var aggErr, usageErr, dedupErr error
	if s.cfg.Enabled {
		aggErr = s.repo.CleanupAggregates(ctx, hourlyCutoff, dailyCutoff)
	}
	if aggErr != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 聚合保留清理失败: %v", aggErr)
	}
	if usageDays > 0 {
		usageErr = s.repo.CleanupUsageLogs(ctx, usageCutoff)
	}
	if usageErr != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] usage_logs 保留清理失败: %v", usageErr)
	}
	if usageDays > 0 {
		dedupErr = s.repo.CleanupUsageBillingDedup(ctx, dedupCutoff)
	}
	if dedupErr != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] usage_billing_dedup 保留清理失败: %v", dedupErr)
	}
	if aggErr == nil && usageErr == nil && dedupErr == nil {
		s.lastRetentionCleanup.Store(now)
	}
}

func (s *DashboardAggregationService) requestRetentionDays(ctx context.Context) (int, error) {
	days := s.cfg.Retention.UsageLogsDays
	if !s.cfg.Enabled {
		days = 0
	}
	if s.settingRepo == nil {
		return days, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOpsRuntimeLogConfig)
	if errors.Is(err, ErrSettingNotFound) {
		return days, nil
	}
	if err != nil {
		return 0, err
	}
	var cfg struct {
		RequestRetentionDays *int `json:"request_retention_days"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return 0, err
	}
	if cfg.RequestRetentionDays != nil {
		days = *cfg.RequestRetentionDays
		if days < 0 || days > 3650 {
			return 0, fmt.Errorf("invalid request_retention_days: %d", days)
		}
	}
	return days, nil
}

func (s *DashboardAggregationService) runScheduledRetention() {
	if !atomic.CompareAndSwapInt32(&s.running, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&s.running, 0)
	ctx, cancel := context.WithTimeout(context.Background(), defaultDashboardAggregationTimeout)
	defer cancel()
	release, ok := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, dashboardAggregationLeaderLockKey, s.instanceID, dashboardAggregationLeaderLockTTL)
	if !ok {
		return
	}
	defer release()
	s.maybeCleanupRetention(ctx, time.Now().UTC())
}

func truncateToDayUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
