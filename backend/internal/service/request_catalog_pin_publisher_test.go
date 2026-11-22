//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Merge acceptance: use the real publisher, without an upstream connection.
func TestRequestCatalogPinPublisherHeadBinding(t *testing.T) {
	s, bs := publishFixture(t, &publishTestStore{})
	ctx := context.Background()
	gw := &OpenAIGatewayService{billingService: bs}
	config := publishConfig(t, s)
	config.Enabled = true
	publishSet(t, s, config)
	publishSupply(s, publishQuote(publishModel, 120, 52, "before"))
	_, err := s.Sync(ctx)
	require.NoError(t, err)
	config = publishConfig(t, s)
	autoRev := config.ActiveRevision
	auto := gw.WithRequestCatalog(ctx)
	config.ManualOverrides[publishModel] = publishManual(t, `{"input":13,"output":68,"cache_write":13,"cache_read":1.3,"cache_write_1h":26}`)
	manualRev := publishSet(t, s, config).ActiveRevision
	manual := gw.WithRequestCatalog(ctx)
	publishSupply(s, publishQuote(publishModel, 136, 52, "after"))
	_, err = s.Sync(ctx)
	require.NoError(t, err)
	config = publishConfig(t, s)
	delete(config.ManualOverrides, publishModel)
	restoreRev := publishSet(t, s, config).ActiveRevision
	restored := gw.WithRequestCatalog(ctx)
	for _, tc := range []struct {
		ctx         context.Context
		rev, origin string
		want        float64
	}{{restored, restoreRev, "auto", 136}, {manual, manualRev, "manual", 68}, {auto, autoRev, "auto", 120}} {
		worker := CopyRequestPricing(tc.ctx, ctx)
		cost, e := bs.CalculateCostUnified(CostInput{Ctx: worker, Model: publishModel, Tokens: UsageTokens{OutputTokens: 1000000}, RateMultiplier: 1})
		require.NoError(t, e)
		require.InDelta(t, tc.want, cost.ActualCost, 1e-9)
		trace := RequestCatalogFromContext(worker).trace(cost, publishModel, nil)
		require.Equal(t, tc.rev, trace.Revision)
		require.Equal(t, tc.origin, trace.Basis.Row.Origin)
		require.NotEmpty(t, trace.Basis.Row.Digest)
		require.NotEmpty(t, trace.BaseHash)
	}
}
