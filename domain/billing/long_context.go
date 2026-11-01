package billing

import (
	"bytes"
	"fmt"
	"math"

	"micro-one-api/pkg/jsonx"
)

const LongContextPricingVersion int32 = 2
const LongContextPolicy = "whole_request_input_gt_v1"

// LongContextConfig applies a complete five-bucket tariff to the whole request
// when the sum of the four canonical input buckets is strictly above the
// threshold. Output tokens never participate in the threshold comparison.
type LongContextConfig struct {
	ThresholdTokens int64                 `json:"threshold_tokens"`
	Policy          string                `json:"policy"`
	Prices          map[string]ModelPrice `json:"prices"`
	UpstreamPrices  map[string]ModelPrice `json:"upstream_prices,omitempty"`
}

func (c *LongContextConfig) Validate(base map[string]ModelPrice) error {
	if c == nil || c.ThresholdTokens < 1 || c.ThresholdTokens > math.MaxInt32 || c.Policy != LongContextPolicy {
		return fmt.Errorf("long_context requires a threshold from 1 to %d and policy %s", math.MaxInt32, LongContextPolicy)
	}
	if err := validateCompleteContextPrices(c.Prices); err != nil {
		return err
	}
	for tier, price := range c.Prices {
		original := base[tier]
		baseRead := original.InputPrice
		if original.CacheReadPrice != nil {
			baseRead = *original.CacheReadPrice
		}
		base5m, base1h := 0.0, 0.0
		if original.CacheCreation5mPrice != nil {
			base5m = *original.CacheCreation5mPrice
		}
		if original.CacheCreation1hPrice != nil {
			base1h = *original.CacheCreation1hPrice
		}
		if price.InputPrice < original.InputPrice || price.OutputPrice < original.OutputPrice || *price.CacheReadPrice < baseRead || *price.CacheCreation5mPrice < base5m || *price.CacheCreation1hPrice < base1h {
			return fmt.Errorf("long_context %s prices must not be below the corresponding base bucket prices", tier)
		}
	}
	if c.UpstreamPrices != nil {
		return validateCompleteContextPrices(c.UpstreamPrices)
	}
	return nil
}

func validateCompleteContextPrices(prices map[string]ModelPrice) error {
	if err := validateTierPrices(prices); err != nil {
		return err
	}
	for tier, price := range prices {
		if price.CacheCreation5mPrice == nil || price.CacheCreation1hPrice == nil {
			return fmt.Errorf("long_context %s requires all five bucket prices", tier)
		}
	}
	return nil
}

// Scalar float fields cannot distinguish an omitted or null JSON value from
// an explicit zero. Check presence before accepting the typed configuration.
func validateLongContextJSON(raw jsonx.RawMessage) error {
	var object map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(raw, &object); err != nil || object == nil {
		return fmt.Errorf("long_context must be an object")
	}
	for _, field := range []string{"prices", "upstream_prices"} {
		data, ok := object[field]
		if !ok && field == "upstream_prices" {
			continue
		}
		if !ok || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			return fmt.Errorf("long_context %s must be an object", field)
		}
		var tiers map[string]map[string]jsonx.RawMessage
		if err := jsonx.Unmarshal(data, &tiers); err != nil {
			return fmt.Errorf("invalid long_context %s: %w", field, err)
		}
		for tier, price := range tiers {
			for _, key := range []string{"input_price", "output_price", "cache_read_price", "cache_creation_5m_price", "cache_creation_1h_price"} {
				value, ok := price[key]
				if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					return fmt.Errorf("long_context %s %s requires %s", field, tier, key)
				}
			}
		}
	}
	return nil
}

// MaximumPrice bounds every bucket across all service and context tiers. It
// deliberately does not infer a context tier from an estimated input bound.
func (c ServiceTierConfig) MaximumPrice() ModelPrice {
	base := MaxTierPrice(c.Prices)
	if c.LongContext == nil {
		return base
	}
	return MaxTierPrice(map[string]ModelPrice{"base": base, "long": MaxTierPrice(c.LongContext.Prices)})
}

// PricesForInput selects a context tariff from verified canonical input only.
// A value equal to the threshold remains in the base context tier.
func (c ServiceTierConfig) PricesForInput(inputTokens int64) (map[string]ModelPrice, string) {
	if c.LongContext != nil && inputTokens > c.LongContext.ThresholdTokens {
		return c.LongContext.Prices, "long"
	}
	return c.Prices, "base"
}
