package billing

import (
	"crypto/sha256"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
)

func contextTestConfig() ServiceTierConfig {
	pointer := func(v float64) *float64 { return &v }
	base := map[string]ModelPrice{}
	long := map[string]ModelPrice{}
	for _, tier := range []string{"default", "priority", "flex"} {
		base[tier] = ModelPrice{InputPrice: 1, OutputPrice: 2, CacheReadPrice: pointer(.5)}
		long[tier] = ModelPrice{InputPrice: 3, OutputPrice: 4, CacheReadPrice: pointer(1), CacheCreation5mPrice: pointer(2), CacheCreation1hPrice: pointer(3)}
	}
	return ServiceTierConfig{Version: 2, Enabled: true, SourceKind: "channel", SourceID: 11, Model: "gpt-5.1", Prices: base, LongContext: &LongContextConfig{ThresholdTokens: 100, Policy: LongContextPolicy, Prices: long}}
}

func contextConfigJSON(t *testing.T, c ServiceTierConfig) string {
	t.Helper()
	raw, err := jsonx.Marshal(c)
	require.NoError(t, err)
	return string(raw)
}

func TestLongContextConfigRequiresVersionedCompleteTariffs(t *testing.T) {
	c := contextTestConfig()
	_, err := ParseServiceTierConfig(contextConfigJSON(t, c))
	require.NoError(t, err)
	c.LongContext.UpstreamPrices = c.LongContext.Prices
	_, err = ParseServiceTierConfig(contextConfigJSON(t, c))
	require.NoError(t, err)
	for name, mutate := range map[string]func(*ServiceTierConfig){
		"version one with context":    func(c *ServiceTierConfig) { c.Version = 1 },
		"version two without context": func(c *ServiceTierConfig) { c.LongContext = nil },
		"unknown version":             func(c *ServiceTierConfig) { c.Version = 3 },
		"zero threshold":              func(c *ServiceTierConfig) { c.LongContext.ThresholdTokens = 0 },
		"negative threshold":          func(c *ServiceTierConfig) { c.LongContext.ThresholdTokens = -1 },
		"threshold overflow":          func(c *ServiceTierConfig) { c.LongContext.ThresholdTokens = math.MaxInt32 + 1 },
		"unknown policy":              func(c *ServiceTierConfig) { c.LongContext.Policy = "excess_only" },
		"missing service tier":        func(c *ServiceTierConfig) { delete(c.LongContext.Prices, "flex") },
		"missing creation price": func(c *ServiceTierConfig) {
			p := c.LongContext.Prices["default"]
			p.CacheCreation5mPrice = nil
			c.LongContext.Prices["default"] = p
		},
		"negative long price": func(c *ServiceTierConfig) {
			p := c.LongContext.Prices["default"]
			p.OutputPrice = -1
			c.LongContext.Prices["default"] = p
		},
		"infinite long price": func(c *ServiceTierConfig) {
			p := c.LongContext.Prices["default"]
			p.InputPrice = math.Inf(1)
			c.LongContext.Prices["default"] = p
		},
		"nan long price": func(c *ServiceTierConfig) {
			p := c.LongContext.Prices["default"]
			p.CacheCreation1hPrice = new(float64)
			*p.CacheCreation1hPrice = math.NaN()
			c.LongContext.Prices["default"] = p
		},
		"incomplete upstream": func(c *ServiceTierConfig) { c.LongContext.UpstreamPrices = c.Prices },
	} {
		t.Run(name, func(t *testing.T) {
			c := contextTestConfig()
			mutate(&c)
			require.Error(t, c.Validate())
		})
	}
	c = contextTestConfig()
	c.LongContext.ThresholdTokens = math.MaxInt32
	require.NoError(t, c.Validate())
}

func TestLongContextJSONRequiresEveryBucketIncludingExplicitZero(t *testing.T) {
	valid := contextConfigJSON(t, contextTestConfig())
	for _, field := range []string{"input_price", "output_price", "cache_read_price", "cache_creation_5m_price", "cache_creation_1h_price"} {
		for _, missing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/missing=%t", field, missing), func(t *testing.T) {
				var object map[string]any
				require.NoError(t, jsonx.Unmarshal([]byte(valid), &object))
				lc := object["long_context"].(map[string]any)
				price := lc["prices"].(map[string]any)["default"].(map[string]any)
				if missing {
					delete(price, field)
				} else {
					price[field] = nil
				}
				raw, err := jsonx.Marshal(object)
				require.NoError(t, err)
				_, err = ParseServiceTierConfig(string(raw))
				require.Error(t, err)
			})
		}
	}
	for _, raw := range []string{
		strings.Replace(valid, `"long_context":{`, `"long_context":{"unknown":true,`, 1),
		strings.Replace(valid, `"prices":{"default":{"input_price":3`, `"prices":{"default":{"input_price":null`, 1),
		strings.Replace(valid, `"policy":"whole_request_input_gt_v1"`, `"policy":null`, 1),
		strings.Replace(valid, `"threshold_tokens":100`, `"threshold_tokens":null`, 1),
		strings.Replace(valid, `"long_context":{`, `"long_context":{"upstream_prices":null,`, 1),
	} {
		_, err := ParseServiceTierConfig(raw)
		require.Error(t, err, raw)
	}
	c := contextTestConfig()
	for tier := range c.Prices {
		zero := 0.0
		p := ModelPrice{CacheReadPrice: &zero, CacheCreation5mPrice: &zero, CacheCreation1hPrice: &zero}
		c.Prices[tier], c.LongContext.Prices[tier] = p, p
	}
	_, err := ParseServiceTierConfig(contextConfigJSON(t, c))
	require.NoError(t, err)
}

func TestLongContextCannotReduceAnyCorrespondingSaleBucket(t *testing.T) {
	for _, field := range []string{"input", "output", "read", "creation5m", "creation1h"} {
		t.Run(field, func(t *testing.T) {
			c := contextTestConfig()
			p := c.LongContext.Prices["priority"]
			base := c.Prices["priority"]
			switch field {
			case "input":
				p.InputPrice = .9
			case "output":
				p.OutputPrice = 1.9
			case "read":
				*p.CacheReadPrice = .4
			case "creation5m":
				base.CacheCreation5mPrice = new(float64)
				*base.CacheCreation5mPrice = 2.1
			case "creation1h":
				base.CacheCreation1hPrice = new(float64)
				*base.CacheCreation1hPrice = 3.1
			}
			c.Prices["priority"], c.LongContext.Prices["priority"] = base, p
			require.Error(t, c.Validate())
		})
	}
}

func TestLongContextSelectsStrictInputThresholdAndMaximumAllBuckets(t *testing.T) {
	c := contextTestConfig()
	for _, input := range []int64{99, 100, 101} {
		prices, tier := c.PricesForInput(input)
		if input <= 100 {
			require.Equal(t, "base", tier)
			require.Equal(t, c.Prices, prices)
		} else {
			require.Equal(t, "long", tier)
			require.Equal(t, c.LongContext.Prices, prices)
		}
	}
	p := c.LongContext.Prices["priority"]
	p.InputPrice = 9
	c.LongContext.Prices["priority"] = p
	p = c.LongContext.Prices["flex"]
	p.OutputPrice = 11
	*p.CacheCreation5mPrice = 13
	c.LongContext.Prices["flex"] = p
	maximum := c.MaximumPrice()
	require.Equal(t, 9.0, maximum.InputPrice)
	require.Equal(t, 11.0, maximum.OutputPrice)
	require.Equal(t, 1.0, *maximum.CacheReadPrice)
	require.Equal(t, 13.0, *maximum.CacheCreation5mPrice)
	require.Equal(t, 3.0, *maximum.CacheCreation1hPrice)
	c.LongContext = nil
	require.Equal(t, MaxTierPrice(c.Prices), c.MaximumPrice())
	_, tier := c.PricesForInput(math.MaxInt64)
	require.Equal(t, "base", tier)
}

func TestLongContextAbsentPreservesLegacyConfigDigest(t *testing.T) {
	c := contextTestConfig()
	c.Version, c.LongContext = 1, nil
	// This is the exact A3 payload shape and ordering before B1 added the
	// optional field; existing request digests must remain verifiable.
	legacy := struct {
		Version        int32                 `json:"version"`
		Enabled        bool                  `json:"enabled"`
		SourceKind     string                `json:"source_kind"`
		SourceID       int64                 `json:"source_id"`
		Model          string                `json:"model"`
		Prices         map[string]ModelPrice `json:"prices"`
		UpstreamPrices map[string]ModelPrice `json:"upstream_prices,omitempty"`
	}{c.Version, c.Enabled, c.SourceKind, c.SourceID, c.Model, c.Prices, c.UpstreamPrices}
	raw, err := jsonx.Marshal(legacy)
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(raw)), c.Digest())
	require.NotContains(t, contextConfigJSON(t, c), "long_context")
}
