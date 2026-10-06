package data

import (
	"context"
	"errors"
	"sort"

	"micro-one-api/domain/subscription/biz"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/domain/authorization"
)

type planModel struct {
	ContractSnapshot *string `gorm:"column:contract_snapshot"`
	Revision         int64   `gorm:"column:revision"`
	ID               int64   `gorm:"column:id"`
	GroupID          int64   `gorm:"column:group_id"`
	Name             string  `gorm:"column:name"`
	Description      string  `gorm:"column:description"`
	PriceQuota       int64   `gorm:"column:price_quota"`
	OriginalPrice    *int64  `gorm:"column:original_price"`
	ValidityDays     int32   `gorm:"column:validity_days"`
	ValidityUnit     string  `gorm:"column:validity_unit"`
	Features         string  `gorm:"column:features"`
	ProductName      string  `gorm:"column:product_name"`
	ForSale          bool    `gorm:"column:for_sale"`
	SortOrder        int32   `gorm:"column:sort_order"`
	CreatedAt        int64   `gorm:"column:created_at"`
	UpdatedAt        int64   `gorm:"column:updated_at"`
}

func (planModel) TableName() string { return "subscription_plans" }

func NewPlanRepo(repo *Repository) biz.PlanRepository {
	return repo
}

func (r *Repository) CreatePlan(ctx context.Context, plan *biz.SubscriptionPlan) error {
	if r.db != nil {
		return r.createPlanDB(ctx, plan)
	}
	return r.createPlanMemory(ctx, plan)
}

func (r *Repository) UpdatePlan(ctx context.Context, plan *biz.SubscriptionPlan) error {
	if r.db != nil {
		return r.updatePlanDB(ctx, plan)
	}
	return r.updatePlanMemory(ctx, plan)
}

func (r *Repository) DeletePlan(ctx context.Context, planID int64) error {
	if r.db != nil {
		return r.deletePlanDB(ctx, planID)
	}
	return r.deletePlanMemory(ctx, planID)
}

func (r *Repository) GetPlanByID(ctx context.Context, planID int64) (*biz.SubscriptionPlan, error) {
	if r.db != nil {
		return r.getPlanByIDDB(ctx, planID)
	}
	return r.getPlanByIDMemory(ctx, planID)
}

// GetPlanByIDInTx keeps both the plan and its quota policy on the purchase
// transaction's connection. Reacquiring the pool deadlocks single-connection SQLite.
func (r *Repository) GetPlanByIDInTx(ctx context.Context, tx biz.Tx, planID int64) (*biz.SubscriptionPlan, error) {
	if tx == nil {
		return nil, errors.New("nil plan transaction")
	}
	if r.db == nil {
		return r.getPlanByIDMemory(ctx, planID)
	}
	var model planModel
	if err := txDB(tx).WithContext(ctx).Where("id = ?", planID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrSubscriptionPlanNotFound
		}
		return nil, err
	}
	plan := planFromModel(&model)
	group, err := r.GetGroupByIDInTx(ctx, tx, plan.GroupID)
	if err != nil {
		return nil, err
	}
	plan.Group = group
	return &plan, nil
}

func (r *Repository) ListPlans(ctx context.Context) ([]*biz.SubscriptionPlan, error) {
	if r.db != nil {
		return r.listPlansDB(ctx, false)
	}
	return r.listPlansMemory(ctx, false)
}

func (r *Repository) ListPlansForSale(ctx context.Context) ([]*biz.SubscriptionPlan, error) {
	if r.db != nil {
		return r.listPlansDB(ctx, true)
	}
	return r.listPlansMemory(ctx, true)
}

func (r *Repository) createPlanDB(ctx context.Context, plan *biz.SubscriptionPlan) error {
	plan.Revision = 1
	model := planToModel(plan)
	if err := runSubscriptionTx(ctx, r.db, func(ctx context.Context, tx *gorm.DB) error {
		if err := requireOperations(ctx, 0, 0, "subscription.plan.create", "subscription.plan.publish"); err != nil {
			return err
		}
		if _, iam := authorization.QueryScopeFromContext(ctx, "subscription.plan.create"); iam {
			if err := validateEnabledQuotaPolicy(tx, plan.GroupID); err != nil {
				return err
			}
		}
		if !biz.EntitlementsEnabled() && plan.Contract == nil {
			if err := tx.Omit("ContractSnapshot", "Revision").Create(&model).Error; err != nil {
				return err
			}
			return auditOperations(ctx, tx, model.ID, "subscription.plan.create", "subscription.plan.publish")
		}
		if err := LockContractReferences(tx); err != nil {
			return err
		}
		if err := ValidateContractBillingModes(tx, plan.Contract); err != nil {
			return err
		}
		if err := tx.Create(&model).Error; err != nil {
			return err
		}
		if err := syncContractCoverage(tx, "subscription_plan_routing_groups", "plan_id", model.ID, plan.Contract); err != nil {
			return err
		}
		return auditOperations(ctx, tx, model.ID, "subscription.plan.create", "subscription.plan.publish")
	}); err != nil {
		return err
	}
	plan.ID = model.ID
	return nil
}

func (r *Repository) updatePlanDB(ctx context.Context, plan *biz.SubscriptionPlan) error {
	model := planToModel(plan)
	updates := map[string]any{
		"group_id":       model.GroupID,
		"name":           model.Name,
		"description":    model.Description,
		"price_quota":    model.PriceQuota,
		"original_price": model.OriginalPrice,
		"validity_days":  model.ValidityDays,
		"validity_unit":  model.ValidityUnit,
		"features":       model.Features,
		"product_name":   model.ProductName,
		"for_sale":       model.ForSale,
		"sort_order":     model.SortOrder,
		"updated_at":     model.UpdatedAt,
	}
	err := runSubscriptionTx(ctx, r.db, func(ctx context.Context, tx *gorm.DB) error {
		var old planModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&old, plan.ID).Error; err != nil {
			return err
		}
		if err := requireOperations(ctx, plan.ID, 0, "subscription.plan.update", "subscription.plan.publish", "subscription.plan.unpublish"); err != nil {
			return err
		}
		if old.ForSale != plan.ForSale {
			action := "subscription.plan.unpublish"
			if plan.ForSale {
				action = "subscription.plan.publish"
			}
			if _, iam := authorization.QueryScopeFromContext(ctx, "subscription.plan.update"); iam {
				if _, ok := authorization.QueryScopeFromContext(ctx, action); !ok {
					return authorization.ErrDenied
				}
			}
			if err := requireOperations(ctx, plan.ID, 0, action); err != nil {
				return err
			}
		}
		_, iam := authorization.QueryScopeFromContext(ctx, "subscription.plan.update")
		if !iam {
			_, iam = authorization.QueryScopeFromContext(ctx, "subscription.plan.publish")
		}
		if !iam {
			_, iam = authorization.QueryScopeFromContext(ctx, "subscription.plan.unpublish")
		}
		if iam {
			if err := validateEnabledQuotaPolicy(tx, plan.GroupID); err != nil {
				return err
			}
		}
		if iam && plan.Revision != old.Revision {
			return biz.ErrSubscriptionContractConflict
		}
		if !biz.EntitlementsEnabled() && plan.Contract == nil {
			if iam {
				updates["revision"] = old.Revision + 1
			}
			if err := tx.Model(&planModel{}).Where("id = ?", plan.ID).Updates(updates).Error; err != nil {
				return err
			}
			return auditOperations(ctx, tx, plan.ID, "subscription.plan.update", "subscription.plan.publish", "subscription.plan.unpublish")
		}

		if err := LockContractReferences(tx); err != nil {
			return err
		}
		if plan.ForSale {
			if err := ValidateContractBillingModes(tx, plan.Contract); err != nil {
				return err
			}
		}
		updates["contract_snapshot"] = model.ContractSnapshot
		updates["revision"] = plan.Revision + 1
		result := tx.Model(&planModel{}).Where("id = ? AND revision = ?", plan.ID, plan.Revision).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return biz.ErrSubscriptionContractConflict
		}
		if err := syncContractCoverage(tx, "subscription_plan_routing_groups", "plan_id", plan.ID, plan.Contract); err != nil {
			return err
		}
		return auditOperations(ctx, tx, plan.ID, "subscription.plan.update", "subscription.plan.publish", "subscription.plan.unpublish")
	})
	if err == nil {
		plan.Revision++
	}
	return err
}

func (r *Repository) deletePlanDB(ctx context.Context, planID int64) error {
	return runSubscriptionTx(ctx, r.db, func(ctx context.Context, tx *gorm.DB) error {
		var old planModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&old, planID).Error; err != nil {
			return err
		}
		if err := requireOperations(ctx, planID, 0, "subscription.plan.delete"); err != nil {
			return err
		}
		if _, iam := authorization.QueryScopeFromContext(ctx, "subscription.plan.delete"); iam {
			expected, ok := biz.ExpectedRevision(ctx)
			if !ok || expected != old.Revision {
				return biz.ErrSubscriptionContractConflict
			}
		}
		if err := tx.Delete(&planModel{}, planID).Error; err != nil {
			return err
		}
		return auditOperations(ctx, tx, planID, "subscription.plan.delete")
	})
}

func (r *Repository) getPlanByIDDB(ctx context.Context, planID int64) (*biz.SubscriptionPlan, error) {
	scoped, scopeErr := planQuery(ctx, r.db.WithContext(ctx))
	if scopeErr != nil {
		return nil, scopeErr
	}
	var model planModel
	if err := scoped.Where("id = ?", planID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrSubscriptionPlanNotFound
		}
		return nil, err
	}
	plan := planFromModel(&model)
	_ = r.hydratePlanGroup(ctx, &plan)
	return &plan, nil
}

func (r *Repository) listPlansDB(ctx context.Context, saleOnly bool) ([]*biz.SubscriptionPlan, error) {
	scoped, scopeErr := planQuery(ctx, r.db.WithContext(ctx))
	if scopeErr != nil {
		return nil, scopeErr
	}
	query := scoped.Order("sort_order ASC").Order("id ASC")
	if saleOnly {
		query = query.Where("for_sale = ?", true)
	}
	var rows []planModel
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]*biz.SubscriptionPlan, 0, len(rows))
	for i := range rows {
		plan := planFromModel(&rows[i])
		_ = r.hydratePlanGroup(ctx, &plan)
		result = append(result, &plan)
	}
	return result, nil
}

func (r *Repository) hydratePlanGroup(ctx context.Context, plan *biz.SubscriptionPlan) error {
	group, err := r.GetGroupByID(ctx, plan.GroupID)
	if err != nil {
		return err
	}
	plan.Group = group
	return nil
}

func planToModel(plan *biz.SubscriptionPlan) planModel {
	if plan == nil {
		return planModel{}
	}
	return planModel{
		ContractSnapshot: encodeContract(plan.Contract), Revision: plan.Revision,
		ID:            plan.ID,
		GroupID:       plan.GroupID,
		Name:          plan.Name,
		Description:   plan.Description,
		PriceQuota:    plan.PriceQuota,
		OriginalPrice: plan.OriginalPrice,
		ValidityDays:  plan.ValidityDays,
		ValidityUnit:  plan.ValidityUnit,
		Features:      plan.Features,
		ProductName:   plan.ProductName,
		ForSale:       plan.ForSale,
		SortOrder:     plan.SortOrder,
		CreatedAt:     plan.CreatedAt,
		UpdatedAt:     plan.UpdatedAt,
	}
}

func planFromModel(model *planModel) biz.SubscriptionPlan {
	if model == nil {
		return biz.SubscriptionPlan{}
	}
	return biz.SubscriptionPlan{
		Contract: decodeContract(model.ContractSnapshot), Coverage: contractCoverage(model.ContractSnapshot), Revision: model.Revision,
		ID:            model.ID,
		GroupID:       model.GroupID,
		Name:          model.Name,
		Description:   model.Description,
		PriceQuota:    model.PriceQuota,
		OriginalPrice: model.OriginalPrice,
		ValidityDays:  model.ValidityDays,
		ValidityUnit:  model.ValidityUnit,
		Features:      model.Features,
		ProductName:   model.ProductName,
		ForSale:       model.ForSale,
		SortOrder:     model.SortOrder,
		CreatedAt:     model.CreatedAt,
		UpdatedAt:     model.UpdatedAt,
	}
}

func (r *Repository) createPlanMemory(ctx context.Context, plan *biz.SubscriptionPlan) error {
	if err := authorization.RequireDurableWrite(ctx, "subscription.plan.create", "subscription.plan.publish"); err != nil {
		return err
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	plan.ID = r.nextPlanID
	r.nextPlanID++
	r.plans[plan.ID] = clonePlan(plan)
	return nil
}

func (r *Repository) updatePlanMemory(ctx context.Context, plan *biz.SubscriptionPlan) error {
	if err := authorization.RequireDurableWrite(ctx, "subscription.plan.update", "subscription.plan.publish", "subscription.plan.unpublish"); err != nil {
		return err
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	r.plans[plan.ID] = clonePlan(plan)
	return nil
}

func (r *Repository) deletePlanMemory(ctx context.Context, planID int64) error {
	if err := authorization.RequireDurableWrite(ctx, "subscription.plan.delete"); err != nil {
		return err
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	delete(r.plans, planID)
	return nil
}

func (r *Repository) getPlanByIDMemory(ctx context.Context, planID int64) (*biz.SubscriptionPlan, error) {
	r.lock.RLock()
	plan, ok := r.plans[planID]
	r.lock.RUnlock()
	if !ok || !memoryVisible(ctx, planID, 0, "subscription.plan.read", "subscription.plan.list") {
		return nil, biz.ErrSubscriptionPlanNotFound
	}
	cloned := clonePlan(plan)
	if group, err := r.GetGroupByID(ctx, cloned.GroupID); err == nil {
		cloned.Group = group
	}
	return cloned, nil
}

func (r *Repository) listPlansMemory(ctx context.Context, saleOnly bool) ([]*biz.SubscriptionPlan, error) {
	r.lock.RLock()
	result := make([]*biz.SubscriptionPlan, 0, len(r.plans))
	for _, plan := range r.plans {
		if !memoryVisible(ctx, plan.ID, 0, "subscription.plan.read", "subscription.plan.list") {
			continue
		}
		if saleOnly && !plan.ForSale {
			continue
		}
		result = append(result, clonePlan(plan))
	}
	r.lock.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		if result[i].SortOrder == result[j].SortOrder {
			return result[i].ID < result[j].ID
		}
		return result[i].SortOrder < result[j].SortOrder
	})
	for _, plan := range result {
		if group, err := r.GetGroupByID(ctx, plan.GroupID); err == nil {
			plan.Group = group
		}
	}
	return result, nil
}

func clonePlan(plan *biz.SubscriptionPlan) *biz.SubscriptionPlan {
	if plan == nil {
		return nil
	}
	cloned := *plan
	cloned.Contract = biz.CloneContract(plan.Contract)
	cloned.Coverage = append([]biz.RoutingCoverage(nil), plan.Coverage...)
	if plan.Group != nil {
		group := *plan.Group
		cloned.Group = &group
	}
	return &cloned
}

func validateEnabledQuotaPolicy(tx *gorm.DB, id int64) error {
	var group groupModel
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "status").Where("id = ?", id).Take(&group).Error; err != nil {
		return err
	}
	if group.Status != biz.SubscriptionGroupStatusEnabled {
		return biz.ErrSubscriptionGroupDisabled
	}
	return nil
}
