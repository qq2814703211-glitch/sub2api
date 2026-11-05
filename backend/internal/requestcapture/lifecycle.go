package requestcapture

import (
	"sync/atomic"
	"time"
)

func (m *Manager) taskRetentionExpired(t *Task) bool {
	return t.EndedAt != nil && !time.Now().Before(t.EndedAt.Add(time.Duration(m.Config().RetentionDays)*24*time.Hour))
}
func recordExpired(r *Record, now time.Time) bool {
	return r != nil && r.ExpiresAt != nil && !now.Before(*r.ExpiresAt)
}
func (m *Manager) noteSkipped(reason string) {
	value, _ := m.skippedByReason.LoadOrStore(reason, &atomic.Int64{})
	if count, ok := value.(*atomic.Int64); ok {
		count.Add(1)
	}
}
