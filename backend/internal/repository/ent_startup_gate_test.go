package repository

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStartupWritesEnabled(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  bool
	}{
		{name: "default", want: true},
		{name: "false", value: "false", want: false},
		{name: "case insensitive", value: "FALSE", want: false},
		{name: "other value", value: "0", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BACKGROUND_JOBS_ENABLED", tc.value)
			require.Equal(t, tc.want, startupWritesEnabled())
		})
	}
}
