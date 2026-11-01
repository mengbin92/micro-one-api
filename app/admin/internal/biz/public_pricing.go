package biz

import (
	"context"

	"micro-one-api/domain/billing"
)

// PublicServiceTierPricing contains customer tariffs only; procurement and
// internal source identifiers never cross this boundary.
type PublicServiceTierPricing struct {
	Model       string
	Prices      map[string]billing.ModelPrice
	LongContext *PublicLongContextPricing
	TimePricing *PublicTimePricing
}

type PublicLongContextPricing struct {
	ThresholdTokens int64
	Prices          map[string]billing.ModelPrice
}

type PublicTimePricing struct {
	Timezone              string
	CalendarValidThrough  string
	Holidays              []string
	PeakWindows           []billing.PricingTimeWindow
	PeakPrices            map[string]billing.ModelPrice
	PeakLongContextPrices map[string]billing.ModelPrice
}

func (uc *SystemOptionsUsecase) PublicServiceTierPricing(ctx context.Context) (*PublicServiceTierPricing, error) {
	if uc == nil || uc.repo == nil {
		return nil, nil
	}
	// Like public content, read the owner value without an operator grant,
	// then expose only the explicit customer-facing fields below.
	raw, err := uc.repo.Get(context.WithValue(ctx, publicOptionKey{}, true), "ServiceTierPricing")
	if err != nil {
		return nil, err
	}
	config, err := billing.ParseServiceTierConfig(raw)
	if err != nil || !config.Enabled {
		return nil, err
	}
	out := &PublicServiceTierPricing{Model: config.Model, Prices: config.Prices}
	if c := config.LongContext; c != nil {
		out.LongContext = &PublicLongContextPricing{ThresholdTokens: c.ThresholdTokens, Prices: c.Prices}
	}
	if c := config.TimePricing; c != nil {
		out.TimePricing = &PublicTimePricing{Timezone: c.Timezone, CalendarValidThrough: c.CalendarValidThrough, Holidays: c.Holidays, PeakWindows: c.PeakWindows, PeakPrices: c.PeakPrices, PeakLongContextPrices: c.PeakLongContextPrices}
	}
	return out, nil
}
