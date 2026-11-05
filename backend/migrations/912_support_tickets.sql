-- In-app support tickets.
--
-- Unlike the reference implementation this schema deliberately does NOT enforce
-- one ticket per user per calendar day. The upstream project's daily creation
-- quota is a business policy of that deployment; here a user may open as many
-- conversations as they need, subject only to the panel-wide rate limiting that
-- already protects every authenticated route.

CREATE TABLE IF NOT EXISTS support_tickets (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    subject TEXT NOT NULL CHECK (char_length(subject) BETWEEN 1 AND 160),
    contact TEXT NOT NULL DEFAULT '' CHECK (char_length(contact) <= 200),
    category TEXT NOT NULL CHECK (category IN ('billing', 'api', 'account', 'other')),
    priority TEXT NOT NULL CHECK (priority IN ('normal', 'high', 'urgent')),
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'in_progress', 'waiting_user', 'resolved', 'closed')),
    assignee_id BIGINT REFERENCES users(id),
    client_id UUID NOT NULL,
    request_hash TEXT NOT NULL,
    user_read_id BIGINT NOT NULL DEFAULT 0,
    admin_read_id BIGINT NOT NULL DEFAULT 0,
    last_message_id BIGINT NOT NULL DEFAULT 0,
    last_message_preview TEXT NOT NULL DEFAULT '',
    last_message_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    -- Retries of the same create request must resolve to the same conversation.
    UNIQUE (user_id, client_id)
);

CREATE TABLE IF NOT EXISTS support_ticket_messages (
    id BIGSERIAL PRIMARY KEY,
    ticket_id BIGINT NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
    author_id BIGINT REFERENCES users(id),
    author_role TEXT NOT NULL CHECK (author_role IN ('user', 'admin', 'system')),
    content TEXT NOT NULL CHECK (char_length(content) BETWEEN 1 AND 10000),
    kind TEXT NOT NULL CHECK (kind IN ('reply', 'event')),
    event_type TEXT NOT NULL DEFAULT '',
    event_data JSONB NOT NULL DEFAULT '{}',
    client_id UUID,
    request_hash TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CHECK (jsonb_typeof(event_data) = 'object')
);

-- Delivery watermarks are distinct from read cursors. A GET never marks messages
-- read; MarkRead additionally verifies the cursor was part of a fetched page.
CREATE TABLE IF NOT EXISTS support_ticket_views (
    ticket_id BIGINT NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
    viewer_id BIGINT NOT NULL REFERENCES users(id),
    admin_view BOOLEAN NOT NULL,
    last_seen_id BIGINT NOT NULL,
    PRIMARY KEY (ticket_id, viewer_id, admin_view)
);

-- Durable transactional outbox. Rows are inserted in the same transaction as the
-- ticket message, so a committed reply always has a deliverable job. Delivery is
-- at-least-once: a crash after SMTP accepts the mail and before the row is deleted
-- can repeat one notification, never lose it.
CREATE TABLE IF NOT EXISTS support_ticket_email_outbox (
    id BIGSERIAL PRIMARY KEY,
    ticket_id BIGINT NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
    message_id BIGINT NOT NULL REFERENCES support_ticket_messages(id) ON DELETE CASCADE,
    audience TEXT NOT NULL CHECK (audience IN ('user', 'admin')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    locked_until TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    failed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (message_id)
);

CREATE INDEX IF NOT EXISTS support_tickets_user_updated_idx ON support_tickets (user_id, updated_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS support_tickets_updated_idx ON support_tickets (updated_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS support_tickets_status_updated_idx ON support_tickets (status, updated_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS support_tickets_assignee_updated_idx ON support_tickets (assignee_id, updated_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS support_ticket_messages_page_idx ON support_ticket_messages (ticket_id, id DESC);
CREATE INDEX IF NOT EXISTS support_ticket_messages_unread_idx ON support_ticket_messages (ticket_id, author_role, id) WHERE kind = 'reply';
CREATE UNIQUE INDEX IF NOT EXISTS support_ticket_messages_client_idx ON support_ticket_messages (ticket_id, author_id, author_role, client_id) WHERE client_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS support_ticket_email_outbox_pending_idx ON support_ticket_email_outbox (next_attempt_at, id) WHERE failed_at IS NULL;

COMMENT ON TABLE support_tickets IS 'User support conversations. Deleted explicitly with their messages/views/outbox rows; see docs/uninstall note in REPORT.md.';
COMMENT ON TABLE support_ticket_email_outbox IS 'At-least-once ticket notification queue; safe to truncate when re-enabling notifications.';
