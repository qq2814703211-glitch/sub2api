//go:build unit

package handler

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestPlazaMediaRateOptions(t *testing.T) {
	var pricing service.ChannelModelPricing
	require.NoError(t, json.Unmarshal([]byte(`{"billing_mode":"per_request","media_pricing":{"free_input_images":1,"extra_input_image_price":0.25,"duration_source":"output","rules":[{"unit":"request","price":2},{"unit":"token","input_price":0,"output_price":0.01}]}}`), &pricing))
	group := service.PlazaGroup{ID: 10, RateMultiplier: 0.1, ImageRateIndependent: true, ImageRateMultiplier: 0.5, VideoRateIndependent: true, VideoRateMultiplier: 0.8, Models: []service.PlazaModel{{Name: "opaque-name", Platform: service.PlatformOpenAI, Pricing: &pricing}}}
	dto := toModelPlazaGroupDTO(&group, map[int64]float64{10: 0.2})
	require.Equal(t, []modelPlazaMediaRateOption{{"general", 0.2}, {"image", 0.5}, {"video", 0.8}}, dto.Models[0].MediaRateOptions)
	for _, mode := range []service.BillingMode{service.BillingModeImage, service.BillingModeVideo, service.BillingModePerRequest} {
		pricing.BillingMode = mode
		require.Equal(t, dto.Models[0].MediaRateOptions, toModelPlazaGroupDTO(&group, map[int64]float64{10: 0.2}).Models[0].MediaRateOptions)
	}
	pricing.BillingMode = service.BillingModeToken
	require.Equal(t, []modelPlazaMediaRateOption{{"all", 0.2}}, toModelPlazaGroupDTO(&group, map[int64]float64{10: 0.2}).Models[0].MediaRateOptions)
	pricing.BillingMode = service.BillingModePerRequest
	if dir := os.Getenv("UI_REVIEW_OUTPUT"); dir != "" {
		wire, err := json.Marshal(dto)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "ui-plaza-rates-dto.json"), wire, 0600))
	}
}

// Consumes the actual service output when executing the cross-layer suite.
func TestPlazaMediaServiceDTO(t *testing.T) {
	dir := os.Getenv("UI_REVIEW_OUTPUT")
	if dir == "" {
		t.Skip("cross-layer fixture output not requested")
	}
	wire, err := os.ReadFile(filepath.Join(dir, "plaza-token.json"))
	require.NoError(t, err)
	var fixture struct {
		Name        string                       `json:"name"`
		Platform    string                       `json:"platform"`
		Pricing     *service.ChannelModelPricing `json:"pricing"`
		TimePricing *service.TimePricingSchedule `json:"time_pricing"`
	}
	require.NoError(t, json.Unmarshal(wire, &fixture))
	group := service.PlazaGroup{ID: 10, Name: "synthetic-group", Platform: service.PlatformOpenAI, RateMultiplier: 0.5, Models: []service.PlazaModel{{Name: fixture.Name, Platform: fixture.Platform, Pricing: fixture.Pricing, TimePricing: fixture.TimePricing}}}
	dto := toModelPlazaGroupDTO(&group, nil)
	require.NotNil(t, dto.Models[0].TimePricing)
	require.Equal(t, 3.0, dto.Models[0].TimePricing.Periods[0].Multiplier)
	encoded, err := json.Marshal(dto)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ui-plaza-actual-dto.json"), encoded, 0600))
}

func TestPlazaEndpointProductionDTO(t *testing.T) {
	dir := os.Getenv("UI_REVIEW_OUTPUT")
	if dir == "" {
		t.Skip("cross-layer output not requested")
	}
	for _, kind := range []string{"image", "video", "general", "token"} {
		wire, err := os.ReadFile(filepath.Join(dir, "endpoint-"+kind+".json"))
		require.NoError(t, err)
		var fixture struct {
			Pricing     *service.ChannelModelPricing `json:"pricing"`
			TimePricing *service.TimePricingSchedule `json:"time_pricing"`
			ActualCost  float64                      `json:"actual_cost"`
		}
		require.NoError(t, json.Unmarshal(wire, &fixture))
		g := service.PlazaGroup{ID: 10, RateMultiplier: 0.1, ImageRateIndependent: true, ImageRateMultiplier: 0.5, VideoRateIndependent: true, VideoRateMultiplier: 0.8, Models: []service.PlazaModel{{Name: "opaque-name", Platform: service.PlatformOpenAI, Pricing: fixture.Pricing, TimePricing: fixture.TimePricing}}}
		dto := toModelPlazaGroupDTO(&g, map[int64]float64{10: 0.2})
		require.NotNil(t, dto.Models[0].TimePricing)
		encoded, err := json.Marshal(map[string]any{"group": dto, "actual_cost": fixture.ActualCost})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "dto-endpoint-"+kind+".json"), encoded, 0600))
	}
}

func TestPlazaMediaRateOptionsZeroAndSharedRates(t *testing.T) {
	var p service.ChannelModelPricing
	require.NoError(t, json.Unmarshal([]byte(`{"billing_mode":"per_request","media_pricing":{"rules":[{"unit":"request","price":0}]}}`), &p))
	g := service.PlazaGroup{ID: 10, RateMultiplier: 0.1, ImageRateIndependent: true, ImageRateMultiplier: 0, VideoRateIndependent: true, VideoRateMultiplier: 0.8}
	require.Equal(t, []modelPlazaMediaRateOption{{"general", 0.2}, {"image", 0}, {"video", 0.8}}, toModelPlazaMediaRateOptions(&p, &g, map[int64]float64{10: 0.2}))
	g.ImageRateIndependent = false
	g.VideoRateIndependent = false
	require.Equal(t, []modelPlazaMediaRateOption{{"all", 0.2}}, toModelPlazaMediaRateOptions(&p, &g, map[int64]float64{10: 0.2}))
	p.MediaPricing = nil
	require.Nil(t, toModelPlazaMediaRateOptions(&p, &g, nil))
}
