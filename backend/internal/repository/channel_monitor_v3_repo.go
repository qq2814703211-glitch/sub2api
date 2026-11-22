package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// channelMonitorV3Repository 是 V3 的持久化实现。
//
// V3 拥有自己的配置表与自己的探针样本表，绝不读写 V1 的 channel_monitors /
// channel_monitor_histories，也不读写 V2 的 group_ids 做门控（只在初始化迁移时
// 复制过一次初始值）。看板聚合复用 V2 已经跑通的分桶 SQL，但作用域（分组/平台）
// 完全由 V3 配置派生，探针兜底也用 V3 自己的样本。
type channelMonitorV3Repository struct {
	db *sql.DB
	// v2 提供已经跑通的 usage_logs 分桶聚合。V3 只借用它的查询实现，不借用它的
	// 配置与门控：调用的入口都显式传入 V3 派生的 scope（见 getV3Matrix）。
	v2 *channelMonitorV2Repository
	// groupDaily 是 usage_group_daily 的读侧。nil 表示没有接入（测试或未装配），
	// 此时缓存率查询完全走原来的 usage_logs 全表扫描路径。
	groupDaily service.UsageGroupDailyReader
	// groupDailyErr 记录汇总表探测/读取失败的原因，供诊断与日志使用。
	// 失败不影响页面：读侧会回落到原查询。
	groupDailyErr error
}

// NewChannelMonitorV3Repository 创建 V3 仓库。
//
// 分组日汇总读侧在这里直接装配：它与 V3 仓库共用同一个 *sql.DB，没有额外依赖，
// 所以不需要调用方额外传参。表不存在或未回填时 RollupUsable 返回 false，读侧
// 自动回落到直接扫 usage_logs。
func NewChannelMonitorV3Repository(db *sql.DB) service.ChannelMonitorV3Repository {
	repo := &channelMonitorV3Repository{db: db, v2: &channelMonitorV2Repository{db: db}}
	if db != nil {
		repo.groupDaily = newUsageGroupDailyRollupRepositoryWithSQL(db)
	}
	return repo
}

// WithGroupDailyReader 装配分组日汇总读侧。返回自身便于链式调用。
func (r *channelMonitorV3Repository) WithGroupDailyReader(reader service.UsageGroupDailyReader) *channelMonitorV3Repository {
	r.groupDaily = reader
	return r
}

const channelMonitorV3ConfigColumns = `version, enabled, groups, cache_average_window_days, cache_average_window_value, cache_average_window_unit, updated_at, updated_by`

func (r *channelMonitorV3Repository) GetConfig(ctx context.Context) (*service.ChannelMonitorV3Config, error) {
	var cfg service.ChannelMonitorV3Config
	var groups []byte
	var windowUnit string
	err := r.db.QueryRowContext(ctx,
		`SELECT `+channelMonitorV3ConfigColumns+` FROM channel_monitor_v3_config WHERE id = 1`,
	).Scan(&cfg.Version, &cfg.Enabled, &groups, &cfg.CacheAverageWindowDays,
		&cfg.CacheAverageWindowValue, &windowUnit, &cfg.UpdatedAt, &cfg.UpdatedBy)
	if err == sql.ErrNoRows {
		// 表存在但行缺失（极少见：单行表由迁移写入）。返回默认值而不是报错，
		// 管理页才能渲染出一份空配置让运维保存。
		return defaultChannelMonitorV3Config(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("get channel monitor v3 config: %w", err)
	}
	cfg.CacheAverageWindowUnit = windowUnit
	if err := decodeChannelMonitorV3ConfigPayload(&cfg, groups); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (r *channelMonitorV3Repository) UpdateConfig(
	ctx context.Context,
	cfg service.ChannelMonitorV3Config,
	expectedVersion int,
) (*service.ChannelMonitorV3Config, error) {
	groups, err := json.Marshal(cfg.Groups)
	if err != nil {
		return nil, fmt.Errorf("encode channel monitor v3 groups: %w", err)
	}
	if cfg.Groups == nil {
		groups = []byte("[]")
	}
	var updated service.ChannelMonitorV3Config
	var rawGroups []byte
	var windowUnit string
	err = r.db.QueryRowContext(ctx, `
		UPDATE channel_monitor_v3_config
		SET version = version + 1, enabled = $1, groups = $2,
		    cache_average_window_days = $3, cache_average_window_value = $4,
		    cache_average_window_unit = $5, updated_by = $6, updated_at = NOW()
		WHERE id = 1 AND version = $7
		RETURNING `+channelMonitorV3ConfigColumns,
		cfg.Enabled, groups, cfg.CacheAverageWindowDays,
		cfg.CacheAverageWindowValue, cfg.CacheAverageWindowUnit,
		cfg.UpdatedBy, expectedVersion,
	).Scan(&updated.Version, &updated.Enabled, &rawGroups,
		&updated.CacheAverageWindowDays, &updated.CacheAverageWindowValue, &windowUnit,
		&updated.UpdatedAt, &updated.UpdatedBy)
	if err == sql.ErrNoRows {
		return nil, service.ErrChannelMonitorV3ConfigConflict
	}
	if err != nil {
		return nil, fmt.Errorf("update channel monitor v3 config: %w", err)
	}
	updated.CacheAverageWindowUnit = windowUnit
	if err := decodeChannelMonitorV3ConfigPayload(&updated, rawGroups); err != nil {
		return nil, err
	}
	return &updated, nil
}

func decodeChannelMonitorV3ConfigPayload(cfg *service.ChannelMonitorV3Config, groups []byte) error {
	cfg.Groups = []service.ChannelMonitorV3Group{}
	if len(groups) > 0 {
		if err := json.Unmarshal(groups, &cfg.Groups); err != nil {
			return fmt.Errorf("decode channel monitor v3 groups: %w", err)
		}
	}
	for i := range cfg.Groups {
		group := &cfg.Groups[i]
		if group.Models == nil {
			group.Models = []string{}
		}
		if group.Probe.ExtraModels == nil {
			group.Probe.ExtraModels = []string{}
		}
		group.APIKeySet = strings.TrimSpace(group.Probe.APIKeyEncrypted) != ""
	}
	return nil
}

func defaultChannelMonitorV3Config() *service.ChannelMonitorV3Config {
	return &service.ChannelMonitorV3Config{
		Version:                 1,
		Enabled:                 false,
		Groups:                  []service.ChannelMonitorV3Group{},
		CacheAverageWindowDays:  service.ChannelMonitorV3DefaultCacheAverageWindowDays,
		CacheAverageWindowValue: service.ChannelMonitorV3DefaultCacheAverageWindowDays,
		CacheAverageWindowUnit:  service.ChannelMonitorV3WindowUnitDay,
	}
}

func (r *channelMonitorV3Repository) InsertProbeSamples(ctx context.Context, samples []service.ChannelMonitorV3ProbeSample) error {
	if len(samples) == 0 {
		return nil
	}
	groupIDs := make([]int64, 0, len(samples))
	statuses := make([]string, 0, len(samples))
	ttfts := make([]sql.NullInt64, 0, len(samples))
	checkedAt := make([]time.Time, 0, len(samples))
	for _, sample := range samples {
		groupIDs = append(groupIDs, sample.GroupID)
		status := service.MonitorStatusFailed
		if sample.Success {
			status = service.MonitorStatusOperational
		}
		statuses = append(statuses, status)
		var ttft sql.NullInt64
		if sample.TTFTMs != nil && *sample.TTFTMs >= 0 {
			ttft = sql.NullInt64{Int64: int64(*sample.TTFTMs), Valid: true}
		}
		ttfts = append(ttfts, ttft)
		at := sample.CheckedAt
		if at.IsZero() {
			at = time.Now().UTC()
		}
		checkedAt = append(checkedAt, at)
	}
	const q = `
		INSERT INTO channel_monitor_v3_probe_samples (group_id, status, ttft_ms, checked_at)
		SELECT * FROM unnest($1::bigint[], $2::varchar[], $3::bigint[], $4::timestamptz[])
	`
	if _, err := r.db.ExecContext(ctx, q, pq.Array(groupIDs), pq.Array(statuses), pq.Array(ttfts), pq.Array(checkedAt)); err != nil {
		return fmt.Errorf("insert channel monitor v3 probe samples: %w", err)
	}
	return nil
}

func (r *channelMonitorV3Repository) ListProbeSamples(
	ctx context.Context,
	start, end time.Time,
) ([]service.ChannelMonitorV3ProbeSample, error) {
	out := []service.ChannelMonitorV3ProbeSample{}
	if !end.After(start) {
		return out, nil
	}
	const q = `
		SELECT group_id, status, ttft_ms, checked_at
		FROM channel_monitor_v3_probe_samples
		WHERE checked_at >= $1 AND checked_at < $2
		ORDER BY checked_at ASC
	`
	rows, err := r.db.QueryContext(ctx, q, start, end)
	if err != nil {
		return nil, fmt.Errorf("query channel monitor v3 probe samples: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var sample service.ChannelMonitorV3ProbeSample
		var status string
		var ttft sql.NullInt64
		if err := rows.Scan(&sample.GroupID, &status, &ttft, &sample.CheckedAt); err != nil {
			return nil, fmt.Errorf("scan channel monitor v3 probe sample: %w", err)
		}
		sample.Success = status == service.MonitorStatusOperational || status == service.MonitorStatusDegraded
		if ttft.Valid {
			value := int(ttft.Int64)
			sample.TTFTMs = &value
		}
		out = append(out, sample)
	}
	return out, rows.Err()
}

// LoadMonitorableGroups 读取分组名与平台。
//
// 刻意只读 name / platform：V3 的可监控前置只有「被勾选」，与 model_allowlist、
// model_pricing、platforms 开关无关（用户明确要求去掉这些门槛）。
func (r *channelMonitorV3Repository) LoadMonitorableGroups(
	ctx context.Context,
	groupIDs []int64,
) (map[int64]service.ChannelMonitorV3GroupInfo, error) {
	out := map[int64]service.ChannelMonitorV3GroupInfo{}
	if len(groupIDs) == 0 {
		return out, nil
	}
	const q = `
		SELECT id, COALESCE(name, ''), lower(COALESCE(NULLIF(TRIM(platform), ''), 'unknown'))
		FROM groups
		WHERE id = ANY($1) AND deleted_at IS NULL AND status = 'active'
	`
	rows, err := r.db.QueryContext(ctx, q, pq.Array(groupIDs))
	if err != nil {
		return nil, fmt.Errorf("load channel monitor v3 groups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var info service.ChannelMonitorV3GroupInfo
		if err := rows.Scan(&id, &info.Name, &info.Platform); err != nil {
			return nil, fmt.Errorf("scan channel monitor v3 group: %w", err)
		}
		out[id] = info
	}
	return out, rows.Err()
}

// channelMonitorV3MonitorableGroupsQuery 是设置页候选分组的唯一来源。
//
// 两个过滤条件都必要：deleted_at IS NULL 挡住软删除（生产上存在 status 仍是
// 'active' 但 deleted_at 有值的行），status = 'active' 挡住临时停用的分组。
const channelMonitorV3MonitorableGroupsQuery = `
	SELECT id, COALESCE(name, ''), lower(COALESCE(NULLIF(TRIM(platform), ''), 'unknown')), COALESCE(status, '')
	FROM groups
	WHERE deleted_at IS NULL AND status = 'active'
	ORDER BY platform, name, id
`

// channelMonitorV3CacheRateWindowExpr 是直扫 usage_logs 时的时间窗表达式。
//
// 用 minute 而不是 day：窗口可以是「30 分钟」，按天折叠会把 30 分钟悄悄变成
// 30 天。它同时是一个纯滚动窗，不依赖 session 时区（与日汇总表的本地自然日口径
// 互不影响）。
const channelMonitorV3CacheRateWindowExpr = `ul.created_at >= NOW() - ($2::int * INTERVAL '1 minute')`

func (r *channelMonitorV3Repository) ListMonitorableGroups(ctx context.Context) ([]service.ChannelMonitorV3MonitorableGroup, error) {
	rows, err := r.db.QueryContext(ctx, channelMonitorV3MonitorableGroupsQuery)
	if err != nil {
		return nil, fmt.Errorf("list channel monitor v3 groups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []service.ChannelMonitorV3MonitorableGroup{}
	for rows.Next() {
		var item service.ChannelMonitorV3MonitorableGroup
		if err := rows.Scan(&item.ID, &item.Name, &item.Platform, &item.Status); err != nil {
			return nil, fmt.Errorf("scan channel monitor v3 monitorable group: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// GroupCacheRates 取分组最近 windowMinutes 分钟真实流量的缓存率均值。
//
// 这是 ADR 0009 的实现：探针桶的缓存率来自真实流量，而不是探针自己。没有任何
// 真实 token 的分组不会出现在结果里，调用方据此把该桶的缓存率标为未知。
// 探针请求本身会被 usageLogProbeExcludeUL 排除，不会污染均值。
//
// 两条路径，按窗口粒度选：
//
//  1. 窗口是整天数 → 先试 usage_group_daily 汇总表。这张表的最小粒度是「天」
//     （主键 group_id, day），无法表达「最近 30 分钟」，但正因为如此它才能把
//     生产上的 19,714ms 降到 0.3ms。天窗口继续走这条路，性能不回退。
//  2. 窗口不是整天数（分钟级）→ 绕过汇总表，直扫 usage_logs。窗口小，扫描范围
//     本来就小；把 30 分钟取整到 1 天会让用户选「30 分钟」却拿到「今天整天」的
//     均值，那是错误答案而不是慢答案。
//
// 直扫路径的 WHERE 用 created_at >= NOW() - ($2 * INTERVAL '1 minute')，不按
// 「日」裁剪，也不依赖 session 时区 —— 它是一个纯滚动时间窗，与汇总表的「本地
// 自然日」口径互不影响。
func (r *channelMonitorV3Repository) GroupCacheRates(
	ctx context.Context,
	groupIDs []int64,
	windowMinutes int,
) (map[int64]float64, error) {
	out := map[int64]float64{}
	if len(groupIDs) == 0 {
		return out, nil
	}
	if windowMinutes <= 0 {
		windowMinutes = service.ChannelMonitorV3DefaultCacheAverageWindowDays * 24 * 60
	}
	// 只有整天数的窗口能用日汇总表；分钟级窗口跳过它。
	if wholeDays := windowMinutes / (24 * 60); windowMinutes%(24*60) == 0 && wholeDays > 0 && r.groupDaily != nil {
		usable, usableErr := r.groupDaily.RollupUsable(ctx)
		if usableErr != nil {
			// 汇总表探测失败不能阻断监控页面：记下原因后走原路径。
			r.groupDailyErr = usableErr
		} else if usable {
			rates, readErr := r.groupDaily.CacheRates(ctx, groupIDs, wholeDays, time.Now())
			if readErr == nil {
				return rates, nil
			}
			r.groupDailyErr = readErr
		}
	}
	const q = `
		SELECT ul.group_id,
		       SUM(ul.cache_read_tokens)::float8
		         / NULLIF(SUM(ul.input_tokens + ul.cache_creation_tokens + ul.cache_read_tokens), 0)::float8
		FROM usage_logs ul
		WHERE ul.group_id = ANY($1)
		  AND ` + channelMonitorV3CacheRateWindowExpr + `
		  AND ul.created_at < NOW()
		  AND ` + usageLogProbeExcludeUL + `
		GROUP BY ul.group_id
		HAVING SUM(ul.input_tokens + ul.cache_creation_tokens + ul.cache_read_tokens) > 0
	`
	rows, err := r.db.QueryContext(ctx, q, pq.Array(groupIDs), windowMinutes)
	if err != nil {
		return nil, fmt.Errorf("query channel monitor v3 group cache rates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var groupID int64
		var rate sql.NullFloat64
		if err := rows.Scan(&groupID, &rate); err != nil {
			return nil, fmt.Errorf("scan channel monitor v3 group cache rate: %w", err)
		}
		if !rate.Valid {
			continue
		}
		out[groupID] = rate.Float64
	}
	return out, rows.Err()
}

// GroupHasOrganicTrafficSince 报告分组自 since 起是否有真实用户流量。
//
// 有真实流量的分组不需要探针填桶（矩阵里已有真实样本），探针跳过可以省上游配额。
func (r *channelMonitorV3Repository) GroupHasOrganicTrafficSince(
	ctx context.Context,
	groupID int64,
	since time.Time,
) (bool, error) {
	if groupID <= 0 {
		return false, nil
	}
	const q = `
		SELECT EXISTS (
			SELECT 1 FROM usage_logs ul
			WHERE ul.group_id = $1
			  AND ul.created_at >= $2
			  AND ` + usageLogProbeExcludeUL + `
			LIMIT 1
		)
	`
	var exists bool
	if err := r.db.QueryRowContext(ctx, q, groupID, since).Scan(&exists); err != nil {
		return false, fmt.Errorf("query channel monitor v3 organic traffic for group %d: %w", groupID, err)
	}
	return exists, nil
}
