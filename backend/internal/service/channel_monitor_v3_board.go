package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// V3 看板数据面。
//
// V3 的分组来源只有 channel_monitor_v3_config.groups —— 不看分组的
// model_allowlist / model_pricing / platforms（用户明确要求去掉这三个前置）。
// 为了让 V3 复用已经跑通的分桶聚合与探针兜底逻辑，看板把 V3 名单「翻译」成一份
// 派生的 V2 形状配置：分组名单来自 V3，平台集合来自这些分组的真实平台，
// models 一律留空（空 = 不过滤模型，这正是「不需要白名单」的实现点）。

// ChannelMonitorV3BoardScope 是一次看板查询的观测范围，全部由 V3 配置派生。
type ChannelMonitorV3BoardScope struct {
	// GroupIDs 是 V3 名单里启用的分组。
	GroupIDs []int64
	// Platforms 是这些分组所属的平台（去重、升序）。
	Platforms []string
	// CacheAverageWindowMinutes 探针桶缓存率的回看窗口（分钟，ADR 0009）。
	// 分钟粒度是用户要求的能力：窗口可以配成「30 分钟」，实现据此判断能否走
	// 日汇总表（最小粒度一天）。
	CacheAverageWindowMinutes int
}

// ChannelMonitorV3BoardRepository 是 V3 看板的只读契约。
type ChannelMonitorV3BoardRepository interface {
	GetBoardSnapshot(ctx context.Context, filter ChannelMonitorV2Filter, scope ChannelMonitorV3BoardScope, admin bool) (*ChannelMonitorV2Snapshot, error)
	GetBoardMatrix(ctx context.Context, filter ChannelMonitorV2Filter, scope ChannelMonitorV3BoardScope, groupBy ChannelMonitorV2GroupBy, admin bool) (*ChannelMonitorV2Matrix, error)
}

// DeriveScope 从 V3 配置与分组元数据派生看板范围。
//
// 分组元数据缺失（分组被删/停用）时那条记录直接丢弃：V3 不该为一个已经不存在的
// 分组持续探针，也不该在看板上留一行永远没有数据的空卡片。
func (c ChannelMonitorV3Config) DeriveScope(groups map[int64]ChannelMonitorV3GroupInfo) ChannelMonitorV3BoardScope {
	scope := ChannelMonitorV3BoardScope{CacheAverageWindowMinutes: c.CacheAverageWindowMinutes()}
	platforms := map[string]struct{}{}
	for _, id := range c.EnabledGroupIDs() {
		info, ok := groups[id]
		if !ok || id <= 0 {
			continue
		}
		scope.GroupIDs = append(scope.GroupIDs, id)
		if platform := strings.TrimSpace(info.Platform); platform != "" {
			platforms[platform] = struct{}{}
		}
	}
	scope.Platforms = make([]string, 0, len(platforms))
	for platform := range platforms {
		scope.Platforms = append(scope.Platforms, platform)
	}
	sort.Strings(scope.Platforms)
	return scope
}

// Empty 报告该范围是否没有任何可观测分组。
func (s ChannelMonitorV3BoardScope) Empty() bool {
	return len(s.GroupIDs) == 0 || len(s.Platforms) == 0
}

// Snapshot 读取 V3 看板快照。
func (s *ChannelMonitorV3Service) Snapshot(ctx context.Context, filter ChannelMonitorV2Filter, admin bool) (*ChannelMonitorV2Snapshot, error) {
	cfg, err := s.getEnabledConfig(ctx)
	if err != nil {
		return nil, err
	}
	scope, board, err := s.boardScope(ctx, *cfg)
	if err != nil {
		return nil, err
	}
	if scope.Empty() {
		return emptyChannelMonitorV3Snapshot(filter), nil
	}
	snap, err := board.GetBoardSnapshot(ctx, filter, scope, admin)
	if err != nil {
		return nil, err
	}
	if !admin && snap != nil {
		redactChannelMonitorV2Snapshot(snap, false)
	}
	return snap, nil
}

// Matrix 读取 V3 看板矩阵（platform_group / platform_group_model 行）。
func (s *ChannelMonitorV3Service) Matrix(ctx context.Context, filter ChannelMonitorV2Filter, groupBy ChannelMonitorV2GroupBy, admin bool) (*ChannelMonitorV2Matrix, error) {
	if !groupBy.Valid() {
		return nil, fmt.Errorf("%w: %s", ErrChannelMonitorV2InvalidGroupBy, groupBy)
	}
	cfg, err := s.getEnabledConfig(ctx)
	if err != nil {
		return nil, err
	}
	scope, board, err := s.boardScope(ctx, *cfg)
	if err != nil {
		return nil, err
	}
	if scope.Empty() {
		return &ChannelMonitorV2Matrix{GroupBy: groupBy, Items: []ChannelMonitorV2MatrixRow{}}, nil
	}
	matrix, err := board.GetBoardMatrix(ctx, filter, scope, groupBy, admin)
	if err != nil {
		return nil, err
	}
	if !admin && matrix != nil {
		for i := range matrix.Items {
			redactChannelMonitorV2Metric(&matrix.Items[i].Metrics, false)
			for j := range matrix.Items[i].Buckets {
				redactChannelMonitorV2Metric(&matrix.Items[i].Buckets[j].Metrics, false)
			}
		}
	}
	return matrix, nil
}

// ParseFilter 复用 V2 的时间窗解析：V3 的 90m/24h/7d/30d 语义与之完全一致。
func (s *ChannelMonitorV3Service) ParseFilter(rangeValue string, platforms, models []string, groupIDs []int64) (ChannelMonitorV2Filter, error) {
	return parseChannelMonitorRangeFilter(rangeValue, platforms, models, groupIDs, s.now)
}

func (s *ChannelMonitorV3Service) getEnabledConfig(ctx context.Context) (*ChannelMonitorV3Config, error) {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	if cfg == nil || !cfg.Enabled {
		return nil, ErrChannelMonitorDisabled
	}
	return cfg, nil
}

func (s *ChannelMonitorV3Service) boardScope(
	ctx context.Context,
	cfg ChannelMonitorV3Config,
) (ChannelMonitorV3BoardScope, ChannelMonitorV3BoardRepository, error) {
	board, ok := s.repo.(ChannelMonitorV3BoardRepository)
	if !ok {
		return ChannelMonitorV3BoardScope{}, nil, errors.New("channel monitor v3 board repository unavailable")
	}
	groups, err := s.repo.LoadMonitorableGroups(ctx, cfg.EnabledGroupIDs())
	if err != nil {
		return ChannelMonitorV3BoardScope{}, nil, err
	}
	return cfg.DeriveScope(groups), board, nil
}

// emptyChannelMonitorV3Snapshot 让前端在「V3 开着但一个分组都没选中」时拿到一个
// 结构完整、没有数据的快照，而不是 500 或永久 loading。
func emptyChannelMonitorV3Snapshot(filter ChannelMonitorV2Filter) *ChannelMonitorV2Snapshot {
	return &ChannelMonitorV2Snapshot{
		Config: ChannelMonitorV2Config{
			Version:                1,
			Enabled:                true,
			RefreshIntervalSeconds: 60,
			Platforms:              []ChannelMonitorV2PlatformConfig{},
			GroupIDs:               []int64{},
			HealthThresholds:       DefaultChannelMonitorV2HealthThresholds(),
		},
		Coverage: ChannelMonitorV2Coverage{
			RequestedStart: filter.Start,
			RequestedEnd:   filter.End,
			BucketSeconds:  int(filter.Bucket / time.Second),
		},
		Trend: []ChannelMonitorV2TrendPoint{},
	}
}
