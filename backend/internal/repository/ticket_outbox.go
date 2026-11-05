package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// queueTicketEmail is called inside the message transaction. The job therefore
// exists if and only if the reply committed.
func queueTicketEmail(ctx context.Context, tx *sql.Tx, ticketID, messageID int64, audience string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO support_ticket_email_outbox (ticket_id, message_id, audience)
		VALUES ($1, $2, $3) ON CONFLICT (message_id) DO NOTHING`, ticketID, messageID, audience)
	return err
}

// ProcessPendingEmail leases one due job with SKIP LOCKED, delivers it outside the
// lease transaction, then deletes it or schedules a bounded retry.
func (r *ticketRepository) ProcessPendingEmail(ctx context.Context, deliver func(context.Context, service.TicketEmailJob) error) (bool, error) {
	if deliver == nil {
		return false, fmt.Errorf("missing ticket email sender")
	}
	var job service.TicketEmailJob
	found := false
	err := r.transaction(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `WITH claim AS (
			SELECT id FROM support_ticket_email_outbox
			WHERE failed_at IS NULL AND next_attempt_at <= clock_timestamp()
				AND (locked_until IS NULL OR locked_until < clock_timestamp())
			ORDER BY next_attempt_at, id
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE support_ticket_email_outbox outbox
		SET locked_until = clock_timestamp() + INTERVAL '60 seconds'
		FROM claim WHERE outbox.id = claim.id
		RETURNING outbox.id, outbox.ticket_id, outbox.message_id, outbox.audience, outbox.attempts`).
			Scan(&job.ID, &job.TicketID, &job.MessageID, &job.Audience, &job.Attempts)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil || !found {
		return false, err
	}

	deliveryErr := deliver(ctx, job)

	return true, r.finishTicketEmail(ctx, job, deliveryErr)
}

func (r *ticketRepository) finishTicketEmail(ctx context.Context, job service.TicketEmailJob, deliveryErr error) error {
	return r.transaction(ctx, func(tx *sql.Tx) error {
		if deliveryErr == nil {
			_, err := tx.ExecContext(ctx, "DELETE FROM support_ticket_email_outbox WHERE id = $1", job.ID)
			return err
		}
		permanentFailure := service.IsTicketEmailPermanent(deliveryErr)
		attempts := job.Attempts + 1
		message := truncateTicketEmailError(deliveryErr.Error())
		if permanentFailure || attempts >= 10 {
			_, err := tx.ExecContext(ctx, `UPDATE support_ticket_email_outbox
				SET attempts = $2, locked_until = NULL, last_error = $3, failed_at = clock_timestamp()
				WHERE id = $1`, job.ID, attempts, message)
			return err
		}
		delay := 30
		for i := 0; i < attempts && i < 7; i++ {
			delay *= 2
		}
		if delay > 3600 {
			delay = 3600
		}
		_, err := tx.ExecContext(ctx, `UPDATE support_ticket_email_outbox
			SET attempts = $2, locked_until = NULL, last_error = $3,
				next_attempt_at = clock_timestamp() + ($4 * INTERVAL '1 second')
			WHERE id = $1`, job.ID, attempts, message, delay)
		return err
	})
}

func truncateTicketEmailError(message string) string {
	message = strings.ReplaceAll(strings.ReplaceAll(message, "\r", " "), "\n", " ")
	runes := []rune(message)
	if len(runes) > 400 {
		runes = runes[:400]
	}
	return string(runes)
}

func (r *ticketRepository) EmailOutboxStats(ctx context.Context) (pending, failed int64, err error) {
	err = r.db.QueryRowContext(ctx, `SELECT
		COUNT(*) FILTER (WHERE failed_at IS NULL),
		COUNT(*) FILTER (WHERE failed_at IS NOT NULL)
		FROM support_ticket_email_outbox`).Scan(&pending, &failed)
	return pending, failed, err
}
