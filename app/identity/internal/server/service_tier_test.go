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
	})
	require.Equal(t, "priority", row["requested_service_tier"])
	require.Empty(t, row["actual_service_tier"])
	require.Equal(t, "missing", row["service_tier_status"])
	require.Equal(t, "max", row["billed_service_tier"])
	require.Equal(t, "actual_or_max_v1", row["service_tier_policy"])
	require.Equal(t, "frozen-config", row["service_tier_config_hash"])
	require.NotContains(t, row, "upstream_prices")
	legacy := ledgerEntryToMap(&commonv1.LedgerEntry{})
	require.Empty(t, legacy["actual_service_tier"])
	require.Empty(t, legacy["billed_service_tier"])
}
