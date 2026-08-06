package biz

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnpricedRoutedModels_FlagsRoutedButUnpriced(t *testing.T) {
	models := []*Model{
		{ModelID: "gpt-4o", DisplayName: "GPT", IsPublic: true, Status: ModelStatusEnabled, ChannelCount: 2},
		{ModelID: "claude-3", DisplayName: "Claude", IsPublic: true, Status: ModelStatusEnabled, SubscriptionCount: 1},
		{ModelID: "glm-5.2", DisplayName: "GLM", IsPublic: true, Status: ModelStatusEnabled, ChannelCount: 1, SubscriptionCount: 1},
		// private discovery — not user-facing, excluded even if routed
		{ModelID: "private-test", IsPublic: false, Status: ModelStatusTesting, ChannelCount: 1},
		// disabled — excluded
		{ModelID: "disabled-model", IsPublic: true, Status: ModelStatusDisabled, ChannelCount: 1},
		// no mappings — not routed, excluded
		{ModelID: "unrouted", IsPublic: true, Status: ModelStatusEnabled},
	}
	// Only gpt-4o and claude-3 are priced; glm-5.2 is unpriced.
	priced := map[string]struct{}{
		"gpt-4o":   {},
		"claude-3": {},
	}

	got := UnpricedRoutedModels(models, priced)
	assert.Len(t, got, 1)
	assert.Equal(t, "glm-5.2", got[0].ModelID)
	assert.EqualValues(t, 1, got[0].ChannelCount)
	assert.EqualValues(t, 1, got[0].SubscriptionCount)
}

func TestUnpricedRoutedModels_CanonicalisesPricedLookup(t *testing.T) {
	// The priced set is already canonical (lowercase). A stored model_id that
	// happens to be uppercase but normalises to a priced entry must NOT be
	// flagged unpriced. (Post Phase-2 §2.1 merge this should not occur, but
	// the audit must be robust to transitional data.)
	models := []*Model{
		{ModelID: "GLM-5.2", IsPublic: true, Status: ModelStatusEnabled, ChannelCount: 1},
	}
	priced := map[string]struct{}{"glm-5.2": {}}

	got := UnpricedRoutedModels(models, priced)
	assert.Empty(t, got, "uppercase stored id that normalises to a priced entry is not unpriced")
}

func TestUnpricedRoutedModels_EmptyInputs(t *testing.T) {
	// No models at all => nil result.
	assert.Nil(t, UnpricedRoutedModels(nil, map[string]struct{}{"gpt-4o": {}}))
	// Routed model but empty priced set => the model IS unpriced (one entry).
	got := UnpricedRoutedModels([]*Model{{ModelID: "x", IsPublic: true, Status: ModelStatusEnabled, ChannelCount: 1}}, nil)
	assert.Len(t, got, 1)
	assert.Equal(t, "x", got[0].ModelID)
	// Priced set covers everything => empty result.
	assert.Empty(t, UnpricedRoutedModels(
		[]*Model{{ModelID: "x", IsPublic: true, Status: ModelStatusEnabled, ChannelCount: 1}},
		map[string]struct{}{"x": {}},
	))
}

func TestUnpricedRoutedModels_SortedByModelID(t *testing.T) {
	models := []*Model{
		{ModelID: "zeta", IsPublic: true, Status: ModelStatusEnabled, ChannelCount: 1},
		{ModelID: "alpha", IsPublic: true, Status: ModelStatusEnabled, ChannelCount: 1},
		{ModelID: "mid", IsPublic: true, Status: ModelStatusEnabled, ChannelCount: 1},
	}
	got := UnpricedRoutedModels(models, nil)
	assert.Len(t, got, 3)
	assert.Equal(t, "alpha", got[0].ModelID)
	assert.Equal(t, "mid", got[1].ModelID)
	assert.Equal(t, "zeta", got[2].ModelID)
}

// ── v0.11.0 Phase 2 §2.1: preflight price-reference attachment ─────────────

func TestPreflightReport_AttachPriceReferences(t *testing.T) {
	report := &PreflightReport{
		Groups: []DuplicateModelGroup{
			{
				CanonicalID: "glm-5.2",
				Members: []DuplicateModelRef{
					{ModelPK: 1, ModelID: "GLM-5.2"}, // stored uppercase
					{ModelPK: 2, ModelID: "glm-5.2"}, // stored canonical
				},
			},
			{
				CanonicalID: "gpt-4o",
				Members: []DuplicateModelRef{
					{ModelPK: 3, ModelID: "GPT-4o"},
					{ModelPK: 4, ModelID: "gpt-4o"},
				},
			},
		},
	}
	// Pricing keys reference both spellings and an unrelated model.
	priceKeys := map[string]struct{}{
		"glm-5.2":                {}, // matches member 2 (and member 1 via canonical)
		"GLM-5.2":                {}, // matches member 1 stored spelling
		"channel:5:z-ai/glm-5.2": {}, // upstream key — should NOT match a bare model member
		"gpt-4o":                 {}, // matches member 4 (and member 3 via canonical)
		"claude-3":               {}, // unrelated
	}

	report.AttachPriceReferences(priceKeys)

	glm := report.Groups[0].Members
	// Both members normalise to "glm-5.2", and the pricing keys "glm-5.2" +
	// "GLM-5.2" both normalise to "glm-5.2", so each member references both.
	// (The point of the audit is to show operators EVERY pricing entry that
	// touches a duplicate, regardless of which member's exact spelling.)
	assert.ElementsMatch(t, []string{"glm-5.2", "GLM-5.2"}, glm[0].PriceReferences)
	assert.ElementsMatch(t, []string{"glm-5.2", "GLM-5.2"}, glm[1].PriceReferences)

	gpt := report.Groups[1].Members
	// Member 3 stored "GPT-4o" — no pricing key spells it "GPT-4o", but its
	// canonical form "gpt-4o" IS a pricing key, so it matches via canonical.
	assert.ElementsMatch(t, []string{"gpt-4o"}, gpt[0].PriceReferences)
	// Member 4 stored "gpt-4o" — direct match on the pricing key.
	assert.ElementsMatch(t, []string{"gpt-4o"}, gpt[1].PriceReferences)
}

func TestPreflightReport_AttachPriceReferences_NilOrEmpty(t *testing.T) {
	// Nil report is a no-op.
	var r *PreflightReport
	r.AttachPriceReferences(map[string]struct{}{"x": {}}) // must not panic

	// Empty price key set is a no-op.
	r2 := &PreflightReport{Groups: []DuplicateModelGroup{{Members: []DuplicateModelRef{{ModelID: "x"}}}}}
	r2.AttachPriceReferences(nil)
	assert.Nil(t, r2.Groups[0].Members[0].PriceReferences)
}
