package server

import (
	"sort"
	"strings"

	"github.com/shopspring/decimal"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/domain/billing"
)

type readonlyLongContextPricing struct {
	ThresholdTokens int64                         `json:"threshold_tokens"`
	Prices          map[string]readonlyModelPrice `json:"prices"`
}

type readonlyTimePricing struct {
	Timezone              string                        `json:"timezone"`
	CalendarValidThrough  string                        `json:"calendar_valid_through"`
	Holidays              []string                      `json:"holidays"`
	PeakWindows           []billing.PricingTimeWindow   `json:"peak_windows"`
	PeakPrices            map[string]readonlyModelPrice `json:"peak_prices"`
	PeakLongContextPrices map[string]readonlyModelPrice `json:"peak_long_context_prices,omitempty"`
}

func readonlyTierPrices(prices map[string]billing.ModelPrice) map[string]readonlyModelPrice {
	if prices == nil {
		return nil
	}
	out := make(map[string]readonlyModelPrice, len(prices))
	for tier, price := range prices {
		out[tier] = readonlyTariffPrice(price)
	}
	return out
}

func readonlyTariffPrice(price billing.ModelPrice) readonlyModelPrice {
	perMillion := func(value *float64) *float64 {
		if value == nil {
			return nil
		}
		scaled, _ := decimal.NewFromFloat(*value).Mul(decimal.NewFromInt(readonlyPricingMTok)).Float64()
		return &scaled
	}
	return readonlyModelPrice{InputPrice: perMillion(&price.InputPrice), OutputPrice: perMillion(&price.OutputPrice), CacheReadPrice: perMillion(price.CacheReadPrice), CacheCreation5mPrice: perMillion(price.CacheCreation5mPrice), CacheCreation1hPrice: perMillion(price.CacheCreation1hPrice)}
}

func mergeReadonlyServiceTierPricing(rows []readonlyPricingRow, tariff *biz.PublicServiceTierPricing) []readonlyPricingRow {
	if tariff == nil {
		return rows
	}
	modelKey := strings.ToLower(strings.TrimSpace(tariff.Model))
	kept := rows[:0]
	for _, row := range rows {
		if strings.ToLower(strings.TrimSpace(row.Model)) != modelKey {
			kept = append(kept, row)
		}
	}
	row := readonlyPricingRow{Model: tariff.Model}
	// Flat fields summarize the base tariff; detailed variants are authoritative.
	price := readonlyTariffPrice(billing.MaxTierPrice(tariff.Prices))
	row.InputPrice, row.OutputPrice, row.CacheReadPrice = price.InputPrice, price.OutputPrice, price.CacheReadPrice
	row.CacheCreation5mPrice, row.CacheCreation1hPrice = price.CacheCreation5mPrice, price.CacheCreation1hPrice
	for _, p := range tariff.Prices {
		if p.CacheCreation5mPrice == nil {
			row.CacheCreation5mPrice = nil
		}
		if p.CacheCreation1hPrice == nil {
			row.CacheCreation1hPrice = nil
		}
	}
	row.CacheCreationUnpriced = row.CacheCreation5mPrice == nil && row.CacheCreation1hPrice == nil
	row.ServiceTierPrices = readonlyTierPrices(tariff.Prices)
	if c := tariff.LongContext; c != nil {
		row.LongContext = &readonlyLongContextPricing{ThresholdTokens: c.ThresholdTokens, Prices: readonlyTierPrices(c.Prices)}
	}
	if c := tariff.TimePricing; c != nil {
		row.TimePricing = &readonlyTimePricing{Timezone: c.Timezone, CalendarValidThrough: c.CalendarValidThrough, Holidays: c.Holidays, PeakWindows: c.PeakWindows, PeakPrices: readonlyTierPrices(c.PeakPrices), PeakLongContextPrices: readonlyTierPrices(c.PeakLongContextPrices)}
	}
	rows = append(kept, row)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Model < rows[j].Model })
	return rows
}
