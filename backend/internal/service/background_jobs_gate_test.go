//go:build unit

package service

import (
	"testing"

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

func TestEmailQueueWorkersFollowBackgroundJobsGate(t *testing.T) {
	t.Setenv("BACKGROUND_JOBS_ENABLED", "false")
	q := NewEmailQueueService(nil, 1)
	require.NotNil(t, q)
	require.Empty(t, q.taskChan)
}
