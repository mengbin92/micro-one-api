package billing

import (
	"bytes"
	"fmt"
	"micro-one-api/pkg/jsonx"
	"time"
	_ "time/tzdata" // Minimal production images do not install system zoneinfo.
)

const TimePricingVersion int32 = 3

// TimePricingConfig treats the root tariff as off-peak. Every peak override
// is complete, so an absent procurement price stays unknown. Calendars expire
// explicitly instead of silently charging with an outdated holiday schedule.
type TimePricingConfig struct {
	Timezone                      string                `json:"timezone"`
	CalendarValidThrough          string                `json:"calendar_valid_through"`
	Holidays                      []string              `json:"holidays"`
	PeakWindows                   []PricingTimeWindow   `json:"peak_windows"`
	PeakPrices                    map[string]ModelPrice `json:"peak_prices"`
	PeakUpstreamPrices            map[string]ModelPrice `json:"peak_upstream_prices,omitempty"`
	PeakLongContextPrices         map[string]ModelPrice `json:"peak_long_context_prices,omitempty"`
	PeakLongContextUpstreamPrices map[string]ModelPrice `json:"peak_long_context_upstream_prices,omitempty"`
}

type PricingTimeWindow struct {
	Weekdays    []int `json:"weekdays"`     // ISO weekdays, Monday=1, Sunday=7.
	StartMinute int   `json:"start_minute"` // Inclusive, local wall-clock time.
	EndMinute   int   `json:"end_minute"`   // Exclusive; split overnight windows.
}

func validateTimePricingJSON(raw jsonx.RawMessage) error {
	var object map[string]jsonx.RawMessage
	if jsonx.Unmarshal(raw, &object) != nil || object == nil {
		return fmt.Errorf("time_pricing must be an object")
	}
	var holidays []string
	if jsonx.Unmarshal(object["holidays"], &holidays) != nil || holidays == nil {
		return fmt.Errorf("time_pricing requires an explicit holidays array (empty is allowed)")
	}
	for _, field := range []string{"peak_prices", "peak_upstream_prices", "peak_long_context_prices", "peak_long_context_upstream_prices"} {
		value, ok := object[field]
		if !ok && field != "peak_prices" {
			continue
		}
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("time_pricing %s requires prices", field)
		}
		if err := validateLongContextJSON(append(append([]byte(`{"prices":`), value...), '}')); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	}
	var windows []map[string]jsonx.RawMessage
	if jsonx.Unmarshal(object["peak_windows"], &windows) != nil {
		return fmt.Errorf("invalid peak_windows")
	}
	for _, window := range windows {
		for _, field := range []string{"start_minute", "end_minute"} {
			var minute *int
			if jsonx.Unmarshal(window[field], &minute) != nil || minute == nil {
				return fmt.Errorf("peak window requires %s", field)
			}
		}
	}
	return nil
}

func pricingDate(value string) bool {
	t, err := time.Parse("2006-01-02", value)
	return err == nil && t.Format("2006-01-02") == value
}

func (c *TimePricingConfig) Validate(longContext *LongContextConfig) error {
	if c == nil {
		return fmt.Errorf("time_pricing is required")
	}
	if c.Timezone == "" || c.Timezone == "Local" {
		return fmt.Errorf("time_pricing requires an explicit timezone")
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return fmt.Errorf("invalid pricing timezone: %w", err)
	}
	if !pricingDate(c.CalendarValidThrough) {
		return fmt.Errorf("time_pricing requires calendar_valid_through YYYY-MM-DD")
	}
	dates := map[string]bool{}
	for _, date := range c.Holidays {
		if !pricingDate(date) || date > c.CalendarValidThrough || dates[date] {
			return fmt.Errorf("invalid or duplicate pricing holiday %q", date)
		}
		dates[date] = true
	}
	if len(c.PeakWindows) == 0 {
		return fmt.Errorf("time_pricing requires peak_windows")
	}
	var occupied [8][1440]bool
	for _, window := range c.PeakWindows {
		if len(window.Weekdays) == 0 || window.StartMinute < 0 || window.StartMinute >= window.EndMinute || window.EndMinute > 1440 {
			return fmt.Errorf("invalid peak window")
		}
		for _, day := range window.Weekdays {
			if day < 1 || day > 7 {
				return fmt.Errorf("invalid ISO pricing weekday")
			}
			for minute := window.StartMinute; minute < window.EndMinute; minute++ {
				if occupied[day][minute] {
					return fmt.Errorf("overlapping peak windows")
				}
				occupied[day][minute] = true
			}
		}
	}
	if err := validateCompleteContextPrices(c.PeakPrices); err != nil {
		return err
	}
	if c.PeakUpstreamPrices != nil {
		if err := validateCompleteContextPrices(c.PeakUpstreamPrices); err != nil {
			return err
		}
	}
	if longContext == nil {
		if c.PeakLongContextPrices != nil || c.PeakLongContextUpstreamPrices != nil {
			return fmt.Errorf("peak long-context prices require long_context")
		}
		return nil
	}
	return (&LongContextConfig{ThresholdTokens: longContext.ThresholdTokens, Policy: longContext.Policy, Prices: c.PeakLongContextPrices, UpstreamPrices: c.PeakLongContextUpstreamPrices}).Validate(c.PeakPrices)
}

func (c *TimePricingConfig) BandAt(now time.Time) (string, error) {
	location, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return "", err
	}
	local := now.In(location)
	date := local.Format("2006-01-02")
	if date > c.CalendarValidThrough {
		return "", fmt.Errorf("pricing calendar expired")
	}
	for _, holiday := range c.Holidays {
		if date == holiday {
			return "off_peak", nil
		}
	}
	day := int(local.Weekday())
	if day == 0 {
		day = 7
	}
	minute := local.Hour()*60 + local.Minute()
	for _, window := range c.PeakWindows {
		for _, weekday := range window.Weekdays {
			if weekday == day && minute >= window.StartMinute && minute < window.EndMinute {
				return "peak", nil
			}
		}
	}
	return "off_peak", nil
}

// ForTimeBand projects only tariffs. The caller retains the original config
// and its digest as immutable evidence; neither the maps nor pointers mutate.
func (c ServiceTierConfig) ForTimeBand(band string) ServiceTierConfig {
	if c.TimePricing == nil || band != "peak" {
		return c
	}
	c.Prices, c.UpstreamPrices = c.TimePricing.PeakPrices, c.TimePricing.PeakUpstreamPrices
	if c.LongContext != nil {
		longContext := *c.LongContext
		longContext.Prices, longContext.UpstreamPrices = c.TimePricing.PeakLongContextPrices, c.TimePricing.PeakLongContextUpstreamPrices
		c.LongContext = &longContext
	}
	return c
}
