package data

import (
	"context"
	"errors"
	"sort"

	"micro-one-api/internal/subscription/biz"

	"gorm.io/gorm"
)

type planModel struct {
	ID            int64  `gorm:"column:id"`
	GroupID       int64  `gorm:"column:group_id"`
	Name          string `gorm:"column:name"`
	Description   string `gorm:"column:description"`
	PriceQuota    int64  `gorm:"column:price_quota"`
	OriginalPrice *int64 `gorm:"column:original_price"`
	ValidityDays  int32  `gorm:"column:validity_days"`
	ValidityUnit  string `gorm:"column:validity_unit"`
	Features      string `gorm:"column:features"`
	ProductName   string `gorm:"column:product_name"`
	ForSale       bool   `gorm:"column:for_sale"`
	SortOrder     int32  `gorm:"column:sort_order"`
	CreatedAt     int64  `gorm:"column:created_at"`
	UpdatedAt     int64  `gorm:"column:updated_at"`
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
	model := planToModel(plan)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return err
	}
	plan.ID = model.ID
	return nil
}

func (r *Repository) updatePlanDB(ctx context.Context, plan *biz.SubscriptionPlan) error {
	model := planToModel(plan)
	return r.db.WithContext(ctx).Model(&planModel{}).Where("id = ?", plan.ID).Updates(map[string]any{
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
	}).Error
}

func (r *Repository) deletePlanDB(ctx context.Context, planID int64) error {
	return r.db.WithContext(ctx).Delete(&planModel{}, planID).Error
}

func (r *Repository) getPlanByIDDB(ctx context.Context, planID int64) (*biz.SubscriptionPlan, error) {
	var model planModel
	if err := r.db.WithContext(ctx).Where("id = ?", planID).First(&model).Error; err != nil {
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
	query := r.db.WithContext(ctx).Order("sort_order ASC").Order("id ASC")
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
	r.lock.Lock()
	defer r.lock.Unlock()
	plan.ID = r.nextPlanID
	r.nextPlanID++
	r.plans[plan.ID] = clonePlan(plan)
	return nil
}

func (r *Repository) updatePlanMemory(ctx context.Context, plan *biz.SubscriptionPlan) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.plans[plan.ID] = clonePlan(plan)
	return nil
}

func (r *Repository) deletePlanMemory(ctx context.Context, planID int64) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	delete(r.plans, planID)
	return nil
}

func (r *Repository) getPlanByIDMemory(ctx context.Context, planID int64) (*biz.SubscriptionPlan, error) {
	r.lock.RLock()
	plan, ok := r.plans[planID]
	r.lock.RUnlock()
	if !ok {
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
	if plan.Group != nil {
		group := *plan.Group
		cloned.Group = &group
	}
	return &cloned
}
