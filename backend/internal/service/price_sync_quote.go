package service

import (
	"context"
	"time"
)

// PriceSyncQuote holds the complete managed portion of a model tariff. Rates are in
// native site units per token, per successful request, per image, or per second.
// Missing keys mean unknown or inapplicable; an explicit zero is a real price.
type PriceSyncQuote struct {
	Model       string             `json:"model"`
	Provider    string             `json:"provider"`
	Currency    string             `json:"currency"`
	BillingMode string             `json:"billing_mode"`
	Rates       map[string]float64 `json:"rates"`
	Tiers       []PriceSyncTier    `json:"tiers,omitempty"`
	Windows     []PriceSyncWindow  `json:"windows,omitempty"`
	SourceURL   string             `json:"source_url"`
	Evidence    string             `json:"evidence"`
	VerifiedAt  time.Time          `json:"verified_at"`
}

// A context threshold selects a whole-request tariff, not marginal blocks.
type PriceSyncTier struct {
	Threshold int                `json:"threshold"`
	Inclusive bool               `json:"inclusive,omitempty"`
	Rates     map[string]float64 `json:"rates"`
}

// Weekdays follow time.Weekday (Sunday=0); times are HH:MM in Timezone.
type PriceSyncWindow struct {
	Timezone          string             `json:"timezone"`
	Weekdays          []int              `json:"weekdays,omitempty"`
	Start             string             `json:"start"`
	End               string             `json:"end"`
	ExcludeCNHolidays bool               `json:"exclude_cn_holidays,omitempty"`
	Rates             map[string]float64 `json:"rates"`
}

// NativeModels is a currency-policy decision independent of whether a source
// returned a usable price. Problems identify rows not safe for publication.
type PriceSyncCollection struct {
	Quotes       map[string]PriceSyncQuote `json:"quotes"`
	NativeModels map[string]bool           `json:"native_models"`
	Problems     map[string]string         `json:"problems"`
	SourceErrors map[string]string         `json:"source_errors"`
	// LegacyUSDEstimates names exact SKUs whose selected row on a non-USD
	// billing endpoint declares no model-level currency. The source's own
	// currency contract classifies such a row as an upstream USD estimate, not
	// as a CNY tariff. This is provenance for quarantining a legacy automatic
	// row only; it never makes the row publishable.
	LegacyUSDEstimates map[string]string `json:"legacy_usd_estimates,omitempty"`
}

// The publisher owns durable history and activation, not network fetches.
type PriceSyncCollector func(context.Context, modelPricingSyncFetcher, string, *CNYOfficialPricingSettings) (PriceSyncCollection, error)
