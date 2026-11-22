//go:build unit

package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCatalogUnitsPlazaSellingAndReference(t *testing.T) {
	gw, _, _, _ := unitCatalogGateway(t, `"second-sku":{"glory_billing_mode":"per_second","cost_per_second":0.375}`)
	groups := []Group{{ID: 6, Platform: PlatformOpenAI, RateMultiplier: 2, ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"second-sku"}}}}
	plaza := newPlazaService(nil, groups, gw.billingService.pricingService)
	plaza.billingService = gw.billingService
	plaza.resolver = gw.resolver
	got, err := plaza.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	m := got[0].Models[0]
	require.NotNil(t, m.Pricing)
	require.Equal(t, "per_second", string(m.Pricing.BillingMode))
	require.NotNil(t, m.OfficialPricing)
	require.Equal(t, 0.375, *m.OfficialPricing.UnitPrice)
	require.Equal(t, 0.375, *m.Pricing.CatalogUnitPrice)
	require.Equal(t, 2., got[0].RateMultiplier)
}

func TestCatalogUnitsPlazaChannelUsesMappedSKU(t *testing.T) {
	gw, _, _, _ := unitCatalogGateway(t, `"mapped-sku":{"glory_billing_mode":"per_second","cost_per_second":0.125}`)
	channel := Channel{ID: 7, Status: StatusActive, GroupIDs: []int64{1}, BillingModelSource: BillingModelSourceChannelMapped, ModelMapping: map[string]map[string]string{PlatformOpenAI: {"client-alias": "mapped-sku"}}}
	plaza := newPlazaService([]Channel{channel}, []Group{{ID: 1, Platform: PlatformOpenAI, RateMultiplier: 3}}, gw.billingService.pricingService)
	plaza.billingService = gw.billingService
	plaza.resolver = gw.resolver
	groups, err := plaza.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Len(t, groups[0].Models, 1)
	m := groups[0].Models[0]
	require.Equal(t, "client-alias", m.Name)
	require.NotNil(t, m.Pricing)
	require.Equal(t, BillingModePerSecond, m.Pricing.BillingMode)
	require.Equal(t, 0.125, *m.Pricing.CatalogUnitPrice)
	require.NotNil(t, m.OfficialPricing)
	require.Equal(t, 0.125, *m.OfficialPricing.UnitPrice)
}
