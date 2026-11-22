package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

const grokSubscriptionExtraKey = "grok_subscription_snapshot"

// LiveGrokSubscription carries successful settings truth, separately from
// billing-window freshness. Scope contains no tokens, subjects or proxy secrets.
type LiveGrokSubscription struct {
	Tier        string `json:"tier"`
	DisplayName string `json:"display_name"`
	Source      string `json:"source"`
	ObservedAt  string `json:"observed_at"`
	RequestedAt string `json:"requested_at"`
	StatusCode  int    `json:"status_code"`
	Scope       string `json:"scope"`
}

func grokSubscriptionScope(a *Account) string {
	if a == nil || !a.IsGrokOAuth() {
		return ""
	}
	token := a.GetGrokAccessToken()
	claims := xai.DecodeJWTClaims(token)
	sub, _ := claims["sub"].(string)
	issuer, _ := claims["iss"].(string)
	// Missing token subject is conservatively bound to this token generation.
	// Do not use a stored sub that may belong to a previous authorization.
	identity := sub
	if identity == "" {
		identity = token
	}
	proxy := any(nil)
	if a.ProxyID != nil {
		if a.Proxy == nil {
			return ""
		}
		proxy = []any{*a.ProxyID, a.Proxy.Protocol, a.Proxy.Host, a.Proxy.Port, a.Proxy.Username, a.Proxy.Password}
	}
	data, _ := json.Marshal([]any{a.ID, a.Platform, a.Type, identity, issuer, a.GetGrokBaseURL(), proxy, a.GetHeaderOverrides()})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func liveGrokSubscription(account *Account) *LiveGrokSubscription {
	if account == nil || !account.IsGrokOAuth() {
		return nil
	}
	data, err := json.Marshal(account.Extra[grokSubscriptionExtraKey])
	if err != nil {
		return nil
	}
	var snapshot LiveGrokSubscription
	if json.Unmarshal(data, &snapshot) != nil || snapshot.Source != "settings" || snapshot.StatusCode != 200 || strings.TrimSpace(snapshot.DisplayName) == "" || snapshot.Tier == "" {
		return nil
	}
	if _, err := time.Parse(time.RFC3339Nano, snapshot.ObservedAt); err != nil {
		return nil
	}
	if _, err := time.Parse(time.RFC3339Nano, snapshot.RequestedAt); err != nil {
		return nil
	}
	if snapshot.Tier != xai.NormalizeSubscriptionTier(snapshot.DisplayName) || snapshot.Scope == "" || snapshot.Scope != grokSubscriptionScope(account) {
		return nil
	}
	return &snapshot
}

// GrokSubscriptionSnapshotValid is shared with public account DTO projection;
// the browser cannot and should not reconstruct identity from redacted secrets.
func GrokSubscriptionSnapshotValid(a *Account) bool { return liveGrokSubscription(a) != nil }

// GrokSubscriptionAccountView drops a mismatched observation and its legacy
// projections without mutating caller-owned account maps. Otherwise a no-tier
// reauthorization could still inherit the old account's materialized Free.
func GrokSubscriptionAccountView(a *Account) *Account {
	if a == nil || a.Extra == nil {
		return a
	}
	raw, present := a.Extra[grokSubscriptionExtraKey]
	if !present || liveGrokSubscription(a) != nil {
		return a
	}
	data, _ := json.Marshal(raw)
	var old LiveGrokSubscription
	_ = json.Unmarshal(data, &old)
	view := *a
	view.Extra = make(map[string]any, len(a.Extra))
	for k, v := range a.Extra {
		if k != grokSubscriptionExtraKey {
			view.Extra[k] = v
		}
	}
	view.Credentials = make(map[string]any, len(a.Credentials))
	for k, v := range a.Credentials {
		view.Credentials[k] = v
	}
	if old.Tier != "" {
		if view.GetCredential("subscription_tier") == old.Tier {
			delete(view.Credentials, "subscription_tier")
		}
		if view.GetExtraString("subscription_tier") == old.Tier {
			delete(view.Extra, "subscription_tier")
		}
	}
	return &view
}

func grokSubscriptionNeedsRefresh(account *Account, now time.Time) bool {
	snapshot := liveGrokSubscription(account)
	if snapshot == nil {
		return true
	}
	observed, _ := time.Parse(time.RFC3339Nano, snapshot.ObservedAt)
	return now.Sub(observed) >= 10*time.Minute
}

// Unsupported repositories must never fall back to unguarded UpdateExtra.
type GrokSubscriptionRepository interface {
	UpdateGrokSubscriptionIfUnchanged(context.Context, int64, map[string]any, *int64, map[string]any, *Proxy) (bool, error)
}

func (s *GrokQuotaService) refreshGrokSubscription(ctx context.Context, account *Account, token, proxyURL string) (resultErr error) {
	requested := time.Now().UTC()
	defer func() {
		if resultErr != nil {
			slog.Warn("grok_subscription_refresh_failed", "account_id", account.ID, "requested_at", requested.Format(time.RFC3339Nano), "reason", resultErr.Error())
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, grokSubscriptionTimeout)
	defer cancel()
	repo, ok := s.accountRepo.(GrokSubscriptionRepository)
	if !ok {
		return errors.New("persistence_not_configured")
	}
	target, err := buildGrokBillingURL(account, s.cfg, false)
	if err != nil {
		return errors.New("invalid_base_url")
	}
	u, err := url.Parse(target)
	if err != nil {
		return errors.New("invalid_base_url")
	}
	u.Path = strings.TrimSuffix(u.Path, "/billing") + "/settings"
	u.RawQuery = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return errors.New("invalid_request")
	}
	xai.ApplyCLIBillingHeaders(req, token)
	account.ApplyHeaderOverrides(req.Header)
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, maxInt(account.Concurrency, 2))
	if err != nil {
		return errors.New("transport")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			resultErr = errors.Join(resultErr, errors.New("response_close"))
		}
	}()
	if resp.StatusCode != 200 {
		return fmt.Errorf("http_%d", resp.StatusCode)
	}
	var payload struct {
		Display string `json:"subscription_tier_display"`
		Tier    string `json:"subscription_tier"`
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, &payload) != nil {
		return errors.New("invalid_response")
	}
	display := strings.TrimSpace(payload.Display)
	if display == "" {
		display = strings.TrimSpace(payload.Tier)
	}
	if display == "" {
		return errors.New("missing_tier")
	}
	scope := grokSubscriptionScope(account)
	if scope == "" {
		return errors.New("missing_scope")
	}
	snapshot := map[string]any{"tier": xai.NormalizeSubscriptionTier(display), "display_name": display, "source": "settings", "observed_at": time.Now().UTC().Format(time.RFC3339Nano), "requested_at": requested.Format(time.RFC3339Nano), "status_code": 200, "scope": scope}
	release, err := s.acquireGrokSubscriptionRefreshLock(ctx, account)
	if err != nil {
		return err
	}
	defer release()
	applied, err := repo.UpdateGrokSubscriptionIfUnchanged(ctx, account.ID, account.Credentials, account.ProxyID, snapshot, account.Proxy)
	if err != nil {
		return errors.New("persistence")
	}
	if !applied {
		return errors.New("stale_observation")
	}
	return nil
}
