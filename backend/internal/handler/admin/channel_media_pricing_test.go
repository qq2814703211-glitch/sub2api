//go:build unit

package admin

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin/binding"
	"github.com/stretchr/testify/require"
)

func TestMediaPricingAdminChannelAndAccountStatsRoundTrip(t *testing.T) {
	const raw = `{"platform":"openai","models":["fixture-video"],"billing_mode":"video","media_pricing":{"extra_input_image_price":0.2,"free_input_images":5,"duration_source":"total","rules":[{"name":"silent-reference","unit":"second","conditions":{"resolution":"1080p","mode":"pro","input_type":"image","audio":false,"reference_video":true,"layer":false,"min_pixels":0,"max_pixels":2073600,"min_duration":0,"max_duration":10,"fps":24,"min_fps":24,"max_fps":24},"price":0,"prompt_price":0.03}]}}`
	for _, allowMultipliers := range []bool{true, false} {
		var request channelModelPricingRequest
		require.NoError(t, json.Unmarshal([]byte(raw), &request))
		require.NoError(t, binding.Validator.ValidateStruct(request))
		converted := pricingRequestToService([]channelModelPricingRequest{request}, allowMultipliers)
		require.Len(t, converted, 1)
		require.NoError(t, service.ValidateMediaPricing(converted[0].MediaPricing))
		require.Equal(t, request.MediaPricing, converted[0].MediaPricing)
		response := pricingToResponse(&converted[0])
		require.Equal(t, request.MediaPricing, response.MediaPricing)
		encoded, err := json.Marshal(response)
		require.NoError(t, err)
		var saved channelModelPricingRequest
		require.NoError(t, json.Unmarshal(encoded, &saved))
		require.Equal(t, request.MediaPricing, saved.MediaPricing)
		*response.MediaPricing.Rules[0].Price = 99
		require.Zero(t, *converted[0].MediaPricing.Rules[0].Price)
		*converted[0].MediaPricing.Rules[0].Conditions.Audio = true
		require.False(t, *request.MediaPricing.Rules[0].Conditions.Audio)
	}
}

func TestMediaPricingAdminGroupJSONRoundTrip(t *testing.T) {
	// Group requests and responses use ChannelModelPricing directly.
	var pricing service.ChannelModelPricing
	require.NoError(t, json.Unmarshal([]byte(`{"models":["fixture-image"],"media_pricing":{"rules":[{"name":"zero","unit":"image","conditions":{"audio":false},"price":0}]}}`), &pricing))
	require.NotNil(t, pricing.MediaPricing.Rules[0].Price)
	require.Zero(t, *pricing.MediaPricing.Rules[0].Price)
	require.NotNil(t, pricing.MediaPricing.Rules[0].Conditions.Audio)
	require.False(t, *pricing.MediaPricing.Rules[0].Conditions.Audio)
	var legacy channelModelPricingRequest
	require.NoError(t, json.Unmarshal([]byte(`{"models":["legacy"]}`), &legacy))
	require.Nil(t, pricingRequestToService([]channelModelPricingRequest{legacy}, true)[0].MediaPricing)
}
