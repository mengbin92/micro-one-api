package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeModelPricingKeys(t *testing.T) {
	prices := normalizeModelPrices(map[string]ModelPrice{
		"  DeepSeek-V4-Flash-0731 ": {InputPrice: 1},
	})
	ratios := normalizeModelRatios(map[string]float64{"  DeepSeek-V4-Flash-0731 ": 2})

	assert.Equal(t, ModelPrice{InputPrice: 1}, prices["deepseek-v4-flash-0731"])
	assert.Equal(t, 2.0, ratios["deepseek-v4-flash-0731"])
}

func TestNormalizeModelPricing_CanonicalKeyWins(t *testing.T) {
	prices := normalizeModelPrices(map[string]ModelPrice{
		"MODEL": {InputPrice: 1},
		"model": {InputPrice: 2},
	})
	ratios := normalizeModelRatios(map[string]float64{
		"MODEL": 1,
		"model": 2,
	})

	assert.Equal(t, 2.0, prices["model"].InputPrice)
	assert.Equal(t, 2.0, ratios["model"])
}

func TestNormalizeUpstreamPrices_PreservesCanonicalModelCase(t *testing.T) {
	prices := normalizeUpstreamPrices(map[string]ModelPrice{
		"channel:5:GLM-5.2": {InputPrice: 1},
		"5:GLM-5.2":         {InputPrice: 2},
		"GLM-5.2":           {InputPrice: 3},
	})

	assert.Contains(t, prices, "channel:5:GLM-5.2")
	assert.NotContains(t, prices, "channel:5:glm-5.2")
	assert.Equal(t, 2.0, prices["5:glm-5.2"].InputPrice)
	assert.Equal(t, 3.0, prices["glm-5.2"].InputPrice)
}

func TestCalculateCostWithUsage_CaseInsensitiveModelPrice(t *testing.T) {
	uc := NewBillingUsecaseWithPricing(nil, nil, nil, nil, PricingConfig{
		ModelPrices: map[string]ModelPrice{
			"deepseek-v4-flash-0731": {InputPrice: 0.001, OutputPrice: 0.002},
		},
	})

	got, _ := uc.calculateCostWithUsage(context.Background(), "default", "DeepSeek-V4-Flash-0731", 0, LedgerUsage{
		PromptTokens:     1000,
		CompletionTokens: 500,
	})

	assert.Equal(t, int64(20000), got)
}

func TestCalculateCost_CaseInsensitiveModelRatiosAndGroupCaseSensitive(t *testing.T) {
	uc := NewBillingUsecaseWithPricing(nil, nil, nil, nil, PricingConfig{
		GroupRatios: map[string]float64{"VIP": 2},
		ModelRatios: map[string]float64{"DEEPSEEK-V4": 2},
		CompletionRatios: map[string]float64{
			"DeepSeek-V4": 3,
		},
	})

	assert.Equal(t, int64(280), uc.calculateCost(context.Background(), "VIP", "deepseek-v4", 10, 20, 0, false))
	assert.Equal(t, int64(140), uc.calculateCost(context.Background(), "vip", "DEEPSEEK-V4", 10, 20, 0, false))
}

func TestCalculateCost_DynamicPricingNormalizesModelKeys(t *testing.T) {
	uc := NewBillingUsecaseWithPricing(nil, nil, nil, nil, PricingConfig{
		PricingStore: mockPricingStore{config: PricingConfig{
			ModelRatios:      map[string]float64{"DEEPSEEK-V4": 2},
			CompletionRatios: map[string]float64{"DEEPSEEK-V4": 3},
		}},
	})

	assert.Equal(t, int64(140), uc.calculateCost(context.Background(), "default", "deepseek-v4", 10, 20, 0, false))
}
