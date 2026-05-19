package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockReconRepo struct {
	accounts []*Account
	ledgerSums map[string]int64
	reservations []*Reservation
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

func (m *mockReconRepo) ListReservationsByStatus(ctx context.Context, status string) ([]*Reservation, error) {
	var result []*Reservation
	for _, r := range m.reservations {
		if r.Status == status {
			result = append(result, r)
		}
	}
	return result, nil
}

func TestRunReconciliation_ExpiredReservations(t *testing.T) {
	account := &Account{UserID: "user1", Quota: 900, FrozenQuota: 100, Group: "default"}
	expiredRes := &Reservation{
		ReservationID: "res1",
		UserID:        "user1",
		Amount:        100,
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

	require.NoError(t, err)
	assert.Equal(t, 1, result.ExpiredCleaned)
	assert.Equal(t, ReservationStatusExpired, expiredRes.Status)
	assert.Equal(t, int64(0), account.FrozenQuota)
}

func TestRunReconciliation_QuotaConsistency(t *testing.T) {
	account := &Account{UserID: "user1", Quota: 1000, FrozenQuota: 0, Group: "default"}

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
	account := &Account{UserID: "user1", Quota: 1000, FrozenQuota: 0, Group: "default"}

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
