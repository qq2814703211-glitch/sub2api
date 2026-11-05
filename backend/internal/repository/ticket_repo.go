package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

const (
	// Today's usage for the requester card: failures leave the card marked as
	// unavailable instead of inventing zeros.
	ticketRequesterUsageSQL = `SELECT COALESCE(SUM(input_tokens::bigint + output_tokens + cache_creation_tokens + cache_read_tokens), 0),
		COALESCE(SUM(actual_cost), 0)
		FROM usage_logs
		WHERE user_id = $1 AND created_at >= $2 AND created_at < $3`
	ticketRequesterRechargeSQL = `SELECT COALESCE(SUM(amount), 0)
		FROM payment_orders
		WHERE user_id = $1 AND order_type = 'balance' AND status IN ('PAID', 'RECHARGING', 'COMPLETED')
			AND paid_at >= $2 AND paid_at < $3`
)

var ticketStatsLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

type ticketRepository struct{ db *sql.DB }

// NewTicketRepository creates the PostgreSQL-backed ticket repository.
func NewTicketRepository(db *sql.DB) service.TicketRepository { return &ticketRepository{db: db} }

type ticketQuerier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type ticketScanner interface{ Scan(...any) error }

// ticketScope limits every user query to the caller's own conversations.
func ticketScope(actor service.TicketActor, args *[]any) string {
	if actor.Admin {
		return "TRUE"
	}
	*args = append(*args, actor.UserID)
	return fmt.Sprintf("t.user_id = $%d", len(*args))
}

// ticketUnread counts replies from the other side that are newer than the
// viewer's read watermark. Admin unread is team-wide, matching the shared inbox.
func ticketUnread(actor service.TicketActor) string {
	role, cursor := "admin", "user_read_id"
	if actor.Admin {
		role, cursor = "user", "admin_read_id"
	}
	return "m.ticket_id = t.id AND m.kind = 'reply' AND m.author_role = '" + role + "' AND m.id > t." + cursor
}

func ticketSelect(actor service.TicketActor) string {
	email, name := "''", "''"
	if actor.Admin {
		email = "COALESCE(u.email, '')"
		name = "COALESCE(u.username, '')"
	}
	return `SELECT t.id, t.user_id, t.subject, t.contact, t.category, t.priority, t.status,
		t.assignee_id, COALESCE(a.username, ''), ` + name + `, ` + email + `,
		t.created_at, t.updated_at, t.last_message_at, t.last_message_preview,
		(SELECT COUNT(*) FROM support_ticket_messages m WHERE ` + ticketUnread(actor) + `), t.last_message_id
		FROM support_tickets t LEFT JOIN users u ON u.id = t.user_id
		LEFT JOIN users a ON a.id = t.assignee_id `
}

func scanTicket(row ticketScanner) (*service.Ticket, error) {
	ticket := &service.Ticket{}
	err := row.Scan(&ticket.ID, &ticket.UserID, &ticket.Subject, &ticket.Contact, &ticket.Category, &ticket.Priority,
		&ticket.Status, &ticket.AssigneeID, &ticket.AssigneeName, &ticket.UserName, &ticket.UserEmail,
		&ticket.CreatedAt, &ticket.UpdatedAt, &ticket.LastMessageAt, &ticket.LastMessagePreview,
		&ticket.UnreadCount, &ticket.LastMessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrTicketNotFound
	}
	return ticket, err
}

func (r *ticketRepository) List(ctx context.Context, actor service.TicketActor, filter service.TicketFilter) ([]service.Ticket, int64, error) {
	args := []any{}
	where := []string{ticketScope(actor, &args)}
	for _, f := range []struct{ column, value string }{{"status", filter.Status}, {"priority", filter.Priority}, {"category", filter.Category}} {
		if f.column == "status" && f.value == "active" {
			where = append(where, "t.status <> 'closed'")
			continue
		}
		if f.value != "" {
			args = append(args, f.value)
			where = append(where, fmt.Sprintf("t.%s = $%d", f.column, len(args)))
		}
	}
	if filter.Search != "" {
		search := strings.TrimSpace(filter.Search)
		digits := strings.TrimPrefix(search, "#")
		isID := digits != "" && strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }) == -1
		if isID {
			id, err := strconv.ParseInt(digits, 10, 64)
			if err != nil {
				where = append(where, "FALSE")
			} else {
				args = append(args, id)
				where = append(where, fmt.Sprintf("t.id = $%d", len(args)))
			}
		} else {
			// LIKE metacharacters are escaped; no user input is interpolated into SQL.
			escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(search)
			args = append(args, "%"+escaped+"%")
			match := fmt.Sprintf("t.subject ILIKE $%d", len(args))
			if actor.Admin {
				match = fmt.Sprintf("(t.subject ILIKE $%[1]d OR u.username ILIKE $%[1]d OR u.email ILIKE $%[1]d)", len(args))
			}
			where = append(where, match)
		}
	}
	if actor.Admin && filter.AssignedTo == "mine" {
		args = append(args, actor.UserID)
		where = append(where, fmt.Sprintf("t.assignee_id = $%d", len(args)))
	} else if actor.Admin && filter.AssignedTo == "unassigned" {
		where = append(where, "t.assignee_id IS NULL")
	}
	clause := " WHERE " + strings.Join(where, " AND ")
	var total int64
	countFrom := "SELECT COUNT(*) FROM support_tickets t"
	if actor.Admin {
		countFrom += " LEFT JOIN users u ON u.id = t.user_id"
	}
	if err := r.db.QueryRowContext(ctx, countFrom+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, filter.Limit(), filter.Offset())
	rows, err := r.db.QueryContext(ctx, ticketSelect(actor)+clause+
		fmt.Sprintf(" ORDER BY t.updated_at DESC, t.id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.Ticket, 0)
	for rows.Next() {
		ticket, err := scanTicket(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *ticket)
	}
	return items, total, rows.Err()
}

func (r *ticketRepository) Stats(ctx context.Context, actor service.TicketActor) (*service.TicketStats, error) {
	args := []any{}
	where := ticketScope(actor, &args)
	stats := &service.TicketStats{}
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*),
		COUNT(*) FILTER (WHERE t.status = 'open'), COUNT(*) FILTER (WHERE t.status = 'in_progress'),
		COUNT(*) FILTER (WHERE t.status = 'waiting_user'), COUNT(*) FILTER (WHERE t.status = 'resolved'),
		COUNT(*) FILTER (WHERE t.status = 'closed'),
		COUNT(*) FILTER (WHERE EXISTS (SELECT 1 FROM support_ticket_messages m WHERE `+ticketUnread(actor)+`))
		FROM support_tickets t WHERE `+where, args...).Scan(&stats.Total, &stats.Open, &stats.InProgress,
		&stats.WaitingUser, &stats.Resolved, &stats.Closed, &stats.Unread)
	if err != nil {
		return nil, err
	}
	return stats, nil
}

func getTicket(ctx context.Context, q ticketQuerier, actor service.TicketActor, id int64, lock bool) (*service.Ticket, error) {
	args := []any{id}
	where := ticketScope(actor, &args)
	query := ticketSelect(actor) + " WHERE t.id = $1 AND " + where
	if lock {
		query += " FOR UPDATE OF t"
	}
	return scanTicket(q.QueryRowContext(ctx, query, args...))
}

func (r *ticketRepository) Detail(ctx context.Context, actor service.TicketActor, id, before int64) (*service.TicketDetail, error) {
	// Freeze the watermark before the page query so messages that arrive while
	// rendering this response are not accidentally acknowledged by MarkRead.
	ticket, err := getTicket(ctx, r.db, actor, id, false)
	if err != nil {
		return nil, err
	}
	if before > 0 {
		var valid bool
		err = r.db.QueryRowContext(ctx,
			"SELECT EXISTS (SELECT 1 FROM support_ticket_messages WHERE ticket_id = $1 AND id = $2 AND id <= $3)",
			id, before, ticket.LastMessageID).Scan(&valid)
		if err != nil {
			return nil, err
		}
		if !valid {
			return nil, infraerrors.BadRequest("TICKET_PAGE_CURSOR_INVALID", "before_id must belong to this ticket")
		}
	}
	rows, err := r.db.QueryContext(ctx, `SELECT m.id, m.ticket_id, m.author_id, m.author_role,
		COALESCE(u.username, ''), m.content, m.kind, m.event_type, m.event_data, m.created_at
		FROM support_ticket_messages m LEFT JOIN users u ON u.id = m.author_id
		WHERE m.ticket_id = $1 AND m.id <= $2 AND ($3::bigint = 0 OR m.id < $3)
		ORDER BY m.id DESC LIMIT 51`, id, ticket.LastMessageID, before)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	messages := make([]service.TicketMessage, 0, service.TicketMessagePageSize+1)
	for rows.Next() {
		var message service.TicketMessage
		var data []byte
		if err := rows.Scan(&message.ID, &message.TicketID, &message.AuthorID, &message.AuthorRole,
			&message.AuthorName, &message.Content, &message.Kind, &message.EventType, &data, &message.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &message.EventData); err != nil {
			return nil, fmt.Errorf("decode ticket event: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	hasMore := len(messages) > service.TicketMessagePageSize
	if hasMore {
		messages = messages[:service.TicketMessagePageSize]
	}
	slices.Reverse(messages)
	detail := &service.TicketDetail{Ticket: ticket, Messages: messages, HasMore: hasMore}
	if actor.Admin {
		requester := &service.TicketRequester{}
		err := r.db.QueryRowContext(ctx, `SELECT id, COALESCE(username, ''), email, balance, status, created_at
			FROM users WHERE id = $1`, ticket.UserID).
			Scan(&requester.ID, &requester.Username, &requester.Email, &requester.Balance, &requester.Status, &requester.CreatedAt)
		if err != nil {
			return nil, err
		}
		detail.Requester = requester
	}
	if len(messages) > 0 {
		_, err = r.db.ExecContext(ctx, `INSERT INTO support_ticket_views (ticket_id, viewer_id, admin_view, last_seen_id)
			VALUES ($1, $2, $3, $4) ON CONFLICT (ticket_id, viewer_id, admin_view)
			DO UPDATE SET last_seen_id = GREATEST(support_ticket_views.last_seen_id, EXCLUDED.last_seen_id)`,
			id, actor.UserID, actor.Admin, messages[len(messages)-1].ID)
		if err != nil {
			return nil, err
		}
	}
	r.fillRequesterStats(ctx, detail.Requester)
	return detail, nil
}

func (r *ticketRepository) fillRequesterStats(ctx context.Context, requester *service.TicketRequester) {
	if r == nil || r.db == nil || requester == nil || requester.ID <= 0 {
		return
	}
	now := time.Now().In(ticketStatsLocation)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, ticketStatsLocation)
	usageCtx, cancelUsage := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancelUsage()
	var tokens int64
	var cost float64
	if err := r.db.QueryRowContext(usageCtx, ticketRequesterUsageSQL, requester.ID, today, now).Scan(&tokens, &cost); err == nil {
		requester.TodayTokens, requester.TodayCost = tokens, cost
		requester.UsageStatsAvailable = true
	}
	rechargeCtx, cancelRecharge := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancelRecharge()
	var recharged float64
	if err := r.db.QueryRowContext(rechargeCtx, ticketRequesterRechargeSQL, requester.ID, today.AddDate(0, 0, -13), now).Scan(&recharged); err == nil {
		requester.Recharged14d = recharged
		requester.RechargeStatsAvailable = true
	}
}

// EmailConversation returns the ticket plus every message up to throughMessageID.
// It is intentionally not scoped to an actor: the outbox worker acts on behalf of
// the system. The caller must verify the message author/audience.
func (r *ticketRepository) EmailConversation(ctx context.Context, id, throughMessageID int64) (*service.TicketDetail, error) {
	ticket, err := getTicket(ctx, r.db, service.TicketActor{Admin: true}, id, false)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT m.id, m.ticket_id, m.author_id, m.author_role,
		COALESCE(u.username, ''), m.content, m.kind, m.event_type, m.event_data, m.created_at
		FROM support_ticket_messages m LEFT JOIN users u ON u.id = m.author_id
		WHERE m.ticket_id = $1 AND m.id <= $2 ORDER BY m.id ASC`, id, throughMessageID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	detail := &service.TicketDetail{Ticket: ticket}
	for rows.Next() {
		var message service.TicketMessage
		var data []byte
		if err := rows.Scan(&message.ID, &message.TicketID, &message.AuthorID, &message.AuthorRole,
			&message.AuthorName, &message.Content, &message.Kind, &message.EventType, &data, &message.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &message.EventData); err != nil {
			return nil, fmt.Errorf("decode ticket event: %w", err)
		}
		detail.Messages = append(detail.Messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, message := range detail.Messages {
		if message.ID == throughMessageID {
			if message.Kind != "reply" || (message.AuthorRole != "user" && message.AuthorRole != "admin") {
				return nil, service.ErrTicketNotFound
			}
			return detail, nil
		}
	}
	return nil, service.ErrTicketNotFound
}

func (r *ticketRepository) transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	// Rollback also covers a canceled request or callback panic; after Commit it is a no-op.
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ticketRepository) Create(ctx context.Context, actor service.TicketActor, in service.CreateTicketInput, hash string) (int64, error) {
	var id int64
	err := r.transaction(ctx, func(tx *sql.Tx) error {
		// A retry with the same client_id returns the committed ticket. ON CONFLICT
		// serializes concurrent duplicates on the unique index instead of racing.
		err := tx.QueryRowContext(ctx, `INSERT INTO support_tickets
			(user_id, subject, contact, category, priority, client_id, request_hash)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (user_id, client_id) DO NOTHING
			RETURNING id`, actor.UserID, in.Subject, in.Contact, in.Category, in.Priority, in.ClientID, hash).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			var originalHash string
			if err := tx.QueryRowContext(ctx,
				"SELECT id, request_hash FROM support_tickets WHERE user_id = $1 AND client_id = $2",
				actor.UserID, in.ClientID).Scan(&id, &originalHash); err != nil {
				return err
			}
			if originalHash != hash {
				return service.ErrTicketClientConflict
			}
			return nil
		}
		if err != nil {
			return err
		}
		ticket := &service.Ticket{ID: id, Status: "open", Priority: in.Priority}
		message := &service.TicketMessage{AuthorID: &actor.UserID, AuthorRole: "user", Kind: "reply", Content: in.Content}
		if err := ticketInsertMessage(ctx, tx, ticket, message, "", ""); err != nil {
			return err
		}
		return ticketSave(ctx, tx, ticket)
	})
	return id, err
}

// CreateForUser starts a conversation on behalf of a user. Unlike the reference
// implementation there is no daily uniqueness constraint, so every proactive
// contact creates its own ticket instead of appending to "today's" thread.
func (r *ticketRepository) CreateForUser(ctx context.Context, actor service.TicketActor, targetUserID int64, subject, content string) (int64, error) {
	var id int64
	err := r.transaction(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `INSERT INTO support_tickets
			(user_id, subject, contact, category, priority, status, client_id, request_hash)
			VALUES ($1, $2, '', 'other', 'normal', 'waiting_user', $3, '') RETURNING id`,
			targetUserID, subject, uuid.New().String()).Scan(&id)
		if err != nil {
			return err
		}
		ticket := &service.Ticket{ID: id, Status: "waiting_user", Priority: "normal"}
		message := &service.TicketMessage{AuthorID: &actor.UserID, AuthorRole: "admin", Kind: "reply", Content: content}
		if err := ticketInsertMessage(ctx, tx, ticket, message, "", ""); err != nil {
			return err
		}
		return ticketSave(ctx, tx, ticket)
	})
	return id, err
}

func (r *ticketRepository) Mutate(ctx context.Context, actor service.TicketActor, id int64, clientID, hash string, change func(*service.Ticket) (*service.TicketChange, error)) error {
	return r.transaction(ctx, func(tx *sql.Tx) error {
		ticket, err := getTicket(ctx, tx, actor, id, true)
		if err != nil {
			return err
		}
		if clientID != "" {
			// Checked after the row lock: a concurrent duplicate commit is visible
			// here, so retries never append a second copy of the same reply.
			var originalHash string
			err = tx.QueryRowContext(ctx, `SELECT request_hash FROM support_ticket_messages
				WHERE ticket_id = $1 AND author_id = $2 AND author_role = $3 AND client_id = $4`,
				id, actor.UserID, actor.Role(), clientID).Scan(&originalHash)
			if err == nil {
				if originalHash != hash {
					return service.ErrTicketClientConflict
				}
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		plan, err := change(ticket)
		if err != nil {
			return err
		}
		if len(plan.Messages) == 0 {
			return nil
		}
		for i := range plan.Messages {
			key, fingerprint := "", ""
			if plan.Messages[i].Kind == "reply" {
				key, fingerprint = clientID, hash
			}
			if err := ticketInsertMessage(ctx, tx, plan.Ticket, &plan.Messages[i], key, fingerprint); err != nil {
				return err
			}
		}
		return ticketSave(ctx, tx, plan.Ticket)
	})
}

func ticketInsertMessage(ctx context.Context, tx *sql.Tx, ticket *service.Ticket, message *service.TicketMessage, clientID, hash string) error {
	data := []byte("{}")
	if message.EventData != nil {
		var err error
		data, err = json.Marshal(message.EventData)
		if err != nil {
			return err
		}
	}
	err := tx.QueryRowContext(ctx, `INSERT INTO support_ticket_messages
		(ticket_id, author_id, author_role, content, kind, event_type, event_data, client_id, request_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, NULLIF($8, '')::uuid, $9) RETURNING id, created_at`,
		ticket.ID, message.AuthorID, message.AuthorRole, message.Content, message.Kind, message.EventType, string(data), clientID, hash).
		Scan(&message.ID, &message.CreatedAt)
	if err != nil {
		return err
	}
	if message.Kind == "reply" {
		audience := "admin"
		if message.AuthorRole == "admin" {
			audience = "user"
		}
		if err := queueTicketEmail(ctx, tx, ticket.ID, message.ID, audience); err != nil {
			return err
		}
	}
	ticket.LastMessageID, ticket.LastMessageAt = message.ID, message.CreatedAt
	preview := []rune(message.Content)
	if len(preview) > 200 {
		preview = preview[:200]
	}
	ticket.LastMessagePreview = string(preview)
	return nil
}

func ticketSave(ctx context.Context, tx *sql.Tx, ticket *service.Ticket) error {
	_, err := tx.ExecContext(ctx, `UPDATE support_tickets SET status = $2, priority = $3, assignee_id = $4,
		last_message_id = $5, last_message_preview = $6, last_message_at = $7, updated_at = clock_timestamp()
		WHERE id = $1`, ticket.ID, ticket.Status, ticket.Priority, ticket.AssigneeID,
		ticket.LastMessageID, ticket.LastMessagePreview, ticket.LastMessageAt)
	return err
}

func (r *ticketRepository) MarkRead(ctx context.Context, actor service.TicketActor, id, lastID int64) error {
	return r.transaction(ctx, func(tx *sql.Tx) error {
		args := []any{id}
		where := ticketScope(actor, &args)
		var ticketID int64
		err := tx.QueryRowContext(ctx, "SELECT t.id FROM support_tickets t WHERE t.id = $1 AND "+where+" FOR UPDATE", args...).Scan(&ticketID)
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrTicketNotFound
		}
		if err != nil {
			return err
		}
		// The cursor must have been delivered by a Detail response for this viewer.
		var seen bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM support_ticket_views v
			JOIN support_ticket_messages m ON m.ticket_id = v.ticket_id AND m.id = $4
			WHERE v.ticket_id = $1 AND v.viewer_id = $2 AND v.admin_view = $3 AND v.last_seen_id >= $4)`,
			id, actor.UserID, actor.Admin, lastID).Scan(&seen)
		if err != nil {
			return err
		}
		if !seen {
			return service.ErrTicketReadCursor
		}
		column := "user_read_id"
		if actor.Admin {
			column = "admin_read_id"
		}
		_, err = tx.ExecContext(ctx, "UPDATE support_tickets SET "+column+" = GREATEST("+column+", $2) WHERE id = $1", id, lastID)
		return err
	})
}
