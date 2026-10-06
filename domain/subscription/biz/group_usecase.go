package biz

import (
	"context"
	"errors"
	"micro-one-api/domain/authorization"
	"time"
)

type GroupUsecase struct {
	authorization authorization.Resolver
	consumer      string
	repo          GroupRepository
	now           func() time.Time
}

func NewGroupUsecase(repo GroupRepository) *GroupUsecase {
	return &GroupUsecase{repo: repo, now: time.Now}
}

func (uc *GroupUsecase) Create(ctx context.Context, group *SubscriptionGroup) error {
	if group != nil && group.Reason != "" {
		ctx = authorization.WithWriteReason(ctx, group.Reason)
	}
	var authErr error
	ctx, authErr = prepareSubscription(ctx, uc.authorization, uc.consumer, "quota_policies", "create")
	if authErr != nil {
		return authErr
	}
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
	if group != nil && group.Reason != "" {
		ctx = authorization.WithWriteReason(ctx, group.Reason)
	}
	var authErr error
	ctx, authErr = prepareSubscription(ctx, uc.authorization, uc.consumer, "quota_policies", "update")
	if authErr != nil {
		return authErr
	}
	if group == nil {
		return ErrSubscriptionGroupNotFound
	}
	group.UpdatedAt = uc.now().Unix()
	return uc.repo.UpdateGroup(ctx, group)
}

func (uc *GroupUsecase) Delete(ctx context.Context, groupID int64) error {
	var authErr error
	ctx, authErr = prepareSubscription(ctx, uc.authorization, uc.consumer, "quota_policies", "delete")
	if authErr != nil {
		return authErr
	}
	return uc.repo.DeleteGroup(ctx, groupID)
}

func (uc *GroupUsecase) Get(ctx context.Context, groupID int64) (*SubscriptionGroup, error) {
	var authErr error
	ctx, authErr = prepareSubscription(ctx, uc.authorization, uc.consumer, "quota_policies", "read")
	if authErr != nil {
		return nil, authErr
	}
	return uc.repo.GetGroupByID(ctx, groupID)
}

func (uc *GroupUsecase) List(ctx context.Context) ([]*SubscriptionGroup, error) {
	var authErr error
	ctx, authErr = prepareSubscription(ctx, uc.authorization, uc.consumer, "quota_policies", "list")
	if authErr != nil {
		return nil, authErr
	}
	return uc.repo.ListGroups(ctx)
}

func (uc *GroupUsecase) ListForPurchase(ctx context.Context) ([]*SubscriptionGroup, error) {
	groups, err := uc.repo.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*SubscriptionGroup, 0, len(groups))
	for _, g := range groups {
		if g.Status == SubscriptionGroupStatusEnabled && g.PriceQuota > 0 && g.DurationDays > 0 {
			out = append(out, g)
		}
	}
	return out, nil
}
