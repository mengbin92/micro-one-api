package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/billing/internal/biz"
	billingdomain "micro-one-api/domain/billing"
	"micro-one-api/pkg/jsonx"
)

func TestLedgerDetailLongContextOmitsProcurementWithoutMutatingFrozenRule(t *testing.T) {
	prices := map[string]billingdomain.ModelPrice{"default": {InputPrice: 0.1, OutputPrice: 0.2}}
	original := &biz.RequestSnapshot{ServiceTier: &biz.FrozenServiceTier{Config: billingdomain.ServiceTierConfig{
		Prices: prices, UpstreamPrices: prices,
		TimePricing: &billingdomain.TimePricingConfig{Timezone: "Asia/Shanghai", PeakPrices: prices, PeakUpstreamPrices: prices, PeakLongContextPrices: prices, PeakLongContextUpstreamPrices: prices},
		LongContext: &billingdomain.LongContextConfig{ThresholdTokens: 100, Policy: "whole_request_input_gt_v1", Prices: prices, UpstreamPrices: prices},
	}}}
	before, err := original.Digest()
	require.NoError(t, err)
	view := ledgerRequestSnapshotView(original)
	encoded, err := jsonx.Marshal(view)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "upstream_prices")
	require.Contains(t, string(encoded), "threshold_tokens")
	require.NotNil(t, original.ServiceTier.Config.UpstreamPrices)
	require.NotNil(t, original.ServiceTier.Config.LongContext.UpstreamPrices)
	require.NotNil(t, original.ServiceTier.Config.TimePricing.PeakUpstreamPrices)
	require.NotNil(t, original.ServiceTier.Config.TimePricing.PeakLongContextUpstreamPrices)
	after, err := original.Digest()
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Nil(t, ledgerRequestSnapshotView(nil))
}

func TestPricingSnapshotLongContextTimeEvidenceMapping(t *testing.T) {
	snapshot := &biz.PricingSnapshot{LongContextPolicy: "whole_request_input_gt_v1", LongContextThreshold: 128000, LongContextTier: "long", PricingTimeBand: "peak", PricingTimezone: "Asia/Shanghai", SnapshotVersion: 4}
	mapped := pricingSnapshotToProto(snapshot)
	require.Equal(t, snapshot.LongContextPolicy, mapped.LongContextPolicy)
	require.Equal(t, snapshot.LongContextThreshold, mapped.LongContextThreshold)
	require.Equal(t, snapshot.LongContextTier, mapped.LongContextTier)
	require.Equal(t, snapshot.PricingTimeBand, mapped.PricingTimeBand)
	require.Equal(t, snapshot.PricingTimezone, mapped.PricingTimezone)
	require.EqualValues(t, 4, mapped.SnapshotVersion)
}
