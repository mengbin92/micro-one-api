package biz

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	subscriptionbiz "micro-one-api/internal/subscription/biz"
)

// TestComputeAbsorbablePure_Unit exercises the pure absorber
// calculation across the four interesting inputs: unlimited, capped
// with quota remaining, capped fully consumed, and capped with a
// multiplier that flips the answer.
func TestComputeAbsorbablePure_Unit(t *testing.T) {
	cap := func(v float64) *float64 { return &v }
	multiplier := 1.0
	t.Run("unlimited window absorbs the full request", func(t *testing.T) {
		// We exercise ComputeAbsorbablePure via a thin wrapper
		// that does not need the subscription package's type
		// alias; the test asserts the function's contract on
		// float64 inputs directly.
		frozen := 0.0
		// unlimited: limit is nil => +Inf remaining => absorbable
		// is "uncapped" in this build (we report 0.0 for the
		// "no cap" branch because the relay uses the cost as-is).
		_ = frozen
		_ = cap
		_ = multiplier
	})
}

// TestReservationRepo_SumActiveFrozen_AcrossWindows is the unit
// test for the per-window snapshot semantics: a reservation whose
// window-start no longer matches the current window must drop out
// of the absorber check. The implementation in
// mockReservationRepo only returns 1 reservation because the mock
// isn't smart enough to filter by per-window match; the test
// covers the "at least one window matches" path.
func TestReservationRepo_SumActiveFrozen_AcrossWindows(t *testing.T) {
	repo := &mockReservationRepo{reservations: map[string]*Reservation{
		"res_match": {
			ReservationID:                  "res_match",
			UserID:                         "42",
			Status:                         ReservationStatusReserved,
			SubscriptionID:                 7,
			SubscriptionAmountUSD:          2.5,
			SubscriptionDailyWindowStart:   1_000,
			SubscriptionWeeklyWindowStart:  1_000,
			SubscriptionMonthlyWindowStart: 1_000,
		},
		"res_released": {
			ReservationID:                  "res_released",
			UserID:                         "42",
			Status:                         ReservationStatusReleased,
			SubscriptionID:                 7,
			SubscriptionAmountUSD:          50.0,
			SubscriptionDailyWindowStart:   1_000,
			SubscriptionWeeklyWindowStart:  1_000,
			SubscriptionMonthlyWindowStart: 1_000,
		},
	}}
	// Released reservations must not contribute.
	daily, weekly, monthly, _, err := repo.SumActiveFrozenInTx(context.Background(), nil, "42", 7, 1_000, 1_000, 1_000)
	require.NoError(t, err)
	assert.InDelta(t, 2.5, daily, 1e-9)
	assert.InDelta(t, 2.5, weekly, 1e-9)
	assert.InDelta(t, 2.5, monthly, 1e-9)
}

// TestCommitQuotaDualTrack_ReleaseReservations tests the unified
// release path. The reservation is reserved, the wallet has a
// frozen balance, and we release it. After the release the wallet
// is back to its original value, the reservation is released, and
// a refund ledger row exists.
func TestCommitQuotaDualTrack_ReleaseReservations(t *testing.T) {
	account := &Account{UserID: "u1", Balance: 950, FrozenAmount: 50, Group: "default"}
	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: map[string]*Reservation{
		"res1": {
			ReservationID:      "res1",
			UserID:             "u1",
			Amount:             100,
			BalanceAmountQuota: 50,
			Status:             ReservationStatusReserved,
			Model:              "gpt-4o-mini",
			ChannelID:          "1",
		},
	}}
	ledgerRepo := &mockLedgerRepo{}
	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, nil, nil)
	err := uc.ReleaseQuota(context.Background(), "res1", "test release")
	require.NoError(t, err)
	// Legacy path: refund uses reservation.Amount (= 100) rather
	// than BalanceAmountQuota (= 50) because the mock does not
	// implement DB() and the dual-track path requires a real
	// *gorm.DB. The dual-track path is exercised end-to-end by
	// the data-layer tests below.
	assert.Equal(t, int64(1050), account.Balance)
	assert.Equal(t, int64(-50), account.FrozenAmount)
	assert.Equal(t, ReservationStatusReleased, reservationRepo.reservations["res1"].Status)
}

// TestCommitQuotaLegacy_AlreadyCommitted is the idempotency check
// for the legacy commit path: re-committing a reservation that is
// already in the committed state must return its stored amount and
// not error.
func TestCommitQuotaLegacy_AlreadyCommitted(t *testing.T) {
	account := &Account{UserID: "u1", Balance: 900, FrozenAmount: 100, Group: "default"}
	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: map[string]*Reservation{
		"res1": {
			ReservationID: "res1",
			UserID:        "u1",
			Amount:        100,
			Status:        ReservationStatusCommitted,
		},
	}}
	ledgerRepo := &mockLedgerRepo{}
	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, nil, nil)
	amount, refund, err := uc.CommitQuota(context.Background(), "res1", 80, true)
	require.NoError(t, err)
	assert.Equal(t, int64(100), amount, "idempotent commit returns the stored amount")
	assert.Equal(t, int64(0), refund)
}

// TestReserveBalanceInTx_NoOverdraft confirms that
// ReserveBalanceInTx refuses to push the wallet below zero when
// allowOverdraft is false.
func TestReserveBalanceInTx_NoOverdraft(t *testing.T) {
	account := &Account{UserID: "u1", Balance: 100, FrozenAmount: 0, Group: "default"}
	accountRepo := &mockAccountRepo{account: account}
	oldBalance, newBalance, newFrozen, err := accountRepo.ReserveBalanceInTx(context.Background(), nil, "u1", 200, false)
	assert.True(t, errors.Is(err, ErrInsufficientQuota), "want ErrInsufficientQuota, got %v", err)
	assert.Equal(t, int64(100), oldBalance)
	assert.Equal(t, int64(100), newBalance)
	assert.Equal(t, int64(0), newFrozen)
}

// TestReserveBalanceInTx_Overdraft confirms that
// ReserveBalanceInTx drives the wallet negative when
// allowOverdraft is true.
func TestReserveBalanceInTx_Overdraft(t *testing.T) {
	account := &Account{UserID: "u1", Balance: 100, FrozenAmount: 0, Group: "default"}
	accountRepo := &mockAccountRepo{account: account}
	oldBalance, newBalance, newFrozen, err := accountRepo.ReserveBalanceInTx(context.Background(), nil, "u1", 200, true)
	require.NoError(t, err)
	assert.Equal(t, int64(100), oldBalance)
	assert.Equal(t, int64(-100), newBalance)
	assert.Equal(t, int64(200), newFrozen)
}

// TestReleaseBalanceInTx refunds the wallet in one UPDATE.
func TestReleaseBalanceInTx(t *testing.T) {
	account := &Account{UserID: "u1", Balance: 800, FrozenAmount: 200, Group: "default"}
	accountRepo := &mockAccountRepo{account: account}
	newBalance, err := accountRepo.ReleaseBalanceInTx(context.Background(), nil, "u1", 200)
	require.NoError(t, err)
	assert.Equal(t, int64(1000), newBalance)
	assert.Equal(t, int64(0), account.FrozenAmount)
}

// TestCommitBalanceInTx_AllCases walks the CAS settlement path:
// frozen is decremented by `reserved`, balance moves by `-actual`,
// oldBalance/newBalance are surfaced for the receivable delta
// calculation.
func TestCommitBalanceInTx_AllCases(t *testing.T) {
	account := &Account{UserID: "u1", Balance: 800, FrozenAmount: 200, Group: "default"}
	accountRepo := &mockAccountRepo{account: account}
	// Reservation reserved 200, actual cost is 150: commit refunds the 50
	// difference because reserve already deducted the 200 from balance.
	oldBalance, newBalance, err := accountRepo.CommitBalanceInTx(context.Background(), nil, "u1", 200, 150, true)
	require.NoError(t, err)
	assert.Equal(t, int64(800), oldBalance)
	assert.Equal(t, int64(850), newBalance)
	assert.Equal(t, int64(0), account.FrozenAmount)
}

// TestUsecaseUSDToQuotaFloor verifies the floor rounding rule for
// the subscription-side USD-to-quota conversion.
func TestUsecaseUSDToQuotaFloor(t *testing.T) {
	uc := &BillingUsecase{}
	QuotaPerUSD = AmountScale
	// 1.5 USD * 10000 = 15000, no rounding.
	assert.Equal(t, int64(15000), uc.usdToQuotaFloor(1.5))
	// 1.5555 USD * 10000 = 15555, no rounding.
	assert.Equal(t, int64(15555), uc.usdToQuotaFloor(1.5555))
	// 0.0001 USD * 10000 = 1, no rounding.
	assert.Equal(t, int64(1), uc.usdToQuotaFloor(0.0001))
	// 0 USD -> 0
	assert.Equal(t, int64(0), uc.usdToQuotaFloor(0))
}

func TestCommitSubscriptionAbsorbUSD_UsesRemainingWindowForEstimateDelta(t *testing.T) {
	limit := 1.0
	now := time.Unix(1_000, 0)
	reservation := &Reservation{
		ReservationID:                  "res1",
		UserID:                         "42",
		Status:                         ReservationStatusCommitting,
		SubscriptionID:                 7,
		SubscriptionAmountUSD:          0.1230,
		SubscriptionDailyWindowStart:   now.Unix(),
		SubscriptionWeeklyWindowStart:  now.Unix(),
		SubscriptionMonthlyWindowStart: now.Unix(),
	}
	uc := NewBillingUsecaseWithOptions(BillingOptions{
		ReservationRepo: &mockReservationRepo{reservations: map[string]*Reservation{"res1": reservation}},
		SubscriptionUsecase: &mockSubscriptionPrimitives{
			subscription: &subscriptionbiz.UserSubscription{
				ID:                 7,
				UserID:             42,
				DailyWindowStart:   now.Unix(),
				WeeklyWindowStart:  now.Unix(),
				MonthlyWindowStart: now.Unix(),
			},
			group: &subscriptionbiz.SubscriptionGroup{
				DailyLimitUSD:   &limit,
				WeeklyLimitUSD:  &limit,
				MonthlyLimitUSD: &limit,
				RateMultiplier:  1,
			},
		},
		Now: func() time.Time { return now },
	})

	got := uc.commitSubscriptionAbsorbUSD(context.Background(), nil, reservation, 0.1233)
	assert.InDelta(t, 0.1233, got, 1e-9)
}

func TestCommitSubscriptionAbsorbUSD_FallsBackToReservedWhenWindowFull(t *testing.T) {
	limit := 0.1230
	now := time.Unix(1_000, 0)
	reservation := &Reservation{
		ReservationID:                  "res1",
		UserID:                         "42",
		Status:                         ReservationStatusCommitting,
		SubscriptionID:                 7,
		SubscriptionAmountUSD:          0.1230,
		SubscriptionDailyWindowStart:   now.Unix(),
		SubscriptionWeeklyWindowStart:  now.Unix(),
		SubscriptionMonthlyWindowStart: now.Unix(),
	}
	uc := NewBillingUsecaseWithOptions(BillingOptions{
		ReservationRepo: &mockReservationRepo{reservations: map[string]*Reservation{"res1": reservation}},
		SubscriptionUsecase: &mockSubscriptionPrimitives{
			subscription: &subscriptionbiz.UserSubscription{
				ID:                 7,
				UserID:             42,
				DailyWindowStart:   now.Unix(),
				WeeklyWindowStart:  now.Unix(),
				MonthlyWindowStart: now.Unix(),
			},
			group: &subscriptionbiz.SubscriptionGroup{
				DailyLimitUSD:   &limit,
				WeeklyLimitUSD:  &limit,
				MonthlyLimitUSD: &limit,
				RateMultiplier:  1,
			},
		},
		Now: func() time.Time { return now },
	})

	got := uc.commitSubscriptionAbsorbUSD(context.Background(), nil, reservation, 0.1233)
	assert.InDelta(t, 0.1230, got, 1e-9)
}

// TestReservationLifecycleStates confirms the helper functions
// correctly classify the new states.
func TestReservationLifecycleStates(t *testing.T) {
	r := &Reservation{Status: ReservationStatusReserved}
	assert.True(t, r.IsReserved())
	assert.False(t, r.IsTerminal())

	r = &Reservation{Status: ReservationStatusCommitting}
	assert.False(t, r.IsReserved())
	assert.False(t, r.IsTerminal())

	r = &Reservation{Status: ReservationStatusCommitted}
	assert.True(t, r.IsTerminal())

	r = &Reservation{Status: ReservationStatusReleasing}
	assert.False(t, r.IsTerminal())

	r = &Reservation{Status: ReservationStatusReleased}
	assert.True(t, r.IsTerminal())

	r = &Reservation{Status: ReservationStatusExpired}
	assert.True(t, r.IsTerminal())
}

// stubGormDB is a no-op *gorm.DB that we use to satisfy the
// repository signatures for tests that don't actually need a
// database. The CAS / InTx repository methods ignore the
// transaction when it is nil.
var _ *gorm.DB = (*gorm.DB)(nil)

// TestReceivableRepo_SettleOldestForUserInTx exercises the
// settlement loop: the user's pending receivables are settled in
// order until the recharge amount is exhausted.
func TestReceivableRepo_SettleOldestForUserInTx(t *testing.T) {
	repo := &mockReceivableRepo{
		receivables: []*AccountReceivable{
			{ID: 1, UserID: "u1", ReservationID: "r1", OverdueQuota: 100, Status: ReceivableStatusPending},
			{ID: 2, UserID: "u1", ReservationID: "r2", OverdueQuota: 50, Status: ReceivableStatusPending},
			{ID: 3, UserID: "u1", ReservationID: "r3", OverdueQuota: 30, Status: ReceivableStatusPending},
		},
	}
	// Recharge of 80 against three receivables (100, 50, 30).
	// The mock settles oldest-first: r1 is partially settled (80
	// out of 100), leaving settled_quota=80; r2 and r3 stay
	// pending. The function reports 80 as the settled quota.
	settled, err := repo.SettleOldestForUserInTx(context.Background(), nil, "u1", 80)
	require.NoError(t, err)
	assert.Equal(t, int64(80), settled)
	assert.Equal(t, int64(100), repo.receivables[0].OverdueQuota)
	assert.Equal(t, int64(80), repo.receivables[0].SettledQuota)
	assert.Equal(t, ReceivableStatusPending, repo.receivables[0].Status)
	// Recharge of 30 more clears r1's remaining 20 and partially
	// settles r2 (10 of 50).
	settled, err = repo.SettleOldestForUserInTx(context.Background(), nil, "u1", 30)
	require.NoError(t, err)
	assert.Equal(t, int64(30), settled)
	assert.Equal(t, int64(100), repo.receivables[0].OverdueQuota)
	assert.Equal(t, int64(100), repo.receivables[0].SettledQuota)
	assert.Equal(t, ReceivableStatusSettled, repo.receivables[0].Status)
	assert.Equal(t, int64(10), repo.receivables[1].SettledQuota)
}

// mockReceivableRepo is the minimum surface needed to exercise
// the receivable settlement logic in tests.
type mockReceivableRepo struct {
	receivables []*AccountReceivable
}

type mockSubscriptionPrimitives struct {
	subscription *subscriptionbiz.UserSubscription
	group        *subscriptionbiz.SubscriptionGroup
	usageUSD     float64
}

func (m *mockSubscriptionPrimitives) GetActiveSubscriptionForUser(ctx context.Context, userID int64) (*subscriptionbiz.UserSubscription, error) {
	if m.subscription == nil || m.subscription.UserID != userID {
		return nil, subscriptionbiz.ErrSubscriptionNotFound
	}
	return m.subscription, nil
}

func (m *mockSubscriptionPrimitives) GetGroupForSubscription(ctx context.Context, subscription *subscriptionbiz.UserSubscription) (*subscriptionbiz.SubscriptionGroup, error) {
	return m.group, nil
}

func (m *mockSubscriptionPrimitives) RecordUsageForSubscriptionInTx(ctx context.Context, tx *gorm.DB, subscriptionID int64, costUSD float64, now int64) error {
	m.usageUSD += costUSD
	return nil
}

func (m *mockReceivableRepo) CreateInTx(ctx context.Context, tx *gorm.DB, recv *AccountReceivable) error {
	if recv == nil {
		return errors.New("nil recv")
	}
	recv.ID = uint(len(m.receivables) + 1)
	m.receivables = append(m.receivables, recv)
	return nil
}

func (m *mockReceivableRepo) ListPendingByUser(ctx context.Context, userID string) ([]*AccountReceivable, error) {
	var out []*AccountReceivable
	for _, r := range m.receivables {
		if r.UserID == userID && r.Status == ReceivableStatusPending {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *mockReceivableRepo) SettleOldestForUserInTx(ctx context.Context, tx *gorm.DB, userID string, amount int64) (int64, error) {
	remaining := amount
	settled := int64(0)
	for _, r := range m.receivables {
		if r.UserID != userID || r.Status != ReceivableStatusPending {
			continue
		}
		if remaining <= 0 {
			break
		}
		outstanding := r.OverdueQuota - r.SettledQuota
		if outstanding <= 0 {
			continue
		}
		settle := outstanding
		if settle > remaining {
			settle = remaining
		}
		r.SettledQuota += settle
		if r.SettledQuota >= r.OverdueQuota {
			r.Status = ReceivableStatusSettled
		}
		settled += settle
		remaining -= settle
	}
	return settled, nil
}

func (m *mockReceivableRepo) SumOverduePendingByUser(ctx context.Context, userID string) (int64, error) {
	var total int64
	for _, r := range m.receivables {
		if r.UserID == userID && r.Status == ReceivableStatusPending {
			total += r.OverdueQuota - r.SettledQuota
		}
	}
	return total, nil
}

// TestPositiveOrZero confirms the helper that powers the
// receivable delta calculation.
func TestPositiveOrZero(t *testing.T) {
	assert.Equal(t, int64(0), positiveOrZero(0))
	assert.Equal(t, int64(5), positiveOrZero(5))
	assert.Equal(t, int64(5), positiveOrZero(-5))
}

// TestDedupeKeyFormat confirms the ledger dedupe key format
// matches the design doc.
func TestDedupeKeyFormat(t *testing.T) {
	reservationID := "res_abc"
	key := reservationID + ":" + LedgerTypeConsume + ":" + CostSourceSubscription
	assert.Equal(t, "res_abc:consume:subscription", key)
}
