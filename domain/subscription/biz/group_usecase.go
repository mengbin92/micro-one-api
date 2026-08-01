package biz

import (
	"context"
	"errors"
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
	if err != nil && !errors.Is(err, ErrSubscriptionGroupNotFound) {
		return err
	}
	if existing != nil {
		return ErrSubscriptionGroupNameTaken
	}
	now := uc.now().Unix()
	group.CreatedAt = now
	group.UpdatedAt = now
	// domain-L3: do NOT coerce Status==0 to Enabled here. 0 is the defined
	// SubscriptionGroupStatusDisabled constant, so this coercion made it
	// impossible to create a pre-disabled group and silently flipped any caller
	// that intentionally passed Disabled. The service/DTO boundary is now
	// responsible for defaulting an unset status to Enabled (the HTTP handler
	// treats an omitted status as Enabled because that is the common create case).
	// A zero multiplier would silently zero out all recorded usage; default to
	// 1.0 (no scaling) when the caller doesn't specify one.
	if group.RateMultiplier <= 0 {
		group.RateMultiplier = 1.0
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
