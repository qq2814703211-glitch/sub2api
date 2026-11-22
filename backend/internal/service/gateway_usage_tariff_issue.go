package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Reconciliation evidence only: no request/response body, credential or
// estimated cost. The original monetary key is still free for accounting replay.
type tariffUsageEvidence struct {
	RequestID              string             `json:"request_id"`
	PricingAt              time.Time          `json:"pricing_at"`
	Tokens                 UsageTokens        `json:"tokens"`
	AccountRateMultiplier  float64            `json:"account_rate_multiplier"`
	ChannelID              int64              `json:"channel_id,omitempty"`
	RateMultiplier         float64            `json:"rate_multiplier"`
	SearchCount            int                `json:"search_count,omitempty"`
	CacheStorageTokenHours float64            `json:"cache_storage_token_hours,omitempty"`
	UpstreamPriceFactor    float64            `json:"upstream_price_factor,omitempty"`
	RequestedModel         string             `json:"requested_model,omitempty"`
	UpstreamModel          string             `json:"upstream_model,omitempty"`
	BillingModel           string             `json:"billing_model"`
	SubscriptionID         *int64             `json:"subscription_id,omitempty"`
	ServiceTier            string             `json:"service_tier,omitempty"`
	ReasoningEffort        string             `json:"reasoning_effort,omitempty"`
	PayloadHash            string             `json:"payload_fingerprint,omitempty"`
	QuotaPlatform          string             `json:"quota_platform,omitempty"`
	PriceTrace             *RequestPriceTrace `json:"price_trace,omitempty"`
}

// Use the existing Ops unresolved-error workflow, not a zero usage row (which
// would block the eventual settled row) or an autonomous debit/retry queue.
type tariffIssueRecorder interface {
	RecordTariffIssue(context.Context, *OpsInsertErrorLogInput) error
}

// This incident is for the account's reporting cost only. It must never
// request a customer billing replay: the original customer price is settled.
func recordAccountStatsIssue(ctx context.Context, repo UsageLogRepository, cause error, usage *UsageLog, platform string, at time.Time, tokens UsageTokens, media *MediaUsage) {
	if cause == nil {
		return
	}
	body, err := json.Marshal(map[string]any{"request_id": usage.RequestID, "pricing_at": at, "tokens": tokens, "image_count": usage.ImageCount, "video_count": usage.VideoCount, "media": media})
	if writer, ok := repo.(tariffIssueRecorder); !ok {
		err = errors.New("incident store unavailable")
	} else if err == nil {
		auditCtx, cancel := detachedBillingContext(ctx)
		defer cancel()
		err = writer.RecordTariffIssue(auditCtx, &OpsInsertErrorLogInput{
			RequestID: truncateString(usage.RequestID, 64), UserID: &usage.UserID, APIKeyID: &usage.APIKeyID, AccountID: &usage.AccountID, GroupID: usage.GroupID,
			Platform: platform, Model: truncateString(usage.Model, 100), RequestedModel: truncateString(usage.RequestedModel, 100), UpstreamModel: truncateString(optionalStringValue(usage.UpstreamModel), 100),
			ErrorPhase: "internal", ErrorOwner: "platform", ErrorSource: "gateway", ErrorType: "account_stats_unsettled", Severity: "P2",
			ErrorMessage: cause.Error(), ErrorBody: string(body), CreatedAt: time.Now(),
		})
	}
	if err != nil {
		slog.ErrorContext(ctx, "account_stats_unsettled", "request_id", usage.RequestID, "api_key_id", usage.APIKeyID, "cause", cause, "audit_error", err)
	}
}

func recordTariffUsageIssue(ctx context.Context, repo UsageLogRepository, err error, user *User, key *APIKey, account *Account, e tariffUsageEvidence) error {
	var issue *TariffPriceIssue
	if !errors.As(err, &issue) {
		return err
	}
	e.BillingModel = issue.Model
	e.AccountRateMultiplier = account.BillingRateMultiplier()
	if p := RequestCatalogFromContext(ctx); p != nil {
		if row := p.billing.exactCatalogRow(issue.Model); row != nil {
			e.PriceTrace = p.trace(&CostBreakdown{priceBasis: priceBasis{Scope: "catalog", Row: row.rowIdentity}}, issue.Model, key.Group)
		}
	}
	slog.ErrorContext(ctx, "usage_tariff_unsettled", "request_id", e.RequestID, "user_id", user.ID, "api_key_id", key.ID, "account_id", account.ID, "reason", issue.Reason, "usage", e)
	writer, ok := repo.(tariffIssueRecorder)
	if !ok {
		return fmt.Errorf("tariff incident store unavailable (request %s): %w", e.RequestID, err)
	}
	body, auditErr := json.Marshal(e)
	if auditErr == nil {
		auditCtx, cancel := detachedBillingContext(ctx)
		defer cancel()
		auditErr = writer.RecordTariffIssue(auditCtx, &OpsInsertErrorLogInput{
			RequestID: truncateString(e.RequestID, 64), UserID: &user.ID, APIKeyID: &key.ID, AccountID: &account.ID, GroupID: key.GroupID,
			Platform: account.Platform, Model: truncateString(issue.Model, 100), RequestedModel: truncateString(e.RequestedModel, 100), UpstreamModel: truncateString(e.UpstreamModel, 100),
			ErrorPhase: "internal", ErrorOwner: "platform", ErrorSource: "gateway", ErrorType: "tariff_unsettled", Severity: "P1",
			ErrorMessage: issue.Error(), ErrorBody: string(body), CreatedAt: time.Now(),
		})
	}
	if auditErr != nil {
		return errors.Join(err, fmt.Errorf("record unresolved tariff for request %s: %w", e.RequestID, auditErr))
	}
	return err
}
