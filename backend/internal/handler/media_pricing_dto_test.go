//go:build unit

package handler

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestMediaPricingUserPlazaDTO(t *testing.T) {
	var pricing service.ChannelModelPricing
	require.NoError(t, json.Unmarshal([]byte(`{"billing_mode":"image","media_pricing":{"extra_input_image_price":0,"free_input_images":0,"duration_source":"output","rules":[{"name":"zero","unit":"image","conditions":{"layer":false},"price":0}]}}`), &pricing))
	dto := toUserPricing(&pricing)
	require.Equal(t, pricing.MediaPricing, dto.MediaPricing)
	encoded, err := json.Marshal(dto)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"media_pricing"`)
	require.Contains(t, string(encoded), `"price":0`)
	require.Contains(t, string(encoded), `"layer":false`)
	*dto.MediaPricing.ExtraInputImagePrice = 3
	*dto.MediaPricing.FreeInputImages = 2
	require.Zero(t, *pricing.MediaPricing.ExtraInputImagePrice)
	require.Zero(t, *pricing.MediaPricing.FreeInputImages)
	*dto.MediaPricing.Rules[0].Price = 5
	require.Zero(t, *pricing.MediaPricing.Rules[0].Price)
}
