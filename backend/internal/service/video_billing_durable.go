package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
)

// Capture the existing public RecordUsage pricing/command seam, without ledger or
// log effects. No separate pricing engine or provider-specific guessed tariff.
func captureVideoPreparedUsage(ctx context.Context, usage *UsageLog, in *OpenAIRecordUsageInput, cost *CostBreakdown, accountRate float64, subscription bool, cfg *config.Config) bool {
	capture, ok := ctx.Value(videoPreparedUsageKey{}).(*videoPreparedUsage)
	if !ok {
		return false
	}
	rateOnly := simpleModeKeyRateLimitBillingEnabled(cfg, in.APIKey)
	p := &postUsageBillingParams{Cost: cost, User: in.User, APIKey: in.APIKey, Account: in.Account,
		Subscription: in.Subscription, IsSubscriptionBill: subscription && !rateOnly, AccountRateMultiplier: accountRate,
		APIKeyService: in.APIKeyService, RequestPayloadHash: in.RequestPayloadHash, SimpleModeKeyRateLimitOnly: rateOnly}
	cmd := buildUsageBillingCommand(usage.RequestID, usage, p)
	if cmd == nil {
		return true
	}
	i := &VideoBillingIntent{Command: *cmd, Usage: *usage, QuotaPlatform: in.QuotaPlatform, RateLimitOnly: rateOnly,
		SimpleMode: cfg != nil && cfg.RunMode == config.RunModeSimple && !rateOnly}
	i.CatalogUnitMode = cost.catalogUnitMode
	i.Usage.User = nil
	i.Usage.APIKey = nil
	i.Usage.Account = nil
	i.Usage.Group = nil
	i.Usage.Subscription = nil
	if subscription && !rateOnly {
		i.RawAmounts[1] = cost.ActualCost
	} else if !rateOnly {
		i.RawAmounts[0] = cost.ActualCost
	}
	if p.shouldDeductAPIKeyQuota() && !rateOnly {
		i.RawAmounts[2] = cost.ActualCost
	}
	if p.shouldUpdateRateLimits() {
		i.RawAmounts[3] = cost.ActualCost
	}
	if p.shouldUpdateAccountQuota() && !rateOnly {
		i.RawAmounts[4] = cost.TotalCost * accountRate
	}
	capture.intent = i
	return true
}

func (s *OpenAIGatewayService) prepareVideoUsage(ctx context.Context, in *OpenAIRecordUsageInput) (*VideoBillingIntent, error) {
	capture := &videoPreparedUsage{}
	if err := s.RecordUsage(context.WithValue(ctx, videoPreparedUsageKey{}, capture), in); err != nil {
		return nil, err
	}
	if capture.intent == nil {
		return nil, errors.New("video billing intent could not be prepared")
	}
	return capture.intent, nil
}

func (s *OpenAIGatewayService) SetVideoBillingAuthCache(cache APIKeyAuthCacheInvalidator) {
	s.videoBillingAuthCache = cache
}
func (s *OpenAIGatewayService) SetVideoBillingJobs(repo VideoBillingJobRepository) {
	s.videoBillingJobs = repo
}
func (s *OpenAIGatewayService) HasDurableVideoBilling() bool {
	return s != nil && s.videoBillingJobs != nil
}

// VideoBillingCreateInfo derives the snapshot from the same protocol adapter
// that will send the create, including relay-pinned duration. No provider I/O.
func VideoBillingCreateInfo(endpoint GrokMediaEndpoint, body []byte, contentType string, account *Account) (GrokMediaRequestInfo, error) {
	if endpoint.IsSeedance() {
		return ParseSeedanceRequest(body)
	}
	normalized, contentType, err := normalizeGrokMediaForwardBody(endpoint, body, contentType, account)
	if err != nil {
		return GrokMediaRequestInfo{}, err
	}
	return ParseGrokMediaRequest(contentType, normalized), nil
}

// Begin is persisted before the only create attempt. A crash with no upstream
// task ID becomes reconciliation; recovery never issues another provider POST.
func (s *OpenAIGatewayService) BeginVideoBillingJob(ctx context.Context, in *OpenAIRecordUsageInput, pending GrokVideoPendingBilling, provider string) (*VideoBillingJob, error) {
	if !s.HasDurableVideoBilling() {
		return nil, nil
	}
	if in == nil || in.Result == nil || in.APIKey == nil || in.User == nil || in.Account == nil || in.APIKey.GroupID == nil {
		return nil, errors.New("video billing owner missing")
	}
	if provider != "seedance" && provider != "grok" {
		return nil, errors.New("unsupported video provider")
	}
	ctx = s.WithRequestCatalog(ctx)
	copyInput := *in
	result := *in.Result
	copyInput.Result = &result
	result.RequestID = "video-pending:" + uuid.NewString()
	result.ResponseID = result.RequestID
	result.VideoCount = 1
	result.VideoDurationSeconds = 1
	probeSeconds := 1.0
	result.ProviderBillableSeconds = &probeSeconds
	result.VideoResolution = NormalizeVideoBillingResolutionOrDefault(pending.VideoResolution)
	if provider == "seedance" {
		result.VideoCount = 0
		billing := s.billingService.forRequest(ctx)
		model := billing.catalogBillingModel(ctx, s.resolver, in.APIKey, firstNonEmpty(result.BillingModel, result.Model), result.UpstreamModel, in.ChannelUsageFields)
		if billing.exactCatalogUnit(model) != nil && s.resolveOpenAIChannelPricing(ctx, model, in.APIKey) == nil {
			result.ResponseID = "seedance:unit-quote"
			result.MediaUsage = &MediaUsage{Count: 1}
		}
		result.Usage = OpenAIUsage{OutputTokens: 1}
	}
	unit, err := s.prepareVideoUsage(ctx, &copyInput)
	if err != nil {
		return nil, err
	}
	tokenBilled := unit.Usage.BillingMode != nil && *unit.Usage.BillingMode == string(BillingModeToken)
	if provider == "grok" && tokenBilled {
		return nil, errors.New("asynchronous Grok video requires duration or request pricing")
	}
	perSecond := unit.Usage.BillingMode != nil && *unit.Usage.BillingMode == string(BillingModePerSecond)
	if provider == "grok" && !unit.CatalogUnitMode.isCatalogUnit() {
		result.VideoDurationSeconds = 2
		two, e := s.prepareVideoUsage(ctx, &copyInput)
		if e != nil {
			return nil, e
		}
		perSecond = two.Usage.TotalCost != unit.Usage.TotalCost || two.Usage.ActualCost != unit.Usage.ActualCost
	}
	job := &VideoBillingJob{ID: fmt.Sprintf("video_%020d_%s", time.Now().UnixMilli(), uuid.NewString()), UserID: in.User.ID,
		APIKeyID: in.APIKey.ID, AccountID: in.Account.ID, GroupID: *in.APIKey.GroupID, Provider: provider, State: "creating",
		Snapshot: VideoBillingSnapshot{Pending: pending, Unit: *unit, PerSecond: perSecond, TokenBilled: tokenBilled}}
	if err = s.videoBillingJobs.Create(ctx, job); err != nil {
		return nil, err
	}
	return job, nil
}

// RejectVideoBillingCreate closes a known-unaccepted attempt before failover.
// A storage failure blocks retry: the old attempt must never look ambiguous.
func (s *OpenAIGatewayService) RejectVideoBillingCreate(ctx context.Context, job *VideoBillingJob) error {
	if job == nil {
		return nil
	}
	rejector, ok := s.videoBillingJobs.(interface {
		RejectCreate(context.Context, string) error
	})
	if !ok {
		return errors.New("video create rejection storage unavailable")
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return rejector.RejectCreate(persistCtx, job.ID)
}

func (s *OpenAIGatewayService) SubmitVideoBillingJob(ctx context.Context, j *VideoBillingJob, task string) error {
	if j == nil {
		return nil
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return s.videoBillingJobs.Submit(persistCtx, j.ID, task)
}

func (s *OpenAIGatewayService) LoadVideoBillingJob(ctx context.Context, task string, user, key, group int64) (*VideoBillingJob, error) {
	if !s.HasDurableVideoBilling() {
		return nil, nil
	}
	job, err := s.videoBillingJobs.GetForOwner(ctx, task, user, key, group)
	if errors.Is(err, ErrVideoJobNotFound) {
		return nil, nil
	}
	return job, err
}

func videoIntentForCompletion(j *VideoBillingJob, result *OpenAIForwardResult) (*VideoBillingIntent, error) {
	snapshot := j.Snapshot
	seconds := result.VideoDurationSeconds
	factor := 1.0
	nativeMode := snapshot.Unit.CatalogUnitMode
	if nativeMode.isCatalogUnit() {
		if result.VideoCount <= 0 && (result.MediaUsage == nil || result.MediaUsage.Count == 0) {
			return nil, errors.New("completion_artifact_missing")
		}
		usage := result.catalogUsage()
		if j.Provider == "seedance" {
			usage.success = result.VideoStatus == "succeeded" && result.MediaUsage != nil && result.MediaUsage.Count > 0
		}
		if !usage.success {
			return nil, errors.New("completion_artifact_missing")
		}
		var err error
		factor, err = usage.units(nativeMode)
		if err != nil {
			return nil, err
		}
	} else if j.Provider == "seedance" {
		if result.VideoStatus != "succeeded" || !result.VideoUsageKnown || result.Usage.OutputTokens < 0 {
			return nil, errors.New("completion_usage_missing")
		}
		if snapshot.TokenBilled {
			factor = float64(result.Usage.OutputTokens)
		}
	} else {
		if result.VideoCount <= 0 {
			return nil, errors.New("completion_artifact_missing")
		}
		if snapshot.PerSecond {
			// Native relays may omit output duration. Only an explicit submitted duration
			// can substitute; never invent the provider default or silently clamp output.
			if seconds < 0 {
				return nil, errors.New("completion_duration_invalid")
			}
			if seconds == 0 {
				seconds = snapshot.Pending.VideoDurationSeconds
			}
			if seconds <= 0 {
				return nil, errors.New("completion_duration_missing")
			}
			factor = float64(seconds)
		}
	}
	if math.IsNaN(factor) || math.IsInf(factor, 0) || factor < 0 {
		return nil, errors.New("completion_usage_invalid")
	}
	intent := snapshot.Unit
	for n, p := range []*float64{&intent.Command.BalanceCost, &intent.Command.SubscriptionCost, &intent.Command.APIKeyQuotaCost, &intent.Command.APIKeyRateLimitCost, &intent.Command.AccountQuotaCost} {
		*p = QuantizeUsageBillingAmount(snapshot.Unit.RawAmounts[n] * factor)
	}
	for _, p := range []*float64{&intent.Usage.InputCost, &intent.Usage.OutputCost, &intent.Usage.ImageInputCost, &intent.Usage.ImageOutputCost, &intent.Usage.CacheCreationCost, &intent.Usage.CacheReadCost, &intent.Usage.TotalCost, &intent.Usage.ActualCost} {
		*p *= factor
	}
	if intent.Usage.AccountStatsCost != nil {
		v := *intent.Usage.AccountStatsCost * factor
		intent.Usage.AccountStatsCost = &v
	}
	intent.Usage.ActualCost = QuantizeUsageBillingAmount(intent.Usage.ActualCost)
	intent.Usage.VideoCount = 1
	if nativeMode.isCatalogUnit() {
		intent.Usage.BillingUnits = &factor
		intent.Usage.VideoDurationSeconds = wholeProviderSeconds(result.ProviderBillableSeconds)
	} else if seconds > 0 {
		intent.Usage.VideoDurationSeconds = &seconds
	} else {
		intent.Usage.VideoDurationSeconds = nil
	}
	if j.Provider == "seedance" {
		intent.Command.OutputTokens = result.Usage.OutputTokens
		intent.Usage.OutputTokens = result.Usage.OutputTokens
	}
	intent.Command.RequestID = StableGrokVideoBillingRequestID(j.TaskID)
	intent.Command.RequestPayloadHash = HashUsageRequestPayload([]byte(j.TaskID))
	intent.Command.MediaType = "video"
	intent.Command.RequestFingerprint = ""
	intent.Command.Normalize()
	intent.Usage.RequestID = intent.Command.RequestID
	intent.Usage.CreatedAt = time.Now()
	duration := int(time.Since(j.CreatedAt).Milliseconds())
	intent.Usage.DurationMs = &duration
	return &intent, nil
}

// Observations from customers and background polling use the same fenced state
// machine. A customer race loses the claim harmlessly; background work retries.
func (s *OpenAIGatewayService) ObserveVideoBilling(ctx context.Context, j *VideoBillingJob, result *OpenAIForwardResult) error {
	if j == nil || result == nil {
		return nil
	}
	claim, err := s.videoBillingJobs.Claim(ctx, j.ID, 2*time.Minute)
	if errors.Is(err, ErrVideoJobNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.processVideoBillingObservation(ctx, claim, result)
}

func (s *OpenAIGatewayService) processVideoBillingObservation(ctx context.Context, j *VideoBillingJob, result *OpenAIForwardResult) error {
	if j.Intent != nil {
		return s.settleVideoBillingJob(ctx, j)
	}
	status := result.VideoStatus
	j.ProviderStatus = status
	switch status {
	case "failed", "cancelled", "canceled":
		return s.videoBillingJobs.Finish(ctx, j, "failed", status, "", 0)
	case "expired":
		return s.videoBillingJobs.Finish(ctx, j, "expired", status, "", 0)
	}
	complete := result.VideoCount > 0 || (j.Provider == "seedance" && status == "succeeded")
	if !complete {
		state, message := "submitted", ""
		if time.Since(j.CreatedAt) > 24*time.Hour {
			state, message = "reconciliation", "provider_pending_over_24h"
		}
		return s.videoBillingJobs.Finish(ctx, j, state, status, message, 30*time.Second)
	}
	intent, err := videoIntentForCompletion(j, result)
	if err != nil {
		return s.videoBillingJobs.Finish(ctx, j, "reconciliation", status, err.Error(), 0)
	}
	if err = s.videoBillingJobs.PrepareIntent(ctx, j, intent); err != nil {
		return err
	}
	return s.settleVideoBillingJob(ctx, j)
}

func (s *OpenAIGatewayService) settleVideoBillingJob(ctx context.Context, j *VideoBillingJob) error {
	result, err := s.videoBillingJobs.Apply(ctx, j)
	if err != nil {
		_ = s.videoBillingJobs.Finish(ctx, j, j.State, j.ProviderStatus, "ledger_apply_failed", 30*time.Second)
		return err
	}
	if result != nil && result.Applied {
		logRequestPriceTrace(ctx, &j.Intent.Usage)
	}
	// Caches are invalidated even on replay, including a crash after the debit.
	cmd := j.Intent.Command
	if s.videoBillingAuthCache != nil {
		s.videoBillingAuthCache.InvalidateAuthCacheByUserID(ctx, j.UserID)
	}
	if s.billingCacheService != nil {
		_ = s.billingCacheService.InvalidateUserBalance(ctx, j.UserID)
		if cmd.SubscriptionID != nil {
			_ = s.billingCacheService.InvalidateSubscription(ctx, j.UserID, j.GroupID)
		}
		_ = s.billingCacheService.InvalidateAPIKeyRateLimit(ctx, j.APIKeyID)
	}
	if result != nil && result.Applied && s.deferredService != nil {
		s.deferredService.ScheduleLastUsedUpdate(j.AccountID)
		if s.billingCacheService != nil && !j.Intent.SimpleMode && !j.Intent.RateLimitOnly {
			// Reuse existing platform-quota and notification policy. Durable SQL has
			// already updated balance/subscription/Key/account quotas; never enqueue
			// another subscription or Key-window increment while recovering logs.
			user := &User{ID: j.UserID}
			account := &Account{ID: j.AccountID, Type: cmd.AccountType}
			if s.userRepo != nil {
				if current, e := s.userRepo.GetByID(ctx, j.UserID); e == nil && current != nil {
					user = current
				}
			}
			if s.accountRepo != nil {
				if current, e := s.accountRepo.GetByID(ctx, j.AccountID); e == nil && current != nil {
					account = current
				}
			}
			cost := &CostBreakdown{TotalCost: j.Intent.Usage.TotalCost, ActualCost: j.Intent.Usage.ActualCost}
			if cmd.SubscriptionID != nil {
				cost.ActualCost = 0
			}
			rate := 1.0
			if j.Intent.Usage.AccountRateMultiplier != nil {
				rate = *j.Intent.Usage.AccountRateMultiplier
			}
			finalizePostUsageBilling(ctx, &postUsageBillingParams{Cost: cost, User: user,
				APIKey: &APIKey{ID: j.APIKeyID, GroupID: &j.GroupID}, Account: account,
				AccountRateMultiplier: rate, IsSubscriptionBill: cmd.SubscriptionID != nil,
				Platform: j.Intent.QuotaPlatform}, s.billingDeps(), result)
		}
	}
	if s.usageLogRepo == nil {
		return errors.New("video usage repository missing")
	}
	usage := j.Intent.Usage
	if _, err = s.usageLogRepo.Create(ctx, &usage); err != nil {
		_ = s.videoBillingJobs.Finish(ctx, j, "billed", j.ProviderStatus, "usage_log_write_failed", 30*time.Second)
		return err
	}
	return s.videoBillingJobs.Finish(ctx, j, "done", j.ProviderStatus, "", 0)
}
