package data

import (
	"context"
	"errors"

	"micro-one-api/domain/authorization"
	"micro-one-api/domain/subscription/biz"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type groupModel struct {
	Revision         int64    `gorm:"column:revision"`
	ID               int64    `gorm:"column:id"`
	Name             string   `gorm:"column:name"`
	DisplayName      string   `gorm:"column:display_name"`
	Platform         string   `gorm:"column:platform"`
	SubscriptionType string   `gorm:"column:subscription_type"`
	DailyLimitUSD    *float64 `gorm:"column:daily_limit_usd"`
	WeeklyLimitUSD   *float64 `gorm:"column:weekly_limit_usd"`
	MonthlyLimitUSD  *float64 `gorm:"column:monthly_limit_usd"`
	RateMultiplier   float64  `gorm:"column:rate_multiplier"`
	Status           int32    `gorm:"column:status"`
	PriceQuota       int64    `gorm:"column:price_quota"`
	DurationDays     int32    `gorm:"column:duration_days"`
	CreatedAt        int64    `gorm:"column:created_at"`
	UpdatedAt        int64    `gorm:"column:updated_at"`
}

func (groupModel) TableName() string { return "subscription_groups" }

func NewGroupRepo(repo *Repository) biz.GroupRepository {
	return repo
}

func (r *Repository) CreateGroup(ctx context.Context, group *biz.SubscriptionGroup) error {
	if r.db != nil {
		return r.createGroupDB(ctx, group)
	}
	return r.createGroupMemory(ctx, group)
}

func (r *Repository) UpdateGroup(ctx context.Context, group *biz.SubscriptionGroup) error {
	if r.db != nil {
		return r.updateGroupDB(ctx, group)
	}
	return r.updateGroupMemory(ctx, group)
}

func (r *Repository) DeleteGroup(ctx context.Context, groupID int64) error {
	if r.db != nil {
		return r.deleteGroupDB(ctx, groupID)
	}
	return r.deleteGroupMemory(ctx, groupID)
}

func (r *Repository) GetGroupByID(ctx context.Context, groupID int64) (*biz.SubscriptionGroup, error) {
	if r.db != nil {
		return r.getGroupByIDDB(ctx, groupID)
	}
	return r.getGroupByIDMemory(ctx, groupID)
}

func (r *Repository) GetGroupByIDInTx(ctx context.Context, tx biz.Tx, groupID int64) (*biz.SubscriptionGroup, error) {
	if tx == nil {
		return nil, errors.New("nil transaction")
	}
	return r.getGroupByIDWithDB(ctx, txDB(tx), groupID)
}

func (r *Repository) GetGroupByName(ctx context.Context, name string) (*biz.SubscriptionGroup, error) {
	if r.db != nil {
		return r.getGroupByNameDB(ctx, name)
	}
	return r.getGroupByNameMemory(ctx, name)
}

func (r *Repository) ListGroups(ctx context.Context) ([]*biz.SubscriptionGroup, error) {
	if r.db != nil {
		return r.listGroupsDB(ctx)
	}
	return r.listGroupsMemory(ctx)
}

func (r *Repository) createGroupDB(ctx context.Context, group *biz.SubscriptionGroup) error {
	model := groupToModel(group)
	model.Revision = 1
	if err := runSubscriptionTx(ctx, r.db, func(ctx context.Context, tx *gorm.DB) error {
		if err := requireOperations(ctx, 0, 0, "subscription.quota_policy.create"); err != nil {
			return err
		}
		if err := tx.Create(&model).Error; err != nil {
			return err
		}
		return auditOperations(ctx, tx, model.ID, "subscription.quota_policy.create")
	}); err != nil {
		return err
	}
	group.ID = model.ID
	group.Revision = model.Revision
	return nil
}

func (r *Repository) updateGroupDB(ctx context.Context, group *biz.SubscriptionGroup) error {
	model := groupToModel(group)
	return runSubscriptionTx(ctx, r.db, func(ctx context.Context, tx *gorm.DB) error {
		var old groupModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&old, group.ID).Error; err != nil {
			return err
		}
		if err := requireOperations(ctx, old.ID, 0, "subscription.quota_policy.update"); err != nil {
			return err
		}
		if _, iam := authorization.QueryScopeFromContext(ctx, "subscription.quota_policy.update"); iam {
			if group.Revision <= 0 {
				return authorization.ErrWritePrecondition
			}
			if group.Revision != old.Revision {
				return authorization.ErrWriteConflict
			}
		}
		if err := tx.Model(&groupModel{}).Where("id = ? AND revision = ?", group.ID, old.Revision).Updates(map[string]any{
			"revision":          old.Revision + 1,
			"name":              model.Name,
			"display_name":      model.DisplayName,
			"platform":          model.Platform,
			"subscription_type": model.SubscriptionType,
			"daily_limit_usd":   model.DailyLimitUSD,
			"weekly_limit_usd":  model.WeeklyLimitUSD,
			"monthly_limit_usd": model.MonthlyLimitUSD,
			"rate_multiplier":   model.RateMultiplier,
			"status":            model.Status,
			"price_quota":       model.PriceQuota,
			"duration_days":     model.DurationDays,
			"created_at":        model.CreatedAt,
			"updated_at":        model.UpdatedAt,
		}).Error; err != nil {
			return err
		}
		group.Revision = old.Revision + 1
		return auditOperations(ctx, tx, group.ID, "subscription.quota_policy.update")
	})
}

func (r *Repository) deleteGroupDB(ctx context.Context, groupID int64) error {
	return runSubscriptionTx(ctx, r.db, func(ctx context.Context, tx *gorm.DB) error {
		var old groupModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&old, groupID).Error; err != nil {
			return err
		}
		if err := requireOperations(ctx, groupID, 0, "subscription.quota_policy.delete"); err != nil {
			return err
		}
		if _, iam := authorization.QueryScopeFromContext(ctx, "subscription.quota_policy.delete"); iam {
			expected, ok := biz.ExpectedRevision(ctx)
			if !ok || expected <= 0 {
				return authorization.ErrWritePrecondition
			}
			if expected != old.Revision {
				return authorization.ErrWriteConflict
			}
		}
		if err := tx.Delete(&groupModel{}, groupID).Error; err != nil {
			return err
		}
		return auditOperations(ctx, tx, groupID, "subscription.quota_policy.delete")
	})
}

func (r *Repository) getGroupByIDDB(ctx context.Context, groupID int64) (*biz.SubscriptionGroup, error) {
	return r.getGroupByIDWithDB(ctx, r.db, groupID)
}

func (r *Repository) getGroupByIDWithDB(ctx context.Context, db *gorm.DB, groupID int64) (*biz.SubscriptionGroup, error) {
	scoped, scopeErr := groupQuery(ctx, db.WithContext(ctx))
	if scopeErr != nil {
		return nil, scopeErr
	}
	var model groupModel
	if err := scoped.Where("id = ?", groupID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrSubscriptionGroupNotFound
		}
		return nil, err
	}
	group := groupFromModel(&model)
	return &group, nil
}

func (r *Repository) getGroupByNameDB(ctx context.Context, name string) (*biz.SubscriptionGroup, error) {
	scoped, scopeErr := groupQuery(ctx, r.db.WithContext(ctx))
	if scopeErr != nil {
		return nil, scopeErr
	}
	var model groupModel
	if err := scoped.Where("name = ?", name).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrSubscriptionGroupNotFound
		}
		return nil, err
	}
	group := groupFromModel(&model)
	return &group, nil
}

func (r *Repository) listGroupsDB(ctx context.Context) ([]*biz.SubscriptionGroup, error) {
	scoped, scopeErr := groupQuery(ctx, r.db.WithContext(ctx))
	if scopeErr != nil {
		return nil, scopeErr
	}
	var rows []groupModel
	if err := scoped.Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]*biz.SubscriptionGroup, 0, len(rows))
	for i := range rows {
		group := groupFromModel(&rows[i])
		result = append(result, &group)
	}
	return result, nil
}

func groupToModel(group *biz.SubscriptionGroup) groupModel {
	if group == nil {
		return groupModel{}
	}
	return groupModel{
		ID:               group.ID,
		Revision:         group.Revision,
		Name:             group.Name,
		DisplayName:      group.DisplayName,
		Platform:         group.Platform,
		SubscriptionType: group.SubscriptionType,
		DailyLimitUSD:    group.DailyLimitUSD,
		WeeklyLimitUSD:   group.WeeklyLimitUSD,
		MonthlyLimitUSD:  group.MonthlyLimitUSD,
		RateMultiplier:   group.RateMultiplier,
		Status:           group.Status,
		PriceQuota:       group.PriceQuota,
		DurationDays:     group.DurationDays,
		CreatedAt:        group.CreatedAt,
		UpdatedAt:        group.UpdatedAt,
	}
}

func groupFromModel(model *groupModel) biz.SubscriptionGroup {
	if model == nil {
		return biz.SubscriptionGroup{}
	}
	return biz.SubscriptionGroup{
		ID:               model.ID,
		Revision:         model.Revision,
		Name:             model.Name,
		DisplayName:      model.DisplayName,
		Platform:         model.Platform,
		SubscriptionType: model.SubscriptionType,
		DailyLimitUSD:    model.DailyLimitUSD,
		WeeklyLimitUSD:   model.WeeklyLimitUSD,
		MonthlyLimitUSD:  model.MonthlyLimitUSD,
		RateMultiplier:   model.RateMultiplier,
		Status:           model.Status,
		PriceQuota:       model.PriceQuota,
		DurationDays:     model.DurationDays,
		CreatedAt:        model.CreatedAt,
		UpdatedAt:        model.UpdatedAt,
	}
}

func (r *Repository) createGroupMemory(ctx context.Context, group *biz.SubscriptionGroup) error {
	if err := authorization.RequireDurableWrite(ctx, "subscription.quota_policy.create"); err != nil {
		return err
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	group.ID = r.nextGroupID
	r.nextGroupID++
	cloned := *group
	r.groups[group.ID] = &cloned
	return nil
}

func (r *Repository) updateGroupMemory(ctx context.Context, group *biz.SubscriptionGroup) error {
	if err := authorization.RequireDurableWrite(ctx, "subscription.quota_policy.update"); err != nil {
		return err
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	r.groups[group.ID] = cloneGroup(group)
	return nil
}

func (r *Repository) deleteGroupMemory(ctx context.Context, groupID int64) error {
	if err := authorization.RequireDurableWrite(ctx, "subscription.quota_policy.delete"); err != nil {
		return err
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	delete(r.groups, groupID)
	return nil
}

func (r *Repository) getGroupByIDMemory(ctx context.Context, groupID int64) (*biz.SubscriptionGroup, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	group, ok := r.groups[groupID]
	if !ok {
		return nil, biz.ErrSubscriptionGroupNotFound
	}
	if !memoryVisible(ctx, group.ID, 0, "subscription.quota_policy.read", "subscription.quota_policy.list") {
		return nil, biz.ErrSubscriptionGroupNotFound
	}
	return cloneGroup(group), nil
}

func (r *Repository) getGroupByNameMemory(ctx context.Context, name string) (*biz.SubscriptionGroup, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	for _, group := range r.groups {
		if group.Name == name && memoryVisible(ctx, group.ID, 0, "subscription.quota_policy.read", "subscription.quota_policy.list") {
			return cloneGroup(group), nil
		}
	}
	return nil, biz.ErrSubscriptionGroupNotFound
}

func (r *Repository) listGroupsMemory(ctx context.Context) ([]*biz.SubscriptionGroup, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	result := make([]*biz.SubscriptionGroup, 0, len(r.groups))
	for _, group := range r.groups {
		if !memoryVisible(ctx, group.ID, 0, "subscription.quota_policy.read", "subscription.quota_policy.list") {
			continue
		}
		result = append(result, cloneGroup(group))
	}
	return result, nil
}

func cloneGroup(group *biz.SubscriptionGroup) *biz.SubscriptionGroup {
	if group == nil {
		return nil
	}
	cloned := *group
	return &cloned
}
