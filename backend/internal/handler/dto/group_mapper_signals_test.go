package dto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestApplyGroupSignalsZeroLeavesPayloadUnchanged(t *testing.T) {
	group := &Group{ID: 1, Name: "g"}
	ApplyGroupSignals(group, service.GroupSignals{})

	raw, err := json.Marshal(group)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "recent_rate_change")
	require.NotContains(t, string(raw), "is_new")
	require.NotContains(t, string(raw), "launched_at")
	require.NotContains(t, string(raw), "launch_rate")
}

func TestApplyGroupSignalsRateChange(t *testing.T) {
	group := &Group{ID: 1, Name: "g"}
	changedAt := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	ApplyGroupSignals(group, service.GroupSignals{RecentRateChange: &service.GroupRecentRateChange{
		Direction:    service.GroupRateUp,
		Delta:        0.02,
		PreviousRate: 0.06,
		ChangedAt:    changedAt,
	}})

	require.NotNil(t, group.RecentRateChange)
	require.Equal(t, "up", group.RecentRateChange.Direction)
	require.InDelta(t, 0.02, group.RecentRateChange.Delta, 1e-9)
	require.InDelta(t, 0.06, group.RecentRateChange.PreviousRate, 1e-9)
	require.Equal(t, changedAt, group.RecentRateChange.ChangedAt)
	require.False(t, group.IsNew)

	raw, err := json.Marshal(group)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"recent_rate_change":{"direction":"up","delta":0.02,"previous_rate":0.06,"changed_at":"2026-11-05T09:00:00Z"}`)
}

func TestApplyGroupSignalsNewGroup(t *testing.T) {
	group := &Group{ID: 1, Name: "g"}
	launchedAt := time.Date(2026, 11, 5, 3, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))
	rate := 0.06
	ApplyGroupSignals(group, service.GroupSignals{IsNew: true, LaunchedAt: launchedAt, LaunchRate: &rate})

	require.True(t, group.IsNew)
	require.NotNil(t, group.LaunchedAt)
	require.Equal(t, launchedAt.UTC(), *group.LaunchedAt, "时间统一按 UTC 下发")
	require.NotNil(t, group.LaunchRate)
	require.InDelta(t, 0.06, *group.LaunchRate, 1e-9)
	require.Nil(t, group.RecentRateChange)
}

func TestApplyGroupSignalsNewGroupWithoutLaunchRate(t *testing.T) {
	group := &Group{ID: 1, Name: "g"}
	ApplyGroupSignals(group, service.GroupSignals{IsNew: true, LaunchedAt: time.Date(2026, 11, 5, 3, 0, 0, 0, time.UTC)})

	require.True(t, group.IsNew)
	require.NotNil(t, group.LaunchedAt)
	require.Nil(t, group.LaunchRate, "个人倍率或历史不可读时不得下发初始倍率")

	raw, err := json.Marshal(group)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "launch_rate")
}

func TestApplyGroupSignalsNilTargetIsSafe(t *testing.T) {
	require.NotPanics(t, func() { ApplyGroupSignals(nil, service.GroupSignals{IsNew: true}) })
}
