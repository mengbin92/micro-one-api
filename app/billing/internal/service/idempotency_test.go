package service

import (
	"context"
	"errors"
	"testing"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/app/billing/internal/biz"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// minimalAccountRepo embeds biz.AccountRepo and implements only the methods
// the in-memory (no-runner) PurchaseSubscription path needs: snapshot read +
// balance update. Any other method panics if invoked (embedded nil interface).
type minimalAccountRepo struct {
	biz.AccountRepo
	balance int64
}

func (r *minimalAccountRepo) GetAccountSnapshot(_ context.Context, _ string) (*biz.Account, error) {
	return &biz.Account{UserID: "1", Balance: r.balance}, nil
}

func (r *minimalAccountRepo) UpdateBalance(_ context.Context, _ string, delta int64, _ string) (int64, error) {
	r.balance += delta
	return r.balance, nil
}

// conflictLedgerRepo embeds biz.LedgerRepo and makes every CreateLedger fail
// with a SQLite-style unique-constraint error, which biz maps to
// ErrDuplicateRequest (the idempotency gate).
type conflictLedgerRepo struct {
	biz.LedgerRepo
}

func (r *conflictLedgerRepo) CreateLedger(_ context.Context, _ *biz.Ledger) error {
	return errors.New("UNIQUE constraint failed: billing_ledgers.ledger_dedupe_key")
}

// TestPurchaseSubscription_DuplicateMapsToAlreadyExists guards the v0.18 P0
// §5.4 requirement: a duplicate (user_id, request_id) purchase surfaces as a
// gRPC AlreadyExists error (which admin maps to HTTP 409), NOT as a swallowed
// Success:false business response (which would yield HTTP 200).
func TestPurchaseSubscription_DuplicateMapsToAlreadyExists(t *testing.T) {
	uc := biz.NewBillingUsecase(
		&minimalAccountRepo{balance: 1000},
		nil,
		&conflictLedgerRepo{},
		nil,
		nil,
	)
	svc := NewBillingService(uc, nil, nil, nil)

	_, err := svc.PurchaseSubscription(context.Background(), &billingv1.PurchaseSubscriptionRequest{
		UserId: "1", PriceAmount: 100, GroupId: 5, Remark: "purchase", RequestId: "req-1",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "duplicate must surface as a gRPC status error, got %v", err)
	require.Equal(t, codes.AlreadyExists, st.Code(), "duplicate must map to AlreadyExists")
}

// TestTopUpQuota_DuplicateMapsToAlreadyExists is the same §5.4 guard for the
// recharge path.
func TestTopUpQuota_DuplicateMapsToAlreadyExists(t *testing.T) {
	uc := biz.NewBillingUsecase(
		&minimalAccountRepo{balance: 1000},
		nil,
		&conflictLedgerRepo{},
		nil,
		nil,
	)
	svc := NewBillingService(uc, nil, nil, nil)

	_, err := svc.TopUpQuota(context.Background(), &billingv1.TopUpQuotaRequest{
		UserId: "1", Amount: 500, OperatorId: "admin", Remark: "topup", RequestId: "req-1",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "duplicate must surface as a gRPC status error, got %v", err)
	require.Equal(t, codes.AlreadyExists, st.Code(), "duplicate must map to AlreadyExists")
}
