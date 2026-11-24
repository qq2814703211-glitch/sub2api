//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChannelMonitorAggregationWanted(t *testing.T) {
	for _, tc := range []struct {
		name string
		v2On bool
		v3On bool
		want bool
	}{
		{"v2 only", true, false, true},
		{"v3 only", false, true, true},
		{"both", true, true, true},
		{"neither", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, channelMonitorAggregationWanted(tc.v2On, tc.v3On))
		})
	}
}

func TestChannelMonitorForwardCatchup(t *testing.T) {
	now := time.Date(2026, 8, 8, 20, 0, 37, 0, time.FixedZone("UTC+8", 8*60*60))
	nowMinute := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	through := now.Add(-30 * time.Hour)
	throughMinute := nowMinute.Add(-30 * time.Hour)
	for _, tc := range []struct {
		name        string
		dataThrough time.Time
		chunk       time.Duration
		wantStart   time.Time
		wantEnd     time.Time
		wantOK      bool
	}{
		{name: "zero watermark", chunk: 30 * time.Minute},
		{name: "inside overlap", dataThrough: now.Add(-5 * time.Minute), chunk: 30 * time.Minute},
		{name: "at overlap boundary", dataThrough: now.Add(-10 * time.Minute), chunk: 30 * time.Minute},
		{"stale watermark", through, 30 * time.Minute, throughMinute, throughMinute.Add(30 * time.Minute), true},
		{"clamp to now", through, 31 * time.Hour, throughMinute, nowMinute, true},
		{"default chunk", through, 0, throughMinute, throughMinute.Add(15 * time.Minute), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start, end, ok := channelMonitorForwardCatchup(tc.dataThrough, now, tc.chunk)
			require.Equal(t, tc.wantOK, ok)
			if ok {
				require.Equal(t, tc.wantStart, start)
				require.Equal(t, tc.wantEnd, end)
			}
		})
	}
}

type channelMonitorV2RecomputeRange struct {
	start, end time.Time
}

type channelMonitorV2CatchupRepo struct {
	channelMonitorV2RepoStub
	watermark   ChannelMonitorV2AggregationWatermark
	ranges      []channelMonitorV2RecomputeRange
	failAtStart time.Time
}

func (r *channelMonitorV2CatchupRepo) GetAggregationWatermark(context.Context) (*ChannelMonitorV2AggregationWatermark, error) {
	wm := r.watermark
	return &wm, nil
}

func (r *channelMonitorV2CatchupRepo) RecomputeRange(_ context.Context, start, end time.Time) error {
	r.ranges = append(r.ranges, channelMonitorV2RecomputeRange{start, end})
	if start.Equal(r.failAtStart) {
		r.failAtStart = time.Time{}
		return errors.New("transient recompute error")
	}
	if end.After(r.watermark.DataThrough) {
		r.watermark.DataThrough = end
	}
	return nil
}

func TestChannelMonitorV2AggregatorFillsGapAfterLiveSeed(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	through := now.Add(-3*time.Hour - 5*time.Minute)
	repo := &channelMonitorV2CatchupRepo{
		watermark: ChannelMonitorV2AggregationWatermark{
			DataThrough:    through,
			BackfillCursor: now.Add(-91 * 24 * time.Hour),
			HasData:        true,
		},
		failAtStart: through,
	}
	s := NewChannelMonitorV2Aggregator(repo, nil, nil)

	s.runOnce()
	require.Len(t, repo.ranges, 2)
	seed, failedChunk := repo.ranges[0], repo.ranges[1]
	require.Equal(t, 2*time.Hour, seed.end.Sub(seed.start))
	require.Equal(t, seed.end, repo.watermark.DataThrough)
	require.Equal(t, channelMonitorV2RecomputeRange{through, through.Add(30 * time.Minute)}, failedChunk)

	s.runOnce()
	require.Len(t, repo.ranges, 3)
	require.Equal(t, failedChunk, repo.ranges[2])

	s.runOnce()
	require.Len(t, repo.ranges, 4)
	require.Equal(t, channelMonitorV2RecomputeRange{through.Add(30 * time.Minute), through.Add(time.Hour)}, repo.ranges[3])

	s.runOnce()
	require.Len(t, repo.ranges, 6)
	lastChunk, overlap := repo.ranges[4], repo.ranges[5]
	require.Equal(t, channelMonitorV2RecomputeRange{through.Add(time.Hour), overlap.end.Add(-2 * time.Hour)}, lastChunk)
	require.Equal(t, 10*time.Minute, overlap.end.Sub(overlap.start))

	s.runOnce()
	require.Len(t, repo.ranges, 7)
	require.Equal(t, 10*time.Minute, repo.ranges[6].end.Sub(repo.ranges[6].start))
}

func TestChannelMonitorV2MaxChunkForDepth(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

	// Within last day → tightest ceiling (2h).
	require.Equal(t, channelMonitorV2MaxChunkNear1d, channelMonitorV2MaxChunkForDepth(now, now.Add(-2*time.Hour)))
	// Between 1d and 7d → 4h.
	require.Equal(t, channelMonitorV2MaxChunkNear7d, channelMonitorV2MaxChunkForDepth(now, now.Add(-2*24*time.Hour)))
	// Older than 7d → 6h (never 24h default).
	require.Equal(t, channelMonitorV2MaxChunkFar, channelMonitorV2MaxChunkForDepth(now, now.Add(-10*24*time.Hour)))
	require.Less(t, channelMonitorV2MaxChunkFar, 24*time.Hour)
	require.Equal(t, time.Hour, channelMonitorV2BackfillChunkInit)
	require.Equal(t, 15*time.Minute, channelMonitorV2MinBackfillChunk)
}

func TestChannelMonitorV2AggregatorAdaptiveChunk(t *testing.T) {
	s := NewChannelMonitorV2Aggregator(nil, nil, nil)
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	cursor := now.Add(-3 * time.Hour)

	// Failure shrinks chunk and sets backoff floor.
	s.backfillChunk = 2 * time.Hour
	s.recordBackfillFailure(now, cursor)
	require.Equal(t, time.Hour, s.backfillChunk)
	require.Equal(t, time.Minute, s.nextWaitFloor)
	require.Equal(t, 1, s.backfillFailures)

	// Repeated failure halves again and raises floor.
	s.recordBackfillFailure(now, cursor)
	require.Equal(t, 30*time.Minute, s.backfillChunk)
	require.Equal(t, 2*time.Minute, s.nextWaitFloor)

	// Fast success grows within depth ceiling and clears backoff.
	s.recordBackfillSuccess(cursor.Add(-30*time.Minute), 5*time.Second, now)
	require.Equal(t, 0, s.backfillFailures)
	require.Equal(t, time.Duration(0), s.nextWaitFloor)
	require.Greater(t, s.backfillChunk, 30*time.Minute)
	require.LessOrEqual(t, s.backfillChunk, channelMonitorV2MaxChunkForDepth(now, cursor.Add(-30*time.Minute)))
}
