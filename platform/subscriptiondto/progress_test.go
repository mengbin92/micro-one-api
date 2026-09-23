package subscriptiondto

import (
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/subscription/biz"
	"micro-one-api/pkg/jsonx"
)

func TestProgressRoundTripPreservesQuotaPresence(t *testing.T) {
	zero := 0.0
	p := &biz.SubscriptionProgress{
		UsageSource: "billing", RateMultiplier: 2,
		DailyUsed:   &biz.QuotaDimension{Unlimited: true, Frozen: &zero},
		WeeklyUsed:  &biz.QuotaDimension{Limit: &zero, Frozen: &zero, Available: &zero},
		MonthlyUsed: &biz.QuotaDimension{},
	}
	wire, err := ProgressToProto(p)
	require.NoError(t, err)
	got, err := ProgressFromProto(wire)
	require.NoError(t, err)
	require.Equal(t, p, got)
	data, err := jsonx.Marshal(got)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, jsonx.Unmarshal(data, &doc))
	daily := doc["daily_used"].(map[string]any)
	require.Contains(t, daily, "available")
	require.Nil(t, daily["available"])
	require.Equal(t, float64(0), daily["frozen"])
	require.Equal(t, float64(0), doc["weekly_used"].(map[string]any)["limit"])
	require.Nil(t, doc["monthly_used"].(map[string]any)["frozen"])
}
