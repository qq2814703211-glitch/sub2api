package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type ticketHandlerRepo struct {
	service.TicketRepository
	mu      sync.Mutex
	ticket  service.Ticket
	created *service.CreateTicketInput
}

func (r *ticketHandlerRepo) List(_ context.Context, actor service.TicketActor, _ service.TicketFilter) ([]service.Ticket, int64, error) {
	if !actor.Admin && r.ticket.UserID != actor.UserID {
		return nil, 0, nil
	}
	return []service.Ticket{r.ticket}, 1, nil
}

func (r *ticketHandlerRepo) Stats(context.Context, service.TicketActor) (*service.TicketStats, error) {
	return &service.TicketStats{Total: 1, Open: 1, Unread: 1}, nil
}

func (r *ticketHandlerRepo) Detail(_ context.Context, actor service.TicketActor, id, _ int64) (*service.TicketDetail, error) {
	if id != r.ticket.ID || (!actor.Admin && r.ticket.UserID != actor.UserID) {
		return nil, service.ErrTicketNotFound
	}
	detail := &service.TicketDetail{
		Ticket:   &r.ticket,
		Messages: []service.TicketMessage{{ID: 1, TicketID: id, AuthorRole: "user", Kind: "reply", Content: "hello", CreatedAt: time.Now()}},
	}
	if actor.Admin {
		detail.Requester = &service.TicketRequester{ID: r.ticket.UserID, Username: "owner", Email: "owner@example.test", Balance: 10}
	}
	return detail, nil
}

func (r *ticketHandlerRepo) Create(_ context.Context, actor service.TicketActor, in service.CreateTicketInput, _ string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.created = &in
	r.ticket.UserID = actor.UserID
	return r.ticket.ID, nil
}

func (r *ticketHandlerRepo) CreateForUser(context.Context, service.TicketActor, int64, string, string, string, string) (int64, error) {
	return r.ticket.ID, nil
}

func (r *ticketHandlerRepo) Mutate(_ context.Context, actor service.TicketActor, id int64, _, _ string, change func(*service.Ticket) (*service.TicketChange, error)) error {
	if id != r.ticket.ID || (!actor.Admin && r.ticket.UserID != actor.UserID) {
		return service.ErrTicketNotFound
	}
	plan, err := change(&r.ticket)
	if err != nil {
		return err
	}
	r.ticket = *plan.Ticket
	return nil
}

func (r *ticketHandlerRepo) MarkRead(context.Context, service.TicketActor, int64, int64) error {
	return nil
}

func (r *ticketHandlerRepo) EmailOutboxStats(context.Context) (int64, int64, error) {
	return 0, 0, nil
}

type ticketHandlerUsers struct {
	service.UserRepository
}

func (ticketHandlerUsers) GetByID(_ context.Context, id int64) (*service.User, error) {
	role := service.RoleUser
	if id == 8 {
		role = service.RoleAdmin
	}
	return &service.User{ID: id, Username: "owner", Email: "owner@example.test", Status: service.StatusActive, Role: role}, nil
}

func newTicketHandlerEngine(t *testing.T) (*gin.Engine, *ticketHandlerRepo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := &ticketHandlerRepo{ticket: service.Ticket{ID: 10, UserID: 7, Subject: "help", Status: "open", Priority: "normal",
		Category: "api", UserName: "owner", UserEmail: "owner@example.test", LastMessageID: 1, LastMessageAt: time.Now()}}
	svc := service.NewTicketService(repo, ticketHandlerUsers{})
	h := NewTicketHandler(svc)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		userID := int64(7)
		role := service.RoleUser
		if c.GetHeader("X-Test-Admin") == "1" {
			userID, role = 8, service.RoleAdmin
		}
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID})
		c.Set(string(middleware.ContextKeyUserRole), role)
		c.Next()
	})
	v1 := engine.Group("/api/v1")
	tickets := v1.Group("/tickets")
	{
		tickets.GET("", h.List)
		tickets.GET("/stats", h.Stats)
		tickets.POST("", h.Create)
		tickets.GET("/:id", h.Detail)
		tickets.POST("/:id/replies", h.Reply)
		tickets.PATCH("/:id", h.Update)
		tickets.POST("/:id/read", h.MarkRead)
	}
	admin := v1.Group("/admin/tickets")
	{
		admin.GET("/stats", h.AdminStats)
		admin.GET("", h.AdminList)
		admin.POST("", h.AdminCreate)
		admin.GET("/:id", h.AdminDetail)
		admin.POST("/:id/replies", h.AdminReply)
		admin.PATCH("/:id", h.AdminUpdate)
		admin.POST("/:id/read", h.AdminMarkRead)
	}
	return engine, repo
}

func ticketRequest(engine *gin.Engine, method, path string, body string, admin bool) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	if admin {
		request.Header.Set("X-Test-Admin", "1")
	}
	engine.ServeHTTP(recorder, request)
	return recorder
}

func decodeTicketEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope), recorder.Body.String())
	return envelope
}

func TestTicketHandlerUserListScrubsRequesterIdentity(t *testing.T) {
	engine, _ := newTicketHandlerEngine(t)
	recorder := ticketRequest(engine, http.MethodGet, "/api/v1/tickets", "", false)
	require.Equal(t, http.StatusOK, recorder.Code)
	envelope := decodeTicketEnvelope(t, recorder)
	require.EqualValues(t, 0, envelope["code"])
	data := requireTicketValue[map[string]any](t, envelope["data"])
	items := requireTicketValue[[]any](t, data["items"])
	require.Len(t, items, 1)
	ticket := requireTicketValue[map[string]any](t, items[0])
	require.NotContains(t, ticket, "user_email")
	require.NotContains(t, ticket, "user_name")
}

func TestTicketHandlerAdminDetailIncludesRequester(t *testing.T) {
	engine, _ := newTicketHandlerEngine(t)
	denied := ticketRequest(engine, http.MethodGet, "/api/v1/admin/tickets/10", "", false)
	require.Equal(t, http.StatusForbidden, denied.Code)
	recorder := ticketRequest(engine, http.MethodGet, "/api/v1/admin/tickets/10", "", true)
	require.Equal(t, http.StatusOK, recorder.Code)
	envelope := decodeTicketEnvelope(t, recorder)
	data := requireTicketValue[map[string]any](t, envelope["data"])
	requester := requireTicketValue[map[string]any](t, data["requester"])
	require.Equal(t, "owner@example.test", requester["email"])
	require.Equal(t, "owner", requireTicketValue[map[string]any](t, data["ticket"])["user_name"])
}

func TestTicketHandlerCreateValidationAndIdempotentBinding(t *testing.T) {
	engine, repo := newTicketHandlerEngine(t)
	body := `{"subject":"help","content":"details","contact":"","category":"api","priority":"normal","client_id":"6b7a9b04-1952-491c-ae6d-a2da990d8a8b"}`
	recorder := ticketRequest(engine, http.MethodPost, "/api/v1/tickets", body, false)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NotNil(t, repo.created)
	require.Equal(t, "6b7a9b04-1952-491c-ae6d-a2da990d8a8b", repo.created.ClientID)

	unknown := ticketRequest(engine, http.MethodPost, "/api/v1/tickets", `{"subject":"help","content":"details","category":"api","priority":"normal","client_id":"6b7a9b04-1952-491c-ae6d-a2da990d8a8b","extra":1}`, false)
	require.Equal(t, http.StatusBadRequest, unknown.Code)
	invalid := ticketRequest(engine, http.MethodPost, "/api/v1/tickets", `{"subject":"help","content":"details","category":"api","priority":"normal","client_id":"nope"}`, false)
	require.Equal(t, http.StatusBadRequest, invalid.Code)
}

func TestTicketHandlerReplyStatusAndReadCursor(t *testing.T) {
	engine, _ := newTicketHandlerEngine(t)
	userStatus := ticketRequest(engine, http.MethodPost, "/api/v1/tickets/10/replies",
		`{"content":"reply","client_id":"6b7a9b04-1952-491c-ae6d-a2da990d8a8b","status":"resolved"}`, false)
	require.Equal(t, http.StatusBadRequest, userStatus.Code)

	missing := ticketRequest(engine, http.MethodPost, "/api/v1/tickets/10/read", `{}`, false)
	require.Equal(t, http.StatusBadRequest, missing.Code)
	ok := ticketRequest(engine, http.MethodPost, "/api/v1/tickets/10/read", `{"last_message_id":1}`, false)
	require.Equal(t, http.StatusOK, ok.Code, ok.Body.String())
}

func TestTicketHandlerListRejectsOversizedPage(t *testing.T) {
	engine, _ := newTicketHandlerEngine(t)
	recorder := ticketRequest(engine, http.MethodGet, "/api/v1/tickets?page_size=101", "", false)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestTicketHandlerAdminCreateRequiresAdmin(t *testing.T) {
	engine, _ := newTicketHandlerEngine(t)
	body := `{"user_id":7,"subject":"hello","content":"support here"}`
	denied := ticketRequest(engine, http.MethodPost, "/api/v1/admin/tickets", body, false)
	require.Equal(t, http.StatusForbidden, denied.Code)
	ok := ticketRequest(engine, http.MethodPost, "/api/v1/admin/tickets", body, true)
	require.Equal(t, http.StatusOK, ok.Code, ok.Body.String())
}

func requireTicketValue[T any](t *testing.T, value any) T {
	t.Helper()
	v, ok := value.(T)
	require.True(t, ok, "unexpected JSON type %T", value)
	return v
}
