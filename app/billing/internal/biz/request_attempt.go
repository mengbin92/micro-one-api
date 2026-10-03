package biz

import (
	"context"
	"math"

	"micro-one-api/domain/authorization"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/domain/routing"
)

func validateAttemptReplay(ctx context.Context, r *Reservation, model, channelID string, subscriptionAccountID int64, routingContext *routing.ResolvedRoutingContext) error {
	a := requesttrace.FromContext(ctx)
	if r.RootRequestID != "" && a.RootRequestID != "" && (r.RootRequestID != a.RootRequestID || r.AttemptNumber != a.Number || r.SourceKind != a.SourceKind || r.UpstreamModelID != a.UpstreamModelID) {
		return ErrRoutingContextConflict
	}
	if r.SourceKind != "" && a.SourceKind != "" && (r.ChannelID != channelID || parseInt64Default(r.SubscriptionAccountID, 0) != subscriptionAccountID) {
		return ErrRoutingContextConflict
	}
	return validateReservationReplay(r, model, routingContext)
}

// The reservation freezes the selected source before execution. Task retries
// use this identity even when the producer omits source fields.
func usageForReservation(usage LedgerUsage, r *Reservation) LedgerUsage {
	if r.SourceKind != "" {
		usage.SourceKind = r.SourceKind
		usage.SubscriptionAccountID = parseInt64Default(r.SubscriptionAccountID, 0)
	}
	if r.UpstreamModelID != "" {
		usage.UpstreamModelID = r.UpstreamModelID
	}
	return usage
}

type RequestAttemptRepo interface {
	ListRequestAttempts(context.Context, string, string, int, int) ([]*Reservation, int64, error)
}

func (uc *BillingUsecase) ListRequestAttempts(ctx context.Context, userID, rootID string, page, size int) ([]*Reservation, int64, error) {
	var err error
	ctx, err = uc.authorizeAccount(ctx, "billing.request_attempts", "billing.account.ledger.read", userID)
	if err != nil {
		return nil, 0, err
	}
	ctx, err = uc.prepareCost(ctx)
	if err != nil {
		return nil, 0, err
	}
	if userID == "" || rootID == "" || len(rootID) > 128 {
		return nil, 0, ErrRoutingContextInvalid
	}
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 100
	}
	if page > math.MaxInt32/size {
		return nil, 0, ErrRoutingContextInvalid
	}
	repo, ok := uc.reservationRepo.(RequestAttemptRepo)
	if !ok {
		return nil, 0, ErrRequestSnapshotUnavailable
	}
	rows, total, err := repo.ListRequestAttempts(ctx, userID, rootID, (page-1)*size, size)
	if err != nil {
		return nil, 0, err
	}
	facts, _ := accountFacts(userID)
	visible := authorization.Require(ctx, "billing.account.cost.read", facts) == nil
	for i, row := range rows {
		copy := *row
		copy.CostFieldsVisible = visible
		if !visible {
			copy.ActualCost = 0
		}
		rows[i] = &copy
	}
	return rows, total, nil
}
