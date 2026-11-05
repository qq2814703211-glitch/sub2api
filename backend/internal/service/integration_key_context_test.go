//go:build unit

package service

import (
 "context"
 "encoding/json"
 "testing"
 "testing/synctest"
 "time"

 pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
 "github.com/stretchr/testify/require"
)

// Exercise the composed producer/consumer contract: disconnect drains are bounded,
// while loss of a capacity lease cancels transport immediately, even when detached.
func TestIntegratedKeyOwnerAndUpstreamDrain(t *testing.T) {
 for _, mode := range []string{"drain", "lease"} {
  t.Run(mode, func(t *testing.T) {
   synctest.Test(t, func(t *testing.T) {
    client, disconnect := context.WithCancel(context.Background())
    request, stopOwner := WithAPIKeyAdmissionOwner(client)
    defer stopOwner()
    detached, releaseBuilder := detachUpstreamContext(request, 2*time.Second)
    releaseBuilder()
    attempt, releaseAttempt := pkghttputil.NewUpstreamAttemptContext(detached, time.Hour)
    defer releaseAttempt()
    disconnect()
    synctest.Wait()
    require.NoError(t, attempt.Err(), "client cancellation retains the billing drain window")
    if mode == "lease" {
     owner, ok := apiKeyAdmissionOwnerFromContext(request)
     require.True(t, ok)
     owner.cancel(ErrAPIKeySlotLeaseLost)
     require.ErrorIs(t, context.Cause(attempt), ErrAPIKeySlotLeaseLost)
    } else {
     time.Sleep(2*time.Second)
     synctest.Wait()
     require.ErrorIs(t, attempt.Err(), context.Canceled)
    }
   })
  })
 }
}

func TestIntegratedRouteAndConcurrencySnapshot(t *testing.T) {
 svc := &APIKeyService{}
 primary := int64(7)
 original := &APIKey{ID: 8, UserID: 2, User: &User{ID: 2}, GroupID: &primary, RouteGroupIDs: []int64{7,3,30}, ConcurrencyLimit: 4}
 snapshot := svc.snapshotFromAPIKey(context.Background(), original)
 payload, err := json.Marshal(snapshot)
 require.NoError(t, err)
 var decoded APIKeyAuthSnapshot
 require.NoError(t, json.Unmarshal(payload, &decoded))
 key, hit, err := svc.applyAuthCacheEntry("sk-synthetic", &APIKeyAuthCacheEntry{Snapshot: &decoded})
 require.NoError(t, err)
 require.True(t, hit)
 require.Equal(t, []int64{7,3,30}, key.CandidateGroupIDs())
 require.Equal(t, 4, key.ConcurrencyLimit)
 decoded.Version = 26
 _, hit, err = svc.applyAuthCacheEntry("sk-synthetic", &APIKeyAuthCacheEntry{Snapshot: &decoded})
 require.NoError(t, err)
 require.False(t, hit, "neither independent lane's v26 snapshot is complete")
}
