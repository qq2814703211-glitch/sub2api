//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A head store whose wake channel is under test control. `silent` reproduces a
// process whose notifications are lost; the backstop must still converge.
type followHeadStore struct {
	*publishTestStore
	mu     sync.Mutex
	wake   func(bool)
	silent bool
	head   error
}

func (s *followHeadStore) revision() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.publishTestStore.raw) == 0 {
		return ""
	}
	var head struct {
		Revision string `json:"revision"`
	}
	if json.Unmarshal(s.publishTestStore.raw, &head) != nil {
		return ""
	}
	return head.Revision
}

func (s *followHeadStore) PriceSyncHead(context.Context) (string, error) {
	if s.head != nil {
		return "", s.head
	}
	return s.revision(), nil
}

func (s *followHeadStore) FollowPriceSyncHead(ctx context.Context, wake func(bool)) error {
	s.mu.Lock()
	s.wake = wake
	silent := s.silent
	s.mu.Unlock()
	if silent {
		<-ctx.Done()
		return nil
	}
	<-ctx.Done()
	return nil
}

func (s *followHeadStore) notify() {
	s.mu.Lock()
	wake := s.wake
	s.mu.Unlock()
	if wake != nil {
		wake(false)
	}
}

func followFixture(t *testing.T, store *followHeadStore) (*ModelPricingSyncService, *BillingService) {
	t.Helper()
	s, b := publishFixture(t, store)
	s.cfg.FollowInterval = 40 * time.Millisecond
	// The wire provider starts the follower right after the first publish.
	s.followPriceSync()
	return s, b
}

func followSave(t *testing.T, s *ModelPricingSyncService, store *followHeadStore) string {
	t.Helper()
	cfg := publishConfig(t, s)
	cfg.ManualOverrides[publishModel] = publishManual(t, `{"input":13,"output":68,"cache_write_1h":26}`)
	saved := publishSet(t, s, cfg)
	store.notify()
	return saved.Revision
}

func converge(t *testing.T, s *ModelPricingSyncService, want string) {
	t.Helper()
	require.Eventually(t, func() bool { return s.activeRevision() == want }, 3*time.Second, 2*time.Millisecond)
}

func TestPriceFollowWakeAppliesCommittedHead(t *testing.T) {
	store := &followHeadStore{publishTestStore: &publishTestStore{}}
	publisher, _ := followFixture(t, store)
	defer publisher.Stop()
	follower, billing := followFixture(t, store)
	defer follower.Stop()
	require.Empty(t, follower.activeRevision())
	rev := followSave(t, publisher, store)
	converge(t, follower, rev)
	publishMeasure(t, billing, publishModel, 68, 26)
	status := follower.Status()["pricing_v2"].(map[string]any)
	require.True(t, status["converged"].(bool))
	require.Equal(t, rev, status["active_revision"])
	require.Equal(t, rev, status["head_revision"])
	require.Equal(t, "notify", status["follow"].(map[string]any)["mode"])
}

// The revision is a hint, not a command: a follower that is told about a head
// it cannot read must keep billing what it loaded and say it is not converged.
func TestPriceFollowHeadReadFailureIsReportedNotGuessed(t *testing.T) {
	store := &followHeadStore{publishTestStore: &publishTestStore{}}
	publisher, _ := followFixture(t, store)
	defer publisher.Stop()
	follower, billing := followFixture(t, store)
	defer follower.Stop()
	rev := followSave(t, publisher, store)
	converge(t, follower, rev)
	store.head = errors.New("database unavailable")
	store.notify()
	require.Eventually(t, func() bool {
		v := follower.Status()["pricing_v2"].(map[string]any)
		_, ok := v["head_error"]
		return ok && v["head_error"] == "database unavailable"
	}, 2*time.Second, 2*time.Millisecond)
	status := follower.Status()["pricing_v2"].(map[string]any)
	require.False(t, status["converged"].(bool))
	require.Equal(t, rev, status["active_revision"])
	publishMeasure(t, billing, publishModel, 68, 26)
}

// A follower that has not verified the head inside the window must stop calling
// itself converged even when nothing looks wrong locally.
func TestPriceFollowStaleVerificationIsNotConverged(t *testing.T) {
	store := &followHeadStore{publishTestStore: &publishTestStore{}}
	publisher, _ := followFixture(t, store)
	defer publisher.Stop()
	follower, _ := followFixture(t, store)
	rev := followSave(t, publisher, store)
	converge(t, follower, rev)
	require.True(t, follower.Status()["pricing_v2"].(map[string]any)["converged"].(bool))
	follower.follow.mu.Lock()
	follower.follow.headAt = time.Now().Add(-3 * follower.followBackstop())
	follower.follow.mu.Unlock()
	status := follower.Status()["pricing_v2"].(map[string]any)
	require.False(t, status["converged"].(bool))
	require.True(t, status["stale"].(bool))
	follower.Stop()
}

func TestPriceFollowBackstopWithoutNotifications(t *testing.T) {
	store := &followHeadStore{publishTestStore: &publishTestStore{}, silent: true}
	publisher, _ := followFixture(t, store)
	defer publisher.Stop()
	follower, billing := followFixture(t, store)
	defer follower.Stop()
	rev := followSave(t, publisher, store)
	converge(t, follower, rev)
	publishMeasure(t, billing, publishModel, 68, 26)
	status := follower.Status()["pricing_v2"].(map[string]any)
	require.Equal(t, "notify", status["follow"].(map[string]any)["mode"])
	require.True(t, status["converged"].(bool))
}

// The wake carries no revision at all, so a burst of commits cannot leave the
// follower parked on an intermediate head; one wake must land on the newest.
func TestPriceFollowBurstLeavesNewestHead(t *testing.T) {
	store := &followHeadStore{publishTestStore: &publishTestStore{}}
	publisher, _ := followFixture(t, store)
	defer publisher.Stop()
	follower, _ := followFixture(t, store)
	cfg := publishConfig(t, publisher)
	first := publishSet(t, publisher, cfg)
	second := publishConfig(t, publisher)
	second.Enabled = true
	latest := publishSet(t, publisher, second)
	require.NotEqual(t, first.Revision, latest.Revision)
	// One wake for the whole burst: the follower must read the newest head.
	store.notify()
	converge(t, follower, latest.Revision)
	follower.Stop()
	// Stop is idempotent for the follow goroutines and must not block.
	done := make(chan struct{})
	go func() { follower.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("follower did not stop")
	}
}

// A failed install must leave the previous projection billing and must not
// report the newer head as applied.
func TestPriceFollowFailedInstallKeepsPreviousProjection(t *testing.T) {
	store := &followHeadStore{publishTestStore: &publishTestStore{}}
	publisher, _ := followFixture(t, store)
	defer publisher.Stop()
	follower, billing := followFixture(t, store)
	defer follower.Stop()
	rev := followSave(t, publisher, store)
	converge(t, follower, rev)
	// Break the private projection directory so the next install cannot land.
	require.NoError(t, os.RemoveAll(follower.cfg.OverrideFile))
	require.NoError(t, os.MkdirAll(follower.cfg.OverrideFile, 0o700))
	cfg := publishConfig(t, publisher)
	cfg.ManualOverrides[publishModel] = publishManual(t, `{"input":13,"output":99,"cache_write_1h":44}`)
	next := publishSet(t, publisher, cfg)
	store.notify()
	require.Eventually(t, func() bool {
		return follower.Status()["pricing_v2"].(map[string]any)["converged"] == false
	}, 2*time.Second, 2*time.Millisecond)
	require.Equal(t, rev, follower.activeRevision())
	publishMeasure(t, billing, publishModel, 68, 26)
	require.Equal(t, next.Revision, follower.Status()["pricing_v2"].(map[string]any)["head_revision"])
}
