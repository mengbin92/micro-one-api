package biz

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"micro-one-api/platform/metrics"
)

type mockReconRepo struct {
	accounts             []*Account
	ledgerSums           map[string]int64
	reservations         []*Reservation
	channels             []*ChannelUsageSnapshot
	channelLedgerUsage   []*ChannelLedgerUsage
	ledgerConsumeSummary *ConsumeSummary
	logConsumeSummary    *ConsumeSummary
	// Phase 2.3 reconciliation coverage fields.
	refundedOrderCount    int64
	refundedOrderCents    int64
	reversalLedgerAmount  int64
}

func (m *mockReconRepo) ListAllAccounts(ctx context.Context) ([]*Account, error) {
	return m.accounts, nil
}

func (m *mockReconRepo) SumLedgerAmounts(ctx context.Context, userID string) (int64, error) {
	if sum, ok := m.ledgerSums[userID]; ok {
		return sum, nil
	}
	return 0, nil
}

func (m *mockReconRepo) ListChannelUsage(ctx context.Context) ([]*ChannelUsageSnapshot, error) {
	return m.channels, nil
}

func (m *mockReconRepo) SumConsumeLedgerUsageByChannel(ctx context.Context) ([]*ChannelLedgerUsage, error) {
	return m.channelLedgerUsage, nil
}

func (m *mockReconRepo) GetLedgerConsumeSummary(ctx context.Context) (*ConsumeSummary, error) {
	if m.ledgerConsumeSummary != nil {
		return m.ledgerConsumeSummary, nil
	}
	return &ConsumeSummary{}, nil
}

func (m *mockReconRepo) GetLogConsumeSummary(ctx context.Context) (*ConsumeSummary, error) {
	if m.logConsumeSummary != nil {
		return m.logConsumeSummary, nil
	}
	return &ConsumeSummary{}, nil
}

func (m *mockReconRepo) ListReservationsByStatus(ctx context.Context, status string) ([]*Reservation, error) {
	var result []*Reservation
	for _, r := range m.reservations {
		if r.Status == status {
			result = append(result, r)
		}
	}
	return result, nil
}

func (m *mockReconRepo) ListActiveSubscriptions(ctx context.Context) ([]*SubscriptionUsageSnapshot, error) {
	return nil, nil
}

func (m *mockReconRepo) SumPendingReceivables(ctx context.Context) (int64, error) {
	return 0, nil
}

func (m *mockReconRepo) SumOverdraftBalances(ctx context.Context) (int64, error) {
	return 0, nil
}

func (m *mockReconRepo) SumReversalLedgerAmounts(ctx context.Context) (int64, error) {
	return m.reversalLedgerAmount, nil
}

func (m *mockReconRepo) CountRefundedOrders(ctx context.Context) (int64, int64, error) {
	return m.refundedOrderCount, m.refundedOrderCents, nil
}

func TestRunReconciliation_ExpiredReservations(t *testing.T) {
	account := &Account{UserID: "user1", Balance: 900, FrozenAmount: 100, Group: "default"}
	expiredRes := &Reservation{
		ReservationID:      "res1",
		UserID:              "user1",
		Amount:              100,
		BalanceAmountQuota:  100, // legacy balance-only: wallet-side == Amount
		Status:              ReservationStatusReserved,
		ExpiredAt:           time.Now().Add(-10 * time.Minute),
	}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: map[string]*Reservation{"res1": expiredRes}}
	reconRepo := &mockReconRepo{
		accounts:     []*Account{account},
		ledgerSums:   map[string]int64{"user1": 900},
		reservations: []*Reservation{expiredRes},
	}

	uc := NewReconciliationUsecase(accountRepo, reservationRepo, reconRepo, nil)
	result, err := uc.RunReconciliation(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 1, result.ExpiredCleaned)
	assert.Equal(t, ReservationStatusExpired, expiredRes.Status)
	assert.Equal(t, int64(0), account.FrozenAmount)
}

func TestRunReconciliation_QuotaConsistency(t *testing.T) {
	account := &Account{UserID: "user1", Balance: 1000, FrozenAmount: 0, Group: "default"}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	reconRepo := &mockReconRepo{
		accounts:   []*Account{account},
		ledgerSums: map[string]int64{"user1": 500}, // mismatch: quota=1000, ledger=500
	}

	uc := NewReconciliationUsecase(accountRepo, reservationRepo, reconRepo, nil)
	result, err := uc.RunReconciliation(context.Background())

	require.NoError(t, err)
	assert.Len(t, result.AccountInconsistencies, 1)
	assert.Equal(t, "user1", result.AccountInconsistencies[0].UserID)
	assert.Equal(t, int64(500), result.AccountInconsistencies[0].ExpectedQuota)
	assert.Equal(t, int64(1000), result.AccountInconsistencies[0].ActualQuota)
}

func TestRunReconciliation_NoInconsistencies(t *testing.T) {
	account := &Account{UserID: "user1", Balance: 1000, FrozenAmount: 0, Group: "default"}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	reconRepo := &mockReconRepo{
		accounts:   []*Account{account},
		ledgerSums: map[string]int64{"user1": 1000},
	}

	uc := NewReconciliationUsecase(accountRepo, reservationRepo, reconRepo, nil)
	result, err := uc.RunReconciliation(context.Background())

	require.NoError(t, err)
	assert.Empty(t, result.AccountInconsistencies)
	assert.Equal(t, 1, result.TotalAccounts)
}

func TestRunReconciliation_RecordsMetrics(t *testing.T) {
	account := &Account{UserID: "user1", Balance: 1000, FrozenAmount: 0, Group: "default"}
	reconRepo := &mockReconRepo{
		accounts:   []*Account{account},
		ledgerSums: map[string]int64{"user1": 500},
	}
	uc := NewReconciliationUsecase(
		&mockAccountRepo{account: account},
		&mockReservationRepo{reservations: make(map[string]*Reservation)},
		reconRepo,
		nil,
	)
	runBefore := testutil.ToFloat64(metrics.ReconciliationRunsTotal.WithLabelValues("discrepancy"))
	accountDiffBefore := testutil.ToFloat64(metrics.ReconciliationDiscrepanciesTotal.WithLabelValues(ReconciliationDiscrepancyTypeAccount))

	_, err := uc.RunReconciliation(context.Background())

	require.NoError(t, err)
	assert.InEpsilon(t, 1, testutil.ToFloat64(metrics.ReconciliationRunsTotal.WithLabelValues("discrepancy"))-runBefore, 0.000001)
	assert.InEpsilon(t, 1, testutil.ToFloat64(metrics.ReconciliationDiscrepanciesTotal.WithLabelValues(ReconciliationDiscrepancyTypeAccount))-accountDiffBefore, 0.000001)
}

func TestRunReconciliation_ChannelUsageConsistency(t *testing.T) {
	account := &Account{UserID: "user1", Balance: 1000, FrozenAmount: 0, Group: "default"}
	reconRepo := &mockReconRepo{
		accounts:   []*Account{account},
		ledgerSums: map[string]int64{"user1": 1000},
		channels: []*ChannelUsageSnapshot{
			{ChannelID: 10, UsedQuota: 250},
		},
		channelLedgerUsage: []*ChannelLedgerUsage{
			{ChannelID: 10, Quota: 500, UpstreamCost: 123},
		},
	}

	uc := NewReconciliationUsecase(
		&mockAccountRepo{account: account},
		&mockReservationRepo{reservations: make(map[string]*Reservation)},
		reconRepo,
		nil,
	)
	result, err := uc.RunReconciliation(context.Background())

	require.NoError(t, err)
	require.Len(t, result.ChannelInconsistencies, 1)
	assert.Equal(t, int64(10), result.ChannelInconsistencies[0].ChannelID)
	assert.Equal(t, int64(500), result.ChannelInconsistencies[0].ExpectedUsedQuota)
	assert.Equal(t, int64(250), result.ChannelInconsistencies[0].ActualUsedQuota)
	assert.Equal(t, int64(123), result.ChannelInconsistencies[0].UpstreamCost)
	assert.Equal(t, 1, result.DiscrepancyCount())
}

func TestRunReconciliation_LogLedgerConsistency(t *testing.T) {
	account := &Account{UserID: "user1", Balance: 1000, FrozenAmount: 0, Group: "default"}
	reconRepo := &mockReconRepo{
		accounts:             []*Account{account},
		ledgerSums:           map[string]int64{"user1": 1000},
		ledgerConsumeSummary: &ConsumeSummary{Count: 2, Quota: 700},
		logConsumeSummary:    &ConsumeSummary{Count: 1, Quota: 400},
	}

	uc := NewReconciliationUsecase(
		&mockAccountRepo{account: account},
		&mockReservationRepo{reservations: make(map[string]*Reservation)},
		reconRepo,
		nil,
	)
	result, err := uc.RunReconciliation(context.Background())

	require.NoError(t, err)
	require.Len(t, result.LogInconsistencies, 1)
	assert.Equal(t, int64(2), result.LogInconsistencies[0].LedgerCount)
	assert.Equal(t, int64(1), result.LogInconsistencies[0].LogCount)
	assert.Equal(t, int64(300), result.LogInconsistencies[0].QuotaDiff)
	assert.Equal(t, 1, result.DiscrepancyCount())
}

// TestRunReconciliation_RefundReversalConsistent verifies that when refunded
// orders and reversal ledger amounts match (quota-normalized), no refund
// discrepancy is reported (phase 2.3 reconciliation coverage).
func TestRunReconciliation_RefundReversalConsistent(t *testing.T) {
	repo := &mockReconRepo{
		refundedOrderCount:   2,
		refundedOrderCents:   400000, // 4000 quota
		reversalLedgerAmount: 4000,   // matches 400000 cents / 100
	}
	accountRepo := &mockAccountRepo{}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	uc := NewReconciliationUsecase(accountRepo, reservationRepo, repo, nil)
	res, err := uc.RunReconciliation(context.Background())
	require.NoError(t, err)
	assert.Empty(t, res.RefundInconsistencies, "no discrepancy when refunds match reversal ledgers")
}

// TestRunReconciliation_RefundReversalMismatch verifies that a mismatch between
// refunded order money and the reversal ledger amount is surfaced as a
// refund-specific discrepancy (phase 2.3 reconciliation coverage).
func TestRunReconciliation_RefundReversalMismatch(t *testing.T) {
	repo := &mockReconRepo{
		refundedOrderCount:   1,
		refundedOrderCents:   200000, // 2000 quota expected
		reversalLedgerAmount: 1500,   // 500 quota short -> missing reversal
	}
	accountRepo := &mockAccountRepo{}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	uc := NewReconciliationUsecase(accountRepo, reservationRepo, repo, nil)
	res, err := uc.RunReconciliation(context.Background())
	require.NoError(t, err)
	require.Len(t, res.RefundInconsistencies, 1, "mismatch must be reported")
	d := res.RefundInconsistencies[0]
	assert.Equal(t, int64(1), d.RefundedOrderCount)
	assert.Equal(t, int64(200000), d.RefundedOrderMoneyCents)
	assert.Equal(t, int64(1500), d.ReversalLedgerAmount)
	// MoneyCentsDiff = refunded_cents - reversal_amount*100 = 200000 - 150000
	assert.Equal(t, int64(50000), d.MoneyCentsDiff)
}

// TestRunReconciliation_RefundedOrdersWithoutAnyReversal verifies the special
// case where refunded orders exist but no reversal ledger was written at all.
func TestRunReconciliation_RefundedOrdersWithoutAnyReversal(t *testing.T) {
	repo := &mockReconRepo{
		refundedOrderCount:   3,
		refundedOrderCents:   600000,
		reversalLedgerAmount: 0,
	}
	accountRepo := &mockAccountRepo{}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	uc := NewReconciliationUsecase(accountRepo, reservationRepo, repo, nil)
	res, err := uc.RunReconciliation(context.Background())
	require.NoError(t, err)
	require.Len(t, res.RefundInconsistencies, 1, "missing reversal must be reported")
}
