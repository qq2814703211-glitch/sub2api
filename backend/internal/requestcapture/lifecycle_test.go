package requestcapture

import (
	"context"
	"github.com/stretchr/testify/require"
	"sync/atomic"
	"testing"
	"time"
)

type countedCaptureStore struct {
	*memoryStore
	calls atomic.Int64
}

func (s *countedCaptureStore) Tasks(c context.Context, i string, l, o int) ([]Task, error) {
	s.calls.Add(1)
	return s.memoryStore.Tasks(c, i, l, o)
}
func (s *countedCaptureStore) SaveTask(c context.Context, t *Task) error {
	s.calls.Add(1)
	return s.memoryStore.SaveTask(c, t)
}
func TestRequestCaptureDisabledRuntimeSleepsAndHotEnableStartsOnlyNewTasks(t *testing.T) {
	store := &countedCaptureStore{memoryStore: newMemoryStore()}
	m, e := New(store, t.TempDir(), Config{QuotaMiB: 16, RetentionDays: 1})
	require.NoError(t, e)
	defer m.Close()
	before := store.calls.Load()
	time.Sleep(1100 * time.Millisecond)
	require.Equal(t, before, store.calls.Load())
	require.Nil(t, m.Begin(Meta{UserID: 27}))
	m.ApplyConfig(Config{Enabled: true, QuotaMiB: 16, RetentionDays: 1})
	task, e := m.Create(t.Context(), CreateTask{TargetType: "user", TargetID: 27, DurationMinutes: 1}, "fixture")
	require.NoError(t, e)
	s := m.Begin(Meta{UserID: 27})
	require.NotNil(t, s)
	s.ClientRequest([]byte(`{"input":"fixture"}`), "application/json", nil)
	m.ApplyConfig(Config{QuotaMiB: 16, RetentionDays: 1})
	drain(t, m)
	require.Eventually(t, func() bool {
		v, e := store.Task(t.Context(), m.instance, task.ID)
		return e == nil && v.Status == "stopped"
	}, time.Second, 10*time.Millisecond)
	before = store.calls.Load()
	time.Sleep(1100 * time.Millisecond)
	require.Equal(t, before, store.calls.Load())
	m.ApplyConfig(Config{Enabled: true, QuotaMiB: 16, RetentionDays: 1})
	require.Nil(t, m.Begin(Meta{UserID: 27}), "re-enable never resurrects stopped tasks")
}
