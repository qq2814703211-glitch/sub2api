package xai

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestNormalizeSubscriptionTierUltraAliases(t *testing.T) {
	for _, value := range []string{"Ultra", "SuperGrok Ultra", "SuperGrokUltra", "supergrok_ultra"} {
		require.Equal(t, "ultra", NormalizeSubscriptionTier(value))
	}
	for _, value := range []string{"plus", "premium", "super"} {
		require.Equal(t, value, NormalizeSubscriptionTier(value))
	}
	require.Equal(t, "supergrok_heavy", NormalizeSubscriptionTier("SuperGrok Heavy"))
	require.Equal(t, "8", MapJWTSubscriptionTier(8))
	require.Equal(t, "9", MapJWTSubscriptionTier(9))
}
