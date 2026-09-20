package data

import (
	"testing"
	"time"

	"micro-one-api/app/billing/internal/biz"

	"github.com/stretchr/testify/require"
)

func TestReconciliationRecordsRoundTripAllDiscrepancyTypes(t *testing.T) {
	result := &biz.ReconciliationResult{
		AccountInconsistencies:       []biz.AccountInconsistency{{UserID: "account", ExpectedQuota: 1}},
		ChannelInconsistencies:       []biz.ChannelInconsistency{{ChannelID: 2, Difference: 3}},
		LogInconsistencies:           []biz.LogInconsistency{{LedgerCount: 4, LogCount: 5}},
		SubscriptionInconsistencies:  []biz.SubscriptionInconsistency{{UserID: 6, SubscriptionID: 7, Window: "daily", WindowStart: 8, Difference: 0.5}},
		ReceivableInconsistencies:    []biz.ReceivableInconsistency{{UserID: "receivable", Difference: 9}},
		RefundInconsistencies:        []biz.RefundInconsistency{{RefundedOrderCount: 10, MoneyCentsDiff: 11}},
		StuckIssuanceInconsistencies: []biz.StuckIssuanceInconsistency{{TradeNo: "trade", UserID: "12", StuckSince: time.Unix(13, 0)}},
	}

	restored := &biz.ReconciliationResult{}
	applyReconciliationRecords(restored, reconciliationRecordsFromResult(result))

	require.Equal(t, result, restored)
}
