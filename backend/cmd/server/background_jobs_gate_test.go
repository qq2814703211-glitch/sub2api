package main

import "testing"

func TestBackgroundJobsEnabled(t *testing.T) {
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
			if got := backgroundJobsEnabled(); got != tc.want {
				t.Fatalf("backgroundJobsEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}
