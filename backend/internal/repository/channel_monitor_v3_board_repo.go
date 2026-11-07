package repository

import (
	"context"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// V3 看板数据面：把 V3 的范围（分组 + 平台）翻译成 V2 已经跑通的聚合查询，
// 再用 V3 自己的探针样本填满没有真实流量的时间桶。
//
// 关键点：V2 的配置门控（platforms / group_ids / model_allowlist / health
// thresholds）在这里完全不参与 —— V3 的 cfg 由 scope 现场构造，models 一律留空
// （空 = 不过滤模型），所以分组即使没有白名单、没有价卡也照样出现在看板上。

// v3BoardConfig 用 V3 范围构造一份「派生 V2 配置」。
//
// 平台集合 = V3 分组实际所属的平台；分组集合 = V3 名单；models 空。
func v3BoardConfig(scope service.ChannelMonitorV3BoardScope) service.ChannelMonitorV2Config {
	platforms := make([]service.ChannelMonitorV2PlatformConfig, 0, len(scope.Platforms))
	for _, platform := range scope.Platforms {
		platforms = append(platforms, service.ChannelMonitorV2PlatformConfig{
			Platform: platform,
			Enabled:  true,
			// 空模型列表 = 展示真实模型名，不折叠成 __other__，也不做白名单过滤。
			Models: []string{},
		})
	}
	return service.ChannelMonitorV2Config{
		Version:                1,
		Enabled:                true,
		RefreshIntervalSeconds: 60,
		Platforms:              platforms,
		GroupIDs:               append([]int64(nil), scope.GroupIDs...),
		HealthThresholds:       service.DefaultChannelMonitorV2HealthThresholds(),
	}
}

func (r *channelMonitorV3Repository) GetBoardSnapshot(
	ctx context.Context,
	filter service.ChannelMonitorV2Filter,
	scope service.ChannelMonitorV3BoardScope,
	admin bool,
) (*service.ChannelMonitorV2Snapshot, error) {
	return r.v2.GetSnapshot(ctx, filter, v3BoardConfig(scope), admin)
}

func (r *channelMonitorV3Repository) GetBoardMatrix(
	ctx context.Context,
	filter service.ChannelMonitorV2Filter,
	scope service.ChannelMonitorV3BoardScope,
	groupBy service.ChannelMonitorV2GroupBy,
	admin bool,
) (*service.ChannelMonitorV2Matrix, error) {
	return r.v2.getMatrix(ctx, filter, v3BoardConfig(scope), groupBy, admin, channelMonitorV3ProbeFallback{
		repo:  r,
		scope: scope,
	})
}

// channelMonitorV3ProbeFallback 用 V3 自己的探针样本填桶。
//
// 与 V2 的两点区别，都是 ADR 0009 的直接要求：
//  1. 样本按 group_id 归属（V3 探针本来就打在一个具体分组上），不按 group_name。
//  2. 缓存率不取探针结果，取该分组最近 N 天真实流量的均值；算不出均值就标未知，
//     前端显示「—」。
type channelMonitorV3ProbeFallback struct {
	repo  *channelMonitorV3Repository
	scope service.ChannelMonitorV3BoardScope
}

func (f channelMonitorV3ProbeFallback) applyProbeFallbackToMatrix(
	ctx context.Context,
	matrix *service.ChannelMonitorV2Matrix,
	filter service.ChannelMonitorV2Filter,
	bucketMinutes int,
	admin bool,
) {
	if matrix == nil || len(matrix.Items) == 0 || bucketMinutes <= 0 {
		return
	}
	start, end, ok := channelMonitorProbeFallbackWindow(matrix, filter)
	if !ok {
		return
	}
	samples, err := f.repo.ListProbeSamples(ctx, start, end)
	if err != nil || len(samples) == 0 {
		return
	}
	// Cache rates come from real traffic only. A query failure here degrades to
	// "unknown" for every probe bucket rather than failing the whole board.
	cacheRates, err := f.repo.GroupCacheRates(ctx, f.scope.GroupIDs, f.scope.CacheAverageWindowDays)
	if err != nil {
		cacheRates = map[int64]float64{}
	}
	byGroup := bucketV3ProbeSamplesByGroup(samples, bucketMinutes)
	applyV3ProbeFallbackToMatrix(matrix, byGroup, cacheRates, bucketMinutes, admin)
}

// bucketV3ProbeSamplesByGroup 把探针样本按 (group_id, 桶起点) 分组。
func bucketV3ProbeSamplesByGroup(
	samples []service.ChannelMonitorV3ProbeSample,
	bucketMinutes int,
) map[int64]map[string][]channelMonitorHybridProbeSample {
	out := make(map[int64]map[string][]channelMonitorHybridProbeSample)
	if bucketMinutes <= 0 {
		return out
	}
	for _, sample := range samples {
		if sample.GroupID <= 0 {
			continue
		}
		key := channelMonitorHybridBucketStart(sample.CheckedAt, bucketMinutes).Format(time.RFC3339Nano)
		byBucket := out[sample.GroupID]
		if byBucket == nil {
			byBucket = make(map[string][]channelMonitorHybridProbeSample)
			out[sample.GroupID] = byBucket
		}
		byBucket[key] = append(byBucket[key], channelMonitorHybridProbeSample{
			Success:   sample.Success,
			TTFTMs:    sample.TTFTMs,
			CheckedAt: sample.CheckedAt,
		})
	}
	return out
}

// applyV3ProbeFallbackToMatrix 用 V3 探针样本填空桶，缓存率取自 cacheRates。
//
// 真实流量永远优先：有请求的桶保持原样。这与 V2 的规则一致，只是数据源与
// 缓存率来源不同。
func applyV3ProbeFallbackToMatrix(
	matrix *service.ChannelMonitorV2Matrix,
	probesByGroup map[int64]map[string][]channelMonitorHybridProbeSample,
	cacheRates map[int64]float64,
	bucketMinutes int,
	admin bool,
) {
	if matrix == nil || len(probesByGroup) == 0 || bucketMinutes <= 0 {
		return
	}
	for i := range matrix.Items {
		row := &matrix.Items[i]
		if row.GroupID == nil || *row.GroupID <= 0 {
			continue
		}
		groupID := *row.GroupID
		byBucket := probesByGroup[groupID]
		if len(byBucket) == 0 {
			continue
		}
		var cacheRate *float64
		if rate, ok := cacheRates[groupID]; ok {
			value := rate
			cacheRate = &value
		}
		seen := make(map[string]struct{}, len(row.Buckets))
		for j := range row.Buckets {
			bucketStart := channelMonitorHybridBucketStart(row.Buckets[j].BucketStart, bucketMinutes)
			key := bucketStart.Format(time.RFC3339Nano)
			seen[key] = struct{}{}
			if row.Buckets[j].Metrics.RequestCount > 0 {
				continue
			}
			samples := byBucket[key]
			if len(samples) == 0 {
				continue
			}
			metrics, ok := probeSamplesToMetricsWithCacheRate(samples, bucketMinutes, admin, cacheRate)
			if !ok {
				continue
			}
			row.Buckets[j].Metrics = metrics
			row.Buckets[j].Health = probeBucketHealth(metrics)
			row.Buckets[j].Source = service.ChannelMonitorBucketSourceProbe
		}
		// 探针-only 的桶 V2 查询里根本不存在，必须补出来，否则一个零流量的分组
		// 会渲染成空图。
		missing := make([]string, 0)
		for key, samples := range byBucket {
			if len(samples) == 0 {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			missing = append(missing, key)
		}
		sort.Strings(missing)
		for _, key := range missing {
			bucketStart, parseErr := time.Parse(time.RFC3339Nano, key)
			if parseErr != nil {
				continue
			}
			metrics, ok := probeSamplesToMetricsWithCacheRate(byBucket[key], bucketMinutes, admin, cacheRate)
			if !ok {
				continue
			}
			row.Buckets = append(row.Buckets, service.ChannelMonitorV2TrendPoint{
				BucketStart: bucketStart,
				Metrics:     metrics,
				Health:      probeBucketHealth(metrics),
				Source:      service.ChannelMonitorBucketSourceProbe,
			})
		}
		sort.Slice(row.Buckets, func(a, b int) bool {
			return row.Buckets[a].BucketStart.Before(row.Buckets[b].BucketStart)
		})
		// 行级聚合也要反映探针桶，否则卡片顶部的最新值取自 row.metrics 时是空的。
		refreshRowMetricsFromBuckets(row)
	}
}

// refreshRowMetricsFromBuckets 在有探针桶补入后重算行级聚合。
//
// 只重算那些「完全没有真实流量」的行：V2 已经算好的真实聚合绝不能被探针覆盖。
func refreshRowMetricsFromBuckets(row *service.ChannelMonitorV2MatrixRow) {
	if row == nil || row.Metrics.RequestCount > 0 {
		return
	}
	latest := row.Buckets[len(row.Buckets)-1]
	if latest.Metrics.RequestCount == 0 {
		return
	}
	row.Metrics = latest.Metrics
	row.Health = latest.Health
}
