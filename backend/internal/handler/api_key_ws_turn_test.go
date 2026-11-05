//go:build unit

package handler

import (
	"context"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"sync/atomic"
	"testing"
)

func TestAPIKeyProtocolWebSocketEachTurnRevalidatesAndReleases(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeCtxPool} {
		t.Run(mode, func(t *testing.T) {
			svc, rdb := protocolAdmissionCache(t)
			var revoked atomic.Bool
			got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
				ingressMode: mode, keyConcurrencyService: svc, keyConcurrencyLimit: 1,
				keyRevalidator: func(context.Context) (int, error) {
					if revoked.Load() {
						return 0, service.NewAPIKeyQueueAuthRejected(infraerrors.Unauthorized("API_KEY_DISABLED", "API key is disabled"))
					}
					return 1, nil
				},
				firstPayload: `{"type":"response.create","model":"gpt-5.1"}`, secondPayload: `{"type":"response.create","model":"gpt-5.1"}`,
				afterFirstUpstreamRequest: func(*service.ChannelService) error { revoked.Store(true); return nil }, secondTurnCloseExpected: true, closeReason: "disabled",
			})
			require.Len(t, got.upstreamPayloads, 1, "revoked second round never forwards")
			require.Eventually(t, func() bool { return rdb.ZCard(context.Background(), "concurrency:api_key:1801").Val() == 0 }, 1000000000, 10000000)
		})
	}
}
