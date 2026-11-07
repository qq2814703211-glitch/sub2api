package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAdminGrokSubscriptionManagedWrites(t *testing.T) {
	for _, value := range []any{nil, map[string]any{"tier": "supergrok_heavy", "requested_at": "old"}} {
		t.Run("create", func(t *testing.T) {
			repo := &inMemoryAccountRepo{}
			created, err := (&adminServiceImpl{accountRepo: repo}).CreateAccount(context.Background(), &CreateAccountInput{Name: "grok", Platform: PlatformGrok, Type: AccountTypeOAuth, Extra: map[string]any{grokSubscriptionExtraKey: value, "custom": "keep"}, SkipDefaultGroupBind: true})
			require.NoError(t, err)
			require.NotContains(t, created.Extra, grokSubscriptionExtraKey)
			require.Equal(t, "keep", created.Extra["custom"])
		})
		t.Run("extra", func(t *testing.T) {
			latest := map[string]any{"tier": "free", "requested_at": "new"}
			repo := &inMemoryAccountRepo{accounts: map[int64]*Account{1: {ID: 1, Platform: PlatformGrok, Type: AccountTypeOAuth, Extra: map[string]any{grokSubscriptionExtraKey: latest}}}}
			require.NoError(t, (&adminServiceImpl{accountRepo: repo}).UpdateAccountExtra(context.Background(), 1, map[string]any{grokSubscriptionExtraKey: value, "custom": "keep"}))
			require.Equal(t, latest, repo.accounts[1].Extra[grokSubscriptionExtraKey])
			require.Equal(t, "keep", repo.accounts[1].Extra["custom"])
		})
		t.Run("bulk", func(t *testing.T) {
			repo := &inMemoryAccountRepo{}
			result, err := (&adminServiceImpl{accountRepo: repo}).BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Extra: map[string]any{grokSubscriptionExtraKey: value, "custom": "keep"}})
			require.NoError(t, err)
			require.Equal(t, 1, result.Success)
			require.NotContains(t, repo.bulkUpdates[0].Extra, grokSubscriptionExtraKey)
			require.Equal(t, "keep", repo.bulkUpdates[0].Extra["custom"])
		})
	}
}
