package billing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
)

func timePricingFixture() ServiceTierConfig {
	pointer := func(v float64) *float64 { return &v }
	prices := map[string]ModelPrice{}
	for _, tier := range []string{"default", "priority", "flex"} {
		prices[tier] = ModelPrice{InputPrice: .001, OutputPrice: .003, CacheReadPrice: pointer(.0001), CacheCreation5mPrice: pointer(0), CacheCreation1hPrice: pointer(0)}
	}
	return ServiceTierConfig{Version: 3, Enabled: true, SourceKind: "channel", SourceID: 11, Model: "deepseek-flash", Prices: prices, TimePricing: &TimePricingConfig{Timezone: "Asia/Shanghai", CalendarValidThrough: "2026-12-31", Holidays: []string{"2026-10-30"}, PeakWindows: []PricingTimeWindow{{Weekdays: []int{1, 2, 3, 4, 5}, StartMinute: 540, EndMinute: 720}, {Weekdays: []int{1, 2, 3, 4, 5}, StartMinute: 840, EndMinute: 1080}}, PeakPrices: prices}}
}

func TestTimePricingLocalClockWindowsHolidaysAndExpiry(t *testing.T) {
	c := timePricingFixture()
	require.NoError(t, c.Validate())
	for _, tc := range []struct{ utc, band string }{
		{"2026-10-29T00:59:59Z", "off_peak"}, {"2026-10-29T01:00:00Z", "peak"},
		{"2026-10-29T03:59:59Z", "peak"}, {"2026-10-29T04:00:00Z", "off_peak"},
		{"2026-10-29T05:59:59Z", "off_peak"}, {"2026-10-29T06:00:00Z", "peak"},
		{"2026-10-29T10:00:00Z", "off_peak"}, {"2026-10-30T01:00:00Z", "off_peak"},
		{"2026-10-31T01:00:00Z", "off_peak"}, {"2026-12-31T15:59:59Z", "off_peak"},
	} {
		t.Run(tc.utc, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tc.utc)
			require.NoError(t, err)
			band, err := c.TimePricing.BandAt(now)
			require.NoError(t, err)
			require.Equal(t, tc.band, band)
		})
	}
	_, err := c.TimePricing.BandAt(time.Date(2026, 12, 31, 16, 0, 0, 0, time.UTC))
	require.Error(t, err)
}

func TestTimePricingRejectsIncompleteOrAmbiguousConfiguration(t *testing.T) {
	for _, mutate := range []func(*ServiceTierConfig){
		func(c *ServiceTierConfig) { c.Version = 1 }, func(c *ServiceTierConfig) { c.Version = 2 },
		func(c *ServiceTierConfig) { c.TimePricing.Timezone = "Local" }, func(c *ServiceTierConfig) { c.TimePricing.Timezone = "Invalid/Zone" },
		func(c *ServiceTierConfig) { c.TimePricing.CalendarValidThrough = "2026-2-2" },
		func(c *ServiceTierConfig) { c.TimePricing.Holidays = []string{"2027-01-01"} },
		func(c *ServiceTierConfig) { c.TimePricing.Holidays = []string{"2026-10-30", "2026-10-30"} },
		func(c *ServiceTierConfig) {
			c.TimePricing.PeakWindows = append(c.TimePricing.PeakWindows, c.TimePricing.PeakWindows[0])
		},
		func(c *ServiceTierConfig) { c.TimePricing.PeakWindows[0].EndMinute = 500 },
		func(c *ServiceTierConfig) { c.TimePricing.PeakWindows[0].Weekdays = []int{0} },
		func(c *ServiceTierConfig) { c.TimePricing.PeakPrices["default"] = ModelPrice{} },
	} {
		c := timePricingFixture()
		mutate(&c)
		require.Error(t, c.Validate())
	}
	c := timePricingFixture()
	raw, err := jsonx.Marshal(c)
	require.NoError(t, err)
	_, err = ParseServiceTierConfig(string(raw))
	require.NoError(t, err)
	var obj map[string]any
	require.NoError(t, jsonx.Unmarshal(raw, &obj))
	obj["time_pricing"].(map[string]any)["peak_prices"].(map[string]any)["default"].(map[string]any)["output_price"] = nil
	raw, err = jsonx.Marshal(obj)
	require.NoError(t, err)
	_, err = ParseServiceTierConfig(string(raw))
	require.Error(t, err)
}

func TestTimePricingProjectionDoesNotMutateFrozenConfiguration(t *testing.T) {
	c := timePricingFixture()
	peak := c.TimePricing.PeakPrices["default"]
	peak.InputPrice = 2
	c.TimePricing.PeakPrices = map[string]ModelPrice{"default": peak, "priority": peak, "flex": peak}
	digest := c.Digest()
	selected := c.ForTimeBand("peak")
	require.Equal(t, 2.0, selected.Prices["default"].InputPrice)
	require.Equal(t, .001, c.Prices["default"].InputPrice)
	require.Equal(t, digest, c.Digest())
	require.Equal(t, c.Prices, c.ForTimeBand("off_peak").Prices)
}
