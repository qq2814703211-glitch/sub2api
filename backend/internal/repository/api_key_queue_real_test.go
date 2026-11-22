package repository

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAPIKeyQueueRealRedis(t *testing.T) {
	addr := os.Getenv("KEY_QUEUE_REDIS_ADDR")
	if addr == "" {
		t.Skip("dedicated Redis endpoint not supplied")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx := context.Background()
	require.NoError(t, client.Ping(ctx).Err())
	// The runner owns this isolated instance; use synthetic IDs only.
	c := &concurrencyCache{rdb: client, slotTTLSeconds: 3, waitQueueTTLSeconds: 3}
	var wg sync.WaitGroup
	var grants atomic.Int32
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ok, _, err := c.AcquireAPIKeySlotWithTime(ctx, 908, 2, fmt.Sprint(i))
			if err != nil {
				t.Error(err)
			}
			if ok {
				grants.Add(1)
			}
		}(i)
	}
	wg.Wait()
	require.EqualValues(t, 2, grants.Load())
	_, now, err := c.AcquireAPIKeySlotWithTime(ctx, 908, 2, "wait")
	require.NoError(t, err)
	deadline := now + 3000
	state, err := c.AdvanceAPIKeyQueue(ctx, 908, "wait", service.APIKeyQueueModeEnter, deadline, 2, 1)
	require.NoError(t, err)
	require.Equal(t, service.APIKeyQueueOutcomeWaiting, state)
	require.NoError(t, c.AbortAPIKeyQueueAttempt(ctx, 908, "wait", deadline, true))
	state, err = c.AdvanceAPIKeyQueue(ctx, 908, "wait", service.APIKeyQueueModeEnter, deadline, 2, 1)
	require.NoError(t, err)
	require.Equal(t, service.APIKeyQueueOutcomeAborted, state)
	require.NoError(t, c.ReleaseAPIKeySlot(ctx, 909, "late"))
	ok, _, err := c.AcquireAPIKeySlotWithTime(ctx, 909, 1, "late")
	require.NoError(t, err)
	require.False(t, ok, "late immediate grant is fenced")
	ok, _, err = c.AcquireAPIKeySlotWithTime(ctx, 910, 1, "expired")
	require.NoError(t, err)
	require.True(t, ok)
	// Redis TIME, expiry and renewal fencing use a real server clock.
	time.Sleep(3100 * time.Millisecond)
	exists, err := c.RefreshAPIKeySlot(ctx, 910, "expired")
	require.NoError(t, err)
	require.False(t, exists, "an expired lease cannot be renewed back to life")
	active, waiting, err := c.GetAPIKeyQueueStats(ctx, 908)
	require.NoError(t, err)
	require.Zero(t, active)
	require.Zero(t, waiting)
}
