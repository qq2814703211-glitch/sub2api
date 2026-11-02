//go:build unit

package service

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestGrokSubscriptionRotationIdentityBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, oldToken, newToken string
		mutate                   func(map[string]any)
		want                     bool
	}{
		{name: "opaque rotation", oldToken: "opaque-before", newToken: "opaque-after", want: true},
		{name: "same subject", oldToken: makeGrokOAuthJWT(map[string]any{"sub": "one", "iat": 1}), newToken: makeGrokOAuthJWT(map[string]any{"sub": "one", "iat": 2}), want: true},
		{name: "different subject", oldToken: makeGrokOAuthJWT(map[string]any{"sub": "one"}), newToken: makeGrokOAuthJWT(map[string]any{"sub": "two"}), want: false},
		{name: "different issuer", oldToken: makeGrokOAuthJWT(map[string]any{"iss": "one"}), newToken: makeGrokOAuthJWT(map[string]any{"iss": "two"}), want: false},
		{name: "different base", oldToken: "before", newToken: "after", mutate: func(c map[string]any) { c["base_url"] = "https://other.example" }, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &Account{ID: 42, Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": tc.oldToken}}
			stamp := time.Now().UTC().Format(time.RFC3339Nano)
			scope := grokSubscriptionScope(a)
			a.Extra = map[string]any{grokSubscriptionExtraKey: map[string]any{"tier": "free", "display_name": "Free", "source": "settings", "requested_at": stamp, "observed_at": stamp, "status_code": 200, "scope": scope}}
			next := map[string]any{"access_token": tc.newToken}
			if tc.mutate != nil {
				tc.mutate(next)
			}
			migrated := GrokSubscriptionAfterTokenRotation(a, next)
			require.Equal(t, tc.want, migrated != nil)
			require.Equal(t, scope, liveGrokSubscription(a).Scope, "migration must not mutate caller snapshot")
			if migrated != nil {
				a.Credentials = next
				a.Extra[grokSubscriptionExtraKey] = migrated
				require.True(t, GrokSubscriptionSnapshotValid(a))
			}
		})
	}
}
