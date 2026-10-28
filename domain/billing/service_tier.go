package billing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"strings"

	"micro-one-api/pkg/jsonx"
)

const ServiceTierPricingVersion int32 = 1
const ServiceTierPolicy = "actual_or_max_v1"

// ModelPrice is a five-bucket, per-token tariff. Optional cache prices preserve
// the existing input fallback / unpriced creation semantics.
type ModelPrice struct {
	InputPrice           float64  `json:"input_price"`
	OutputPrice          float64  `json:"output_price"`
	CacheReadPrice       *float64 `json:"cache_read_price,omitempty"`
	CacheCreation5mPrice *float64 `json:"cache_creation_5m_price,omitempty"`
	CacheCreation1hPrice *float64 `json:"cache_creation_1h_price,omitempty"`
}

// ServiceTierConfig deliberately admits one exact source/model. A disabled
// configuration is inert; enabling requires a complete atomic tariff set.
type ServiceTierConfig struct {
	Version        int32                 `json:"version"`
	Enabled        bool                  `json:"enabled"`
	SourceKind     string                `json:"source_kind"`
	SourceID       int64                 `json:"source_id"`
	Model          string                `json:"model"`
	Prices         map[string]ModelPrice `json:"prices"`
	UpstreamPrices map[string]ModelPrice `json:"upstream_prices,omitempty"`
}

func ParseServiceTierConfig(raw string) (ServiceTierConfig, error) {
	var c ServiceTierConfig
	if strings.TrimSpace(raw) == "" {
		return c, nil
	}
	if strings.TrimSpace(raw)[0] != '{' {
		return c, fmt.Errorf("ServiceTierPricing must be a JSON object")
	}
	decoder := jsonx.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return c, fmt.Errorf("invalid ServiceTierPricing: %w", err)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return c, fmt.Errorf("trailing ServiceTierPricing content")
	}
	if c.Enabled {
		var object map[string]jsonx.RawMessage
		if err := jsonx.Unmarshal([]byte(raw), &object); err != nil {
			return c, err
		}
		for _, field := range []string{"prices", "upstream_prices"} {
			data, ok := object[field]
			if !ok && field == "upstream_prices" {
				continue
			}
			if !ok || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
				return c, fmt.Errorf("%s must be an object", field)
			}
			var tiers map[string]map[string]jsonx.RawMessage
			if err := jsonx.Unmarshal(data, &tiers); err != nil {
				return c, err
			}
			for tier, price := range tiers {
				for _, key := range []string{"input_price", "output_price", "cache_read_price"} {
					value, ok := price[key]
					if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
						return c, fmt.Errorf("%s %s requires %s", field, tier, key)
					}
				}
			}
		}
	}
	return c, c.Validate()
}
func ValidateServiceTierConfig(raw string) error { _, err := ParseServiceTierConfig(raw); return err }
func (c ServiceTierConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Version != ServiceTierPricingVersion || c.SourceKind != "channel" || c.SourceID <= 0 || c.Model == "" || c.Model != strings.TrimSpace(c.Model) || len(c.Model) > 255 {
		return fmt.Errorf("ServiceTierPricing requires version 1 and one channel/model")
	}
	if err := validateTierPrices(c.Prices); err != nil {
		return err
	}
	if c.UpstreamPrices != nil {
		return validateTierPrices(c.UpstreamPrices)
	}
	return nil
}
func validateTierPrices(prices map[string]ModelPrice) error {
	if len(prices) != 3 {
		return fmt.Errorf("service tiers require exactly default, priority and flex prices")
	}
	for _, tier := range []string{"default", "priority", "flex"} {
		p, ok := prices[tier]
		if !ok || p.CacheReadPrice == nil {
			return fmt.Errorf("service tier %s requires input/output/cache-read prices", tier)
		}
		for _, v := range []float64{p.InputPrice, p.OutputPrice, *p.CacheReadPrice} {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				return fmt.Errorf("invalid service tier %s price", tier)
			}
		}
		for _, v := range []*float64{p.CacheCreation5mPrice, p.CacheCreation1hPrice} {
			if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0) {
				return fmt.Errorf("invalid service tier %s cache creation price", tier)
			}
		}
	}
	return nil
}
func (c ServiceTierConfig) Digest() string {
	b, _ := jsonx.Marshal(c)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func ValidRequestedServiceTier(t string) bool {
	switch t {
	case "", "auto", "default", "priority", "flex":
		return true
	}
	return false
}
func KnownActualServiceTier(t string) bool { return t == "default" || t == "priority" || t == "flex" }

// MaxTierPrice is a per-bucket maximum, so no allowed downgrade or actual tier
// can exceed the reservation merely because bucket weights differ.
func MaxTierPrice(prices map[string]ModelPrice) ModelPrice {
	var p ModelPrice
	cr, c5, c1 := 0.0, 0.0, 0.0
	for _, v := range prices {
		p.InputPrice = math.Max(p.InputPrice, v.InputPrice)
		p.OutputPrice = math.Max(p.OutputPrice, v.OutputPrice)
		read := v.InputPrice
		if v.CacheReadPrice != nil {
			read = *v.CacheReadPrice
		}
		cr = math.Max(cr, read)
		if v.CacheCreation5mPrice != nil {
			c5 = math.Max(c5, *v.CacheCreation5mPrice)
		}
		if v.CacheCreation1hPrice != nil {
			c1 = math.Max(c1, *v.CacheCreation1hPrice)
		}
	}
	p.CacheReadPrice = &cr
	p.CacheCreation5mPrice = &c5
	p.CacheCreation1hPrice = &c1
	return p
}

// ServiceTierRequest is producer metadata, never evidence of the actual tier.
type ServiceTierRequest struct {
	RequestedServiceTier string `json:"requested_service_tier"`
	Protocol             string `json:"protocol"`
	InputTokenBound      int64  `json:"input_token_bound"`
	OutputTokenBound     int64  `json:"output_token_bound"`
	Version              int32  `json:"version"`
}
type serviceTierRequestKey struct{}

func WithServiceTierRequest(ctx context.Context, r ServiceTierRequest) context.Context {
	return context.WithValue(ctx, serviceTierRequestKey{}, r)
}
func GetServiceTierRequest(ctx context.Context) ServiceTierRequest {
	r, _ := ctx.Value(serviceTierRequestKey{}).(ServiceTierRequest)
	return r
}
