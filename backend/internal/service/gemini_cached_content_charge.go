package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// GeminiCachedContentChargeIntent is an immutable, credential-free snapshot.
// Recovery must never consult live prices, ownership, or multiplier settings.
type GeminiCachedContentChargeIntent struct {
	Command                    UsageBillingCommand `json:"command"`
	Usage                      UsageLog            `json:"usage"`
	QuotaPlatform              string              `json:"quota_platform"`
	StorageTokenHours          float64             `json:"storage_token_hours"`
	PricingAt                  time.Time           `json:"pricing_at"`
	SimpleMode                 bool                `json:"simple_mode"`
	SimpleModeKeyRateLimitOnly bool                `json:"simple_mode_key_rate_limit_only"`
}

type preparedCachedContentChargeKey struct{}
type preparedCachedContentCharge struct {
	intent *GeminiCachedContentChargeIntent
}

func (s *GatewayService) capturePreparedCachedContentCharge(ctx context.Context, usage *UsageLog, input *recordUsageCoreInput, cost *CostBreakdown, accountRate float64, subscriptionBill bool) bool {
	capture, ok := ctx.Value(preparedCachedContentChargeKey{}).(*preparedCachedContentCharge)
	if !ok {
		return false
	}
	cmd := buildUsageBillingCommand(usage.RequestID, usage, &postUsageBillingParams{
		Cost: cost, User: input.User, APIKey: input.APIKey, Account: input.Account, Subscription: input.Subscription,
		IsSubscriptionBill: subscriptionBill, AccountRateMultiplier: accountRate, APIKeyService: input.APIKeyService,
		RequestPayloadHash: input.RequestPayloadHash, SimpleModeKeyRateLimitOnly: simpleModeKeyRateLimitBillingEnabled(s.cfg, input.APIKey),
	})
	if cmd == nil {
		return true
	}
	if input.Result == nil || !strings.HasPrefix(cmd.RequestID, GeminiCachedContentUsageRequestIDPrefix) {
		return true
	}
	if input.APIKey == nil || usage.UserID != cmd.UserID {
		return true
	}
	if capture != nil {
		capture.intent = &GeminiCachedContentChargeIntent{Command: *cmd, Usage: *usage, QuotaPlatform: input.QuotaPlatform, StorageTokenHours: input.Result.CacheStorageTokenHours, PricingAt: input.PricingAt, SimpleMode: s.cfg != nil && s.cfg.RunMode == config.RunModeSimple, SimpleModeKeyRateLimitOnly: simpleModeKeyRateLimitBillingEnabled(s.cfg, input.APIKey)}
		capture.intent.Usage.User, capture.intent.Usage.APIKey, capture.intent.Usage.Account, capture.intent.Usage.Group, capture.intent.Usage.Subscription = nil, nil, nil, nil, nil
	}
	return true
}

// PrepareCachedContentCharge freezes model prices and the existing multiplier
// rules. It executes no ledger effects and persists no usage row.
func (s *GatewayService) PrepareCachedContentCharge(ctx context.Context, input *RecordUsageInput) (*GeminiCachedContentChargeIntent, error) {
	if input == nil || input.Result == nil || input.APIKey == nil || input.Account == nil {
		return nil, errors.New("cached content billing identity is missing")
	}
	copyInput := *input
	if copyInput.User == nil {
		copyInput.User = copyInput.APIKey.User
	}
	if copyInput.User == nil {
		return nil, errors.New("cached content billing user is missing")
	}
	capture := &preparedCachedContentCharge{}
	if err := s.RecordUsage(context.WithValue(ctx, preparedCachedContentChargeKey{}, capture), &copyInput); err != nil {
		return nil, err
	}
	if capture.intent == nil {
		return nil, errors.New("cached content charge could not be prepared")
	}
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple && !simpleModeKeyRateLimitBillingEnabled(s.cfg, copyInput.APIKey) {
		capture.intent.Command.BalanceCost, capture.intent.Command.SubscriptionCost, capture.intent.Command.APIKeyQuotaCost, capture.intent.Command.APIKeyRateLimitCost, capture.intent.Command.AccountQuotaCost = 0, 0, 0, 0, 0
		capture.intent.Command.RequestFingerprint = ""
		capture.intent.Command.Normalize()
	}
	return capture.intent, nil
}

// ApplyCachedContentCharge replays only frozen, credential-free ledger data.
// A ledger commit followed by a process crash is safe: Apply's durable dedup key
// and UsageLogRepository's unique request key make recovery idempotent.
func (s *GatewayService) ApplyCachedContentCharge(ctx context.Context, intent *GeminiCachedContentChargeIntent) error {
	if s == nil || intent == nil || s.usageBillingRepo == nil || s.usageLogRepo == nil {
		return errors.New("cached content settlement is unavailable")
	}
	cmd := &intent.Command
	if !strings.HasPrefix(cmd.RequestID, GeminiCachedContentUsageRequestIDPrefix) || cmd.RequestID != intent.Usage.RequestID || cmd.UserID != intent.Usage.UserID || cmd.APIKeyID != intent.Usage.APIKeyID || cmd.AccountID != intent.Usage.AccountID {
		return errors.New("cached content charge identity conflict")
	}
	result, err := s.usageBillingRepo.Apply(ctx, cmd)
	if err != nil {
		return err
	}
	if s.billingCacheService != nil {
		_ = s.billingCacheService.InvalidateUserBalance(ctx, cmd.UserID)
		if cmd.SubscriptionID != nil && intent.Usage.GroupID != nil {
			_ = s.billingCacheService.InvalidateSubscription(ctx, cmd.UserID, *intent.Usage.GroupID)
		}
		if cmd.APIKeyRateLimitCost > 0 {
			_ = s.billingCacheService.InvalidateAPIKeyRateLimit(ctx, cmd.APIKeyID)
		}
	}
	if result != nil && result.Applied && (!intent.SimpleMode || intent.SimpleModeKeyRateLimitOnly) && s.billingCacheService != nil && s.deferredService != nil {
		// Reuse existing quota/notification updates after the authoritative
		// ledger commit. Subscription counters above were invalidated rather
		// than incremented, so suppress that one optimistic update here.
		key := &APIKey{ID: cmd.APIKeyID, UserID: cmd.UserID, GroupID: intent.Usage.GroupID}
		if cmd.APIKeyRateLimitCost > 0 {
			key.RateLimit5h = 1
		}
		cost := &CostBreakdown{ActualCost: intent.Usage.ActualCost, TotalCost: intent.Usage.TotalCost}
		params := &postUsageBillingParams{Cost: cost, User: &User{ID: cmd.UserID}, APIKey: key, Account: &Account{ID: cmd.AccountID, Type: cmd.AccountType}, AccountRateMultiplier: 1, Platform: intent.QuotaPlatform, SimpleModeKeyRateLimitOnly: intent.SimpleModeKeyRateLimitOnly, SkipBalanceCache: true}
		if intent.Usage.AccountRateMultiplier != nil {
			params.AccountRateMultiplier = *intent.Usage.AccountRateMultiplier
		}
		if cmd.SubscriptionID != nil {
			cost.ActualCost = 0
			params.Platform = ""
		}
		// API-key rate counters already updated immediately above.
		key.RateLimit5h = 0
		finalizePostUsageBilling(ctx, params, s.billingDeps(), result)
	}
	if s.billingCacheService != nil {
		_ = s.billingCacheService.InvalidateUserBalance(ctx, cmd.UserID)
	}
	usage := intent.Usage
	if _, err := s.usageLogRepo.Create(ctx, &usage); err != nil {
		return err
	}
	return nil
}
