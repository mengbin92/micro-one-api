package server

import (
	"testing"

	"github.com/stretchr/testify/require"
	commonv1 "micro-one-api/api/common/v1"
)

// Self-service statements preserve the distinction between a requested tier,
// unknown upstream evidence and the tier policy that actually charged money.
func TestSelfLedgerServiceTierExplanation(t *testing.T) {
	row := ledgerEntryToMap(&commonv1.LedgerEntry{
		RequestedServiceTier: "priority", ActualServiceTier: "", ServiceTierStatus: "missing",
		BilledServiceTier: "max", ServiceTierPolicy: "actual_or_max_v1", ServiceTierConfigHash: "frozen-config",
		LongContextPolicy: "whole_request_input_gt_v1", LongContextThreshold: 100, LongContextTier: "long", LongContextInputTokens: 101,
		PricingTimeBand: "peak", PricingTimezone: "Asia/Shanghai", PricingTimeUnix: 1793232000,
	})
	require.Equal(t, "priority", row["requested_service_tier"])
	require.Empty(t, row["actual_service_tier"])
	require.Equal(t, "missing", row["service_tier_status"])
	require.Equal(t, "max", row["billed_service_tier"])
	require.Equal(t, "actual_or_max_v1", row["service_tier_policy"])
	require.Equal(t, "frozen-config", row["service_tier_config_hash"])
	require.Equal(t, "whole_request_input_gt_v1", row["long_context_policy"])
	require.EqualValues(t, 100, row["long_context_threshold"])
	require.Equal(t, "long", row["long_context_tier"])
	require.EqualValues(t, 101, row["long_context_input_tokens"])
	require.Equal(t, "peak", row["pricing_time_band"])
	require.Equal(t, "Asia/Shanghai", row["pricing_timezone"])
	require.EqualValues(t, 1793232000, row["pricing_time_unix"])
	require.NotContains(t, row, "upstream_prices")
	legacy := ledgerEntryToMap(&commonv1.LedgerEntry{})
	require.Empty(t, legacy["actual_service_tier"])
	require.Empty(t, legacy["billed_service_tier"])
}
