package biz

import (
	"context"
	"time"
)

type PlanUsecase struct {
	routingGroups ContractGroupReader
	repo          PlanRepository
	groupRepo     GroupRepository
	now           func() time.Time
}

func NewPlanUsecase(repo PlanRepository, groupRepo GroupRepository) *PlanUsecase {
	return &PlanUsecase{repo: repo, groupRepo: groupRepo, now: time.Now}
}

func (uc *PlanUsecase) SetContractGroupReader(r ContractGroupReader) { uc.routingGroups = r }

func (uc *PlanUsecase) Create(ctx context.Context, plan *SubscriptionPlan) error {
	if plan == nil {
		return ErrSubscriptionPlanNotFound
	}
	if !EntitlementsEnabled() && (len(plan.Coverage) > 0 || plan.Contract != nil) {
		return ErrSubscriptionRoutingUnavailable
	}
	plan.ForSale = true
	if EntitlementsEnabled() && len(plan.Coverage) == 0 {
		return ErrSubscriptionContractInvalid
	}
	if err := uc.preparePlan(ctx, plan); err != nil {
		return err
	}
	now := uc.now().Unix()
	plan.CreatedAt = now
	plan.UpdatedAt = now
	return uc.repo.CreatePlan(ctx, plan)
}

func (uc *PlanUsecase) Update(ctx context.Context, plan *SubscriptionPlan) error {
	if plan == nil {
		return ErrSubscriptionPlanNotFound
	}
	previous, err := uc.repo.GetPlanByID(ctx, plan.ID)
	if err != nil {
		return err
	}
	if previous.Contract != nil && len(plan.Coverage) == 0 {
		return ErrSubscriptionContractInvalid
	}
	if err := uc.preparePlan(ctx, plan); err != nil {
		return err
	}
	plan.UpdatedAt = uc.now().Unix()
	return uc.repo.UpdatePlan(ctx, plan)
}

func (uc *PlanUsecase) Delete(ctx context.Context, planID int64) error {
	return uc.repo.DeletePlan(ctx, planID)
}

func (uc *PlanUsecase) Get(ctx context.Context, planID int64) (*SubscriptionPlan, error) {
	return uc.repo.GetPlanByID(ctx, planID)
}

func (uc *PlanUsecase) List(ctx context.Context) ([]*SubscriptionPlan, error) {
	return uc.repo.ListPlans(ctx)
}

func (uc *PlanUsecase) ListForSale(ctx context.Context) ([]*SubscriptionPlan, error) {
	return uc.repo.ListPlansForSale(ctx)
}

// ListOffSale returns plans that have been taken off-shelf (for_sale=false).
// Admins use it to audit retired plans without filtering the full list by
// hand. It is the complement of ListForSale.
func (uc *PlanUsecase) ListOffSale(ctx context.Context) ([]*SubscriptionPlan, error) {
	all, err := uc.repo.ListPlans(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*SubscriptionPlan, 0, len(all))
	for _, p := range all {
		if p != nil && !p.ForSale {
			out = append(out, p)
		}
	}
	return out, nil
}

// SetForSale flips a plan's for_sale flag without rewriting the rest of the
// row. It is the narrow operation the admin "上架/下架" buttons call so a
// mistyped price or validity in a full Update cannot sneak in through the
// on/off-shelf path. Returns ErrSubscriptionPlanNotFound when the plan does
// not exist.
func (uc *PlanUsecase) SetForSale(ctx context.Context, planID int64, forSale bool) error {
	plan, err := uc.repo.GetPlanByID(ctx, planID)
	if err != nil {
		return err
	}
	if plan == nil {
		return ErrSubscriptionPlanNotFound
	}
	if forSale && plan.Contract != nil {
		if plan.Contract.Validate() != nil || uc.routingGroups == nil {
			return ErrSubscriptionContractInvalid
		}
		for _, g := range plan.Contract.Coverage {
			if _, err := uc.routingGroups.ValidateSubscriptionGroup(ctx, g.GroupID); err != nil {
				return err
			}
		}
	}
	plan.ForSale = forSale
	plan.UpdatedAt = uc.now().Unix()
	return uc.repo.UpdatePlan(ctx, plan)
}

func (uc *PlanUsecase) preparePlan(ctx context.Context, plan *SubscriptionPlan) error {
	if plan.GroupID <= 0 {
		return ErrSubscriptionGroupNotFound
	}
	group, err := uc.groupRepo.GetGroupByID(ctx, plan.GroupID)
	if err != nil {
		return err
	}
	if group.Status != SubscriptionGroupStatusEnabled {
		return ErrSubscriptionGroupDisabled
	}
	if len(plan.Coverage) > 0 || plan.Contract != nil {
		coverage := plan.Coverage
		if len(coverage) == 0 {
			coverage = plan.Contract.Coverage
		}
		if uc.routingGroups == nil {
			return ErrSubscriptionRoutingUnavailable
		}
		coverage = append([]RoutingCoverage(nil), coverage...)
		for i := range coverage {
			key, err := uc.routingGroups.ValidateSubscriptionGroup(ctx, coverage[i].GroupID)
			if err != nil {
				return err
			}
			coverage[i].GroupKey = key
		}
		contract, err := NewContract(group, coverage)
		if err != nil {
			return err
		}
		plan.Contract = contract
		plan.Coverage = contract.Coverage
	}
	if plan.ValidityDays <= 0 {
		plan.ValidityDays = 30
	}
	if plan.ValidityUnit == "" {
		plan.ValidityUnit = "day"
	}
	return nil
}
