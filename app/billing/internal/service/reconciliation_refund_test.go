package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/billing/internal/biz"
)

func TestReconciliationRefundKeepsFixedAmountDifferenceBelowOneCNYCent(t *testing.T) {
	run, err := reconciliationRunToProto(&biz.ReconciliationResult{RefundInconsistencies: []biz.RefundInconsistency{{
		TradeNo: "partial-credit", UserID: "42", ExpectedRefundAmount: 100000, ReversalLedgerAmount: 99999,
		AmountDiff: 1, MoneyCentsDiff: 0, RefundedOrderCount: 1, ReversalLedgerCount: 1,
	}}})
	require.NoError(t, err)
	require.Len(t, run.Discrepancies, 1)
	d := run.Discrepancies[0]
	require.Equal(t, "partial-credit", d.TradeNo)
	require.Equal(t, "42", d.UserId)
	require.Equal(t, int64(100000), d.ExpectedQuota)
	require.Equal(t, int64(99999), d.ActualQuota)
	require.Equal(t, int64(1), d.Difference)
	require.Zero(t, d.MoneyCentsDiff)
}

func TestReconciliationHistoricalRefundDoesNotInventFixedAmountDifference(t *testing.T) {
	run, err := reconciliationRunToProto(&biz.ReconciliationResult{RefundInconsistencies: []biz.RefundInconsistency{{
		RefundedOrderCount: 1, RefundedOrderMoneyCents: 200000, ReversalLedgerAmount: 1500, MoneyCentsDiff: 50000,
	}}})
	require.NoError(t, err)
	require.Len(t, run.Discrepancies, 1)
	d := run.Discrepancies[0]
	require.Zero(t, d.ExpectedQuota)
	require.Zero(t, d.ActualQuota, "historical records carry only the legacy cents difference")
	require.Zero(t, d.Difference)
	require.Equal(t, int64(50000), d.MoneyCentsDiff)
}
