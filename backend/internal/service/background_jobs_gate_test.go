//go:build unit

package service

import (
	"os"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestBackgroundJobsEnabledDefaultsOnAndHonorsExplicitOff(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset", value: "", want: true},
		{name: "enabled", value: "true", want: true},
		{name: "disabled", value: "false", want: false},
		{name: "case and whitespace", value: " FALSE ", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BACKGROUND_JOBS_ENABLED", tc.value)
			require.Equal(t, tc.want, BackgroundJobsEnabled())
		})
	}
}

func TestGreenConfigDisablesConfigScheduledWriters(t *testing.T) {
	configFile := "../../../green-config.yaml"
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		t.Skip("isolated deployment config fixture is not installed")
	}
	t.Setenv("CONFIG_FILE", configFile)
	cfg, err := config.LoadForBootstrap()
	require.NoError(t, err)
	require.False(t, cfg.TokenRefresh.Enabled)
	require.False(t, cfg.DashboardAgg.Enabled)
	require.False(t, cfg.UsageCleanup.Enabled)
	require.False(t, cfg.Ops.Enabled)
	require.Equal(t, "/app/data/model_pricing_overrides.json", cfg.Pricing.OverrideFile)
}

func TestEmailQueueWorkersFollowBackgroundJobsGate(t *testing.T) {
	t.Setenv("BACKGROUND_JOBS_ENABLED", "false")
	q := NewEmailQueueService(nil, 1)
	require.NotNil(t, q)
	require.Empty(t, q.taskChan)
}
