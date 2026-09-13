package biz

import (
	"context"
	"github.com/go-kratos/kratos/v3/errors"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/domain/routing"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
)

type RoutingPolicyRepo interface {
	Get(context.Context, int64) (*routing.BillingPolicy, error)
	GetInTx(context.Context, subscriptionbiz.Tx, int64) (*routing.BillingPolicy, error)
	Publish(context.Context, *routing.BillingPolicy, int64) error
}

func (uc *BillingUsecase) SetRoutingPolicyRepo(repo RoutingPolicyRepo) { uc.routingPolicies = repo }
func (uc *BillingUsecase) RoutingBillingPolicy(ctx context.Context, id int64) (*routing.BillingPolicy, error) {
	if !uc.RoutingSnapshotsAvailable() || uc.routingPolicies == nil {
		return nil, ErrRequestSnapshotUnavailable
	}
	p, err := uc.routingPolicies.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if p != nil {
		return p, nil
	}
	quote, err := uc.RoutingGroupPrice(ctx, id, 0)
	if err != nil {
		return nil, err
	}
	return &routing.BillingPolicy{GroupID: id, BillingMode: routing.SubscriptionFirst, PriceRatio: quote.PriceRatio}, nil
}
func (uc *BillingUsecase) PublishRoutingBillingPolicy(ctx context.Context, p *routing.BillingPolicy, expected int64) error {
	if !uc.RoutingSnapshotsAvailable() || uc.routingPolicies == nil {
		return ErrRequestSnapshotUnavailable
	}
	if p == nil || !p.Valid() || expected < 0 {
		return ErrRoutingContextInvalid
	}
	g, err := uc.routingGroups.GetRoutingGroup(ctx, p.GroupID)
	if err != nil {
		return err
	}
	if g == nil || g.Status == "archived" {
		return ErrRoutingContextInvalid
	}
	return uc.routingPolicies.Publish(ctx, p, expected)
}

// ValidateSubscriptionGroup is injected into the shared subscription domain.
func (uc *BillingUsecase) ValidateSubscriptionGroup(ctx context.Context, id int64) (string, error) {
	return uc.validateSubscriptionGroup(ctx, nil, id)
}

func (uc *BillingUsecase) validateSubscriptionGroup(ctx context.Context, tx subscriptionbiz.Tx, id int64) (string, error) {
	if !uc.RoutingSnapshotsAvailable() || uc.routingPolicies == nil {
		return "", ErrRequestSnapshotUnavailable
	}
	g, err := uc.routingGroups.GetRoutingGroup(ctx, id)
	if err != nil {
		return "", err
	}
	if g == nil || g.Status != "enabled" {
		return "", ErrRoutingContextInvalid
	}
	var policy *routing.BillingPolicy
	if tx != nil {
		policy, err = uc.routingPolicies.GetInTx(ctx, tx, id)
	} else {
		policy, err = uc.routingPolicies.Get(ctx, id)
	}
	if err != nil {
		return "", err
	}
	if policy != nil && policy.BillingMode == routing.WalletOnly {
		return "", ErrRoutingContextInvalid
	}
	return g.Key, nil
}

var (
	ErrSubscriptionCoverageRequired  = errors.Forbidden(billingv1.RoutingBillingErrorReason_SUBSCRIPTION_COVERAGE_REQUIRED.String(), "an active subscription covering this group is required")
	ErrSubscriptionQuotaInsufficient = errors.Forbidden(billingv1.RoutingBillingErrorReason_SUBSCRIPTION_QUOTA_INSUFFICIENT.String(), "subscription quota is insufficient")
	ErrSubscriptionBoundRequired     = errors.BadRequest(billingv1.RoutingBillingErrorReason_SUBSCRIPTION_BOUND_REQUIRED.String(), "subscription-only requires a supported bounded request; currently text embeddings only")
	ErrSubscriptionBoundExceeded     = errors.Conflict(billingv1.RoutingBillingErrorReason_SUBSCRIPTION_BOUND_EXCEEDED.String(), "upstream usage exceeded the reserved bound; settlement requires reconciliation")
)
