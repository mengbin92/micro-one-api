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
	refundRows          []*RefundReconciliation
	stuckIssuedOrders   []*PaymentOrder
	activeSubscriptions []*SubscriptionUsageSnapshot
	subscriptionCosts   map[int64]map[int64]int64 // subscription id -> window start unix -> amount
}

type recordingReconciliationRunStore struct {
	saved []*ReconciliationResult
	err   error
}

func (s *recordingReconciliationRunStore) SaveRun(_ context.Context, result *ReconciliationResult) (int64, error) {
	s.saved = append(s.saved, result)
	if s.err != nil {
		return 0, s.err
	}
	return int64(len(s.saved)), nil
}

func (*recordingReconciliationRunStore) ListRuns(context.Context, int32, int32) ([]*ReconciliationResult, int64, error) {
	return nil, 0, nil
}

func (*recordingReconciliationRunStore) GetRun(context.Context, int64) (*ReconciliationResult, error) {
	return nil, nil
}

func (m *mockReconRepo) ListAllAccounts(ctx context.Context) ([]*Account, error) {
	return m.accounts, nil
}

func (m *mockReconRepo) LatestLedgerBalanceAfter(ctx context.Context, userID string) (int64, bool, error) {
	if sum, ok := m.ledgerSums[userID]; ok {
		return sum, true, nil
	}
	return 0, false, nil
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
	return m.activeSubscriptions, nil
}

func (m *mockReconRepo) SumSubscriptionCostSince(ctx context.Context, subscriptionID int64, since time.Time) (int64, error) {
	if windows := m.subscriptionCosts[subscriptionID]; windows != nil {
		return windows[since.Unix()], nil
	}
	return 0, nil
}

func (m *mockReconRepo) SumPendingReceivables(ctx context.Context) (int64, error) {
	return 0, nil
}

func (m *mockReconRepo) SumOverdraftBalances(ctx context.Context) (int64, error) {
	return 0, nil
}

func (m *mockReconRepo) ListRefundReconciliations(context.Context) ([]*RefundReconciliation, error) {
	return m.refundRows, nil
}

func (m *mockReconRepo) ListStuckIssuedOrders(ctx context.Context) ([]*PaymentOrder, error) {
	return m.stuckIssuedOrders, nil
}

func TestRunReconciliation_ExpiredReservations(t *testing.T) {
	account := &Account{UserID: "user1", Balance: 900, FrozenAmount: 100, Group: "default"}
	expiredRes := &Reservation{
		ReservationID: "res1",
		UserID:        "user1",
		Amount:        100,
		BalanceAmount: 100, // legacy balance-only: wallet-side == Amount
		Status:        ReservationStatusReserved,
		ExpiredAt:     time.Now().Add(-10 * time.Minute),
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
	require.ErrorContains(t, err, "atomic reservation releaser")
	assert.Equal(t, 0, result.ExpiredCleaned)
	assert.Equal(t, ReservationStatusReserved, expiredRes.Status)
	assert.Equal(t, int64(100), account.FrozenAmount)
	assert.Equal(t, int64(900), account.Balance)

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

func TestRunReconciliation_PersistsExactlyOneCompletedRun(t *testing.T) {
	account := &Account{UserID: "user1", Balance: 1000}
	store := &recordingReconciliationRunStore{}
	uc := NewReconciliationUsecase(
		&mockAccountRepo{account: account},
		&mockReservationRepo{reservations: map[string]*Reservation{}},
		&mockReconRepo{accounts: []*Account{account}, ledgerSums: map[string]int64{"user1": 1000}},
		store,
	)

	result, err := uc.RunReconciliation(context.Background())

	require.NoError(t, err)
	require.Len(t, store.saved, 1)
	assert.Equal(t, int64(1), result.RunID)
	assert.Same(t, result, store.saved[0])
}

func TestRunReconciliation_ActiveFrozenBalanceMatchesSettledSnapshot(t *testing.T) {
	account := &Account{UserID: "user1", Balance: 900, FrozenAmount: 100, Group: "default"}
	reconRepo := &mockReconRepo{
		accounts:   []*Account{account},
		ledgerSums: map[string]int64{"user1": 1000},
	}
	uc := NewReconciliationUsecase(
		&mockAccountRepo{account: account},
		&mockReservationRepo{reservations: map[string]*Reservation{}},
		reconRepo,
		nil,
	)

	result, err := uc.RunReconciliation(context.Background())
	require.NoError(t, err)
	assert.Empty(t, result.AccountInconsistencies)
}

func TestRunReconciliation_SubscriptionWindows(t *testing.T) {
	daily := time.Unix(100, 0)
	weekly := time.Unix(200, 0)
	monthly := time.Unix(300, 0)
	repo := &mockReconRepo{
		activeSubscriptions: []*SubscriptionUsageSnapshot{{
			SubscriptionID:     7,
			UserID:             42,
			DailyUsageUSD:      1.25,
			WeeklyUsageUSD:     2.50,
			MonthlyUsageUSD:    3.01, // expected 3.00: one real mismatch
			DailyWindowStart:   daily.Unix(),
			WeeklyWindowStart:  weekly.Unix(),
			MonthlyWindowStart: monthly.Unix(),
			RateMultiplier:     2,
		}},
		subscriptionCosts: map[int64]map[int64]int64{
			7: {
				daily.Unix():   6250,
				weekly.Unix():  12500,
				monthly.Unix(): 15000,
			},
		},
	}
	uc := NewReconciliationUsecase(&mockAccountRepo{}, &mockReservationRepo{reservations: map[string]*Reservation{}}, repo, nil)
	result, err := uc.RunReconciliation(context.Background())
	require.NoError(t, err)
	require.Len(t, result.SubscriptionInconsistencies, 1)
	assert.Equal(t, "monthly", result.SubscriptionInconsistencies[0].Window)
	assert.InDelta(t, 0.01, result.SubscriptionInconsistencies[0].Difference, 1e-9)
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
			// Deleting a channel removes its counter, but preserves its ledger.
			{ChannelID: 11, Quota: 700, UpstreamCost: 200},
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
		refundRows: []*RefundReconciliation{
			{TradeNo: "refund1", RefundedOrder: true, MoneyCents: 200000, ReversalLedgerCount: 1, ReversalLedgerAmount: 2000},
			{TradeNo: "refund2", RefundedOrder: true, MoneyCents: 200000, ReversalLedgerCount: 1, ReversalLedgerAmount: 2000},
		},
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
		refundRows: []*RefundReconciliation{{TradeNo: "short-refund", RefundedOrder: true, MoneyCents: 200000, ReversalLedgerCount: 1, ReversalLedgerAmount: 1500}},
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
		refundRows: []*RefundReconciliation{{TradeNo: "missing-refund", RefundedOrder: true, MoneyCents: 200000}},
	}
	accountRepo := &mockAccountRepo{}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	uc := NewReconciliationUsecase(accountRepo, reservationRepo, repo, nil)
	res, err := uc.RunReconciliation(context.Background())
	require.NoError(t, err)
	require.Len(t, res.RefundInconsistencies, 1, "missing reversal must be reported")
}

func TestRunReconciliation_RefundMixedFrozenRates(t *testing.T) {
	repo := &mockReconRepo{
		refundRows: []*RefundReconciliation{
			{TradeNo: "legacy", UserID: "1", RefundedOrder: true, MoneyCents: 10000, ReversalLedgerCount: 1, ReversalLedgerAmount: 100},
			{TradeNo: "rate2.5", UserID: "2", RefundedOrder: true, Currency: "CNY", MoneyCents: 5001, PlanSnapshot: `{"plan_id":1,"price_quota":100,"price_conversion":{"version":1,"recharge_multiplier":2.5,"wallet_amount":2500000}}`, ReversalLedgerCount: 1, ReversalLedgerAmount: 1250250},
			{TradeNo: "group-only-rate10", UserID: "3", RefundedOrder: true, Currency: "CNY", MoneyCents: 5999, PlanSnapshot: `{"plan_id":0,"group_id":3,"price_quota":100,"price_conversion":{"version":1,"recharge_multiplier":10,"wallet_amount":10000000}}`, ReversalLedgerCount: 1, ReversalLedgerAmount: 5999000},
		},
	}
	uc := NewReconciliationUsecase(&mockAccountRepo{}, &mockReservationRepo{}, repo, nil)
	res, err := uc.RunReconciliation(context.Background())
	require.NoError(t, err)
	require.Empty(t, res.RefundInconsistencies)
}

func TestRunReconciliation_RefundPerOrderPreventsOffsettingErrors(t *testing.T) {
	repo := &mockReconRepo{
		refundRows: []*RefundReconciliation{
			{TradeNo: "short", UserID: "1", RefundedOrder: true, MoneyCents: 10000, ReversalLedgerCount: 1, ReversalLedgerAmount: 99},
			{TradeNo: "extra", UserID: "2", RefundedOrder: true, MoneyCents: 10000, ReversalLedgerCount: 1, ReversalLedgerAmount: 101},
		},
	}
	res, err := NewReconciliationUsecase(&mockAccountRepo{}, &mockReservationRepo{}, repo, nil).RunReconciliation(context.Background())
	require.NoError(t, err)
	require.Len(t, res.RefundInconsistencies, 2)
	require.Equal(t, int64(1), res.RefundInconsistencies[0].AmountDiff)
	require.Equal(t, int64(-1), res.RefundInconsistencies[1].AmountDiff)
}

func TestRunReconciliation_RefundFixedAmountCountAndLegacyFallback(t *testing.T) {
	repo := &mockReconRepo{refundRows: []*RefundReconciliation{
		{TradeNo: "one-unit-short", UserID: "1", RefundedOrder: true, Currency: "CNY", MoneyCents: 10000,
			PlanSnapshot:        `{"plan_id":1,"price_quota":100,"price_conversion":{"version":1,"recharge_multiplier":10,"wallet_amount":10000000}}`,
			ReversalLedgerCount: 1, ReversalLedgerAmount: 9999999},
		{TradeNo: "duplicate", UserID: "2", RefundedOrder: true, MoneyCents: 10000, ReversalLedgerCount: 2, ReversalLedgerAmount: 100},
		{TradeNo: "orphan", UserID: "3", ReversalLedgerCount: 1, ReversalLedgerAmount: 40},
		{TradeNo: "legacy-fallback", UserID: "4", RefundedOrder: true, MoneyCents: 0,
			PlanSnapshot: `{"plan_id":1,"price_quota":100}`, ReversalLedgerCount: 1, ReversalLedgerAmount: 100},
	}}
	res, err := NewReconciliationUsecase(&mockAccountRepo{}, &mockReservationRepo{}, repo, nil).RunReconciliation(context.Background())
	require.NoError(t, err)
	require.Len(t, res.RefundInconsistencies, 3)
	require.Equal(t, int64(1), res.RefundInconsistencies[0].AmountDiff)
	require.Zero(t, res.RefundInconsistencies[0].MoneyCentsDiff)
	require.Equal(t, int64(2), res.RefundInconsistencies[1].ReversalLedgerCount)
	require.Zero(t, res.RefundInconsistencies[1].AmountDiff)
	require.Zero(t, res.RefundInconsistencies[2].RefundedOrderCount)
	require.Equal(t, int64(-40), res.RefundInconsistencies[2].AmountDiff)
}

func TestRunReconciliation_InvalidRefundSnapshotIsPartial(t *testing.T) {
	repo := &mockReconRepo{refundRows: []*RefundReconciliation{{TradeNo: "invalid", RefundedOrder: true, MoneyCents: 10000,
		PlanSnapshot:        `{"plan_id":1,"price_quota":100,"price_conversion":{"version":1,"recharge_multiplier":10,"wallet_amount":1}}`,
		ReversalLedgerCount: 1, ReversalLedgerAmount: 100}}}
	res, err := NewReconciliationUsecase(&mockAccountRepo{}, &mockReservationRepo{}, repo, nil).RunReconciliation(context.Background())
	require.NoError(t, err)
	require.Equal(t, "partial", res.Status)
	require.Contains(t, res.ErrorMessage, "refund price invalid")
}

// TestRunReconciliation_StuckIssuance verifies that paid+issued+subscription_id=0
// orders are reported as stuck-issuance discrepancies so operators can
// re-trigger completion (code-review M1).
func TestRunReconciliation_StuckIssuance(t *testing.T) {
	stuck := &PaymentOrder{
		TradeNo:          "PAY-STUCK-1",
		UserID:           "42",
		Status:           PaymentOrderStatusPaid,
		AssetIssueStatus: PaymentAssetIssueStatusIssued,
		SubscriptionID:   0,
		GroupID:          7,
		MoneyCents:       3000,
	}
	repo := &mockReconRepo{stuckIssuedOrders: []*PaymentOrder{stuck}}
	accountRepo := &mockAccountRepo{}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	uc := NewReconciliationUsecase(accountRepo, reservationRepo, repo, nil)
	res, err := uc.RunReconciliation(context.Background())
	require.NoError(t, err)
	require.Len(t, res.StuckIssuanceInconsistencies, 1, "stuck order must be reported")
	inc := res.StuckIssuanceInconsistencies[0]
	assert.Equal(t, "PAY-STUCK-1", inc.TradeNo)
	assert.Equal(t, "42", inc.UserID)
	assert.Equal(t, int64(7), inc.GroupID)
	assert.Equal(t, int64(3000), inc.MoneyCents)
	assert.Equal(t, 1, res.DiscrepancyCount())
}

// TestRunReconciliation_NoStuckIssuance verifies that an empty stuck set
// produces no stuck-issuance discrepancies.
func TestRunReconciliation_NoStuckIssuance(t *testing.T) {
	repo := &mockReconRepo{}
	accountRepo := &mockAccountRepo{}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	uc := NewReconciliationUsecase(accountRepo, reservationRepo, repo, nil)
	res, err := uc.RunReconciliation(context.Background())
	require.NoError(t, err)
	assert.Empty(t, res.StuckIssuanceInconsistencies)
}
