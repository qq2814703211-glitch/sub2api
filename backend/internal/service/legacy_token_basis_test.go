//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestDirectTokenPricingRecordsAppliedSource(t *testing.T) {
	const model = "basis-test-model"
	catalog := newStubPricingServiceFromJSON(t, `{"basis-test-model":{"input_cost_per_token":2,"output_cost_per_token":1}}`)
	billing := NewBillingService(&config.Config{}, catalog)
	usage := UsageTokens{InputTokens: 3, OutputTokens: 1}

	base, err := billing.CalculateCost(model, usage, 1)
	require.NoError(t, err)
	require.Equal(t, 7.0, base.ActualCost)
	require.Equal(t, model, base.priceBasis.Model)
	require.Equal(t, "catalog", base.priceBasis.Scope)
	require.Equal(t, model, base.priceBasis.Row.SKU)
	require.NotEmpty(t, base.priceBasis.Row.Digest)
	require.Zero(t, base.priceBasis.CardID)

	price := 7.0
	updated := time.Date(2026, time.January, 12, 9, 0, 0, 0, time.UTC)
	card := &ChannelModelPricing{ID: 42, InputPrice: &price, UpdatedAt: updated}
	sale, err := billing.calculateCostInternalWithPolicy(model, usage, 1, "", card, true)
	require.NoError(t, err)
	require.Equal(t, 22.0, sale.ActualCost)
	require.Equal(t, model, sale.priceBasis.Model)
	require.Equal(t, PricingSourceChannel, sale.priceBasis.Scope)
	require.Equal(t, base.priceBasis.Row, sale.priceBasis.Row)
	require.Equal(t, int64(42), sale.priceBasis.CardID)
	require.Equal(t, &updated, sale.priceBasis.CardUpdatedAt)
}
