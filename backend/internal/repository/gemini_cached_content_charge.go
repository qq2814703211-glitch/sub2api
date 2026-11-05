package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.GeminiCachedContentChargeRepository = (*geminiCachedContentRepository)(nil)

func (r *geminiCachedContentRepository) CommitChargedMutation(ctx context.Context, id int64, token string, target time.Time, intent *service.GeminiCachedContentChargeIntent) error {
	if intent == nil || intent.Command.RequestID == "" {
		return errors.New("cached content charge intent is missing")
	}
	payload, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	// The baseline remains committed until settlement. The mutation token is
	// consumed in the same SQL transaction that stores target and frozen bill.
	result, err := r.sql.ExecContext(ctx, `UPDATE gemini_cached_contents
 SET charge_intent=$4, charge_target_expire_time=$3, billing_ready=FALSE,
 mutation_token=NULL, mutation_lease_until=NULL, charge_next_attempt_at=NOW(),
 charge_attempts=0, charge_last_error=NULL, updated_at=NOW()
 WHERE id=$1 AND mutation_token=$2 AND mutation_lease_until>NOW()
 AND deleted_at IS NULL AND charge_intent IS NULL AND billing_ready
 AND user_id=$5 AND api_key_id=$6 AND account_id=$7 AND group_id=$8`,
		id, token, target, payload, intent.Command.UserID, intent.Command.APIKeyID, intent.Command.AccountID, intent.Usage.GroupID)
	return cachedContentChargeUpdated(result, err)
}

func cachedContentChargeUpdated(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return service.ErrGeminiCachedContentMutationConflict
	}
	return nil
}

func (r *geminiCachedContentRepository) ClaimCharge(ctx context.Context, id int64, request, token string, lease time.Duration) (*service.GeminiCachedContent, error) {
	record, err := scanGeminiCachedContent(r.sql.QueryRowContext(ctx, `UPDATE gemini_cached_contents
 SET charge_lease_token=$3, charge_lease_until=NOW()+make_interval(secs=>$4::double precision),
 charge_attempts=charge_attempts+1
 WHERE id=$1 AND charge_intent->'command'->>'RequestID'=$2
 AND (charge_lease_until IS NULL OR charge_lease_until<NOW())
 RETURNING `+geminiCachedContentColumns, id, request, token, lease.Seconds()))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return record, err
}

func (r *geminiCachedContentRepository) CompleteCharge(ctx context.Context, id int64, request, token string) error {
	result, err := r.sql.ExecContext(ctx, `UPDATE gemini_cached_contents
 SET expire_time=COALESCE(charge_target_expire_time,expire_time), billing_ready=TRUE,
 charge_completed_request_id=$2, charge_intent=NULL, charge_target_expire_time=NULL,
 charge_lease_token=NULL, charge_lease_until=NULL, charge_last_error=NULL, updated_at=NOW()
 WHERE id=$1 AND charge_intent->'command'->>'RequestID'=$2
 AND charge_lease_token=$3 AND charge_lease_until>NOW()`, id, request, token)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 1 {
		return nil
	}
	// An old completion must not unlock a newer bill or claim success for it.
	var completed bool
	err = r.sql.QueryRowContext(ctx, `SELECT charge_intent IS NULL AND charge_completed_request_id=$2
 FROM gemini_cached_contents WHERE id=$1`, id, request).Scan(&completed)
	if err != nil {
		return err
	}
	if !completed {
		return service.ErrGeminiCachedContentMutationConflict
	}
	return nil
}

func (r *geminiCachedContentRepository) FailCharge(ctx context.Context, id int64, token, message string) error {
	if len(message) > 2000 {
		message = message[:2000]
	}
	_, err := r.sql.ExecContext(ctx, `UPDATE gemini_cached_contents
 SET charge_last_error=$3, charge_lease_token=NULL, charge_lease_until=NULL,
 charge_next_attempt_at=NOW()+INTERVAL '1 minute'
 WHERE id=$1 AND charge_lease_token=$2 AND charge_intent IS NOT NULL`, id, token, message)
	return err
}

func (r *geminiCachedContentRepository) ListPendingCharges(ctx context.Context, limit int) ([]*service.GeminiCachedContent, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT `+geminiCachedContentColumns+` FROM gemini_cached_contents
 WHERE charge_intent IS NOT NULL AND charge_next_attempt_at<=NOW()
 AND (charge_lease_until IS NULL OR charge_lease_until<NOW())
 ORDER BY charge_next_attempt_at, id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var records []*service.GeminiCachedContent
	for rows.Next() {
		record, err := scanGeminiCachedContent(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}
