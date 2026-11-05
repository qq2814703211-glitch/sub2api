//go:build unit

package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGeminiCachedChargePreparePreservesSimpleMode(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "free", true: "key-windows-only"}[enabled], func(t *testing.T) {
			logs := &openAIRecordUsageLogRepoStub{inserted: true}
			ledger := &openAIRecordUsageBillingRepoStub{}
			users := &openAIRecordUsageUserRepoStub{}
			subs := &openAIRecordUsageSubRepoStub{}
			gw := newGatewayRecordUsageServiceWithBillingRepoForTest(logs, ledger, users, subs)
			gw.cfg.RunMode = config.RunModeSimple
			gw.cfg.SimpleModeKeyRateLimitEnabled = enabled
			gid := int64(5)
			input := &RecordUsageInput{Result: &ForwardResult{RequestID: "gcache:create:synthetic", Model: "claude-sonnet-4", Usage: ClaudeUsage{InputTokens: 100}},
				APIKey: &APIKey{ID: 1, UserID: 2, Quota: 100, RateLimit5h: 30, GroupID: &gid, Group: &Group{ID: gid, RateMultiplier: 2}},
				User:   &User{ID: 2}, Account: &Account{ID: 3, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "must-not-persist"}}}
			intent, err := gw.PrepareCachedContentCharge(context.Background(), input)
			require.NoError(t, err)
			require.Zero(t, logs.calls)
			require.Zero(t, ledger.calls)
			require.Zero(t, users.deductCalls)
			require.Zero(t, subs.incrementCalls)
			require.True(t, intent.SimpleMode)
			require.Equal(t, enabled, intent.SimpleModeKeyRateLimitOnly)
			require.Zero(t, intent.Command.BalanceCost)
			require.Zero(t, intent.Command.SubscriptionCost)
			require.Zero(t, intent.Command.APIKeyQuotaCost)
			require.Zero(t, intent.Command.AccountQuotaCost)
			require.Nil(t, intent.Usage.Account)
			require.Nil(t, intent.Usage.APIKey)
			require.Nil(t, intent.Usage.User)
			if enabled {
				require.Positive(t, intent.Command.APIKeyRateLimitCost)
			} else {
				require.Zero(t, intent.Command.APIKeyRateLimitCost)
			}
		})
	}
}

func TestGeminiCachedChargePrepareRejectsMissingInput(t *testing.T) {
	gw := &GatewayService{}
	_, err := gw.PrepareCachedContentCharge(context.Background(), nil)
	require.Error(t, err)
	_, err = gw.PrepareCachedContentCharge(context.Background(), &RecordUsageInput{APIKey: &APIKey{}, Account: &Account{}})
	require.Error(t, err)
}
