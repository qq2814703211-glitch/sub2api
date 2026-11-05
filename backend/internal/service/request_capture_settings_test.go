package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
	"github.com/Wei-Shaw/sub2api/internal/requestcapture/capturetest"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

type captureSettingsRepo struct {
	SettingRepository
	mu     sync.Mutex
	values map[string]string
}

func (r *captureSettingsRepo) GetValue(_ context.Context, k string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.values[k]
	if !ok {
		return "", ErrSettingNotFound
	}
	return v, nil
}
func (r *captureSettingsRepo) SetMultiple(_ context.Context, v map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, value := range v {
		r.values[k] = value
	}
	return nil
}
func TestRequestCaptureSettingsDefaultHotSwitchAndSlotIsolation(t *testing.T) {
	t.Setenv("SUB2API_CAPTURE_INSTANCE", "fixture-blue")
	repo := &captureSettingsRepo{values: map[string]string{}}
	s := NewSettingService(repo, nil)
	defaults, e := s.GetCaptureSettings(t.Context())
	require.NoError(t, e)
	require.False(t, defaults.Enabled)
	m, e := requestcapture.New(capturetest.NewStore(), t.TempDir(), defaults.captureConfig())
	require.NoError(t, e)
	defer m.Close()
	s.requestCaptureRuntime = &captureRuntime{manager: m}
	enabled := CaptureSettings{Enabled: true, QuotaMiB: 16, RetentionDays: 1}
	require.NoError(t, s.UpdateCaptureSettings(t.Context(), enabled))
	require.True(t, m.Enabled())
	task, e := m.Create(t.Context(), requestcapture.CreateTask{TargetType: "user", TargetID: 27, DurationMinutes: 1}, "fixture")
	require.NoError(t, e)
	require.NoError(t, s.UpdateCaptureSettings(t.Context(), CaptureSettings{QuotaMiB: 16, RetentionDays: 1}))
	require.False(t, m.Enabled())
	v, e := m.Task(t.Context(), task.ID)
	require.NoError(t, e)
	require.Equal(t, "stopped", v.Status)
	require.NoError(t, s.UpdateCaptureSettings(t.Context(), enabled))
	require.Nil(t, m.Begin(requestcapture.Meta{UserID: 27}), "enabling does not resume old tasks")
	require.Error(t, s.UpdateCaptureSettings(t.Context(), CaptureSettings{Enabled: true, QuotaMiB: 0, RetentionDays: 1}))
	stored, e := s.GetCaptureSettings(t.Context())
	require.NoError(t, e)
	require.Equal(t, enabled, stored)
	t.Setenv("SUB2API_CAPTURE_INSTANCE", "fixture-green")
	green, e := s.GetCaptureSettings(t.Context())
	require.NoError(t, e)
	require.False(t, green.Enabled)
	s.StopRequestCapture()
	s.StopRequestCapture()
	time.Sleep(time.Millisecond)
}
