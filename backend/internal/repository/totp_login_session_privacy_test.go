//go:build unit

package repository

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestLoginSessionDiscardLegacyEnvelopeAndConsumeOnce(t *testing.T) {
	server := miniredis.RunT(t)
	ctx := context.Background()
	var instances []*service.TotpService
	for range 2 {
		client := redis.NewClient(&redis.Options{Addr: server.Addr()})
		t.Cleanup(func() { _ = client.Close() })
		instances = append(instances, service.NewTotpService(nil, nil, NewTotpCache(client), nil, nil, nil))
	}
	token, err := instances[0].CreateLoginSession(ctx, 42, "test@example.invalid")
	require.NoError(t, err)
	key := totpLoginKeyPrefix + token
	raw, err := server.Get(key)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(raw), &fields))
	require.NotContains(t, fields, "password_login_envelope")
	fields["password_login_envelope"] = json.RawMessage(`"legacy-fixture"`)
	legacy, err := json.Marshal(fields)
	require.NoError(t, err)
	require.NoError(t, server.Set(key, string(legacy)))
	session, err := instances[1].GetLoginSession(ctx, token)
	require.NoError(t, err)
	require.NotNil(t, session)
	require.Equal(t, int64(42), session.UserID)
	clean, err := json.Marshal(session)
	require.NoError(t, err)
	require.JSONEq(t, raw, string(clean))
	var wins atomic.Int32
	var workers sync.WaitGroup
	for index := range 16 {
		workers.Go(func() {
			got, err := instances[index%2].ConsumeLoginSession(ctx, token)
			if err != nil {
				t.Errorf("consume login session: %v", err)
			} else if got != nil {
				wins.Add(1)
			}
		})
	}
	workers.Wait()
	require.Equal(t, int32(1), wins.Load())
	require.False(t, server.Exists(key))
	token, err = instances[0].CreateLoginSession(ctx, 42, "test@example.invalid")
	require.NoError(t, err)
	server.FastForward(6 * time.Minute)
	expired, err := instances[1].ConsumeLoginSession(ctx, token)
	require.NoError(t, err)
	require.Nil(t, expired)
}
