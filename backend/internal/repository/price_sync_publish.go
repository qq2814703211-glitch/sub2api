package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cleanup"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// One channel for the committed head. The payload is the revision id and is
// only a hint; followers always re-read the row, so a truncated, duplicated or
// reordered notification cannot change a price by itself.
const priceSyncHeadChannel = "sub2api_price_sync_head"

type priceSyncStore struct {
	db  *sql.DB
	dsn string
}

// Followers normally read only the indexed head identifier, not the full ledger.
func (s *priceSyncStore) PriceSyncHead(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT revision FROM price_sync_revisions ORDER BY seq DESC LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func NewPriceSyncPublishStore(db *sql.DB) service.PriceSyncPublishStore {
	return &priceSyncStore{db: db}
}

// ProvidePriceSyncPublishStore adds the LISTEN connection used to wake
// followers the moment a revision commits. Without a DSN the store still
// works; followers then fall back to the bounded backstop check.
func ProvidePriceSyncPublishStore(db *sql.DB, cfg *config.Config) service.PriceSyncPublishStore {
	var dsn string
	if cfg != nil {
		dsn = cfg.Database.DSNWithTimezone(cfg.Timezone)
	}
	return &priceSyncStore{db: db, dsn: dsn}
}

// Hold the lock across reading the head, preparing the local projection and appending.
// Never hold it during collection. A dead publisher releases its transaction lock.
func (s *priceSyncStore) WithPriceSyncHead(ctx context.Context, f func([]byte) (string, []byte, error)) (retErr error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer cleanup.Rollback(&retErr, tx)
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(73461721938102)`); err != nil {
		return err
	}
	var head []byte
	err = tx.QueryRowContext(ctx, `SELECT snapshot FROM price_sync_revisions ORDER BY seq DESC LIMIT 1`).Scan(&head)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	id, next, err := f(head)
	if err != nil {
		return err
	}
	if next != nil {
		if _, err = tx.ExecContext(ctx, `INSERT INTO price_sync_revisions(revision,snapshot) VALUES ($1,$2::jsonb)`, id, string(next)); err != nil {
			return err
		}
		if head != nil {
			if _, err = tx.ExecContext(ctx, priceSyncHistoryDiff, string(head), string(next), id); err != nil {
				return err
			}
		}
		// Same transaction as the append: PostgreSQL delivers this only after
		// commit, so a rollback or a crash before commit cannot announce a
		// head that does not exist.
		if _, err = tx.ExecContext(ctx, `SELECT pg_notify($1,$2)`, priceSyncHeadChannel, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// A dedicated LISTEN connection, not the pooled *sql.DB: notifications are
// delivered on the session that issued LISTEN. pq reconnects on its own and
// then reports a nil notification, which the caller treats as "notifications
// may have been missed, re-read the head".
func (s *priceSyncStore) FollowPriceSyncHead(ctx context.Context, wake func(reconnected bool)) (err error) {
	if s.dsn == "" {
		return errors.New("price sync head listener requires a database DSN")
	}
	listener := pq.NewListener(s.dsn, time.Second, 10*time.Second, func(event pq.ListenerEventType, err error) {
		if err != nil && event == pq.ListenerEventConnectionAttemptFailed {
			// The backstop still reconciles; keep this out of the log per retry.
			return
		}
	})
	defer func() { err = errors.Join(err, listener.Close()) }()
	done := make(chan error, 1)
	go func() { done <- listener.Listen(priceSyncHeadChannel) }()
	select {
	case <-ctx.Done():
		return nil
	case err := <-done:
		if err != nil {
			return err
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case notification, ok := <-listener.Notify:
			if !ok {
				return errors.New("price sync head listener closed")
			}
			wake(notification == nil)
		}
	}
}

// Compatibility projection only: V1 looks up "models.dev"/"cny-official"
// ownership, so V2 audit rows must never reuse those source identifiers.
// Do not manufacture legacy ownership or import history on first activation.
const priceSyncHistoryDiff = `WITH docs AS (SELECT $1::jsonb AS a,$2::jsonb AS b),
changed AS (
 SELECT item.key AS model, a->'models'->item.key->'effective' AS oldq,
        item.value->'effective' AS newq, item.value->>'mode' AS mode
 FROM docs CROSS JOIN LATERAL jsonb_each(b->'models') AS item
 WHERE item.value->'effective' IS NOT NULL AND (
   ((a->'models'->item.key->'effective') - 'verified_at' - 'evidence') IS DISTINCT FROM ((item.value->'effective') - 'verified_at' - 'evidence')
   OR a->'models'->item.key->>'mode' IS DISTINCT FROM item.value->>'mode')),
rates AS (SELECT model,oldq,newq,mode,field FROM changed CROSS JOIN LATERAL
 jsonb_object_keys(COALESCE(oldq->'rates','{}') || COALESCE(newq->'rates','{}')) AS field
 WHERE oldq->'rates'->field IS DISTINCT FROM newq->'rates'->field),
events AS (SELECT model,mode,newq,field,COALESCE((oldq->'rates'->>field)::numeric,0) AS prev,
 COALESCE((newq->'rates'->>field)::numeric,0) AS next FROM rates
 UNION ALL SELECT model,mode,newq,'revision',0,0 FROM changed c WHERE NOT EXISTS (SELECT 1 FROM rates r WHERE r.model=c.model))
INSERT INTO model_pricing_sync_history(model,field,old_value,new_value,source,applied_at,reason)
 SELECT LEFT(model,200),LEFT(field,80),prev,next,CASE WHEN mode='manual' THEN 'v2-manual' ELSE 'v2-auto-' || lower(newq->>'currency') END,
 now(), 'price revision ' || $3 FROM events`
