package repository

import (
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func marshalMediaPricing(pricing *service.MediaPricing) (any, error) {
	if pricing == nil {
		return nil, nil
	}
	if err := service.ValidateMediaPricing(pricing); err != nil {
		return nil, err
	}
	data, err := json.Marshal(pricing)
	if err != nil {
		return nil, fmt.Errorf("marshal media pricing: %w", err)
	}
	return string(data), nil
}

func unmarshalMediaPricing(data []byte) (*service.MediaPricing, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var pricing *service.MediaPricing
	if err := json.Unmarshal(data, &pricing); err != nil {
		return nil, fmt.Errorf("unmarshal media pricing: %w", err)
	}
	if err := service.ValidateMediaPricing(pricing); err != nil {
		return nil, fmt.Errorf("stored media pricing: %w", err)
	}
	return pricing, nil
}
