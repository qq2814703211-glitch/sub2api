package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type videoBillingJobRepository struct{ db *sql.DB }

func NewVideoBillingJobRepository(db *sql.DB) service.VideoBillingJobRepository {
	return &videoBillingJobRepository{db: db}
}

const videoJobColumns = `id,user_id,api_key_id,account_id,group_id,provider,COALESCE(task_id,''),state,
 snapshot,intent,provider_status,last_error,attempts,COALESCE(lease_token,''),created_at,updated_at,available_at`

type videoJobScanner interface{ Scan(...any) error }

func scanVideoJob(row videoJobScanner) (*service.VideoBillingJob, error) {
	j := &service.VideoBillingJob{}
	var snapshot, intent []byte
	err := row.Scan(&j.ID, &j.UserID, &j.APIKeyID, &j.AccountID, &j.GroupID, &j.Provider, &j.TaskID, &j.State,
		&snapshot, &intent, &j.ProviderStatus, &j.LastError, &j.Attempts, &j.LeaseToken, &j.CreatedAt, &j.UpdatedAt, &j.AvailableAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrVideoJobNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(snapshot, &j.Snapshot); err != nil {
		return nil, err
	}
	if len(intent) > 0 {
		if err = json.Unmarshal(intent, &j.Intent); err != nil {
			return nil, err
		}
	}
	return j, nil
}

func (r *videoBillingJobRepository) Create(ctx context.Context, j *service.VideoBillingJob) error {
	payload, err := json.Marshal(j.Snapshot)
	if err != nil {
		return err
	}
	return r.db.QueryRowContext(ctx, `INSERT INTO video_billing_jobs
 (id,user_id,api_key_id,account_id,group_id,provider,state,snapshot,available_at)
 VALUES ($1,$2,$3,$4,$5,$6,'creating',$7,NOW()+INTERVAL '10 minutes') RETURNING created_at,updated_at,available_at`,
		j.ID, j.UserID, j.APIKeyID, j.AccountID, j.GroupID, j.Provider, payload).Scan(&j.CreatedAt, &j.UpdatedAt, &j.AvailableAt)
}

func videoJobAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return service.ErrVideoJobLeaseLost
	}
	return nil
}

// Only a producer-proven unaccepted create may become terminal without a task ID.
func (r *videoBillingJobRepository) RejectCreate(ctx context.Context, id string) error {
	return videoJobAffected(r.db.ExecContext(ctx, `UPDATE video_billing_jobs
 SET state='failed',last_error='create_rejected',updated_at=NOW()
 WHERE id=$1 AND state='creating' AND task_id IS NULL`, id))
}

func (r *videoBillingJobRepository) Submit(ctx context.Context, id, taskID string) error {
	if taskID == "" {
		return errors.New("video task id is empty")
	}
	return videoJobAffected(r.db.ExecContext(ctx, `UPDATE video_billing_jobs SET task_id=$2,state='submitted',
 available_at=NOW(),updated_at=NOW() WHERE id=$1 AND state='creating' AND task_id IS NULL`, id, taskID))
}

func (r *videoBillingJobRepository) GetForOwner(ctx context.Context, task string, user, key, group int64) (*service.VideoBillingJob, error) {
	return scanVideoJob(r.db.QueryRowContext(ctx, `SELECT `+videoJobColumns+` FROM video_billing_jobs
 WHERE task_id=$1 AND user_id=$2 AND api_key_id=$3 AND group_id=$4`, task, user, key, group))
}

func (r *videoBillingJobRepository) Claim(ctx context.Context, id string, lease time.Duration) (*service.VideoBillingJob, error) {
	return scanVideoJob(r.db.QueryRowContext(ctx, `UPDATE video_billing_jobs SET lease_token=$2,
 lease_until=NOW()+make_interval(secs=>$3::double precision),attempts=attempts+1,updated_at=NOW()
 WHERE id=$1 AND state IN ('submitted','ready','billed','reconciliation') AND task_id IS NOT NULL
 AND (lease_until IS NULL OR lease_until<NOW()) RETURNING `+videoJobColumns, id, uuid.NewString(), lease.Seconds()))
}

func (r *videoBillingJobRepository) ClaimDue(ctx context.Context, lease time.Duration) (*service.VideoBillingJob, error) {
	return scanVideoJob(r.db.QueryRowContext(ctx, `UPDATE video_billing_jobs SET lease_token=$1,
 lease_until=NOW()+make_interval(secs=>$2::double precision),attempts=attempts+1,updated_at=NOW()
 WHERE id=(SELECT id FROM video_billing_jobs WHERE state IN ('creating','submitted','ready','billed','reconciliation')
 AND (state <> 'reconciliation' OR task_id IS NOT NULL)
 AND available_at<=NOW() AND (lease_until IS NULL OR lease_until<NOW())
 ORDER BY available_at,id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING `+videoJobColumns, uuid.NewString(), lease.Seconds()))
}

func (r *videoBillingJobRepository) PrepareIntent(ctx context.Context, j *service.VideoBillingJob, i *service.VideoBillingIntent) error {
	payload, err := json.Marshal(i)
	if err != nil {
		return err
	}
	err = videoJobAffected(r.db.ExecContext(ctx, `UPDATE video_billing_jobs SET intent=$3,state='ready',provider_status=$4,updated_at=NOW()
 WHERE id=$1 AND lease_token=$2 AND lease_until>NOW() AND intent IS NULL AND state IN ('submitted','reconciliation')`, j.ID, j.LeaseToken, payload, j.ProviderStatus))
	if err == nil {
		j.Intent = i
		j.State = "ready"
	}
	return err
}

// The task row fence, existing ledger dedup key, debit and billed transition share
// one transaction. An expired worker cannot charge or release a successor's lease.
func (r *videoBillingJobRepository) Apply(ctx context.Context, j *service.VideoBillingJob) (*service.UsageBillingApplyResult, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var state string
	var payload []byte
	err = tx.QueryRowContext(ctx, `SELECT state,intent FROM video_billing_jobs WHERE id=$1 AND lease_token=$2
 AND lease_until>NOW() FOR UPDATE`, j.ID, j.LeaseToken).Scan(&state, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrVideoJobLeaseLost
	}
	if err != nil {
		return nil, err
	}
	if state != "ready" && state != "billed" {
		return nil, fmt.Errorf("video job is not ready: %s", state)
	}
	var intent service.VideoBillingIntent
	if err = json.Unmarshal(payload, &intent); err != nil {
		return nil, err
	}
	result := &service.UsageBillingApplyResult{}
	if state == "ready" && !intent.SimpleMode {
		cmd := intent.Command
		cmd.Normalize()
		if cmd.RequestID == "" || cmd.UserID != j.UserID || cmd.APIKeyID != j.APIKeyID || cmd.AccountID != j.AccountID {
			return nil, errors.New("invalid video billing intent owner")
		}
		ledger := &usageBillingRepository{db: r.db}
		applied, e := ledger.claimUsageBillingKey(ctx, tx, &cmd)
		if e != nil {
			return nil, e
		}
		result.Applied = applied
		if applied {
			if err = ledger.applyUsageBillingEffects(ctx, tx, &cmd, result); err != nil {
				return nil, err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE video_billing_jobs SET state='billed',updated_at=NOW() WHERE id=$1`, j.ID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	j.State = "billed"
	j.Intent = &intent
	return result, nil
}

func (r *videoBillingJobRepository) Finish(ctx context.Context, j *service.VideoBillingJob, state, status, message string, delay time.Duration) error {
	switch state {
	case "submitted", "ready", "billed", "done", "failed", "expired", "reconciliation":
	default:
		return errors.New("invalid video job state")
	}
	// Internal error codes only: upstream bodies, URLs and credentials never enter last_error.
	if len(message) > 200 {
		message = message[:200]
	}
	if len(status) > 80 {
		status = status[:80]
	}
	return videoJobAffected(r.db.ExecContext(ctx, `UPDATE video_billing_jobs SET state=$3,provider_status=$4,
 last_error=$5,available_at=NOW()+make_interval(secs=>$6::double precision),updated_at=NOW(),lease_token=NULL,lease_until=NULL
 WHERE id=$1 AND lease_token=$2 AND lease_until>NOW()`, j.ID, j.LeaseToken, state, status, message, delay.Seconds()))
}

func (r *videoBillingJobRepository) List(ctx context.Context, state, before string, limit int) ([]*service.VideoBillingJob, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+videoJobColumns+` FROM video_billing_jobs WHERE ($1='' OR state=$1)
 AND ($2='' OR id<$2) ORDER BY id DESC LIMIT $3`, state, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*service.VideoBillingJob, 0)
	for rows.Next() {
		j, e := scanVideoJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func (r *videoBillingJobRepository) Retry(ctx context.Context, id string) error {
	return videoJobAffected(r.db.ExecContext(ctx, `UPDATE video_billing_jobs SET state=CASE WHEN intent IS NULL THEN 'submitted' ELSE state END,
 available_at=NOW(),last_error='',updated_at=NOW() WHERE id=$1 AND task_id IS NOT NULL
 AND state IN ('submitted','ready','billed','reconciliation') AND (lease_until IS NULL OR lease_until<NOW())`, id))
}
