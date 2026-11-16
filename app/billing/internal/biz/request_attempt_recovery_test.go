package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"micro-one-api/domain/authorization"
	"micro-one-api/platform/security/serviceidentity"
)

// relayRecoveryRepo only implements RequestAttemptRepo; embedding the nil
// ReservationRepo interface satisfies the usecase field type while the
// overridden method serves the type assertion in ListRequestAttempts.
type relayRecoveryRepo struct {
	ReservationRepo
	rows  []*Reservation
	total int64
}

func (r *relayRecoveryRepo) ListRequestAttempts(_ context.Context, _, _ string, _, _ int) ([]*Reservation, int64, error) {
	return r.rows, r.total, nil
}

// TestListRequestAttemptsRelaySystemCallerSeesCommittedCost pins the v0.44.0
// code-review P1 fix: relay's dedicated identity must read authoritative
// attempt state (including committed ActualCost) through the real
// transport-shaped context — external, no user credential, principal with
// the RPC's system capability — without an operator IAM scope. Before the
// registry granted relay the capability this returned PermissionDenied at
// the transport; with capability, prepareBilling/prepareCost skip operator
// auth and the cost fields must stay visible so settlement recovery can
// synthesize the success path instead of committed_cost_hidden.
func TestListRequestAttemptsRelaySystemCallerSeesCommittedCost(t *testing.T) {
	const method = "/api.billing.v1.BillingService/ListRequestAttempts"
	repo := &relayRecoveryRepo{rows: []*Reservation{{
		Status:        ReservationStatusCommitted,
		ActualCost:    4321,
		RootRequestID: "root-1",
	}}, total: 1}
	uc := NewBillingUsecase(nil, nil, nil, nil, nil)
	uc.reservationRepo = repo

	ctx := context.Background()
	ctx = authorization.WithExternal(ctx)
	ctx = serviceidentity.WithPrincipal(ctx, serviceidentity.Principal{Name: "relay", Dedicated: true})
	ctx = serviceidentity.WithRPCMethod(ctx, method)

	rows, total, err := uc.ListRequestAttempts(ctx, "7", "root-1", 1, 100)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	require.Equal(t, ReservationStatusCommitted, rows[0].Status)
	require.True(t, rows[0].CostFieldsVisible, "system capability must not blank committed cost for settlement recovery")
	require.EqualValues(t, 4321, rows[0].ActualCost)
}
