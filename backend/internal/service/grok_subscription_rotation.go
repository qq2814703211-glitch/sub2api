package service

import (
	"reflect"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

// GrokSubscriptionAfterTokenRotation carries a confirmed observation across a
// provider-issued refresh only. Admin reauthorization must never call this.
// The repository CASes the old credentials and snapshot together with this copy.
func GrokSubscriptionAfterTokenRotation(account *Account, credentials map[string]any) *LiveGrokSubscription {
	live := liveGrokSubscription(account)
	if live == nil {
		return nil
	}
	before := xai.DecodeJWTClaims(account.GetGrokAccessToken())
	next := *account
	next.Credentials = credentials
	if next.GetGrokBaseURL() != account.GetGrokBaseURL() || !reflect.DeepEqual(next.GetHeaderOverrides(), account.GetHeaderOverrides()) {
		return nil
	}
	after := xai.DecodeJWTClaims(next.GetGrokAccessToken())
	for _, key := range []string{"sub", "iss"} {
		oldValue, _ := before[key].(string)
		newValue, _ := after[key].(string)
		if oldValue != "" && newValue != "" && oldValue != newValue {
			return nil
		}
	}
	scope := grokSubscriptionScope(&next)
	if scope == "" || next.GetGrokAccessToken() == "" {
		return nil
	}
	migrated := *live
	migrated.Scope = scope
	return &migrated
}
