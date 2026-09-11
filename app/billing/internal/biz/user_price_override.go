package biz

import (
	"context"
	"fmt"
)

// UserPriceOverride is the live per-user routing-group price override. The
// ratio REPLACES (never multiplies) the group base ratio; the billing mode
// still comes from the published routing billing policy.

type UserPriceOverride struct {
	UserID, GroupID, Version int64
	PriceRatio               float64
}

// UserPriceOverrideRepo is the extensible resolver + audit source for
// user-specific group pricing. Implementations live in the data layer (097).

type UserPriceOverrideRepo interface {
	Get(ctx context.Context, userID, groupID int64) (*UserPriceOverride, error)
	Set(ctx context.Context, userID, groupID int64, ratio float64) (int64, error)
	Clear(ctx context.Context, userID, groupID int64) error
}

func (uc *BillingUsecase) SetUserPriceOverrideRepo(repo UserPriceOverrideRepo) {
	uc.userPriceOverrides = repo
}

// userPriceVersion is the frozen version string stamped into request
// snapshots and quotes whenever a user override is the effective ratio.
func userPriceVersion(o *UserPriceOverride) string {
	return fmt.Sprintf("user_routing_price:%d:%d:%d", o.GroupID, o.UserID, o.Version)
}

func (uc *BillingUsecase) SetUserRoutingPrice(ctx context.Context, userID, groupID int64, ratio float64) (int64, error) {
	if !uc.RoutingSnapshotsAvailable() || uc.userPriceOverrides == nil {
		return 0, ErrRequestSnapshotUnavailable
	}
	if userID <= 0 || groupID <= 0 || !finitePositive(ratio) {
		return 0, ErrRoutingContextInvalid
	}
	g, err := uc.routingGroups.GetRoutingGroup(ctx, groupID)
	if err != nil {
		return 0, err
	}
	if g == nil || g.Status == "archived" {
		return 0, ErrRoutingContextInvalid
	}
	return uc.userPriceOverrides.Set(ctx, userID, groupID, ratio)
}

func (uc *BillingUsecase) ClearUserRoutingPrice(ctx context.Context, userID, groupID int64) error {
	if !uc.RoutingSnapshotsAvailable() || uc.userPriceOverrides == nil {
		return ErrRequestSnapshotUnavailable
	}
	if userID <= 0 || groupID <= 0 {
		return ErrRoutingContextInvalid
	}
	return uc.userPriceOverrides.Clear(ctx, userID, groupID)
}

// resolveUserPriceOverride returns the live user override for the resolved
// group, or nil. Shared by the request-snapshot freeze and the quote path so
// both resolve with identical precedence.
func (uc *BillingUsecase) resolveUserPriceOverride(ctx context.Context, userID, groupID int64) (*UserPriceOverride, error) {
	if uc.userPriceOverrides == nil || userID <= 0 || groupID <= 0 {
		return nil, nil
	}
	return uc.userPriceOverrides.Get(ctx, userID, groupID)
}

// applyUserPriceOverride folds the override into a frozen snapshot that has
// already resolved GroupRatio and BillingPolicyVersion from base -> policy.
func applyUserPriceOverride(s *RequestSnapshot, o *UserPriceOverride) {
	s.Pricing.GroupRatio = o.PriceRatio
	s.BillingPolicyVersion = userPriceVersion(o)
}
