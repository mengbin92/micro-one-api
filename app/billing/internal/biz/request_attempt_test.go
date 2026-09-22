package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/requesttrace"
)

func TestAttemptReserveAndCommitReplayFreezeExecutionSource(t *testing.T) {
	account := &Account{UserID: "1", Balance: 1000, Group: "default"}
	repo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledger := &mockLedgerRepo{}
	uc := NewBillingUsecase(&mockAccountRepo{account: account}, repo, ledger, &mockRedeemRepo{}, nil)
	trace := requesttrace.Attempt{RootRequestID: "root", Number: 2, SourceKind: "channel", UpstreamModelID: "Actual-Model"}
	ctx := requesttrace.WithAttempt(context.Background(), trace)
	r, err := uc.ReserveQuota(ctx, "1", "attempt-2", 100, "public", "7", 0)
	require.NoError(t, err)
	replay, err := uc.ReserveQuota(ctx, "1", "attempt-2", 100, "public", "7", 0)
	require.NoError(t, err)
	require.Equal(t, r.ReservationID, replay.ReservationID)
	require.EqualValues(t, 900, account.Balance)
	for _, conflicting := range []requesttrace.Attempt{
		{RootRequestID: "other", Number: 2, SourceKind: "channel", UpstreamModelID: "Actual-Model"},
		{RootRequestID: "root", Number: 3, SourceKind: "channel", UpstreamModelID: "Actual-Model"},
		{RootRequestID: "root", Number: 2, SourceKind: "channel", UpstreamModelID: "changed"},
	} {
		_, err = uc.ReserveQuota(requesttrace.WithAttempt(ctx, conflicting), "1", "attempt-2", 100, "public", "7", 0)
		require.ErrorIs(t, err, ErrRoutingContextConflict)
	}
	_, err = uc.ReserveQuota(ctx, "1", "attempt-2", 100, "public", "8", 0)
	require.ErrorIs(t, err, ErrRoutingContextConflict)
	_, err = uc.ReserveQuota(ctx, "1", "attempt-2", 100, "public", "7", 99)
	require.ErrorIs(t, err, ErrRoutingContextConflict)
	// A resumed settlement uses the persisted source even if its payload is stale.
	_, _, err = uc.CommitQuotaWithUsage(context.Background(), r.ReservationID, 80, true, LedgerUsage{SourceKind: "subscription", UpstreamModelID: "changed", SubscriptionAccountID: 99})
	require.NoError(t, err)
	balance, entries := account.Balance, len(ledger.ledgers)
	_, _, err = uc.CommitQuotaWithUsage(context.Background(), r.ReservationID, 80, true, LedgerUsage{})
	require.NoError(t, err)
	require.Equal(t, balance, account.Balance)
	require.Len(t, ledger.ledgers, entries)
	var consume *Ledger
	for _, entry := range ledger.ledgers {
		if entry.Type == LedgerTypeConsume {
			consume = entry
		}
	}
	require.NotNil(t, consume)
	require.Equal(t, "channel", consume.SourceKind)
	require.Equal(t, "Actual-Model", consume.UpstreamModelID)
	require.Zero(t, consume.SubscriptionAccountID)
}
