package routes

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Registering static /settings next to /:id must not panic, and every public and
// admin ticket route must actually be mounted.
func TestTicketRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	h := &handler.Handlers{Ticket: handler.NewTicketHandler(nil)}
	registerUserTicketRoutes(v1, h)
	registerTicketRoutes(v1.Group("/admin"), h)

	got := map[string]bool{}
	for _, route := range engine.Routes() {
		got[route.Method+" "+route.Path] = true
	}
	for _, want := range []string{
		"GET /api/v1/tickets",
		"GET /api/v1/tickets/stats",
		"POST /api/v1/tickets",
		"GET /api/v1/tickets/:id",
		"POST /api/v1/tickets/:id/replies",
		"PATCH /api/v1/tickets/:id",
		"POST /api/v1/tickets/:id/read",
		"GET /api/v1/admin/tickets",
		"GET /api/v1/admin/tickets/stats",
		"GET /api/v1/admin/tickets/settings",
		"PUT /api/v1/admin/tickets/settings",
		"POST /api/v1/admin/tickets",
		"GET /api/v1/admin/tickets/:id",
		"POST /api/v1/admin/tickets/:id/replies",
		"PATCH /api/v1/admin/tickets/:id",
		"POST /api/v1/admin/tickets/:id/read",
	} {
		require.True(t, got[want], "missing route %s", want)
	}
}

func TestTicketRoutesSkipPartialHandlerGraphs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	require.NotPanics(t, func() {
		registerUserTicketRoutes(v1, &handler.Handlers{})
		registerTicketRoutes(v1.Group("/admin"), &handler.Handlers{})
	})
	require.Empty(t, engine.Routes())
}
