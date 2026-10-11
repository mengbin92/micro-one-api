package biz

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWalletPriceAmount(t *testing.T) {
	for _, tt := range []struct {
		rate string
		want int64
	}{
		{"", 10_000_000}, {"10", 10_000_000}, {"2.5", 2_500_000},
		{"0", 10_000_000}, {"-1", 10_000_000}, {"NaN", 10_000_000}, {"+Inf", 10_000_000}, {"invalid", 10_000_000},
	} {
		t.Run(tt.rate, func(t *testing.T) {
			t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", tt.rate)
			amount, err := WalletPriceAmount(100)
			require.NoError(t, err)
			require.Equal(t, tt.want, amount)
		})
	}
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "10")
	_, err := WalletPriceAmount(-1)
	require.Error(t, err)
	_, err = WalletPriceAmount(math.MaxInt64)
	require.Error(t, err)
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "0.000000001")
	_, err = WalletPriceAmount(1)
	require.Error(t, err)
}

func TestWalletAmountForMoneyCents(t *testing.T) {
	amount, err := WalletAmountForMoneyCents(12345, 2.5)
	require.NoError(t, err)
	require.EqualValues(t, 3_086_250, amount)
	amount, err = WalletAmountForMoneyCents(0, 10)
	require.NoError(t, err)
	require.Zero(t, amount)
	for _, rate := range []float64{0, -1, math.NaN(), math.Inf(1), math.MaxFloat64} {
		_, err = WalletAmountForMoneyCents(100, rate)
		require.Error(t, err)
	}
	_, err = WalletAmountForMoneyCents(-1, 10)
	require.Error(t, err)
	_, err = WalletAmountForMoneyCents(100, float64(math.MaxInt64)/10000)
	require.Error(t, err, "float64 rounds MaxInt64 to 2^63 and must never reach int64 conversion")
}

func TestWalletConversionUsesExactDecimalRounding(t *testing.T) {
	for _, tc := range []struct {
		name, rate string
		priceCNY   int64
		want       int64
	}{
		{"half unit", "1.00185", 1, 10_019},
		{"large integer", "0.0001", 9_007_199_254_740_993, 9_007_199_254_740_993},
		{"largest valid amount", "0.0001", math.MaxInt64, math.MaxInt64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", tc.rate)
			amount, err := WalletPriceAmount(tc.priceCNY)
			require.NoError(t, err)
			require.Equal(t, tc.want, amount)
		})
	}
	for _, tc := range []struct {
		name       string
		moneyCents int64
		rate       float64
		want       int64
	}{
		{"whole CNY half unit", 100, 1.00185, 10_019},
		{"fractional CNY half unit", 1, 0.005, 1},
		{"large integer cents", 9_007_199_254_740_993, 0.01, 9_007_199_254_740_993},
		{"largest valid cents amount", math.MaxInt64, 0.01, math.MaxInt64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			amount, err := WalletAmountForMoneyCents(tc.moneyCents, tc.rate)
			require.NoError(t, err)
			require.Equal(t, tc.want, amount)
		})
	}
}

func TestPlanSnapshotFreezesPriceConversionWithoutRepricingHistory(t *testing.T) {
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "10")
	plan := &SubscriptionPlan{ID: 1, PriceQuota: 100}
	snapshot := plan.ToPlanSnapshot()
	require.Nil(t, snapshot.PriceConversion, "canonical legacy completion must not rewrite an old paid order")
	conversion, err := CapturePriceConversion(plan.PriceQuota)
	require.NoError(t, err)
	snapshot.PriceConversion = conversion
	raw := EncodePlanSnapshot(snapshot)
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "20")
	decoded, err := DecodePlanSnapshot(raw)
	require.NoError(t, err)
	require.Equal(t, &PriceConversion{Version: 1, RechargeMultiplier: 10, WalletAmount: 10_000_000}, decoded.PriceConversion)
}

func TestDecodePlanSnapshotPriceConversionValidation(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown version":    `{"plan_id":1,"price_quota":100,"price_conversion":{"version":2,"recharge_multiplier":10,"wallet_amount":10000000}}`,
		"invalid multiplier": `{"plan_id":1,"price_quota":100,"price_conversion":{"version":1,"recharge_multiplier":0,"wallet_amount":10000000}}`,
		"mismatched amount":  `{"plan_id":1,"price_quota":100,"price_conversion":{"version":1,"recharge_multiplier":10,"wallet_amount":100}}`,
		"missing nominal":    `{"plan_id":1,"price_conversion":{"version":1,"recharge_multiplier":10,"wallet_amount":0}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodePlanSnapshot(raw)
			require.Error(t, err, "untrusted frozen pricing must not become legacy refund pricing")
		})
	}
	_, err := DecodePlanSnapshot(`{"plan_id":1,"price_quota":100}`)
	require.NoError(t, err, "historical snapshots remain valid")
}
