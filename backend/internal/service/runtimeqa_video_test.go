//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type runtimeQAPendingCache struct {
	GatewayCache
	raw map[string][]byte
}

func (c *runtimeQAPendingCache) SetGrokVideoPendingBilling(_ context.Context, k string, v []byte, _ time.Duration) error {
	c.raw[k] = append([]byte(nil), v...)
	return nil
}
func (c *runtimeQAPendingCache) GetGrokVideoPendingBilling(_ context.Context, k string) ([]byte, error) {
	return c.raw[k], nil
}

func TestRuntimeQAVideoOwnerBoundCreationTrace(t *testing.T) {
	const sku = "qa-runtime-video"
	ctx := context.Background()
	pub, billing := publishFixture(t, &publishTestStore{})
	settings := publishConfig(t, pub)
	settings.Enabled = true
	publishSet(t, pub, settings)
	q := publishQuote(sku, 1, 1, "create")
	q.BillingMode = "per_second"
	q.Rates = map[string]float64{"cost_per_second": 0.375}
	publishSupply(pub, q)
	_, err := pub.Sync(ctx)
	require.NoError(t, err)
	createRev := publishConfig(t, pub).ActiveRevision
	gw, in, logs, ledger := unitCatalogGateway(t, `"qa-unused":{"input_cost_per_token":9}`)
	gw.billingService = billing
	gw.resolver = NewModelPricingResolver(nil, billing)
	cache := &runtimeQAPendingCache{raw: map[string][]byte{}}
	gw.cache = cache
	group := in.APIKey.Group
	group.UpdatedAt = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	createCtx := gw.WithRequestCatalog(ctx)
	createPin := RequestCatalogFromContext(createCtx)
	require.Equal(t, createRev, createPin.revision)
	pending := GrokVideoPendingBilling{Model: "public-video", BillingModel: "public-video", UpstreamModel: sku}
	require.NoError(t, gw.FreezeVideoCatalogQuote(createCtx, in.APIKey, in.User.ID, &pending))
	require.NotNil(t, pending.CatalogQuote)
	require.NoError(t, gw.StoreGrokVideoPendingBilling(ctx, "video-task", in.User.ID, in.APIKey.ID, pending))
	foreign, err := gw.LoadGrokVideoPendingBilling(ctx, "video-task", in.User.ID+1, in.APIKey.ID)
	require.NoError(t, err)
	require.Nil(t, foreign)
	settings = publishConfig(t, pub)
	settings.ManualOverrides[sku] = publishManual(t, `{"billing_mode":"per_request","per_request_price":27}`)
	pollRev := publishSet(t, pub, settings).ActiveRevision
	require.NotEqual(t, createRev, pollRev)
	fresh := gw.WithRequestCatalog(ctx)
	require.Equal(t, pollRev, RequestCatalogFromContext(fresh).revision)
	seconds := 2.5
	settle := func(t *testing.T, task string, want float64, mode string) {
		t.Helper()
		loaded, err := gw.LoadGrokVideoPendingBilling(ctx, task, in.User.ID, in.APIKey.ID)
		require.NoError(t, err)
		require.NotNil(t, loaded)
		in.Result = &OpenAIForwardResult{Model: loaded.Model, UpstreamModel: loaded.UpstreamModel, BillingModel: loaded.BillingModel, VideoCount: 1, ProviderBillableSeconds: &seconds, RequestID: StableGrokVideoBillingRequestID(task), FrozenVideoQuote: loaded.CatalogQuote, BillingClaimOwner: "test-lease"}
		require.NoError(t, gw.RecordUsage(fresh, in))
		require.Equal(t, want, ledger.lastCmd.BalanceCost)
		require.Equal(t, want, ledger.lastCmd.APIKeyQuotaCost)
		require.Equal(t, mode, *logs.lastLog.BillingMode)
	}
	t.Run("new_snapshot", func(t *testing.T) {
		settle(t, "video-task", 1.875, "per_second")
		tr := logs.lastLog.PriceTrace
		require.NotNil(t, tr)
		require.Equal(t, createRev, tr.Revision)
		require.Equal(t, sku, tr.Basis.Model)
		require.Equal(t, "catalog", tr.Basis.Scope)
		require.Equal(t, sku, tr.Basis.Row.SKU)
		require.Equal(t, "auto", tr.Basis.Row.Origin)
		require.NotEmpty(t, tr.Basis.Row.Digest)
		require.NotEmpty(t, tr.BaseHash)
		require.Equal(t, createPin.baseHash, tr.BaseHash)
		require.Equal(t, createPin.layersHash, tr.LayersHash)
		require.True(t, createPin.at.Equal(tr.PricingAt))
		require.Equal(t, group.UpdatedAt, *tr.GroupUpdatedAt)
		var stored struct {
			Quote struct {
				Trace *RequestPriceTrace `json:"price_trace"`
			} `json:"catalog_quote"`
		}
		require.NoError(t, json.Unmarshal(cache.raw[grokVideoPendingBillingKey("video-task", in.User.ID, in.APIKey.ID)], &stored))
		require.Equal(t, stored.Quote.Trace, tr)
	})
	t.Run("old_frozen_scalar_without_trace", func(t *testing.T) {
		cache.raw[grokVideoPendingBillingKey("old-task", in.User.ID, in.APIKey.ID)] = []byte(`{"model":"public-video","billing_model":"qa-runtime-video","catalog_quote":{"mode":"per_second","price":0.375,"multiplier":2}}`)
		settle(t, "old-task", 1.875, "per_second")
		require.Nil(t, logs.lastLog.PriceTrace, "poll revision is not evidence for the price frozen by an older binary")
	})
	t.Run("old_pending_without_quote", func(t *testing.T) {
		cache.raw[grokVideoPendingBillingKey("no-quote", in.User.ID, in.APIKey.ID)] = []byte(`{"model":"public-video","upstream_model":"qa-runtime-video"}`)
		settle(t, "no-quote", 54, "per_request")
		require.Nil(t, logs.lastLog.PriceTrace)
	})
}

func TestRuntimeQAVideoFreezeDefersToSellingCard(t *testing.T) {
	for _, scope := range []string{"group", "channel"} {
		t.Run(scope, func(t *testing.T) {
			gw, in, _, _ := unitCatalogGateway(t, `"remote-video":{"glory_billing_mode":"per_second","cost_per_second":0.25}`)
			grp := in.APIKey.Group
			grp.Platform = PlatformOpenAI
			card := ChannelModelPricing{Models: []string{"public-video"}, Platform: PlatformOpenAI, BillingMode: BillingModePerRequest, PerRequestPrice: tariffTestPtr(0.)}
			if scope == "group" {
				grp.ModelPricing = []ChannelModelPricing{card}
			} else {
				cs := &ChannelService{}
				cs.cache.Store(populateChannelCache([]Channel{{ID: 8, Status: StatusActive, GroupIDs: []int64{grp.ID}, ModelPricing: []ChannelModelPricing{card}}}, map[int64]string{grp.ID: grp.Platform}))
				gw.channelService = cs
				gw.resolver = NewModelPricingResolver(cs, gw.billingService)
			}
			pending := GrokVideoPendingBilling{Model: "public-video", BillingModel: "public-video", UpstreamModel: "remote-video"}
			require.NoError(t, gw.FreezeVideoCatalogQuote(context.Background(), in.APIKey, in.User.ID, &pending))
			require.Nil(t, pending.CatalogQuote, "an explicit free selling card must not be replaced by the catalog rate")
		})
	}
}

func TestRuntimeQASeedanceProbeUsesCreateCatalog(t *testing.T) {
	gw, in, _, _ := unitCatalogGateway(t, `"example-call":{"glory_billing_mode":"per_second","cost_per_second":0.25}`)
	gw.SetVideoBillingJobs(&videoJobCaptureRepo{})
	in.APIKey.Group.VideoRateIndependent = true
	in.APIKey.Group.VideoRateMultiplier = 7
	old := gw.WithRequestCatalog(context.Background())
	gw.billingService.pricingService.pricingData = newStubPricingServiceFromJSON(t, `{"example-call":{"input_cost_per_token":9,"output_cost_per_token":9}}`).pricingData
	job, err := gw.BeginVideoBillingJob(old, in, GrokVideoPendingBilling{Model: "example-call"}, "seedance")
	require.NoError(t, err)
	require.Equal(t, 1.75, job.Snapshot.Unit.Command.BalanceCost)
	seconds := 2.5
	job.TaskID = "seedance:original"
	intent, err := videoIntentForCompletion(job, &OpenAIForwardResult{VideoStatus: "succeeded", MediaUsage: &MediaUsage{Count: 1}, ProviderBillableSeconds: &seconds})
	require.NoError(t, err)
	require.Equal(t, 4.375, intent.Command.BalanceCost)
}
