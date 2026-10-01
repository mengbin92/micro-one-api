package biz

import (
	"context"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/security/serviceidentity"
	"strconv"
)

func prepareBilling(ctx context.Context, r authorization.Resolver, point, op string) (context.Context, error) {
	// identity's public self adapters are authenticated again at the owner;
	// their dedicated credential alone never grants another user's finances.
	if authorization.External(ctx) && serviceidentity.FromContext(ctx).Name == "identity" {
		return authorization.PrepareSelf(ctx, r, "billing.self", op)
	}
	if !authorization.External(ctx) || serviceidentity.HasSystemCapability(ctx, serviceidentity.RPCMethod(ctx)) {
		return ctx, nil
	}
	return authorization.Prepare(ctx, r, point, op)
}
func (uc *PaymentUsecase) SetAuthorization(r authorization.Resolver) { uc.authorization = r }
func (uc *BillingUsecase) SetAuthorization(r authorization.Resolver) { uc.authorization = r }
func (uc *RefundUsecase) SetAuthorization(r authorization.Resolver)  { uc.authorization = r }
func accountFacts(userID string) (authorization.ObjectFacts, error) {
	id, err := strconv.ParseInt(userID, 10, 64)
	if err != nil || id <= 0 {
		return authorization.ObjectFacts{}, authorization.ErrDenied
	}
	return authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: id, OwnerUserID: id}, nil
}
func paymentFacts(order *PaymentOrder) (authorization.ObjectFacts, error) {
	if order == nil {
		return authorization.ObjectFacts{}, authorization.ErrDenied
	}
	out, err := accountFacts(order.UserID)
	out.ResourceID = order.ID
	return out, err
}
func (uc *BillingUsecase) authorizeAccount(ctx context.Context, point, op, user string) (context.Context, error) {
	ctx, err := prepareBilling(ctx, uc.authorization, point, op)
	if err != nil {
		return ctx, err
	}
	if _, iam := authorization.QueryScopeFromContext(ctx, op); !iam {
		return ctx, nil
	}
	facts, err := accountFacts(user)
	if err != nil {
		return ctx, err
	}
	return ctx, authorization.Require(ctx, op, facts)
}

func (uc *BillingUsecase) AuthorizeSelf(ctx context.Context, user string) (context.Context, error) {
	ctx, err := authorization.PrepareSelf(ctx, uc.authorization, "billing.self", "billing.account.read")
	if err != nil {
		return ctx, err
	}
	facts, err := accountFacts(user)
	if err != nil {
		return ctx, err
	}
	return ctx, authorization.Require(ctx, "billing.account.read", facts)
}
func (uc *BillingUsecase) prepareCost(ctx context.Context) (context.Context, error) {
	if !authorization.External(ctx) || serviceidentity.HasSystemCapability(ctx, serviceidentity.RPCMethod(ctx)) {
		return ctx, nil
	}
	return authorization.PrepareOptional(ctx, uc.authorization, "billing.accounts.read", "billing.account.cost.read")
}
func ledgerView(ctx context.Context, ledger *Ledger) *Ledger {
	if ledger == nil {
		return nil
	}
	copy := *ledger
	facts, err := accountFacts(ledger.UserID)
	copy.CostFieldsVisible = err == nil && authorization.Require(ctx, "billing.account.cost.read", facts) == nil
	if !copy.CostFieldsVisible {
		copy.UpstreamCost = 0
	}
	return &copy
}
func ledgerViews(ctx context.Context, ledgers []*Ledger) []*Ledger {
	for i, l := range ledgers {
		ledgers[i] = ledgerView(ctx, l)
	}
	return ledgers
}
