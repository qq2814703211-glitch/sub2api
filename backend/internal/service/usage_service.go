package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/keyusage"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

var (
	ErrUsageLogNotFound = infraerrors.NotFound("USAGE_LOG_NOT_FOUND", "usage log not found")
)

// CreateUsageLogRequest 创建使用日志请求
type CreateUsageLogRequest struct {
	UserID                int64   `json:"user_id"`
	APIKeyID              int64   `json:"api_key_id"`
	AccountID             int64   `json:"account_id"`
	RequestID             string  `json:"request_id"`
	Model                 string  `json:"model"`
	InputTokens           int     `json:"input_tokens"`
	OutputTokens          int     `json:"output_tokens"`
	CacheCreationTokens   int     `json:"cache_creation_tokens"`
	CacheReadTokens       int     `json:"cache_read_tokens"`
	CacheCreation5mTokens int     `json:"cache_creation_5m_tokens"`
	CacheCreation1hTokens int     `json:"cache_creation_1h_tokens"`
	InputCost             float64 `json:"input_cost"`
	OutputCost            float64 `json:"output_cost"`
	CacheCreationCost     float64 `json:"cache_creation_cost"`
	CacheReadCost         float64 `json:"cache_read_cost"`
	TotalCost             float64 `json:"total_cost"`
	ActualCost            float64 `json:"actual_cost"`
	RateMultiplier        float64 `json:"rate_multiplier"`
	Stream                bool    `json:"stream"`
	DurationMs            *int    `json:"duration_ms"`
}

// UsageStats 使用统计
type UsageStats struct {
	TotalRequests            int64   `json:"total_requests"`
	TotalInputTokens         int64   `json:"total_input_tokens"`
	TotalOutputTokens        int64   `json:"total_output_tokens"`
	TotalCacheTokens         int64   `json:"total_cache_tokens"`
	TotalCacheCreationTokens int64   `json:"total_cache_creation_tokens"`
	TotalCacheReadTokens     int64   `json:"total_cache_read_tokens"`
	TotalTokens              int64   `json:"total_tokens"`
	TotalCost                float64 `json:"total_cost"`
	TotalActualCost          float64 `json:"total_actual_cost"`
	AverageDurationMs        float64 `json:"average_duration_ms"`
}

// UsageService 使用统计服务
type UsageService struct {
	keyUsage             *keyusage.Runtime
	keyDaily             UsageKeyDailyReader
	usageRepo            UsageLogRepository
	userRepo             UserRepository
	entClient            *dbent.Client
	authCacheInvalidator APIKeyAuthCacheInvalidator
}

// NewUsageService 创建使用统计服务实例
func NewUsageService(usageRepo UsageLogRepository, userRepo UserRepository, entClient *dbent.Client, authCacheInvalidator APIKeyAuthCacheInvalidator) *UsageService {
	return &UsageService{
		usageRepo:            usageRepo,
		userRepo:             userRepo,
		entClient:            entClient,
		authCacheInvalidator: authCacheInvalidator,
	}
}

// Create 创建使用日志
func (s *UsageService) Create(ctx context.Context, req CreateUsageLogRequest) (*UsageLog, error) {
	// 使用数据库事务保证「使用日志插入」与「扣费」的原子性，避免重复扣费或漏扣风险。
	tx, err := s.entClient.Tx(ctx)
	if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}

	txCtx := ctx
	if err == nil {
		defer func() { _ = tx.Rollback() }()
		txCtx = dbent.NewTxContext(ctx, tx)
	}

	// 验证用户存在
	_, err = s.userRepo.GetByID(txCtx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	// 创建使用日志
	usageLog := &UsageLog{
		UserID:                req.UserID,
		APIKeyID:              req.APIKeyID,
		AccountID:             req.AccountID,
		RequestID:             req.RequestID,
		Model:                 req.Model,
		InputTokens:           req.InputTokens,
		OutputTokens:          req.OutputTokens,
		CacheCreationTokens:   req.CacheCreationTokens,
		CacheReadTokens:       req.CacheReadTokens,
		CacheCreation5mTokens: req.CacheCreation5mTokens,
		CacheCreation1hTokens: req.CacheCreation1hTokens,
		InputCost:             req.InputCost,
		OutputCost:            req.OutputCost,
		CacheCreationCost:     req.CacheCreationCost,
		CacheReadCost:         req.CacheReadCost,
		TotalCost:             req.TotalCost,
		ActualCost:            req.ActualCost,
		RateMultiplier:        req.RateMultiplier,
		Stream:                req.Stream,
		DurationMs:            req.DurationMs,
	}

	inserted, err := s.usageRepo.Create(txCtx, usageLog)
	if err != nil {
		return nil, fmt.Errorf("create usage log: %w", err)
	}

	// 扣除用户余额
	balanceUpdated := false
	if inserted && req.ActualCost > 0 {
		if err := s.userRepo.UpdateBalance(txCtx, req.UserID, -req.ActualCost); err != nil {
			return nil, fmt.Errorf("update user balance: %w", err)
		}
		balanceUpdated = true
	}

	if tx != nil {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit transaction: %w", err)
		}
	}

	s.invalidateUsageCaches(ctx, req.UserID, balanceUpdated)

	return usageLog, nil
}

func (s *UsageService) invalidateUsageCaches(ctx context.Context, userID int64, balanceUpdated bool) {
	if !balanceUpdated || s.authCacheInvalidator == nil {
		return
	}
	s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
}

// GetByID 根据ID获取使用日志
func (s *UsageService) GetByID(ctx context.Context, id int64) (*UsageLog, error) {
	log, err := s.usageRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get usage log: %w", err)
	}
	return log, nil
}

// ListByUser 获取用户的使用日志列表
func (s *UsageService) ListByUser(ctx context.Context, userID int64, params pagination.PaginationParams) ([]UsageLog, *pagination.PaginationResult, error) {
	logs, pagination, err := s.usageRepo.ListByUser(ctx, userID, params)
	if err != nil {
		return nil, nil, fmt.Errorf("list usage logs: %w", err)
	}
	return logs, pagination, nil
}

// ListByAPIKey 获取API Key的使用日志列表
func (s *UsageService) ListByAPIKey(ctx context.Context, apiKeyID int64, params pagination.PaginationParams) ([]UsageLog, *pagination.PaginationResult, error) {
	logs, pagination, err := s.usageRepo.ListByAPIKey(ctx, apiKeyID, params)
	if err != nil {
		return nil, nil, fmt.Errorf("list usage logs: %w", err)
	}
	return logs, pagination, nil
}

// ListByAccount 获取账号的使用日志列表
func (s *UsageService) ListByAccount(ctx context.Context, accountID int64, params pagination.PaginationParams) ([]UsageLog, *pagination.PaginationResult, error) {
	logs, pagination, err := s.usageRepo.ListByAccount(ctx, accountID, params)
	if err != nil {
		return nil, nil, fmt.Errorf("list usage logs: %w", err)
	}
	return logs, pagination, nil
}

// GetStatsByUser 获取用户的使用统计
func (s *UsageService) GetStatsByUser(ctx context.Context, userID int64, startTime, endTime time.Time) (*UsageStats, error) {
	stats, err := s.usageRepo.GetUserStatsAggregated(ctx, userID, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("get user stats: %w", err)
	}

	return &UsageStats{
		TotalRequests:            stats.TotalRequests,
		TotalInputTokens:         stats.TotalInputTokens,
		TotalOutputTokens:        stats.TotalOutputTokens,
		TotalCacheTokens:         stats.TotalCacheTokens,
		TotalCacheCreationTokens: stats.TotalCacheCreationTokens,
		TotalCacheReadTokens:     stats.TotalCacheReadTokens,
		TotalTokens:              stats.TotalTokens,
		TotalCost:                stats.TotalCost,
		TotalActualCost:          stats.TotalActualCost,
		AverageDurationMs:        stats.AverageDurationMs,
	}, nil
}

// GetStatsByAPIKey 获取API Key的使用统计
func (s *UsageService) GetStatsByAPIKey(ctx context.Context, apiKeyID int64, startTime, endTime time.Time) (*UsageStats, error) {
	stats, err := s.usageRepo.GetAPIKeyStatsAggregated(ctx, apiKeyID, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("get api key stats: %w", err)
	}

	return &UsageStats{
		TotalRequests:            stats.TotalRequests,
		TotalInputTokens:         stats.TotalInputTokens,
		TotalOutputTokens:        stats.TotalOutputTokens,
		TotalCacheTokens:         stats.TotalCacheTokens,
		TotalCacheCreationTokens: stats.TotalCacheCreationTokens,
		TotalCacheReadTokens:     stats.TotalCacheReadTokens,
		TotalTokens:              stats.TotalTokens,
		TotalCost:                stats.TotalCost,
		TotalActualCost:          stats.TotalActualCost,
		AverageDurationMs:        stats.AverageDurationMs,
	}, nil
}

// GetStatsByAccount 获取账号的使用统计
func (s *UsageService) GetStatsByAccount(ctx context.Context, accountID int64, startTime, endTime time.Time) (*UsageStats, error) {
	stats, err := s.usageRepo.GetAccountStatsAggregated(ctx, accountID, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("get account stats: %w", err)
	}

	return &UsageStats{
		TotalRequests:            stats.TotalRequests,
		TotalInputTokens:         stats.TotalInputTokens,
		TotalOutputTokens:        stats.TotalOutputTokens,
		TotalCacheTokens:         stats.TotalCacheTokens,
		TotalCacheCreationTokens: stats.TotalCacheCreationTokens,
		TotalCacheReadTokens:     stats.TotalCacheReadTokens,
		TotalTokens:              stats.TotalTokens,
		TotalCost:                stats.TotalCost,
		TotalActualCost:          stats.TotalActualCost,
		AverageDurationMs:        stats.AverageDurationMs,
	}, nil
}

// GetStatsByModel 获取模型的使用统计
func (s *UsageService) GetStatsByModel(ctx context.Context, modelName string, startTime, endTime time.Time) (*UsageStats, error) {
	stats, err := s.usageRepo.GetModelStatsAggregated(ctx, modelName, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("get model stats: %w", err)
	}

	return &UsageStats{
		TotalRequests:            stats.TotalRequests,
		TotalInputTokens:         stats.TotalInputTokens,
		TotalOutputTokens:        stats.TotalOutputTokens,
		TotalCacheTokens:         stats.TotalCacheTokens,
		TotalCacheCreationTokens: stats.TotalCacheCreationTokens,
		TotalCacheReadTokens:     stats.TotalCacheReadTokens,
		TotalTokens:              stats.TotalTokens,
		TotalCost:                stats.TotalCost,
		TotalActualCost:          stats.TotalActualCost,
		AverageDurationMs:        stats.AverageDurationMs,
	}, nil
}

// GetDailyStats 获取每日使用统计（最近N天）
func (s *UsageService) GetDailyStats(ctx context.Context, userID int64, days int) ([]map[string]any, error) {
	endTime := time.Now()
	startTime := endTime.AddDate(0, 0, -days)

	stats, err := s.usageRepo.GetDailyStatsAggregated(ctx, userID, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("get daily stats: %w", err)
	}

	return stats, nil
}

// Delete 删除使用日志（管理员功能，谨慎使用）
func (s *UsageService) Delete(ctx context.Context, id int64) error {
	if err := s.usageRepo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete usage log: %w", err)
	}
	return nil
}

// GetUserDashboardStats returns per-user dashboard summary stats.
func (s *UsageService) GetUserDashboardStats(ctx context.Context, userID int64) (*usagestats.UserDashboardStats, error) {
	stats, err := s.usageRepo.GetUserDashboardStats(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user dashboard stats: %w", err)
	}
	return stats, nil
}

// GetAPIKeyDashboardStats returns dashboard summary stats filtered by API Key.
func (s *UsageService) GetAPIKeyDashboardStats(ctx context.Context, apiKeyID int64) (*usagestats.UserDashboardStats, error) {
	stats, err := s.usageRepo.GetAPIKeyDashboardStats(ctx, apiKeyID)
	if err != nil {
		return nil, fmt.Errorf("get api key dashboard stats: %w", err)
	}
	return stats, nil
}

// GetUserUsageTrendByUserID returns per-user usage trend.
func (s *UsageService) GetUserUsageTrendByUserID(ctx context.Context, userID int64, startTime, endTime time.Time, granularity string) ([]usagestats.TrendDataPoint, error) {
	trend, err := s.usageRepo.GetUserUsageTrendByUserID(ctx, userID, startTime, endTime, granularity)
	if err != nil {
		return nil, fmt.Errorf("get user usage trend: %w", err)
	}
	return trend, nil
}

// GetUsageTrendWithFilters returns trend data using the shared usage filter shape.
func (s *UsageService) GetUsageTrendWithFilters(ctx context.Context, startTime, endTime time.Time, granularity string, filters usagestats.UsageLogFilters) ([]usagestats.TrendDataPoint, error) {
	type usageTrendWithFiltersRepo interface {
		GetUsageTrendWithUsageFilters(ctx context.Context, startTime, endTime time.Time, granularity string, filters usagestats.UsageLogFilters) ([]usagestats.TrendDataPoint, error)
	}
	if filterRepo, ok := s.usageRepo.(usageTrendWithFiltersRepo); ok {
		trend, err := filterRepo.GetUsageTrendWithUsageFilters(ctx, startTime, endTime, granularity, filters)
		if err != nil {
			return nil, fmt.Errorf("get usage trend with filters: %w", err)
		}
		return trend, nil
	}
	trend, err := s.usageRepo.GetUsageTrendWithFilters(ctx, startTime, endTime, granularity, filters.UserID, filters.APIKeyID, filters.AccountID, filters.GroupID, filters.Model, filters.RequestType, filters.Stream, filters.BillingType)
	if err != nil {
		return nil, fmt.Errorf("get usage trend with filters: %w", err)
	}
	return trend, nil
}

// GetUserModelStats returns per-user model usage stats.
func (s *UsageService) GetUserModelStats(ctx context.Context, userID int64, startTime, endTime time.Time) ([]usagestats.ModelStat, error) {
	stats, err := s.usageRepo.GetUserModelStats(ctx, userID, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("get user model stats: %w", err)
	}
	return stats, nil
}

// GetModelStatsWithFiltersBySource returns model stats using the shared usage filter shape.
func (s *UsageService) GetModelStatsWithFiltersBySource(ctx context.Context, startTime, endTime time.Time, filters usagestats.UsageLogFilters, modelSource string) ([]usagestats.ModelStat, error) {
	normalizedSource := usagestats.NormalizeModelSource(modelSource)
	type modelStatsWithUsageFiltersRepo interface {
		GetModelStatsWithUsageFiltersBySource(ctx context.Context, startTime, endTime time.Time, filters usagestats.UsageLogFilters, source string) ([]usagestats.ModelStat, error)
	}
	if filterRepo, ok := s.usageRepo.(modelStatsWithUsageFiltersRepo); ok {
		stats, err := filterRepo.GetModelStatsWithUsageFiltersBySource(ctx, startTime, endTime, filters, normalizedSource)
		if err != nil {
			return nil, fmt.Errorf("get model stats with filters by source: %w", err)
		}
		return stats, nil
	}
	type modelStatsBySourceRepo interface {
		GetModelStatsWithFiltersBySource(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, requestType *int16, stream *bool, billingType *int8, source string) ([]usagestats.ModelStat, error)
	}
	if sourceRepo, ok := s.usageRepo.(modelStatsBySourceRepo); ok {
		stats, err := sourceRepo.GetModelStatsWithFiltersBySource(ctx, startTime, endTime, filters.UserID, filters.APIKeyID, filters.AccountID, filters.GroupID, filters.RequestType, filters.Stream, filters.BillingType, normalizedSource)
		if err != nil {
			return nil, fmt.Errorf("get model stats with filters by source: %w", err)
		}
		return stats, nil
	}
	stats, err := s.usageRepo.GetModelStatsWithFilters(ctx, startTime, endTime, filters.UserID, filters.APIKeyID, filters.AccountID, filters.GroupID, filters.RequestType, filters.Stream, filters.BillingType)
	if err != nil {
		return nil, fmt.Errorf("get model stats with filters: %w", err)
	}
	return stats, nil
}

// GetGroupStatsWithFilters returns group stats using the shared usage filter shape.
func (s *UsageService) GetGroupStatsWithFilters(ctx context.Context, startTime, endTime time.Time, filters usagestats.UsageLogFilters) ([]usagestats.GroupStat, error) {
	type groupStatsWithUsageFiltersRepo interface {
		GetGroupStatsWithUsageFilters(ctx context.Context, startTime, endTime time.Time, filters usagestats.UsageLogFilters) ([]usagestats.GroupStat, error)
	}
	if filterRepo, ok := s.usageRepo.(groupStatsWithUsageFiltersRepo); ok {
		stats, err := filterRepo.GetGroupStatsWithUsageFilters(ctx, startTime, endTime, filters)
		if err != nil {
			return nil, fmt.Errorf("get group stats with filters: %w", err)
		}
		return stats, nil
	}
	stats, err := s.usageRepo.GetGroupStatsWithFilters(ctx, startTime, endTime, filters.UserID, filters.APIKeyID, filters.AccountID, filters.GroupID, filters.RequestType, filters.Stream, filters.BillingType)
	if err != nil {
		return nil, fmt.Errorf("get group stats with filters: %w", err)
	}
	return stats, nil
}

// GetAPIKeyModelStats returns per-model usage stats for a specific API Key.
func (s *UsageService) GetAPIKeyModelStats(ctx context.Context, apiKeyID int64, startTime, endTime time.Time) ([]usagestats.ModelStat, error) {
	stats, err := s.usageRepo.GetModelStatsWithFilters(ctx, startTime, endTime, 0, apiKeyID, 0, 0, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("get api key model stats: %w", err)
	}
	return stats, nil
}

// GetAPIKeyDailyUsage returns daily usage stats for a user's API key.
//
// userID is validated by the caller (the handler proves the key belongs to the
// requesting user) but is deliberately NOT passed into the trend query. Adding
// it as a second predicate gives the planner a competing single-column index
// (idx_usage_logs_user_id) and it picks that one, which on a large key means
// reading the whole user's history instead of one key's — measured 8.9s vs 4.9s
// for the same 5.7M-row key. api_key_id alone is both sufficient and selective.
//
// Fast path: the pre-aggregated usage_key_daily rollup answers this in one index
// scan when the whole window is covered. On production the hottest keys hold
// 2.4M–5.7M rows, and grouping them into ~30 daily points cost 4769 ms with a
// 139 MB external merge; the rollup returns the same numbers in well under a
// millisecond. Any window the rollup cannot express (unusual bounds, rollup
// disabled or not yet backfilled) falls through to the original query below, so
// the response is never allowed to differ from the raw aggregation.
func (s *UsageService) GetAPIKeyDailyUsage(ctx context.Context, userID, apiKeyID int64, startTime, endTime time.Time) ([]usagestats.APIKeyDailyUsagePoint, error) {
	if points, ok := s.apiKeyDailyUsageFromRollup(ctx, apiKeyID, startTime, endTime); ok {
		return points, nil
	}
	trend, err := s.usageRepo.GetUsageTrendWithFilters(ctx, startTime, endTime, "day", 0, apiKeyID, 0, 0, "", nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("get api key daily usage: %w", err)
	}

	points := make([]usagestats.APIKeyDailyUsagePoint, 0, len(trend))
	for _, row := range trend {
		points = append(points, usagestats.APIKeyDailyUsagePoint{
			Date:             row.Date,
			Requests:         row.Requests,
			InputTokens:      row.InputTokens,
			OutputTokens:     row.OutputTokens,
			CacheReadTokens:  row.CacheReadTokens,
			CacheWriteTokens: row.CacheCreationTokens,
			TotalTokens:      row.TotalTokens,
			Cost:             row.Cost,
			ActualCost:       row.ActualCost,
		})
	}
	return points, nil
}

// apiKeyDailyUsageFromRollup 用日汇总表构造按日趋势点。
//
// 返回 ok=false 表示这条请求不该由汇总表答复，调用方走原路径。
//
// 与原查询的语义对齐（两处必须逐字段相同，否则前端图表会跳变）：
//   - 只输出有数据的日子，空日不补零（原查询是 GROUP BY，同样不补零）。
//   - Date 是 TO_CHAR(..., 'YYYY-MM-DD') 的字符串，取本地自然日。
//   - CacheWriteTokens 对应 cache_creation_tokens，CacheReadTokens 对应
//     cache_read_tokens，TotalTokens 是四项之和（含两个 cache 项）——与
//     trend 查询的 SUM(input+output+cache_creation+cache_read) 一致。
//   - Cost/ActualCost 分别取 total_cost / actual_cost。
func (s *UsageService) apiKeyDailyUsageFromRollup(ctx context.Context, apiKeyID int64, startTime, endTime time.Time) ([]usagestats.APIKeyDailyUsagePoint, bool) {
	if apiKeyID <= 0 {
		return nil, false
	}
	reader := s.keyDailyReader(ctx)
	if reader == nil {
		return nil, false
	}
	now := timezone.Now()
	rows, err := reader.DailyPoints(ctx, apiKeyID, startTime, endTime, now)
	if err != nil {
		logger.LegacyPrintf("service.usage", "key daily rollup trend read failed, falling back to raw scan: %v", err)
		return nil, false
	}
	loc := timezone.Location()
	points := make([]usagestats.APIKeyDailyUsagePoint, 0, len(rows))
	for _, row := range rows {
		totalTokens := row.InputTokens + row.OutputTokens + row.CacheCreationTokens + row.CacheReadTokens
		points = append(points, usagestats.APIKeyDailyUsagePoint{
			Date:             row.Day.In(loc).Format("2006-01-02"),
			Requests:         row.Requests,
			InputTokens:      row.InputTokens,
			OutputTokens:     row.OutputTokens,
			CacheReadTokens:  row.CacheReadTokens,
			CacheWriteTokens: row.CacheCreationTokens,
			TotalTokens:      totalTokens,
			Cost:             row.TotalCost,
			ActualCost:       row.ActualCost,
		})
	}
	return points, true
}

// GetAPIKeyUsageSummary returns the sanitized aggregate counters for one API
// key inside [startTime, endTime).
//
// Callers must verify the API key belongs to the requesting user first; the
// repository query is keyed by api_key_id only (idx_usage_logs_api_key_created_at)
// and deliberately avoids the wider endpoint-breakdown aggregate.
//
// This is the fast path behind the key-usage dialog's totals. Both the totals and
// the daily trend now consult usage_key_daily when the requested window aligns to
// local day boundaries; the raw aggregate below stays as the fallback for any
// other window, so an odd range still returns exactly what it always did.
func (s *UsageService) GetAPIKeyUsageSummary(ctx context.Context, apiKeyID int64, startTime, endTime time.Time) (*usagestats.APIKeyUsageSummary, error) {
	if summary, ok := s.apiKeyUsageSummaryFromRollup(ctx, apiKeyID, startTime, endTime); ok {
		return summary, nil
	}
	stats, err := s.usageRepo.GetAPIKeyStatsAggregated(ctx, apiKeyID, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("get api key usage summary: %w", err)
	}
	return &usagestats.APIKeyUsageSummary{
		Requests:         stats.TotalRequests,
		InputTokens:      stats.TotalInputTokens,
		OutputTokens:     stats.TotalOutputTokens,
		CacheReadTokens:  stats.TotalCacheReadTokens,
		CacheWriteTokens: stats.TotalCacheCreationTokens,
		TotalTokens:      stats.TotalTokens,
		Cost:             stats.TotalCost,
		ActualCost:       stats.TotalActualCost,
	}, nil
}

// apiKeyUsageSummaryFromRollup 用日汇总表答复单 key 的区间汇总。
//
// 只有当窗口两端都落在本地日界上时才可用：日桶无法表达「从某个半天开始」，
// 硬套会少算或多算起止那半天。窗口不整日时回落到原始聚合查询。
//
// AverageDurationMs 不在 APIKeyUsageSummary 里，所以这里不需要它；但写侧仍然
// 存了 duration_sum/duration_count，便于以后要加平均耗时而不必改表。
func (s *UsageService) apiKeyUsageSummaryFromRollup(ctx context.Context, apiKeyID int64, startTime, endTime time.Time) (*usagestats.APIKeyUsageSummary, bool) {
	if apiKeyID <= 0 || startTime.IsZero() || endTime.IsZero() || !endTime.After(startTime) {
		return nil, false
	}
	if !onLocalDayBoundary(startTime) || !onLocalDayBoundary(endTime) {
		return nil, false
	}
	reader := s.keyDailyReader(ctx)
	if reader == nil {
		return nil, false
	}
	now := timezone.Now()
	totals, err := reader.TotalsForKeys(ctx, []int64{apiKeyID}, startTime, endTime, now)
	if err != nil {
		logger.LegacyPrintf("service.usage", "key daily rollup summary read failed, falling back to raw scan: %v", err)
		return nil, false
	}
	row, ok := totals[apiKeyID]
	if !ok {
		// 窗口内没有任何用量：返回全零与原始查询的空聚合语义一致。
		return &usagestats.APIKeyUsageSummary{}, true
	}
	totalTokens := row.InputTokens + row.OutputTokens + row.CacheCreationTokens + row.CacheReadTokens
	return &usagestats.APIKeyUsageSummary{
		Requests:         row.Requests,
		InputTokens:      row.InputTokens,
		OutputTokens:     row.OutputTokens,
		CacheReadTokens:  row.CacheReadTokens,
		CacheWriteTokens: row.CacheCreationTokens,
		TotalTokens:      totalTokens,
		Cost:             row.TotalCost,
		ActualCost:       row.ActualCost,
	}, true
}

// onLocalDayBoundary 报告该时刻是否恰好是服务端时区内的零点。
//
// 这是「日桶能表达这个窗口」的判据：起点不在零点，就需要用到半天数据。
func onLocalDayBoundary(t time.Time) bool {
	return t.Equal(timezone.StartOfDayInUserLocation(t, timezone.Name()))
}

// keyDailyReader 返回可用的日汇总读侧；nil 表示该能力未接线或当前不可信任。
//
// 「不可信任」包括：能力没接线、运维把开关关了、表里还没有数据（迁移刚跑完
// 没回填）、或写入时的时区口径与当前配置不一致。任何一种都必须回落到直接扫
// usage_logs 的原路径，绝不能返回可能错位的汇总值。
func (s *UsageService) keyDailyReader(ctx context.Context) UsageKeyDailyReader {
	if s.keyDaily == nil {
		return nil
	}
	usable, err := s.keyDaily.RollupUsable(ctx)
	if err != nil {
		logger.LegacyPrintf("service.usage", "key daily rollup usability check failed: %v", err)
		return nil
	}
	if !usable {
		return nil
	}
	return s.keyDaily
}

// GetBatchAPIKeyUsageStats returns today/total actual_cost for given api keys.
func (s *UsageService) GetBatchAPIKeyUsageStats(ctx context.Context, apiKeyIDs []int64, startTime, endTime time.Time) (map[int64]*usagestats.BatchAPIKeyUsageStats, error) {
	if stats, ok, err := s.batchAPIKeyUsageFromRollup(ctx, apiKeyIDs, startTime, endTime); ok {
		return stats, err
	}
	if s.keyUsage != nil {
		stats, handled, err := s.keyUsage.TryRead(ctx, apiKeyIDs, startTime, endTime)
		if handled {
			return stats, err
		}
	}
	stats, err := s.usageRepo.GetBatchAPIKeyUsageStats(ctx, apiKeyIDs, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("get batch api key usage stats: %w", err)
	}
	return stats, nil
}

// batchAPIKeyUsageFromRollup 用日汇总表答复「一批 key 的 today/total」。
//
// ok=false 表示这次请求不该由汇总表答复（能力未接线、汇总表不可信、或时间
// 窗口带了汇总表无法表达的参数），调用方回落到原路径。语义与
// usageLogRepository.GetBatchAPIKeyUsageStats 完全一致：
//   - startTime 为零 → 默认最近 30 天；endTime 为零 → 现在
//   - total  = [startTime, endTime) 内的 actual_cost 合计
//   - today  = [本地今日零点, +∞) 内的 actual_cost 合计（注意是开区间上界，
//     与原始 SQL 的 `created_at >= $4` 相同，因此未来时间戳的行也计入）
//
// 日汇总表按「本地自然日」分桶，所以只有当 startTime 恰好落在本地日界上时
// 才能用它表达 total；否则起点的半天会被多算。这种情况下回落到原路径，
// 宁可慢也不给错数。
func (s *UsageService) batchAPIKeyUsageFromRollup(ctx context.Context, apiKeyIDs []int64, startTime, endTime time.Time) (map[int64]*usagestats.BatchAPIKeyUsageStats, bool, error) {
	reader := s.keyDailyReader(ctx)
	if reader == nil {
		return nil, false, nil
	}
	ids := make([]int64, 0, len(apiKeyIDs))
	seen := make(map[int64]bool, len(apiKeyIDs))
	for _, id := range apiKeyIDs {
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	result := make(map[int64]*usagestats.BatchAPIKeyUsageStats, len(ids))
	for _, id := range ids {
		result[id] = &usagestats.BatchAPIKeyUsageStats{APIKeyID: id}
	}
	if len(ids) == 0 {
		return result, true, nil
	}

	now := timezone.Now()
	start := startTime
	if start.IsZero() {
		start = now.AddDate(0, 0, -30)
	}
	end := endTime
	if end.IsZero() {
		end = now
	}
	// 日桶只能表达整个自然日。起点不在本地日界上的窗口交给原始 SQL。
	if !start.Equal(timezone.StartOfDayInUserLocation(start, timezone.Name())) {
		return nil, false, nil
	}
	today := timezone.Today()

	totals, err := reader.TotalsForKeys(ctx, ids, start, end, now)
	if err != nil {
		// 读汇总失败不能变成 500：回落到原始查询即可，但要把原因留痕。
		logger.LegacyPrintf("service.usage", "key daily rollup read failed, falling back to raw scan: %v", err)
		return nil, false, nil
	}
	todayTotals, err := reader.TotalsForKeys(ctx, ids, today, now.AddDate(0, 0, 1), now)
	if err != nil {
		logger.LegacyPrintf("service.usage", "key daily rollup today read failed, falling back to raw scan: %v", err)
		return nil, false, nil
	}

	for _, id := range ids {
		stats := result[id]
		if row, ok := totals[id]; ok {
			stats.TotalActualCost = row.ActualCost
		}
		// today 只取窗口内今天那一段；TotalsForKeys 已经按窗口裁剪。
		if row, ok := todayTotals[id]; ok {
			stats.TodayActualCost = row.ActualCost
		}
	}
	return result, true, nil
}

// ListWithFilters lists usage logs with admin filters.
func (s *UsageService) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters usagestats.UsageLogFilters) ([]UsageLog, *pagination.PaginationResult, error) {
	logs, result, err := s.usageRepo.ListWithFilters(ctx, params, filters)
	if err != nil {
		return nil, nil, fmt.Errorf("list usage logs with filters: %w", err)
	}
	return logs, result, nil
}

// GetGlobalStats returns global usage stats for a time range.
func (s *UsageService) GetGlobalStats(ctx context.Context, startTime, endTime time.Time) (*usagestats.UsageStats, error) {
	stats, err := s.usageRepo.GetGlobalStats(ctx, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("get global usage stats: %w", err)
	}
	return stats, nil
}

// GetStatsWithFilters returns usage stats with optional filters.
func (s *UsageService) GetStatsWithFilters(ctx context.Context, filters usagestats.UsageLogFilters) (*usagestats.UsageStats, error) {
	stats, err := s.usageRepo.GetStatsWithFilters(ctx, filters)
	if err != nil {
		return nil, fmt.Errorf("get usage stats with filters: %w", err)
	}
	return stats, nil
}
