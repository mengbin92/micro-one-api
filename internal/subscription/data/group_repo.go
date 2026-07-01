package data

import (
	"context"
	"errors"

	"micro-one-api/internal/subscription/biz"

	"gorm.io/gorm"
)

type groupModel struct {
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
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return err
	}
	group.ID = model.ID
	return nil
}

func (r *Repository) updateGroupDB(ctx context.Context, group *biz.SubscriptionGroup) error {
	model := groupToModel(group)
	return r.db.WithContext(ctx).Model(&groupModel{}).Where("id = ?", group.ID).Updates(map[string]any{
		"name":              model.Name,
		"display_name":      model.DisplayName,
		"platform":          model.Platform,
		"subscription_type": model.SubscriptionType,
		"daily_limit_usd":   model.DailyLimitUSD,
		"weekly_limit_usd":  model.WeeklyLimitUSD,
		"monthly_limit_usd": model.MonthlyLimitUSD,
		"rate_multiplier":   model.RateMultiplier,
		"status":            model.Status,
		"created_at":        model.CreatedAt,
		"updated_at":        model.UpdatedAt,
	}).Error
}

func (r *Repository) deleteGroupDB(ctx context.Context, groupID int64) error {
	return r.db.WithContext(ctx).Delete(&groupModel{}, groupID).Error
}

func (r *Repository) getGroupByIDDB(ctx context.Context, groupID int64) (*biz.SubscriptionGroup, error) {
	var model groupModel
	if err := r.db.WithContext(ctx).Where("id = ?", groupID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrSubscriptionGroupNotFound
		}
		return nil, err
	}
	group := groupFromModel(&model)
	return &group, nil
}

func (r *Repository) getGroupByNameDB(ctx context.Context, name string) (*biz.SubscriptionGroup, error) {
	var model groupModel
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrSubscriptionGroupNotFound
		}
		return nil, err
	}
	group := groupFromModel(&model)
	return &group, nil
}

func (r *Repository) listGroupsDB(ctx context.Context) ([]*biz.SubscriptionGroup, error) {
	var rows []groupModel
	if err := r.db.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
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
		Name:             group.Name,
		DisplayName:      group.DisplayName,
		Platform:         group.Platform,
		SubscriptionType: group.SubscriptionType,
		DailyLimitUSD:    group.DailyLimitUSD,
		WeeklyLimitUSD:   group.WeeklyLimitUSD,
		MonthlyLimitUSD:  group.MonthlyLimitUSD,
		RateMultiplier:   group.RateMultiplier,
		Status:           group.Status,
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
		Name:             model.Name,
		DisplayName:      model.DisplayName,
		Platform:         model.Platform,
		SubscriptionType: model.SubscriptionType,
		DailyLimitUSD:    model.DailyLimitUSD,
		WeeklyLimitUSD:   model.WeeklyLimitUSD,
		MonthlyLimitUSD:  model.MonthlyLimitUSD,
		RateMultiplier:   model.RateMultiplier,
		Status:           model.Status,
		CreatedAt:        model.CreatedAt,
		UpdatedAt:        model.UpdatedAt,
	}
}

func (r *Repository) createGroupMemory(ctx context.Context, group *biz.SubscriptionGroup) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	group.ID = r.nextGroupID
	r.nextGroupID++
	cloned := *group
	r.groups[group.ID] = &cloned
	return nil
}

func (r *Repository) updateGroupMemory(ctx context.Context, group *biz.SubscriptionGroup) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.groups[group.ID] = cloneGroup(group)
	return nil
}

func (r *Repository) deleteGroupMemory(ctx context.Context, groupID int64) error {
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
	return cloneGroup(group), nil
}

func (r *Repository) getGroupByNameMemory(ctx context.Context, name string) (*biz.SubscriptionGroup, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	for _, group := range r.groups {
		if group.Name == name {
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
