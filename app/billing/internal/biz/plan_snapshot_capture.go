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

// GroupSnapshotter captures the canonical CNY price of a legacy group order.
type GroupSnapshotter interface {
	CaptureGroupSnapshot(ctx context.Context, groupID int64) (PlanSnapshot, error)
}

func (a *paymentSubscriptionAssigner) CaptureGroupSnapshot(ctx context.Context, groupID int64) (PlanSnapshot, error) {
	if a == nil || a.groups == nil {
		return PlanSnapshot{}, ErrRequestSnapshotUnavailable
	}
	group, err := a.groups.GetGroupByID(ctx, groupID)
	if err != nil {
		return PlanSnapshot{}, err
	}
	if group == nil || group.Status != subscriptionbiz.SubscriptionGroupStatusEnabled || group.PriceQuota <= 0 || group.DurationDays <= 0 {
		return PlanSnapshot{}, subscriptionbiz.ErrSubscriptionPlanNotSaleable
	}
	conversion, err := subscriptionbiz.CapturePriceConversion(group.PriceQuota)
	if err != nil {
		return PlanSnapshot{}, err
	}
	name := group.DisplayName
	if name == "" {
		name = group.Name
	}
	return PlanSnapshot{GroupID: group.ID, Name: name, PriceQuota: group.PriceQuota, ValidityDays: group.DurationDays, PriceConversion: conversion}, nil
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
	if !plan.ForSale || plan.PriceQuota <= 0 || plan.ValidityDays <= 0 || plan.Group == nil || plan.Group.Status != subscriptionbiz.SubscriptionGroupStatusEnabled {
		return PlanSnapshot{}, subscriptionbiz.ErrSubscriptionPlanNotSaleable
	}
	if subscriptionbiz.EntitlementsEnabled() {
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
	snapshot := plan.ToPlanSnapshot()
	snapshot.PriceConversion, err = subscriptionbiz.CapturePriceConversion(plan.PriceQuota)
	if err != nil {
		return PlanSnapshot{}, err
	}
	return snapshot, nil
}

// ApplyPlanSnapshotToOrder stores a plan or priced legacy-group snapshot.
// It is a no-op when the snapshot has neither a plan nor a price conversion.
func ApplyPlanSnapshotToOrder(order *PaymentOrder, snapshot PlanSnapshot) {
	if order == nil {
		return
	}
	if snapshot.PlanID == 0 && snapshot.PriceConversion == nil {
		return
	}
	order.PlanSnapshot = EncodePlanSnapshot(snapshot)
}
