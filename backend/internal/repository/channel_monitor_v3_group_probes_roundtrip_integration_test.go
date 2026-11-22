//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 保存路径要证明：分组探针真的落到 groups jsonb 并能读回，根级 probe 保持空，
// 旧实例试图写回共享探针时会被 CHECK 约束拦下。
func TestChannelMonitorV3UpdateConfigPersistsPerGroupProbes(t *testing.T) {
	ctx := context.Background()

	_, err := integrationDB.Exec(`UPDATE channel_monitor_v3_config
		SET probe = '{}'::jsonb, groups = '[]'::jsonb, version = 1, enabled = false WHERE id = 1`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.Exec(`UPDATE channel_monitor_v3_config
			SET probe = '{}'::jsonb, groups = '[]'::jsonb WHERE id = 1`)
	})

	repo := NewChannelMonitorV3Repository(integrationDB)
	cfg := service.ChannelMonitorV3Config{
		Enabled:                 true,
		CacheAverageWindowDays:  7,
		CacheAverageWindowValue: 7,
		CacheAverageWindowUnit:  "day",
		Groups: []service.ChannelMonitorV3Group{
			{
				GroupID:      41,
				Enabled:      true,
				ProbeEnabled: true,
				Probe: service.ChannelMonitorV3Probe{
					Endpoint:        "https://group-41.example",
					APIKeyEncrypted: "cipher-41",
					Provider:        "openai",
					PrimaryModel:    "gpt-5",
					IntervalSeconds: 60,
				},
			},
			{GroupID: 42, Enabled: true},
		},
	}

	saved, err := repo.UpdateConfig(ctx, cfg, 1)
	require.NoError(t, err)
	require.Equal(t, 2, saved.Version)

	got, err := repo.GetConfig(ctx)
	require.NoError(t, err)
	require.Len(t, got.Groups, 2)
	require.Equal(t, int64(41), got.Groups[0].GroupID)
	require.Equal(t, "https://group-41.example", got.Groups[0].Probe.Endpoint)
	require.Equal(t, "cipher-41", got.Groups[0].Probe.APIKeyEncrypted)
	require.Equal(t, "gpt-5", got.Groups[0].Probe.PrimaryModel)
	require.True(t, got.Groups[0].HasOwnProbe())
	require.False(t, got.Groups[1].HasOwnProbe())

	var rootProbe string
	require.NoError(t, integrationDB.QueryRow(
		`SELECT probe::text FROM channel_monitor_v3_config WHERE id = 1`).Scan(&rootProbe))
	require.JSONEq(t, `{}`, rootProbe)

	_, err = integrationDB.Exec(`UPDATE channel_monitor_v3_config
		SET probe = '{"endpoint":"https://legacy.example","primary_model":"legacy"}'::jsonb WHERE id = 1`)
	require.Error(t, err, "shared probe must be rejected by the CHECK constraint")
}
