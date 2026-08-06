package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCanonicalUpstreamPriceKey covers the v0.11.0 Phase 2 §2.2 cost-key
// builder. The canonical key is only produced when source kind, source id and
// upstream model id are all present; otherwise it returns "" so the caller
// falls back to the legacy key.
func TestCanonicalUpstreamPriceKey(t *testing.T) {
	cases := []struct {
		name            string
		sourceKind      string
		sourceID        int64
		upstreamModelID string
		want            string
	}{
		{"channel full", CostSourceChannel, 5, "z-ai/glm-5.2", "channel:5:z-ai/glm-5.2"},
		{"subscription full", CostSourceSubscription, 7, "claude-sonnet-4-5", "subscription:7:claude-sonnet-4-5"},
		{"empty kind", "", 5, "glm", ""},
		{"empty upstream model id", CostSourceChannel, 5, "", ""},
		{"zero source id", CostSourceChannel, 0, "glm", ""},
		{"unknown kind", "cdn", 5, "glm", ""},
		{"trimmed inputs", "  channel  ", 5, "  glm-5.2  ", "channel:5:glm-5.2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := canonicalUpstreamPriceKey(tc.sourceKind, tc.sourceID, tc.upstreamModelID)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestUpstreamPriceKey_LegacyForm pins the pre-v0.11.0 key format so the
// fallback read continues to resolve existing configs during the migration
// window.
func TestUpstreamPriceKey_LegacyForm(t *testing.T) {
	assert.Equal(t, "glm-5.2", upstreamPriceKey(0, "glm-5.2"))
	assert.Equal(t, "5:glm-5.2", upstreamPriceKey(5, "glm-5.2"))
}

// newUpstreamCostTestUsecase builds a BillingUsecase with only the embedded
// pricing maps populated (no repos, no store). pricingConfig returns the
// embedded maps when the store is nil, so this is sufficient to exercise the
// key-resolution logic without a database.
func newUpstreamCostTestUsecase(upstream map[string]ModelPrice) *BillingUsecase {
	return NewBillingUsecaseWithPricing(nil, nil, nil, nil, PricingConfig{
		UpstreamPrices: upstream,
	})
}

// TestCalculateUpstreamCostWithUsage_KeyResolution verifies the three-tier
// key lookup: canonical key first, legacy key second, bare model id last.
// This is the core of Phase 2 §2.2 "same public model via channel +
// subscription: user price identical, upstream cost recorded per source".
func TestCalculateUpstreamCostWithUsage_KeyResolution(t *testing.T) {
	// Seed three tiers of upstream price for the same public model "glm-5.2":
	//   - canonical channel key (channel:5:z-ai/glm-5.2) — the new scheme
	//   - legacy key (5:glm-5.2)                          — pre-v0.11.0
	//   - bare model (glm-5.2)                            — global default
	canonical := ModelPrice{InputPrice: 1.0, OutputPrice: 2.0}
	legacy := ModelPrice{InputPrice: 10.0, OutputPrice: 20.0}
	bare := ModelPrice{InputPrice: 100.0, OutputPrice: 200.0}
	uc := newUpstreamCostTestUsecase(map[string]ModelPrice{
		"channel:5:z-ai/glm-5.2": canonical,
		"5:glm-5.2":              legacy,
		"glm-5.2":                bare,
	})

	usage := LedgerUsage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		UpstreamModelID:  "z-ai/glm-5.2",
		SourceKind:       CostSourceChannel,
	}
	// channelID=5 matches both the canonical and legacy keys; canonical wins.
	got := uc.calculateUpstreamCostWithUsage(context.Background(), 5, "glm-5.2", 0, usage)
	assert.Equal(t, canonicalCost(canonical, 1000, 500), got, "canonical key should win over legacy")

	// Drop the canonical key: legacy takes over.
	delete(uc.upstreamPrices, "channel:5:z-ai/glm-5.2")
	got = uc.calculateUpstreamCostWithUsage(context.Background(), 5, "glm-5.2", 0, usage)
	assert.Equal(t, canonicalCost(legacy, 1000, 500), got, "legacy key fallback")

	// Drop the legacy key too: bare model default.
	delete(uc.upstreamPrices, "5:glm-5.2")
	got = uc.calculateUpstreamCostWithUsage(context.Background(), 5, "glm-5.2", 0, usage)
	assert.Equal(t, canonicalCost(bare, 1000, 500), got, "bare model fallback")
}

// TestCalculateUpstreamCostWithUsage_SubscriptionVsChannel confirms the
// acceptance criterion "same public model via channel + subscription:
// upstream cost recorded per source". Two requests for "claude-sonnet-4-5"
// through different source kinds resolve to DIFFERENT upstream prices even
// though they share the same channel/account id space numerically.
func TestCalculateUpstreamCostWithUsage_SubscriptionVsChannel(t *testing.T) {
	channelPrice := ModelPrice{InputPrice: 3.0, OutputPrice: 6.0}
	subPrice := ModelPrice{InputPrice: 0.5, OutputPrice: 1.0}
	uc := newUpstreamCostTestUsecase(map[string]ModelPrice{
		"channel:5:claude-sonnet-4-5":      channelPrice,
		"subscription:5:claude-sonnet-4-5": subPrice, // same numeric id 5, different kind
	})

	chUsage := LedgerUsage{PromptTokens: 1000, CompletionTokens: 500, UpstreamModelID: "claude-sonnet-4-5", SourceKind: CostSourceChannel}
	subUsage := LedgerUsage{PromptTokens: 1000, CompletionTokens: 500, UpstreamModelID: "claude-sonnet-4-5", SourceKind: CostSourceSubscription, SubscriptionAccountID: 5}

	chCost := uc.calculateUpstreamCostWithUsage(context.Background(), 5, "claude-sonnet-4-5", 0, chUsage)
	subCost := uc.calculateUpstreamCostWithUsage(context.Background(), 5, "claude-sonnet-4-5", 0, subUsage)

	assert.Equal(t, canonicalCost(channelPrice, 1000, 500), chCost)
	assert.Equal(t, canonicalCost(subPrice, 1000, 500), subCost)
	assert.NotEqual(t, chCost, subCost, "channel vs subscription cost must differ")
}

// canonicalCost applies the canonical formula to a price tier so the test
// asserts on the resolved price rather than duplicating the arithmetic.
func canonicalCost(p ModelPrice, prompt, completion int64) int64 {
	return calculateCanonicalCost(p, prompt, completion, 0, 0, 0, 1, false).CanonicalCost
}
