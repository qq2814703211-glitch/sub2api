package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Run with TICKET_TEST_DATABASE_URL pointing at an explicitly supplied, isolated
// database. The test creates and drops its own schema and applies only migration
// 912, so it never touches an application schema.
func ticketPostgres(t *testing.T) (*sql.DB, *service.TicketService, context.Context) {
	t.Helper()
	raw := os.Getenv("TICKET_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TICKET_TEST_DATABASE_URL is not set")
	}
	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	require.Contains(t, []string{"postgres", "postgresql"}, parsed.Scheme)
	root, err := sql.Open("postgres", raw)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	require.NoError(t, root.PingContext(ctx))
	schema := "ticket_test_" + uuid.New().String()[:8]
	_, err = root.ExecContext(ctx, "CREATE SCHEMA "+pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_, err := root.ExecContext(cleanup, "DROP SCHEMA "+pq.QuoteIdentifier(schema)+" CASCADE")
		require.NoError(t, err)
	})
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	db.SetMaxOpenConns(20)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.ExecContext(ctx, `CREATE TABLE users (
		id BIGINT PRIMARY KEY, username TEXT, email TEXT NOT NULL, balance NUMERIC(20,8) NOT NULL DEFAULT 100,
		status TEXT NOT NULL DEFAULT 'active', role TEXT NOT NULL DEFAULT 'user', deleted_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp());
		CREATE TABLE usage_logs (
			id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL, input_tokens INT NOT NULL DEFAULT 0,
			output_tokens INT NOT NULL DEFAULT 0, cache_creation_tokens INT NOT NULL DEFAULT 0,
			cache_read_tokens INT NOT NULL DEFAULT 0, actual_cost NUMERIC(20,10) NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
		CREATE TABLE payment_orders (
			id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL, amount NUMERIC(20,2) NOT NULL,
			order_type TEXT NOT NULL DEFAULT 'balance', status TEXT NOT NULL DEFAULT 'PENDING', paid_at TIMESTAMPTZ);
		INSERT INTO users (id,username,email) SELECT n, 'user' || n, 'user' || n || '@example.test' FROM generate_series(1,30) n;
		INSERT INTO users (id,username,email,role) VALUES (99,'support99','support99@example.test','admin'), (100,'support100','support100@example.test','admin');`)
	require.NoError(t, err)
	ddl, err := migrations.FS.ReadFile("912_support_tickets.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = db.ExecContext(ctx, string(ddl))
		require.NoError(t, err)
	}
	repo := NewTicketRepository(db)
	return db, service.NewTicketService(repo, &ticketPostgresUsers{db: db}), ctx
}

type ticketPostgresUsers struct {
	service.UserRepository
	db *sql.DB
}

func (r *ticketPostgresUsers) GetByID(ctx context.Context, id int64) (*service.User, error) {
	user := &service.User{}
	err := r.db.QueryRowContext(ctx, "SELECT id, username, email, balance, status, role, created_at, deleted_at FROM users WHERE id = $1", id).
		Scan(&user.ID, &user.Username, &user.Email, &user.Balance, &user.Status, &user.Role, &user.CreatedAt, &user.DeletedAt)
	return user, err
}

func (r *ticketPostgresUsers) GetFirstAdmin(ctx context.Context) (*service.User, error) {
	user := &service.User{}
	err := r.db.QueryRowContext(ctx, `SELECT id, username, email, balance, status, role, created_at, deleted_at
		FROM users WHERE role = 'admin' AND deleted_at IS NULL ORDER BY id LIMIT 1`).
		Scan(&user.ID, &user.Username, &user.Email, &user.Balance, &user.Status, &user.Role, &user.CreatedAt, &user.DeletedAt)
	return user, err
}

func ticketPGInput() service.CreateTicketInput {
	return service.CreateTicketInput{Subject: "API request failure", Content: "Please check this request",
		Contact: "contact-handle", Category: "api", Priority: "normal", ClientID: uuid.NewString()}
}

func ticketPGCreate(t *testing.T, svc *service.TicketService, ctx context.Context, userID int64) *service.TicketDetail {
	t.Helper()
	detail, err := svc.Create(ctx, service.TicketActor{UserID: userID}, ticketPGInput())
	require.NoError(t, err)
	return detail
}

// There is no daily creation quota. Concurrent distinct requests all succeed, and
// a repeated client_id is idempotent rather than a second conversation.
func TestTicketPostgresNoDailyQuotaAndIdempotentCreate(t *testing.T) {
	db, svc, ctx := ticketPostgres(t)
	for _, sameClient := range []bool{true, false} {
		owner := int64(1)
		if !sameClient {
			owner = 2
		}
		input := ticketPGInput()
		type outcome struct {
			id  int64
			err error
		}
		results := make(chan outcome, 16)
		start := make(chan struct{})
		for range 16 {
			go func() {
				<-start
				request := input
				if !sameClient {
					request.ClientID = uuid.NewString()
				}
				detail, err := svc.Create(ctx, service.TicketActor{UserID: owner}, request)
				if err != nil {
					results <- outcome{err: err}
					return
				}
				results <- outcome{id: detail.Ticket.ID}
			}()
		}
		close(start)
		ids := map[int64]bool{}
		for range 16 {
			result := <-results
			require.NoError(t, result.err)
			ids[result.id] = true
		}
		var tickets, messages int
		require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM support_tickets WHERE user_id = $1", owner).Scan(&tickets))
		if sameClient {
			require.Len(t, ids, 1)
			require.Equal(t, 1, tickets)
		} else {
			require.Len(t, ids, 16)
			require.Equal(t, 16, tickets)
		}
		for id := range ids {
			require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM support_ticket_messages WHERE ticket_id = $1", id).Scan(&messages))
			require.Equal(t, 1, messages)
		}
	}
	// A replay after closing still resolves to the original conversation.
	first := ticketPGCreate(t, svc, ctx, 3)
	_, err := svc.Update(ctx, service.TicketActor{UserID: 3}, first.Ticket.ID, service.UpdateTicketInput{Status: ticketPGString("closed")})
	require.NoError(t, err)
	input := ticketPGInput()
	created, err := svc.Create(ctx, service.TicketActor{UserID: 3}, input)
	require.NoError(t, err)
	_, err = svc.Update(ctx, service.TicketActor{UserID: 3}, first.Ticket.ID, service.UpdateTicketInput{Status: ticketPGString("open")})
	require.NoError(t, err)
	closed, err := svc.Create(ctx, service.TicketActor{UserID: 3}, input)
	require.NoError(t, err)
	require.Equal(t, created.Ticket.ID, closed.Ticket.ID)
	changed := input
	changed.Contact = "different-contact"
	_, err = svc.Create(ctx, service.TicketActor{UserID: 3}, changed)
	require.ErrorIs(t, err, service.ErrTicketClientConflict)
	require.NotEqual(t, first.Ticket.ID, created.Ticket.ID)
}

func TestTicketPostgresReadCursorAndSharedAdminUnread(t *testing.T) {
	_, svc, ctx := ticketPostgres(t)
	owner := service.TicketActor{UserID: 1}
	admin := service.TicketActor{UserID: 99, Admin: true}
	otherAdmin := service.TicketActor{UserID: 100, Admin: true}
	created := ticketPGCreate(t, svc, ctx, 1)
	id := created.Ticket.ID
	_, err := svc.Detail(ctx, admin, id, 0)
	require.NoError(t, err)
	require.NoError(t, svc.MarkRead(ctx, admin, id, created.Ticket.LastMessageID))
	team, err := svc.Detail(ctx, otherAdmin, id, 0)
	require.NoError(t, err)
	require.Zero(t, team.Ticket.UnreadCount)
	_, err = svc.Reply(ctx, admin, id, service.ReplyTicketInput{Content: "first reply", ClientID: uuid.NewString()})
	require.NoError(t, err)
	seen, err := svc.Detail(ctx, owner, id, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), seen.Ticket.UnreadCount)
	newer, err := svc.Reply(ctx, admin, id, service.ReplyTicketInput{Content: "concurrent arrival", ClientID: uuid.NewString()})
	require.NoError(t, err)
	require.ErrorIs(t, svc.MarkRead(ctx, owner, id, newer.Ticket.LastMessageID), service.ErrTicketReadCursor)
	require.NoError(t, svc.MarkRead(ctx, owner, id, seen.Ticket.LastMessageID))
	latest, err := svc.Detail(ctx, owner, id, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), latest.Ticket.UnreadCount)
	require.NoError(t, svc.MarkRead(ctx, owner, id, latest.Ticket.LastMessageID))
	require.NoError(t, svc.MarkRead(ctx, owner, id, seen.Ticket.LastMessageID))
	latest, err = svc.Detail(ctx, owner, id, 0)
	require.NoError(t, err)
	require.Zero(t, latest.Ticket.UnreadCount)
	foreign := ticketPGCreate(t, svc, ctx, 2)
	require.ErrorIs(t, svc.MarkRead(ctx, owner, id, foreign.Ticket.LastMessageID), service.ErrTicketReadCursor)
	_, err = svc.Detail(ctx, service.TicketActor{UserID: 2}, id, 0)
	require.ErrorIs(t, err, service.ErrTicketNotFound)
	_, err = svc.Detail(ctx, owner, id, foreign.Ticket.LastMessageID)
	require.Error(t, err)
}

func TestTicketPostgresReplyLocksIdempotencyAndRollback(t *testing.T) {
	db, svc, ctx := ticketPostgres(t)
	owner := service.TicketActor{UserID: 1}
	admin := service.TicketActor{UserID: 99, Admin: true}
	id := ticketPGCreate(t, svc, ctx, 1).Ticket.ID
	_, err := svc.Update(ctx, admin, id, service.UpdateTicketInput{Status: ticketPGString("resolved")})
	require.NoError(t, err)
	input := service.ReplyTicketInput{Content: "reopen with reply", ClientID: uuid.NewString()}
	start := make(chan struct{})
	results := make(chan error, 12)
	for range 12 {
		go func() { <-start; _, err := svc.Reply(ctx, owner, id, input); results <- err }()
	}
	close(start)
	for range 12 {
		require.NoError(t, <-results)
	}
	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM support_ticket_messages WHERE ticket_id=$1 AND client_id=$2", id, input.ClientID).Scan(&count))
	require.Equal(t, 1, count)
	detail, err := svc.Detail(ctx, owner, id, 0)
	require.NoError(t, err)
	require.Equal(t, "open", detail.Ticket.Status)
	// Hold a close transaction ahead of a reply; the reply must inspect the
	// locked current state rather than the state read before waiting.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	_, err = tx.ExecContext(ctx, "UPDATE support_tickets SET status='closed' WHERE id=$1", id)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		_, err := svc.Reply(ctx, owner, id, service.ReplyTicketInput{Content: "must not appear", ClientID: uuid.NewString()})
		done <- err
	}()
	require.NoError(t, tx.Commit())
	require.ErrorIs(t, <-done, service.ErrTicketClosed)
	replay, err := svc.Reply(ctx, owner, id, input)
	require.NoError(t, err)
	require.Equal(t, "closed", replay.Ticket.Status)
	changed := input
	changed.Content = "changed"
	_, err = svc.Reply(ctx, owner, id, changed)
	require.ErrorIs(t, err, service.ErrTicketClientConflict)
	// A successful event insert followed by a failed reply must roll back both.
	_, err = svc.Update(ctx, owner, id, service.UpdateTicketInput{Status: ticketPGString("open")})
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM support_ticket_messages WHERE ticket_id=$1", id).Scan(&count))
	repo := &ticketRepository{db: db}
	err = repo.Mutate(ctx, owner, id, uuid.NewString(), "hash", func(ticket *service.Ticket) (*service.TicketChange, error) {
		ticket.Status = "resolved"
		return &service.TicketChange{Ticket: ticket, Messages: []service.TicketMessage{
			{Kind: "event", AuthorRole: "system", Content: "event"},
			{Kind: "reply", AuthorRole: "user", Content: ""},
		}}, nil
	})
	require.Error(t, err)
	var after int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM support_ticket_messages WHERE ticket_id=$1", id).Scan(&after))
	require.Equal(t, count, after)
	detail, err = svc.Detail(ctx, owner, id, 0)
	require.NoError(t, err)
	require.Equal(t, "open", detail.Ticket.Status)
}

func TestTicketPostgresSearchAssignmentPrivacyAndLiveBalance(t *testing.T) {
	db, svc, ctx := ticketPostgres(t)
	owner := service.TicketActor{UserID: 1}
	admin := service.TicketActor{UserID: 99, Admin: true}
	first := ticketPGCreate(t, svc, ctx, 1)
	ticketPGCreate(t, svc, ctx, 2)
	input := ticketPGInput()
	input.Subject = "Invoice check"
	input.Category = "billing"
	_, err := svc.Create(ctx, service.TicketActor{UserID: 3}, input)
	require.NoError(t, err)
	for _, tc := range []struct {
		actor  service.TicketActor
		search string
		total  int64
	}{
		{owner, strconv.FormatInt(first.Ticket.ID, 10), 1}, {owner, "#" + strconv.FormatInt(first.Ticket.ID, 10), 1},
		{admin, "Invoice", 1}, {admin, "user2", 1}, {admin, "user3@example.test", 1},
		{owner, "user2@example.test", 0}, {owner, "Invoice", 0}, {admin, "%", 0}, {admin, "#99999999999999999999999", 0},
	} {
		_, total, err := svc.List(ctx, tc.actor, service.TicketFilter{PaginationParams: pagination.DefaultPagination(), Search: tc.search})
		require.NoError(t, err, tc.search)
		require.Equal(t, tc.total, total, tc.search)
	}
	_, err = svc.Update(ctx, admin, first.Ticket.ID, service.UpdateTicketInput{
		AssignedTo: ticketPGString("me"), Priority: ticketPGString("urgent"), Status: ticketPGString("in_progress")})
	require.NoError(t, err)
	items, total, err := svc.List(ctx, admin, service.TicketFilter{PaginationParams: pagination.DefaultPagination(),
		AssignedTo: "mine", Priority: "urgent", Status: "in_progress", Category: "api"})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, int64(99), *items[0].AssigneeID)
	require.Equal(t, "user1@example.test", items[0].UserEmail)
	_, total, err = svc.List(ctx, admin, service.TicketFilter{PaginationParams: pagination.DefaultPagination(), AssignedTo: "unassigned"})
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	page1, _, err := svc.List(ctx, admin, service.TicketFilter{PaginationParams: pagination.PaginationParams{Page: 1, PageSize: 1}})
	require.NoError(t, err)
	page2, _, err := svc.List(ctx, admin, service.TicketFilter{PaginationParams: pagination.PaginationParams{Page: 2, PageSize: 1}})
	require.NoError(t, err)
	require.NotEqual(t, page1[0].ID, page2[0].ID)
	for _, balance := range []float64{12.5, 9.25} {
		_, err = db.ExecContext(ctx, "UPDATE users SET balance=$2 WHERE id=$1", 1, balance)
		require.NoError(t, err)
		detail, err := svc.Detail(ctx, admin, first.Ticket.ID, 0)
		require.NoError(t, err)
		require.Equal(t, balance, detail.Requester.Balance)
		require.Equal(t, "user1", detail.Requester.Username)
		require.Equal(t, "contact-handle", detail.Ticket.Contact)
		private, err := svc.Detail(ctx, owner, first.Ticket.ID, 0)
		require.NoError(t, err)
		require.Nil(t, private.Requester)
		require.Empty(t, private.Ticket.UserEmail)
	}
	_, err = svc.Update(ctx, admin, first.Ticket.ID, service.UpdateTicketInput{
		AssignedTo: ticketPGString("unassigned"), Status: ticketPGString("closed")})
	require.NoError(t, err)
}

func TestTicketPostgresMessagePages(t *testing.T) {
	db, svc, ctx := ticketPostgres(t)
	owner := service.TicketActor{UserID: 1}
	id := ticketPGCreate(t, svc, ctx, 1).Ticket.ID
	_, err := db.ExecContext(ctx, `INSERT INTO support_ticket_messages (ticket_id,author_id,author_role,content,kind)
		SELECT $1,99,'admin','reply ' || n,'reply' FROM generate_series(1,120) n;`, id)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE support_tickets SET last_message_id=(SELECT MAX(id) FROM support_ticket_messages WHERE ticket_id=$1) WHERE id=$1`, id)
	require.NoError(t, err)
	seen := map[int64]bool{}
	var before int64
	for page, want := range []int{50, 50, 21} {
		detail, err := svc.Detail(ctx, owner, id, before)
		require.NoError(t, err)
		require.Len(t, detail.Messages, want)
		require.Equal(t, page < 2, detail.HasMore)
		for i, message := range detail.Messages {
			require.False(t, seen[message.ID])
			seen[message.ID] = true
			if i > 0 {
				require.Greater(t, message.ID, detail.Messages[i-1].ID)
			}
		}
		before = detail.Messages[0].ID
	}
	require.Len(t, seen, 121)
}

func TestTicketPostgresConcurrentCloseAndReply(t *testing.T) {
	_, svc, ctx := ticketPostgres(t)
	owner := service.TicketActor{UserID: 1}
	id := ticketPGCreate(t, svc, ctx, 1).Ticket.ID
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := svc.Update(ctx, owner, id, service.UpdateTicketInput{Status: ticketPGString("closed")})
		errs <- err
	}()
	go func() {
		defer wg.Done()
		_, err := svc.Reply(ctx, owner, id, service.ReplyTicketInput{Content: "race", ClientID: uuid.NewString()})
		errs <- err
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, service.ErrTicketClosed) {
			t.Fatal(fmt.Errorf("concurrent change: %w", err))
		}
	}
	detail, err := svc.Detail(ctx, owner, id, 0)
	require.NoError(t, err)
	require.Equal(t, "closed", detail.Ticket.Status)
}

func TestTicketPostgresOwnerAndAdminAuthorization(t *testing.T) {
	db, svc, ctx := ticketPostgres(t)
	created := ticketPGCreate(t, svc, ctx, 1)
	foreign := service.TicketActor{UserID: 2}
	id := created.Ticket.ID
	_, err := svc.Detail(ctx, foreign, id, 0)
	require.ErrorIs(t, err, service.ErrTicketNotFound)
	_, err = svc.Reply(ctx, foreign, id, service.ReplyTicketInput{Content: "unauthorized", ClientID: uuid.NewString()})
	require.ErrorIs(t, err, service.ErrTicketNotFound)
	_, err = svc.Update(ctx, foreign, id, service.UpdateTicketInput{Status: ticketPGString("closed")})
	require.ErrorIs(t, err, service.ErrTicketNotFound)
	require.ErrorIs(t, svc.MarkRead(ctx, foreign, id, created.Ticket.LastMessageID), service.ErrTicketNotFound)
	items, total, err := svc.List(ctx, foreign, service.TicketFilter{PaginationParams: pagination.DefaultPagination()})
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, items)
	stats, err := svc.Stats(ctx, foreign)
	require.NoError(t, err)
	require.Zero(t, stats.Total)
	_, err = svc.Detail(ctx, service.TicketActor{UserID: 2, Admin: true}, id, 0)
	require.Equal(t, 403, infraerrors.Code(err))
	_, err = svc.Update(ctx, service.TicketActor{UserID: 1}, id, service.UpdateTicketInput{AssignedTo: ticketPGString("me")})
	require.Equal(t, 400, infraerrors.Code(err))
	// Authorization re-checks the persisted role, not only the route identity.
	_, err = db.ExecContext(ctx, "UPDATE users SET role='user' WHERE id=99")
	require.NoError(t, err)
	_, err = svc.Update(ctx, service.TicketActor{UserID: 99, Admin: true}, id, service.UpdateTicketInput{AssignedTo: ticketPGString("me")})
	require.Equal(t, 403, infraerrors.Code(err))
	var messages, views int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM support_ticket_messages WHERE ticket_id=$1", id).Scan(&messages))
	require.Equal(t, 1, messages)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM support_ticket_views WHERE viewer_id=2").Scan(&views))
	require.Zero(t, views)
}

func TestTicketPostgresEmailOutboxRetryAndAudience(t *testing.T) {
	db, svc, ctx := ticketPostgres(t)
	admin := service.TicketActor{UserID: 99, Admin: true}
	id := ticketPGCreate(t, svc, ctx, 1).Ticket.ID

	var audience string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT audience FROM support_ticket_email_outbox WHERE ticket_id=$1`, id).Scan(&audience))
	require.Equal(t, "admin", audience)

	repo := NewTicketRepository(db)
	pendingCount, failedCount, err := repo.EmailOutboxStats(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), pendingCount)
	require.Zero(t, failedCount)

	// Transient failures keep the job and schedule a bounded retry.
	processed, err := repo.ProcessPendingEmail(ctx, func(context.Context, service.TicketEmailJob) error {
		return errors.New("smtp timeout")
	})
	require.NoError(t, err)
	require.True(t, processed)
	var attempts int
	var next time.Time
	var lastError string
	var failedAt sql.NullTime
	require.NoError(t, db.QueryRowContext(ctx, `SELECT attempts, next_attempt_at, last_error, failed_at
		FROM support_ticket_email_outbox WHERE ticket_id=$1`, id).Scan(&attempts, &next, &lastError, &failedAt))
	require.Equal(t, 1, attempts)
	require.True(t, next.After(time.Now()))
	require.Contains(t, lastError, "smtp timeout")
	require.False(t, failedAt.Valid)
	// Not due yet: nothing is claimed.
	processed, err = repo.ProcessPendingEmail(ctx, func(context.Context, service.TicketEmailJob) error { return nil })
	require.NoError(t, err)
	require.False(t, processed)

	// Make it due again; a successful delivery removes the job.
	_, err = db.ExecContext(ctx, `UPDATE support_ticket_email_outbox SET next_attempt_at = clock_timestamp() WHERE ticket_id=$1`, id)
	require.NoError(t, err)
	var delivered service.TicketEmailJob
	processed, err = repo.ProcessPendingEmail(ctx, func(_ context.Context, job service.TicketEmailJob) error {
		delivered = job
		return nil
	})
	require.NoError(t, err)
	require.True(t, processed)
	require.Equal(t, id, delivered.TicketID)
	require.Equal(t, "admin", delivered.Audience)
	pendingCount, failedCount, err = repo.EmailOutboxStats(ctx)
	require.NoError(t, err)
	require.Zero(t, pendingCount)
	require.Zero(t, failedCount)

	// Admin replies notify the owner, not the support inbox.
	_, err = svc.Reply(ctx, admin, id, service.ReplyTicketInput{Content: "support reply", ClientID: uuid.NewString()})
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT audience FROM support_ticket_email_outbox
		WHERE ticket_id=$1 AND audience='user'`, id).Scan(&audience))
	require.Equal(t, "user", audience)

	// Permanent failures are recorded once and never retried.
	processed, err = repo.ProcessPendingEmail(ctx, func(context.Context, service.TicketEmailJob) error {
		return servicePermanentTestError{}
	})
	require.NoError(t, err)
	require.True(t, processed)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT attempts, failed_at FROM support_ticket_email_outbox WHERE ticket_id=$1`, id).
		Scan(&attempts, &failedAt))
	require.Equal(t, 1, attempts)
	require.True(t, failedAt.Valid)
	pendingCount, failedCount, err = repo.EmailOutboxStats(ctx)
	require.NoError(t, err)
	require.Zero(t, pendingCount)
	require.Equal(t, int64(1), failedCount)
}

// servicePermanentTestError implements the same marker as the service's internal
// permanent error, which is exactly what IsTicketEmailPermanent checks.
type servicePermanentTestError struct{}

func (servicePermanentTestError) Error() string         { return "permanent" }
func (servicePermanentTestError) TicketEmailPermanent() {}

func TestTicketPostgresEmailConversationDoesNotIncludeLaterMessages(t *testing.T) {
	db, svc, ctx := ticketPostgres(t)
	owner := service.TicketActor{UserID: 1}
	admin := service.TicketActor{UserID: 99, Admin: true}
	id := ticketPGCreate(t, svc, ctx, 1).Ticket.ID
	replied, err := svc.Reply(ctx, admin, id, service.ReplyTicketInput{Content: "admin answer", ClientID: uuid.NewString()})
	require.NoError(t, err)
	adminMessageID := replied.Ticket.LastMessageID
	_, err = svc.Reply(ctx, owner, id, service.ReplyTicketInput{Content: "later user secret", ClientID: uuid.NewString()})
	require.NoError(t, err)
	var lastID int64
	require.NoError(t, db.QueryRowContext(ctx, "SELECT last_message_id FROM support_tickets WHERE id=$1", id).Scan(&lastID))
	require.Greater(t, lastID, adminMessageID)

	repo := NewTicketRepository(db)
	detail, err := repo.EmailConversation(ctx, id, adminMessageID)
	require.NoError(t, err)
	require.NotEmpty(t, detail.Messages)
	require.Equal(t, adminMessageID, detail.Messages[len(detail.Messages)-1].ID)
	for _, message := range detail.Messages {
		require.NotEqual(t, "later user secret", message.Content)
	}
	_, err = repo.EmailConversation(ctx, id, 999999)
	require.ErrorIs(t, err, service.ErrTicketNotFound)
}

func ticketPGString(value string) *string { return &value }
