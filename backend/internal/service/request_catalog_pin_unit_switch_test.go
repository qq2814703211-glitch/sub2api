//go:build unit

package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Install in internal/service after integrating the units lane (unit tag).
func TestRequestCatalogPinUnitsSwitchDuringFlight(t *testing.T) {
	s, in, logs, ledger := unitCatalogGateway(t, `"example-call":{"glory_billing_mode":"per_request","cost_per_request":0.375}`)
	ctx := context.Background()
	old := s.WithRequestCatalog(ctx)
	p := s.billingService.pricingService
	file := filepath.Join(t.TempDir(), "replacement.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"example-call":{"glory_billing_mode":"per_image","output_cost_per_image":2.5}}`), 0600))
	require.NoError(t, p.loadPricingData(file))
	fresh := s.WithRequestCatalog(ctx)
	in.Result.ImageCount, in.Result.ImageEndpoint = 2, true
	for _, tc := range []struct {
		start context.Context
		cost  float64
		mode  string
	}{{fresh, 10, "per_image"}, {old, 0.75, "per_request"}} {
		require.NoError(t, s.RecordUsage(CopyRequestPricing(tc.start, ctx), in))
		require.Equal(t, tc.cost, ledger.lastCmd.BalanceCost)
		require.Equal(t, tc.cost, ledger.lastCmd.APIKeyQuotaCost)
		require.Equal(t, tc.mode, *logs.lastLog.BillingMode)
		require.Equal(t, "example-call", logs.lastLog.PriceTrace.Basis.Row.SKU)
		require.NotEmpty(t, logs.lastLog.PriceTrace.Basis.Row.Digest)
	}
}
