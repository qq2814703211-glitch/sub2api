//go:build unit

package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMediaRuntimeCompatibleRoutesRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter(service.PlatformOpenAI)
	routes := map[string]bool{}
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	require.True(t, routes["POST /v1/video/generations"])
	require.True(t, routes["GET /v1/video/generations/:task_id"])
	require.False(t, routes["DELETE /v1/video/generations/:task_id"], "no unverified delete protocol")
}
