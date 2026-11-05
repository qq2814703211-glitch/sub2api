//go:build unit

package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// Synthetic prices in the site's settlement unit, not live supplier invoices.
func TestMediaReferenceInputFeeOncePerSuccessfulRequest(t *testing.T) {
	var pricing MediaPricing
	require.NoError(t, json.Unmarshal([]byte(`{"extra_input_image_price":0.02,"free_input_images":1,"rules":[{"unit":"image","price":0.3}]}`), &pricing))
	var usage MediaUsage
	require.NoError(t, json.Unmarshal([]byte(`{"count":2,"input_image_count":2,"images":[{"count":1},{"count":1}]}`), &usage))
	cost, err := CalculateMediaPricingCost(&pricing, &usage, UsageTokens{}, 2)
	require.NoError(t, err)
	require.InDelta(t, 0.02, cost.ImageInputCost, 1e-12)
	require.InDelta(t, 0.6, cost.ImageOutputCost, 1e-12)
	require.InDelta(t, 0.62, cost.TotalCost, 1e-12)
	require.InDelta(t, 1.24, cost.ActualCost, 1e-12)
}

func TestMediaReferenceMiniMaxTotalDuration(t *testing.T) {
	var pricing MediaPricing
	require.NoError(t, json.Unmarshal([]byte(`{"duration_source":"total","extra_input_image_price":0.2,"free_input_images":5,"rules":[{"unit":"second","price":0.5}]}`), &pricing))
	var usage MediaUsage
	require.NoError(t, json.Unmarshal([]byte(`{"count":1,"duration_seconds":4,"input_duration_seconds":3,"total_duration_seconds":7,"input_image_count":6}`), &usage))
	cost, err := CalculateMediaPricingCost(&pricing, &usage, UsageTokens{}, 2)
	require.NoError(t, err)
	require.InDelta(t, 3.5, cost.OutputCost, 1e-12)
	require.InDelta(t, 0.2, cost.ImageInputCost, 1e-12)
	require.InDelta(t, 7.4, cost.ActualCost, 1e-12)
}

func TestMediaReferenceInputBoundariesUnified(t *testing.T) {
	for _, tc := range []struct {
		name      string
		count     *int
		successes int
		free      *int
		price     *float64
		want      float64
		missing   bool
	}{
		{"first output charged zero inputs", mediaTestPtr(0), 1, mediaTestPtr(1), mediaTestPtr(0.02), 0.3, false},
		{"allowance", mediaTestPtr(1), 1, mediaTestPtr(1), mediaTestPtr(0.02), 0.3, false},
		{"excess", mediaTestPtr(2), 1, mediaTestPtr(1), mediaTestPtr(0.02), 0.32, false},
		{"many", mediaTestPtr(10), 1, mediaTestPtr(1), mediaTestPtr(0.02), 0.48, false},
		{"explicit zero allowance", mediaTestPtr(2), 1, mediaTestPtr(0), mediaTestPtr(0.02), 0.34, false},
		{"default allowance zero", mediaTestPtr(2), 1, nil, mediaTestPtr(0.02), 0.34, false},
		{"explicit zero price", mediaTestPtr(2), 1, mediaTestPtr(1), mediaTestPtr(0.0), 0.3, false},
		{"legacy unknown count", nil, 1, nil, nil, 0.3, false},
		{"unknown billable inputs", nil, 1, mediaTestPtr(1), mediaTestPtr(0.02), 0, true},
		{"unknown explicit zero price", nil, 1, nil, mediaTestPtr(0.0), 0, true},
		{"all failed", nil, 0, mediaTestPtr(1), mediaTestPtr(0.02), 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pricing := mediaTestPricing("image", 0.3, MediaPricingConditions{})
			pricing.FreeInputImages = tc.free
			pricing.ExtraInputImagePrice = tc.price
			group := &Group{ModelPricing: []ChannelModelPricing{{Models: []string{"fixture"}, BillingMode: BillingModeImage, MediaPricing: pricing}}}
			billing := &BillingService{}
			cost, err := billing.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: "fixture", Group: group, Resolver: NewModelPricingResolver(nil, billing), RateMultiplier: 2, MediaUsage: &MediaUsage{Count: tc.successes, InputImageCount: tc.count}})
			if tc.missing {
				require.ErrorIs(t, err, ErrMediaUsageIncomplete)
				require.Nil(t, cost)
				return
			}
			require.NoError(t, err)
			require.InDelta(t, tc.want, cost.TotalCost, 1e-12)
			require.InDelta(t, tc.want*2, cost.ActualCost, 1e-12)
		})
	}
}

func TestMediaReferenceDurationSourcesAndUnknown(t *testing.T) {
	for _, tc := range []struct {
		name    string
		source  MediaBillingDuration
		usage   MediaUsage
		want    float64
		missing bool
	}{
		{"default output", "", MediaUsage{Count: 1, DurationSeconds: 4, InputDurationSeconds: mediaTestPtr(3.0), TotalDurationSeconds: mediaTestPtr(7.0)}, 2, false},
		{"explicit output", MediaBillingDurationOutput, MediaUsage{Count: 1, DurationSeconds: 4, TotalDurationSeconds: mediaTestPtr(7.0)}, 2, false},
		{"total authoritative", MediaBillingDurationTotal, MediaUsage{Count: 1, TotalDurationSeconds: mediaTestPtr(7.0)}, 3.5, false},
		{"no double addition", MediaBillingDurationTotal, MediaUsage{Count: 1, DurationSeconds: 4, InputDurationSeconds: mediaTestPtr(3.0), TotalDurationSeconds: mediaTestPtr(7.0)}, 3.5, false},
		{"sum exact", MediaBillingDurationTotal, MediaUsage{Count: 1, DurationSeconds: 4, InputDurationSeconds: mediaTestPtr(3.0)}, 3.5, false},
		{"no video", MediaBillingDurationTotal, MediaUsage{Count: 1, DurationSeconds: 4, InputDurationSeconds: mediaTestPtr(0.0)}, 2, false},
		{"input unknown", MediaBillingDurationTotal, MediaUsage{Count: 1, DurationSeconds: 4}, 0, true},
		{"output unknown", MediaBillingDurationTotal, MediaUsage{Count: 1, InputDurationSeconds: mediaTestPtr(3.0)}, 0, true},
		{"zero total not absent", MediaBillingDurationTotal, MediaUsage{Count: 1, DurationSeconds: 4, InputDurationSeconds: mediaTestPtr(3.0), TotalDurationSeconds: mediaTestPtr(0.0)}, 0, true},
		{"failed", MediaBillingDurationTotal, MediaUsage{}, 0, false},
		{"total request once", MediaBillingDurationTotal, MediaUsage{Count: 2, TotalDurationSeconds: mediaTestPtr(7.0), Images: []MediaUsage{{Count: 1}, {Count: 1}}}, 3.5, false},
		{"input seconds once across outputs", MediaBillingDurationTotal, MediaUsage{Count: 2, InputDurationSeconds: mediaTestPtr(3.0), Images: []MediaUsage{{Count: 1, DurationSeconds: 4}, {Count: 1, DurationSeconds: 5}}}, 6, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := mediaTestPricing("second", 0.5, MediaPricingConditions{})
			p.BillingDuration = tc.source
			cost, err := CalculateMediaPricingCost(p, &tc.usage, UsageTokens{}, 1)
			if tc.missing {
				require.ErrorIs(t, err, ErrMediaUsageIncomplete)
				return
			}
			require.NoError(t, err)
			require.InDelta(t, tc.want, cost.TotalCost, 1e-12)
		})
	}
}

func TestMediaReferenceConfigValidationCloneAndCache(t *testing.T) {
	p := mediaTestPricing("second", 0.5, MediaPricingConditions{})
	p.ExtraInputImagePrice = mediaTestPtr(0.2)
	p.FreeInputImages = mediaTestPtr(5)
	p.BillingDuration = MediaBillingDurationTotal
	for _, edit := range []func(*MediaPricing){
		func(p *MediaPricing) { p.ExtraInputImagePrice = mediaTestPtr(-1.0) },
		func(p *MediaPricing) { p.ExtraInputImagePrice = mediaTestPtr(math.Inf(1)) },
		func(p *MediaPricing) { p.ExtraInputImagePrice = mediaTestPtr(math.NaN()) },
		func(p *MediaPricing) { p.FreeInputImages = mediaTestPtr(-1) },
		func(p *MediaPricing) { p.BillingDuration = "input" },
		func(p *MediaPricing) { p.Rules[0].Unit = "image" },
	} {
		copy := p.Clone()
		edit(copy)
		require.Error(t, ValidateMediaPricing(copy))
	}
	require.NoError(t, ValidateMediaPricing(p))
	ch := Channel{ID: 1, Status: StatusActive, GroupIDs: []int64{2}, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"fixture"}, MediaPricing: p}}}
	clone := ch.Clone()
	*clone.ModelPricing[0].MediaPricing.FreeInputImages = 0
	*clone.ModelPricing[0].MediaPricing.ExtraInputImagePrice = 0
	require.Equal(t, 5, *p.FreeInputImages)
	require.Equal(t, 0.2, *p.ExtraInputImagePrice)
	group := &Group{ModelPricing: ch.ModelPricing}
	duplicated := cloneGroupForDuplicate(group, "fixture")
	*duplicated.ModelPricing[0].MediaPricing.FreeInputImages = 0
	require.Equal(t, 5, *p.FreeInputImages)
	cs := &ChannelService{}
	cs.cache.Store(populateChannelCache([]Channel{ch}, map[int64]string{2: PlatformOpenAI}))
	// Cache lookups return independent cards; mutation cannot change settlement.
	first := cs.GetChannelModelPricing(context.Background(), 2, "fixture")
	require.NotNil(t, first)
	require.Equal(t, p, first.MediaPricing)
	*first.MediaPricing.ExtraInputImagePrice = 7
	require.Equal(t, 0.2, *cs.GetChannelModelPricing(context.Background(), 2, "fixture").MediaPricing.ExtraInputImagePrice)
}
