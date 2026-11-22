package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/tidwall/gjson"
)

const VideoBillingWorkerSetting = "video_billing_worker_enabled"

func (s *OpenAIGatewayService) VideoBillingWorkerEnabled(ctx context.Context) (bool, error) {
	if s.settingService == nil || s.settingService.settingRepo == nil {
		return true, nil
	}
	values, err := s.settingService.settingRepo.GetMultiple(ctx, []string{VideoBillingWorkerSetting})
	if err != nil {
		return false, err
	}
	return values[VideoBillingWorkerSetting] != "false", nil
}
func (s *OpenAIGatewayService) SetVideoBillingWorkerEnabled(ctx context.Context, enabled bool) error {
	if s.settingService == nil || s.settingService.settingRepo == nil {
		return errors.New("video worker settings unavailable")
	}
	return s.settingService.settingRepo.Set(ctx, VideoBillingWorkerSetting, fmt.Sprint(enabled))
}
func (s *OpenAIGatewayService) ListVideoBillingJobs(ctx context.Context, state, before string, limit int) ([]*VideoBillingJob, error) {
	if !s.HasDurableVideoBilling() {
		return nil, errors.New("video billing unavailable")
	}
	return s.videoBillingJobs.List(ctx, state, before, limit)
}
func (s *OpenAIGatewayService) RetryVideoBillingJob(ctx context.Context, id string) error {
	if !s.HasDurableVideoBilling() {
		return errors.New("video billing unavailable")
	}
	return s.videoBillingJobs.Retry(ctx, id)
}

func (s *OpenAIGatewayService) StartVideoBillingWorker() {
	if !s.HasDurableVideoBilling() {
		return
	}
	s.videoWorkerOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.videoWorkerCancel = cancel
		s.videoWorkerDone = make(chan struct{})
		go func() {
			defer close(s.videoWorkerDone)
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				if err := s.RunVideoBillingRecovery(ctx, 8); err != nil && ctx.Err() == nil {
					logger.LegacyPrintf("service.video_billing", "recovery pass failed: %v", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	})
}
func (s *OpenAIGatewayService) StopVideoBillingWorker() {
	if s.videoWorkerCancel != nil {
		s.videoWorkerCancel()
		<-s.videoWorkerDone
	}
}

// Public for bounded operational recovery and deterministic restart tests.
func (s *OpenAIGatewayService) RunVideoBillingRecovery(ctx context.Context, limit int) error {
	if !s.HasDurableVideoBilling() {
		return nil
	}
	enabled, err := s.VideoBillingWorkerEnabled(ctx)
	if err != nil || !enabled {
		return err
	}
	if limit <= 0 || limit > 100 {
		limit = 8
	}
	for n := 0; n < limit; n++ {
		if n > 0 {
			enabled, err = s.VideoBillingWorkerEnabled(ctx)
			if err != nil || !enabled {
				return err
			}
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		j, e := s.videoBillingJobs.ClaimDue(ctx, 2*time.Minute)
		if errors.Is(e, ErrVideoJobNotFound) {
			return nil
		}
		if e != nil {
			return e
		}
		runCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		e = s.recoverVideoBillingJob(runCtx, j)
		cancel()
		if e != nil {
			// Static codes only. Do not log provider bodies or credential-bearing URLs.
			logger.LegacyPrintf("service.video_billing", "recovery deferred job=%s state=%s", j.ID, j.State)
		}
	}
	return nil
}

func (s *OpenAIGatewayService) recoverVideoBillingJob(ctx context.Context, j *VideoBillingJob) error {
	if j.Intent != nil {
		return s.settleVideoBillingJob(ctx, j)
	}
	if j.State == "creating" {
		return s.videoBillingJobs.Finish(ctx, j, "reconciliation", "", "create_outcome_unknown_no_replay", 0)
	}
	result, code, err := s.pollVideoBillingJob(ctx, j)
	if err != nil {
		state := "submitted"
		if code == "account_revoked" || code == "provider_auth_failed" || time.Since(j.CreatedAt) > 24*time.Hour {
			state = "reconciliation"
		}
		_ = s.videoBillingJobs.Finish(ctx, j, state, "", code, 30*time.Second)
		return err
	}
	return s.processVideoBillingObservation(ctx, j, result)
}

// Poll only the persisted account. Never schedule, create, download artifacts or
// fall back to another provider account during background recovery.
func (s *OpenAIGatewayService) pollVideoBillingJob(ctx context.Context, j *VideoBillingJob) (*OpenAIForwardResult, string, error) {
	if s.accountRepo == nil || s.httpUpstream == nil {
		return nil, "provider_unavailable", errors.New("provider unavailable")
	}
	account, err := s.accountRepo.GetByID(ctx, j.AccountID)
	if err != nil || account == nil || account.Status != StatusActive || !slices.Contains(account.GroupIDs, j.GroupID) {
		return nil, "account_revoked", errors.New("bound account unavailable")
	}
	var target, token string
	if j.Provider == "seedance" {
		if !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilitySeedance) {
			return nil, "account_revoked", errors.New("bound account capability revoked")
		}
		base, e := s.validateUpstreamBaseURL(account.GetCredential("base_url"))
		if e != nil {
			return nil, "provider_config_invalid", e
		}
		target, err = buildSeedanceURL(base, SeedanceEndpointStatus, strings.TrimPrefix(j.TaskID, "seedance:"))
		token = strings.TrimSpace(account.GetCredential("api_key"))
	} else {
		if account.Platform != PlatformGrok {
			return nil, "account_revoked", errors.New("bound account platform changed")
		}
		target, err = buildGrokMediaURL(account, s.cfg, GrokMediaEndpointVideoStatus, j.TaskID)
		if err == nil {
			token, _, err = s.getRequestCredential(ctx, nil, account)
		}
	}
	if err != nil {
		return nil, "provider_config_or_credential_error", err
	}
	if token == "" {
		return nil, "provider_auth_failed", errors.New("provider credential missing")
	}
	req, err := http.NewRequestWithContext(WithHTTPUpstreamRedirectsDisabled(ctx), http.MethodGet, target, nil)
	if err != nil {
		return nil, "provider_config_invalid", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if j.Provider == "grok" && account.IsGrokOAuth() && isGrokCLIProxyTarget(target) {
		applyGrokCLIHeaders(req.Header)
	}
	account.ApplyHeaderOverrides(req.Header)
	proxy := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
	if err != nil {
		return nil, "provider_transport_error", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		code := "provider_http_error"
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			code = "provider_auth_failed"
		}
		if resp.StatusCode == 404 {
			code = "provider_task_missing"
		}
		return nil, code, errors.New(code)
	}
	body, err := ReadUpstreamResponseBody(resp.Body, s.cfg, nil, nil)
	if err != nil || !gjson.ValidBytes(body) {
		return nil, "provider_invalid_response", errors.New("invalid video status response")
	}
	result := &OpenAIForwardResult{ResponseID: j.TaskID}
	if j.Provider == "seedance" {
		applySeedanceVideoStatus(result, body)
	} else {
		result.VideoStatus = strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "status").String()))
		if billed := ExtractGrokVideoBillingFromStatusBody(body, nil, j.TaskID); billed != nil {
			result.VideoCount = billed.VideoCount
			result.VideoDurationSeconds = actualGrokVideoSeconds(body)
			result.ProviderBillableSeconds = billed.ProviderBillableSeconds
		}
	}
	return result, "", nil
}

// Zero means omitted; -1 means a present but invalid/non-integral duration.
// Never truncate or cap actual provider seconds to the request's 15-second limit.
func actualGrokVideoSeconds(body []byte) int {
	value := gjson.GetBytes(body, "video.duration")
	if !value.Exists() {
		return 0
	}
	if value.Type != gjson.Number || value.Float() <= 0 || value.Float() != float64(value.Int()) {
		return -1
	}
	return int(value.Int())
}

// Both native forwarding and worker polling preserve missing vs explicit zero.
func applySeedanceVideoStatus(result *OpenAIForwardResult, body []byte) {
	result.VideoStatus = strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "status").String()))
	result.UpstreamModel = gjson.GetBytes(body, "model").String()
	if result.VideoStatus != "succeeded" {
		return
	}
	root := gjson.ParseBytes(body)
	result.MediaUsage = &MediaUsage{}
	if strings.TrimSpace(root.Get("content.video_url").String()) != "" && !mediaResponseHasError(root) {
		result.MediaUsage = MergeMediaUsage(extractMediaDimensions(root), extractCompatibleVideoResponseUsage(body))
		result.MediaUsage.Count = 1
		result.ProviderBillableSeconds = reportedVideoSeconds(result.MediaUsage, 1)
	}
	tokens := gjson.GetBytes(body, "usage.completion_tokens")
	result.VideoUsageKnown = tokens.Exists() && tokens.Type == gjson.Number && tokens.Float() >= 0 && tokens.Float() == float64(tokens.Int())
	if result.VideoUsageKnown {
		result.Usage.OutputTokens = int(tokens.Int())
	}
}
