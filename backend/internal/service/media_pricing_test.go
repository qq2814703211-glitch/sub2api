//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// Exercise the existing public seam with JSON so the baseline executes rather
// than failing to compile before the optional media fields have been added.
func TestMediaPricingUnifiedConfiguredRulesOverrideScalar(t *testing.T) {
	var group Group
	require.NoError(t, json.Unmarshal([]byte(`{"ModelPricing":[{"models":["fixture-image"],"billing_mode":"image","per_request_price":0.01,"media_pricing":{"rules":[{"name":"output","unit":"image","price":0.2,"prompt_price":0.03,"conditions":{}}]}}]}`), &group))
	var input CostInput
	require.NoError(t, json.Unmarshal([]byte(`{"media_usage":{"count":2}}`), &input))
	billing := &BillingService{}
	input.Ctx = context.Background()
	input.Model = "fixture-image"
	input.Group = &group
	input.RequestCount = 2
	input.RateMultiplier = 2
	input.Resolver = NewModelPricingResolver(nil, billing)
	got, err := billing.CalculateCostUnified(input)
	require.NoError(t, err)
	require.InDelta(t, 0.43, got.TotalCost, 1e-12)
	require.InDelta(t, 0.86, got.ActualCost, 1e-12)
}
