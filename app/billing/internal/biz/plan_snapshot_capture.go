package biz

import (
	"context"
	"fmt"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
)

// PlanSnapshotter captures the immutable purchase-time view of a plan into a
// PaymentOrder. The interface is kept separate from SubscriptionPlanGetter so
// the payment usecase can depend on a narrow capability instead of the full
// plan repo.
type PlanSnapshotter interface {
	CapturePlanSnapshot(ctx context.Context, planID int64) (PlanSnapshot, error)
}

// paymentPlanSnapshotter is the default implementation backed by the
// subscription plan repository. It reads the live plan row once and copies the
// fulfilment-relevant fields into a PlanSnapshot via the canonical
// SubscriptionPlan.ToPlanSnapshot helper (shared with the admin plan-snapshot
// completion path). The snapshot is then frozen on the payment order; later
// edits to the plan do not retroactively change the order.
type paymentPlanSnapshotter struct {
	plans     SubscriptionPlanGetter
	validator subscriptionbiz.ContractGroupReader
}

// NewPaymentPlanSnapshotter builds a snapshotter from a plan getter.
func NewPaymentPlanSnapshotter(plans SubscriptionPlanGetter, validators ...subscriptionbiz.ContractGroupReader) PlanSnapshotter {
	s := &paymentPlanSnapshotter{plans: plans}
	if len(validators) > 0 {
		s.validator = validators[0]
	}
	return s
}

func (s *paymentPlanSnapshotter) CapturePlanSnapshot(ctx context.Context, planID int64) (PlanSnapshot, error) {
	if s == nil || s.plans == nil {
		return PlanSnapshot{}, fmt.Errorf("plan snapshotter is not configured")
	}
	if planID <= 0 {
		return PlanSnapshot{}, nil
	}
	plan, err := s.plans.GetPlanByID(ctx, planID)
	if err != nil {
		return PlanSnapshot{}, err
	}
	if plan == nil || plan.ID <= 0 {
		return PlanSnapshot{}, nil
	}
	if subscriptionbiz.EntitlementsEnabled() {
		if !plan.ForSale || plan.PriceQuota <= 0 || plan.Group == nil || plan.Group.Status != subscriptionbiz.SubscriptionGroupStatusEnabled {
			return PlanSnapshot{}, subscriptionbiz.ErrSubscriptionPlanNotSaleable
		}
		if plan.Contract != nil {
			if plan.Contract.Validate() != nil || s.validator == nil {
				return PlanSnapshot{}, subscriptionbiz.ErrSubscriptionContractInvalid
			}
			for _, g := range plan.Contract.Coverage {
				if _, err := s.validator.ValidateSubscriptionGroup(ctx, g.GroupID); err != nil {
					return PlanSnapshot{}, err
				}
			}
		}
	}
	return plan.ToPlanSnapshot(), nil
}

// ApplyPlanSnapshotToOrder encodes the snapshot onto the order's PlanSnapshot
// field. It is a no-op when the order has no plan_id or the snapshot is zero.
func ApplyPlanSnapshotToOrder(order *PaymentOrder, snapshot PlanSnapshot) {
	if order == nil {
		return
	}
	if snapshot.PlanID == 0 {
		return
	}
	order.PlanSnapshot = EncodePlanSnapshot(snapshot)
}
