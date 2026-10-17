package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
)

type stubSettlementTaskStore struct {
	status string
	err    error
}

func (s *stubSettlementTaskStore) SaveTask(context.Context, *SettleTask) error { return nil }
func (s *stubSettlementTaskStore) ListPending(context.Context, int) ([]*SettleTask, error) {
	return nil, nil
}
func (s *stubSettlementTaskStore) MarkCompleted(context.Context, string) error { return nil }
func (s *stubSettlementTaskStore) MarkFailed(context.Context, string, string, time.Time) error {
	return nil
}
func (s *stubSettlementTaskStore) TaskStatusByReservation(context.Context, string) (string, error) {
	return s.status, s.err
}

func settlementStatusUsecase(status string, taskStore SettlementTaskStore) *BillingUsecase {
	reservationRepo := &mockReservationRepo{reservations: map[string]*Reservation{
		"res-live": {ReservationID: "res-live", UserID: "42", Status: status},
	}}
	uc := NewBillingUsecase(&mockAccountRepo{}, reservationRepo, &mockLedgerRepo{}, nil, nil)
	uc.SetAuthorization(&authztest.Resolver{ActorID: 42, Scopes: map[string]authorization.QueryScope{"billing.account.ledger.read": authztest.Users(42)}})
	if taskStore != nil {
		uc.SetSettlementTaskStore(taskStore)
	}
	return uc
}

func TestGetSettlementStatusMapsReservationAndTaskStates(t *testing.T) {
	t.Run("committed reservation reports committed from the reservation row", func(t *testing.T) {
		uc := settlementStatusUsecase(ReservationStatusCommitted, nil)
		status, err := uc.GetSettlementStatus(authztest.Context(), "res-live")
		require.NoError(t, err)
		require.Equal(t, "committed", status.Status)
		require.Equal(t, "reservation", status.Source)
		require.Positive(t, status.ObservedAtMS)
	})

	t.Run("released reservation reports released", func(t *testing.T) {
		uc := settlementStatusUsecase(ReservationStatusReleased, nil)
		status, err := uc.GetSettlementStatus(authztest.Context(), "res-live")
		require.NoError(t, err)
		require.Equal(t, "released", status.Status)
	})

	t.Run("live reservation with queued task reports enqueued", func(t *testing.T) {
		uc := settlementStatusUsecase(ReservationStatusReserved, &stubSettlementTaskStore{status: "pending"})
		status, err := uc.GetSettlementStatus(authztest.Context(), "res-live")
		require.NoError(t, err)
		require.Equal(t, "enqueued", status.Status)
		require.Equal(t, "reservation+settlement_task", status.Source)
	})

	t.Run("live reservation without a task stays reserved", func(t *testing.T) {
		uc := settlementStatusUsecase(ReservationStatusReserved, &stubSettlementTaskStore{status: ""})
		status, err := uc.GetSettlementStatus(authztest.Context(), "res-live")
		require.NoError(t, err)
		require.Equal(t, "reserved", status.Status)
	})

	t.Run("failed task on a live reservation reports failed", func(t *testing.T) {
		uc := settlementStatusUsecase(ReservationStatusReserved, &stubSettlementTaskStore{status: "failed"})
		status, err := uc.GetSettlementStatus(authztest.Context(), "res-live")
		require.NoError(t, err)
		require.Equal(t, "failed", status.Status)
	})

	t.Run("invalid task on a live reservation reports failed", func(t *testing.T) {
		uc := settlementStatusUsecase(ReservationStatusReserved, &stubSettlementTaskStore{status: "invalid"})
		status, err := uc.GetSettlementStatus(authztest.Context(), "res-live")
		require.NoError(t, err)
		require.Equal(t, "failed", status.Status)
		require.Equal(t, "reservation+settlement_task", status.Source)
	})

	t.Run("committed reservation wins over a stale failed task", func(t *testing.T) {
		uc := settlementStatusUsecase(ReservationStatusCommitted, &stubSettlementTaskStore{status: "failed"})
		status, err := uc.GetSettlementStatus(authztest.Context(), "res-live")
		require.NoError(t, err)
		require.Equal(t, "committed", status.Status)
	})
}

func TestGetSettlementStatusGuards(t *testing.T) {
	t.Run("unknown reservation returns not found", func(t *testing.T) {
		uc := settlementStatusUsecase(ReservationStatusReserved, nil)
		_, err := uc.GetSettlementStatus(authztest.Context(), "missing")
		require.ErrorIs(t, err, ErrSettlementReservationNotFound)
	})

	t.Run("empty reservation id is invalid", func(t *testing.T) {
		uc := settlementStatusUsecase(ReservationStatusReserved, nil)
		_, err := uc.GetSettlementStatus(authztest.Context(), "")
		require.ErrorIs(t, err, ErrLedgerTraceInvalid)
	})

	t.Run("another user's reservation is denied without revealing existence", func(t *testing.T) {
		uc := settlementStatusUsecase(ReservationStatusReserved, nil)
		uc.SetAuthorization(&authztest.Resolver{ActorID: 7, Scopes: map[string]authorization.QueryScope{"billing.account.ledger.read": authztest.Users(7)}})
		_, err := uc.GetSettlementStatus(authztest.Context(), "res-live")
		require.ErrorIs(t, err, authorization.ErrDenied)
	})

	t.Run("platform scope can read any reservation", func(t *testing.T) {
		uc := settlementStatusUsecase(ReservationStatusCommitted, nil)
		uc.SetAuthorization(&authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"billing.account.ledger.read": authztest.All()}})
		status, err := uc.GetSettlementStatus(authztest.Context(), "res-live")
		require.NoError(t, err)
		require.Equal(t, "committed", status.Status)
	})
}
