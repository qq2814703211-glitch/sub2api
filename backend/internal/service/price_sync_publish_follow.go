package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// A committed revision is announced, never inferred. The notification is a
// hint: every follower re-reads the committed head and installs the whole
// revision locally before it reports the revision as active. A lost,
// duplicated or out-of-order notification can therefore only delay a follower,
// it can never make one claim a revision it has not loaded.
type PriceSyncHeadFollower interface {
	// FollowPriceSyncHead blocks until ctx is done. wake is called for each
	// notification; reconnected is true when the transport came back and
	// notifications may have been missed.
	FollowPriceSyncHead(ctx context.Context, wake func(reconnected bool)) error
}

const (
	priceSyncFollowDefaultBackstop = 15 * time.Second
	priceSyncFollowCatchUpLimit    = 8
	priceSyncFollowTimeout         = 10 * time.Second
)

// priceSyncFollow is the per-process view of the published head. It never
// holds a price or a projection; it only records what this process has
// verified, so a stale process can say so instead of echoing the cluster.
type priceSyncFollow struct {
	mu      sync.Mutex
	node    string
	mode    string
	head    string
	headAt  time.Time
	headErr string

	appliedAt    time.Time
	projectionOK bool

	wakes       int64
	reconnects  int64
	lastWakeAt  time.Time
	reconcileAt time.Time
	lastErr     string
}

type priceSyncFollowSnapshot struct {
	node         string
	mode         string
	head         string
	headAt       time.Time
	headErr      string
	appliedAt    time.Time
	projectionOK bool
	wakes        int64
	reconnects   int64
	lastWakeAt   time.Time
	reconcileAt  time.Time
	lastErr      string
}

func newPriceSyncFollow() *priceSyncFollow {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return &priceSyncFollow{node: fmt.Sprintf("%s:%d", host, os.Getpid())}
}

func (f *priceSyncFollow) setMode(mode string) {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.mode = mode
	f.mu.Unlock()
}

func (f *priceSyncFollow) recordHead(revision string, at time.Time) {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.head = revision
	f.headAt = at
	f.headErr = ""
	f.mu.Unlock()
}

func (f *priceSyncFollow) recordHeadError(err error) {
	if f == nil || err == nil {
		return
	}
	f.mu.Lock()
	f.headErr = err.Error()
	f.mu.Unlock()
}

// recordApplied is called only after the local projection and the in-memory
// map were swapped together. Anything less must not clear a fault.
func (f *priceSyncFollow) recordApplied(revision string, at time.Time) {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.projectionOK = true
	f.appliedAt = at
	if f.head == "" {
		f.head = revision
		f.headAt = at
	}
	f.mu.Unlock()
}

func (f *priceSyncFollow) recordProjectionOK() {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.projectionOK = true
	f.mu.Unlock()
}

func (f *priceSyncFollow) recordProjectionFault() {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.projectionOK = false
	f.mu.Unlock()
}

func (f *priceSyncFollow) recordWake(reconnected bool) {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.wakes++
	f.lastWakeAt = time.Now().UTC()
	if reconnected {
		f.reconnects++
	}
	f.mu.Unlock()
}

func (f *priceSyncFollow) recordReconcile(err error) {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.reconcileAt = time.Now().UTC()
	if err != nil {
		f.lastErr = err.Error()
	} else {
		f.lastErr = ""
	}
	f.mu.Unlock()
}

func (f *priceSyncFollow) snapshot() priceSyncFollowSnapshot {
	if f == nil {
		return priceSyncFollowSnapshot{}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return priceSyncFollowSnapshot{
		node: f.node, mode: f.mode, head: f.head, headAt: f.headAt, headErr: f.headErr,
		appliedAt: f.appliedAt, projectionOK: f.projectionOK, wakes: f.wakes,
		reconnects: f.reconnects, lastWakeAt: f.lastWakeAt, reconcileAt: f.reconcileAt, lastErr: f.lastErr,
	}
}

func (s *ModelPricingSyncService) followBackstop() time.Duration {
	if s != nil && s.cfg != nil && s.cfg.FollowInterval > 0 {
		return s.cfg.FollowInterval
	}
	return priceSyncFollowDefaultBackstop
}

func (s *ModelPricingSyncService) activeRevision() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return ""
	}
	return s.active.Revision
}

// Followers reconcile committed local projections even with fetching disabled.
// The backstop is not a price poll: it reads one indexed head identifier and
// only touches the projection when the head actually moved.
func (s *ModelPricingSyncService) followPriceSync() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		wake := make(chan struct{}, 1)
		if follower, ok := s.cfg.Store.(PriceSyncHeadFollower); ok {
			s.follow.setMode("notify")
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				err := follower.FollowPriceSyncHead(ctx, func(reconnected bool) {
					s.follow.recordWake(reconnected)
					select {
					case wake <- struct{}{}:
					default: // a pending catch-up already reads the newest head
					}
				})
				if err != nil && ctx.Err() == nil {
					s.follow.recordReconcile(err)
					slog.Warn("price revision listener stopped; backstop reconciliation continues", "error", err)
				}
			}()
		} else {
			s.follow.setMode("poll")
		}
		ticker := time.NewTicker(s.followBackstop())
		defer ticker.Stop()
		for {
			select {
			case <-s.stopCh:
				cancel()
				return
			case <-wake:
				s.catchUpPriceSync(ctx, "notify")
			case <-ticker.C:
				s.catchUpPriceSync(ctx, "backstop")
			}
		}
	}()
}

func (s *ModelPricingSyncService) catchUpPriceSync(parent context.Context, reason string) {
	for i := 0; i < priceSyncFollowCatchUpLimit; i++ {
		ctx, cancel := context.WithTimeout(parent, priceSyncFollowTimeout)
		advanced, err := s.reconcilePriceHead(ctx)
		cancel()
		s.follow.recordReconcile(err)
		if err != nil {
			slog.Warn("price revision reconciliation failed", "reason", reason, "error", err)
			return
		}
		if !advanced {
			return
		}
	}
	slog.Warn("price revision catch-up did not settle", "reason", reason)
}

// reconcilePriceHead returns true when this process installed a different
// revision, so a caller can drain revisions committed during the previous
// install instead of waiting for one more notification.
func (s *ModelPricingSyncService) reconcilePriceHead(ctx context.Context) (bool, error) {
	if reader, ok := s.cfg.Store.(interface {
		PriceSyncHead(context.Context) (string, error)
	}); ok {
		id, err := reader.PriceSyncHead(ctx)
		if err != nil {
			s.follow.recordHeadError(err)
			return false, err
		}
		s.follow.recordHead(id, time.Now().UTC())
		same := s.activeRevision() == id && id != ""
		if same && !s.projectionFaulted() {
			return false, nil
		}
	}
	before := s.activeRevision()
	if _, err := s.publish(ctx, nil); err != nil {
		return false, err
	}
	after := s.activeRevision()
	return after != "" && after != before, nil
}

func (s *ModelPricingSyncService) projectionFaulted() bool {
	s.pricing.buildMu.Lock()
	defer s.pricing.buildMu.Unlock()
	return s.pricing.projectionFault
}

// mergePriceSyncFollow reports what this process verified. `converged` is a
// statement about one process and one verification time, never about the
// cluster, so a lagging process can be told apart from a healthy one.
func (s *ModelPricingSyncService) mergePriceSyncFollow(status map[string]any) {
	if status == nil || s.follow == nil {
		return
	}
	v, ok := status["pricing_v2"].(map[string]any)
	if !ok {
		return
	}
	f := s.follow.snapshot()
	backstop := s.followBackstop()
	now := time.Now().UTC()
	active, _ := v["active_revision"].(string)
	fresh := !f.headAt.IsZero() && now.Sub(f.headAt) <= 2*backstop
	v["applied_revision"] = active
	if !f.appliedAt.IsZero() {
		v["applied_at"] = f.appliedAt
	}
	if f.head != "" {
		v["head_revision"] = f.head
	}
	if !f.headAt.IsZero() {
		v["head_checked_at"] = f.headAt
		v["head_age_seconds"] = now.Sub(f.headAt).Seconds()
	}
	v["converged"] = active != "" && f.head == active && f.headErr == "" && f.projectionOK && fresh
	v["stale"] = !fresh
	v["scope"] = "process"
	v["node"] = f.node
	v["follow"] = map[string]any{
		"mode": f.mode, "backstop_seconds": int(backstop.Seconds()),
		"wakes": f.wakes, "reconnects": f.reconnects,
		"last_wake_at": f.lastWakeAt, "last_reconcile_at": f.reconcileAt, "last_error": f.lastErr,
	}
	if f.headErr != "" {
		v["head_error"] = f.headErr
	}
}
