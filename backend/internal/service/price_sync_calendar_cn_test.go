//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPriceSyncHolidayCalendarCoverage(t *testing.T) {
	for _, tc := range []struct {
		instant        string
		holiday, known bool
	}{
		{"2025-12-31T15:59:59Z", false, false},
		{"2025-12-31T16:00:00Z", true, true},
		{"2026-10-07T15:59:59Z", true, true},
		{"2026-10-07T16:00:00Z", false, true},
		{"2027-06-09T03:00:00Z", true, true},
		{"2027-09-15T03:00:00Z", true, true},
		{"2027-12-31T15:59:59Z", false, true},
		{"2027-12-31T16:00:00Z", false, false},
	} {
		at, err := time.Parse(time.RFC3339, tc.instant)
		require.NoError(t, err)
		h, k := chinaTariffHoliday(at)
		require.Equal(t, tc.holiday, h, tc.instant)
		require.Equal(t, tc.known, k, tc.instant)
	}
}

// USD fallback is a control for the date decision, not a source of CNY tariffs.
func TestPriceSyncHolidayAtDebit(t *testing.T) {
	bs := newTestBillingService()
	r := NewModelPricingResolver(nil, bs)
	for _, tc := range []struct {
		name       string
		instant    string
		outputCost float64
	}{
		{"mid-autumn Friday", "2026-09-25T02:00:00Z", 0.6},
		{"National Day holiday Monday", "2026-10-05T07:00:00Z", 0.6},
		{"first working Thursday after holiday", "2026-10-08T02:00:00Z", 1.2},
		{"make-up work Saturday", "2026-10-10T02:00:00Z", 0.6},
		{"Spring Festival Thursday", "2027-02-11T07:00:00Z", 0.6},
		{"working Monday after Spring Festival", "2027-02-15T02:00:00Z", 1.2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at, err := time.Parse(time.RFC3339, tc.instant)
			require.NoError(t, err)
			bill, err := bs.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: "deepseek-v4-flash", Resolver: r, PricingAt: at, RateMultiplier: 1, Tokens: UsageTokens{OutputTokens: 1_000_000}})
			require.NoError(t, err)
			require.InDelta(t, tc.outputCost, bill.ActualCost, 1e-12)
		})
	}
}
