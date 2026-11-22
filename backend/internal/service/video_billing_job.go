package service

// Adapted from kiss-kedaya media_billing_job.go / media_billing.go.
// Upstream: cdf13f1991c369dbc5a4585ad197c103f0772451
import (
	"context"
	"errors"
	"time"
)

var (
	ErrVideoJobNotFound  = errors.New("video billing job not found")
	ErrVideoJobLeaseLost = errors.New("video billing job lease lost")
)

// VideoBillingIntent contains no credentials, request bodies or media URLs.
// Raw amounts are preserved until real usage is known; quantize only the final bill.
type VideoBillingIntent struct {
	CatalogUnitMode BillingMode         `json:"catalog_unit_mode,omitempty"`
	Command         UsageBillingCommand `json:"command"`
	Usage           UsageLog            `json:"usage"`
	RawAmounts      [5]float64          `json:"raw_amounts"`
	QuotaPlatform   string              `json:"quota_platform"`
	SimpleMode      bool                `json:"simple_mode"`
	RateLimitOnly   bool                `json:"rate_limit_only"`
}

type VideoBillingSnapshot struct {
	Pending     GrokVideoPendingBilling `json:"pending"`
	Unit        VideoBillingIntent      `json:"unit"`
	PerSecond   bool                    `json:"per_second"`
	TokenBilled bool                    `json:"token_billed"`
}

type VideoBillingJob struct {
	ID             string               `json:"id"`
	UserID         int64                `json:"user_id"`
	APIKeyID       int64                `json:"api_key_id"`
	AccountID      int64                `json:"account_id"`
	GroupID        int64                `json:"group_id"`
	Provider       string               `json:"provider"`
	TaskID         string               `json:"task_id"`
	State          string               `json:"state"`
	Snapshot       VideoBillingSnapshot `json:"snapshot"`
	Intent         *VideoBillingIntent  `json:"intent,omitempty"`
	ProviderStatus string               `json:"provider_status"`
	LastError      string               `json:"last_error"`
	Attempts       int                  `json:"attempts"`
	LeaseToken     string               `json:"-"`
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
	AvailableAt    time.Time            `json:"available_at"`
}

type VideoBillingJobRepository interface {
	Create(context.Context, *VideoBillingJob) error
	Submit(context.Context, string, string) error
	GetForOwner(context.Context, string, int64, int64, int64) (*VideoBillingJob, error)
	Claim(context.Context, string, time.Duration) (*VideoBillingJob, error)
	ClaimDue(context.Context, time.Duration) (*VideoBillingJob, error)
	PrepareIntent(context.Context, *VideoBillingJob, *VideoBillingIntent) error
	Apply(context.Context, *VideoBillingJob) (*UsageBillingApplyResult, error)
	Finish(context.Context, *VideoBillingJob, string, string, string, time.Duration) error
	List(context.Context, string, string, int) ([]*VideoBillingJob, error)
	Retry(context.Context, string) error
}

type videoPreparedUsageKey struct{}
type videoPreparedUsage struct{ intent *VideoBillingIntent }

type videoBeforePublishKey struct{}
type VideoBeforePublish func(context.Context, *OpenAIForwardResult) error

func ContextWithVideoBeforePublish(ctx context.Context, callback VideoBeforePublish) context.Context {
	return context.WithValue(ctx, videoBeforePublishKey{}, callback)
}

func runVideoBeforePublish(ctx context.Context, result *OpenAIForwardResult) error {
	if callback, ok := ctx.Value(videoBeforePublishKey{}).(VideoBeforePublish); ok && callback != nil {
		return callback(ctx, result)
	}
	return nil
}
