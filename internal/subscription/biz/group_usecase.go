package biz

import (
	"context"
	"time"
)

type GroupUsecase struct {
	repo GroupRepository
	now  func() time.Time
}

func NewGroupUsecase(repo GroupRepository) *GroupUsecase {
	return &GroupUsecase{repo: repo, now: time.Now}
}

func (uc *GroupUsecase) Create(ctx context.Context, group *SubscriptionGroup) error {
	if group == nil {
		return ErrSubscriptionGroupNotFound
	}
	existing, err := uc.repo.GetGroupByName(ctx, group.Name)
	if err == nil && existing != nil {
		return ErrSubscriptionGroupNameTaken
	}
	now := uc.now().Unix()
	group.CreatedAt = now
	group.UpdatedAt = now
	if group.Status == 0 {
		group.Status = SubscriptionGroupStatusEnabled
	}
	return uc.repo.CreateGroup(ctx, group)
}

func (uc *GroupUsecase) Update(ctx context.Context, group *SubscriptionGroup) error {
	if group == nil {
		return ErrSubscriptionGroupNotFound
	}
	group.UpdatedAt = uc.now().Unix()
	return uc.repo.UpdateGroup(ctx, group)
}

func (uc *GroupUsecase) Delete(ctx context.Context, groupID int64) error {
	return uc.repo.DeleteGroup(ctx, groupID)
}

func (uc *GroupUsecase) Get(ctx context.Context, groupID int64) (*SubscriptionGroup, error) {
	return uc.repo.GetGroupByID(ctx, groupID)
}

func (uc *GroupUsecase) List(ctx context.Context) ([]*SubscriptionGroup, error) {
	return uc.repo.ListGroups(ctx)
}
