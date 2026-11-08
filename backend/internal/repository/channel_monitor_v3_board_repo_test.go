//go:build unit

package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// The V3 board fills traffic-free buckets from V3's own probe samples and takes
// the cache rate from the group's real-traffic average (ADR 0009). These tests
// pin both the "unknown" and the "measured" branch, and prove the two boards never
// share probe data.

func v3ProbeSamplesAt(groupID int64, at time.Time) []service.ChannelMonitorV3ProbeSample {
	ttft := 120
	return []service.ChannelMonitorV3ProbeSample{{
		GroupID:   groupID,
		Success:   true,
		TTFTMs:    &ttft,
		CheckedAt: at,
	}}
}

// (c) A probe-only bucket with no real traffic must NOT report a 100% cache rate.
func TestChannelMonitorV3ProbeBucketHasNoCacheRateWithoutRealTraffic(t *testing.T) {
	base := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	groupID := int64(7)
	matrix := &service.ChannelMonitorV2Matrix{Items: []service.ChannelMonitorV2MatrixRow{{
		Platform: "anthropic",
		GroupID:  &groupID,
	}}}

	byGroup := bucketV3ProbeSamplesByGroup(v3ProbeSamplesAt(groupID, base.Add(2*time.Minute)), 5)
	applyV3ProbeFallbackToMatrix(matrix, byGroup, map[int64]float64{}, 5, false)

	require.Len(t, matrix.Items[0].Buckets, 1, "a probe-only bucket must still produce a column")
	bucket := matrix.Items[0].Buckets[0]
	require.Equal(t, service.ChannelMonitorBucketSourceProbe, bucket.Source)
	require.NotEqual(t, float64(1), bucket.Metrics.CacheRate,
		"a probe must never be credited with a 100%% cache rate (ADR 0009)")
	require.NotNil(t, bucket.Metrics.CacheRateKnown, "the cache rate must be explicitly flagged")
	require.False(t, *bucket.Metrics.CacheRateKnown, "with no real traffic the cache rate is unknown")
	require.Equal(t, int64(0), bucket.Metrics.CacheRateNumerator)
	require.Equal(t, int64(0), bucket.Metrics.CacheRateDenominator)
}

// (d) With real traffic in the window, the bucket carries that average instead.
func TestChannelMonitorV3ProbeBucketUsesRealTrafficCacheAverage(t *testing.T) {
	base := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	groupID := int64(7)
	matrix := &service.ChannelMonitorV2Matrix{Items: []service.ChannelMonitorV2MatrixRow{{
		Platform: "anthropic",
		GroupID:  &groupID,
	}}}

	byGroup := bucketV3ProbeSamplesByGroup(v3ProbeSamplesAt(groupID, base.Add(2*time.Minute)), 5)
	applyV3ProbeFallbackToMatrix(matrix, byGroup, map[int64]float64{groupID: 0.37}, 5, false)

	bucket := matrix.Items[0].Buckets[0]
	require.InDelta(t, 0.37, bucket.Metrics.CacheRate, 1e-9,
		"the probe bucket must borrow the group's real-traffic cache average")
	require.NotNil(t, bucket.Metrics.CacheRateKnown)
	require.True(t, *bucket.Metrics.CacheRateKnown, "a measured average is a known value")
}

// Real traffic always wins: a bucket that has requests keeps its own numbers.
func TestChannelMonitorV3ProbeFallbackNeverOverwritesOrganicBuckets(t *testing.T) {
	base := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	groupID := int64(7)
	matrix := &service.ChannelMonitorV2Matrix{Items: []service.ChannelMonitorV2MatrixRow{{
		Platform: "anthropic",
		GroupID:  &groupID,
		Buckets: []service.ChannelMonitorV2TrendPoint{{
			BucketStart: base,
			Metrics: service.ChannelMonitorV2Metric{
				RequestCount:         8,
				SuccessRequests:      6,
				CacheRateNumerator:   50,
				CacheRateDenominator: 200,
			},
		}},
	}}}

	byGroup := bucketV3ProbeSamplesByGroup(v3ProbeSamplesAt(groupID, base.Add(time.Minute)), 5)
	applyV3ProbeFallbackToMatrix(matrix, byGroup, map[int64]float64{groupID: 0.9}, 5, false)

	require.Len(t, matrix.Items[0].Buckets, 1)
	bucket := matrix.Items[0].Buckets[0]
	require.Equal(t, int64(8), bucket.Metrics.RequestCount, "organic traffic must be preserved")
	require.NotEqual(t, service.ChannelMonitorBucketSourceProbe, bucket.Source)
	require.Equal(t, int64(50), bucket.Metrics.CacheRateNumerator, "the real 50/200 rate must not be replaced")
	require.Equal(t, int64(200), bucket.Metrics.CacheRateDenominator, "the real 50/200 rate must not be replaced")
	require.Nil(t, bucket.Metrics.CacheRateKnown, "an organic bucket is measured, not flagged unknown")
}

// V3 probes are keyed by group_id, so they can never land on another group's row.
func TestChannelMonitorV3ProbeFallbackStaysWithinItsGroup(t *testing.T) {
	base := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	groupID := int64(7)
	otherGroup := int64(9)
	matrix := &service.ChannelMonitorV2Matrix{Items: []service.ChannelMonitorV2MatrixRow{
		{Platform: "anthropic", GroupID: &groupID},
		{Platform: "openai", GroupID: &otherGroup},
	}}

	byGroup := bucketV3ProbeSamplesByGroup(v3ProbeSamplesAt(groupID, base), 5)
	applyV3ProbeFallbackToMatrix(matrix, byGroup, map[int64]float64{groupID: 0.5}, 5, false)

	require.Len(t, matrix.Items[0].Buckets, 1, "the probed group gets its bucket")
	require.Empty(t, matrix.Items[1].Buckets, "another group must not inherit it")
}

// A failed probe still renders an availability signal, with cache left unknown.
func TestChannelMonitorV3FailedProbeBucketKeepsCacheUnknown(t *testing.T) {
	base := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	groupID := int64(7)
	matrix := &service.ChannelMonitorV2Matrix{Items: []service.ChannelMonitorV2MatrixRow{{
		Platform: "anthropic",
		GroupID:  &groupID,
	}}}

	samples := []service.ChannelMonitorV3ProbeSample{{
		GroupID:   groupID,
		Success:   false,
		CheckedAt: base,
	}}
	applyV3ProbeFallbackToMatrix(matrix, bucketV3ProbeSamplesByGroup(samples, 5), map[int64]float64{}, 5, false)

	bucket := matrix.Items[0].Buckets[0]
	require.Equal(t, int64(1), bucket.Metrics.RequestCount)
	require.Equal(t, float64(0), bucket.Metrics.SuccessRate, "a failed probe is 0/1")
	require.NotNil(t, bucket.Metrics.CacheRateKnown)
	require.False(t, *bucket.Metrics.CacheRateKnown)
	require.Equal(t, "critical", bucket.Health.Overall)
}

// The derived V2 config that carries the V3 scope must not impose a model
// allow-list or a platform gate of its own: empty models == show every model.
func TestChannelMonitorV3BoardConfigImposesNoAllowList(t *testing.T) {
	cfg := v3BoardConfig(service.ChannelMonitorV3BoardScope{
		GroupIDs:                  []int64{7, 9},
		Platforms:                 []string{"anthropic", "openai"},
		CacheAverageWindowMinutes: 30 * 24 * 60,
	})

	require.True(t, cfg.Enabled)
	require.Equal(t, []int64{7, 9}, cfg.GroupIDs)
	require.Len(t, cfg.Platforms, 2)
	for _, platform := range cfg.Platforms {
		require.Empty(t, platform.Models, "V3 must not filter models by an allow-list")
		require.True(t, platform.Enabled)
	}
}

// GroupCacheRates reads real traffic only, over the configured window, and excludes
// probe traffic from the average.
func TestChannelMonitorV3GroupCacheRatesQueryUsesRealTrafficWindow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &channelMonitorV3Repository{db: db}

	rows := sqlmock.NewRows([]string{"group_id", "cache_rate"}).
		AddRow(int64(7), 0.42).
		AddRow(int64(9), nil)
	mock.ExpectQuery("FROM usage_logs").
		WithArgs(sqlmock.AnyArg(), 30).
		WillReturnRows(rows)

	rates, err := repo.GroupCacheRates(context.Background(), []int64{7, 9}, 30)
	require.NoError(t, err)
	require.Equal(t, map[int64]float64{7: 0.42}, rates,
		"a group with no real tokens must be absent so the board can render an em dash")
	require.NoError(t, mock.ExpectationsWereMet())
}

// The cache-rate query must exclude probe traffic, otherwise the average would feed
// on the very probes whose cache behaviour it is supposed to replace.
func TestChannelMonitorV3GroupCacheRatesExcludesProbeTraffic(t *testing.T) {
	require.Contains(t, strings.ToLower(usageLogProbeExcludeUL), "probe",
		"the cache average must exclude probe traffic (ADR 0009)")
}
