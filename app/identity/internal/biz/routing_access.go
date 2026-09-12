package biz

import (
	"context"
	"micro-one-api/domain/routing"
)

// RoutingAccessChange is an identity-owned command. Remote group references
// have already been checked by admin orchestration; no channel PO crosses here.
type RoutingAccessChange struct {
	UserID, ExpectedRevision, GroupID                             int64
	Operation, GroupKey, SourceType, SourceRef, PublicGroupAccess string
	StartsAt, ExpiresAt                                           int64
}
type RoutingAccessRepo interface {
	UserRoutingFacts(context.Context, int64) (*routing.SubjectFacts, error)
	UpdateRoutingAccess(context.Context, RoutingAccessChange) error
	SetTokenRouting(context.Context, int64, int64, string, int64, int64, []int64) (int64, error)
}

func (uc *IdentityUsecase) routingAccessRepo() (RoutingAccessRepo, error) {
	r, ok := uc.repo.(RoutingAccessRepo)
	if !ok || !RoutingV2Enabled() {
		return nil, ErrRoutingFactsUnavailable
	}
	return r, nil
}
func (uc *IdentityUsecase) UserRoutingFacts(ctx context.Context, userID int64) (*routing.SubjectFacts, error) {
	r, err := uc.routingAccessRepo()
	if err != nil {
		return nil, err
	}
	return r.UserRoutingFacts(ctx, userID)
}
func (uc *IdentityUsecase) UpdateRoutingAccess(ctx context.Context, c RoutingAccessChange) (*routing.SubjectFacts, error) {
	r, err := uc.routingAccessRepo()
	if err != nil {
		return nil, err
	}
	if c.UserID <= 0 || c.ExpectedRevision <= 0 {
		return nil, ErrRoutingAccessConflict
	}
	switch c.Operation {
	case "grant", "revoke":
		if c.GroupID <= 0 || (c.SourceType != "admin" && !(c.Operation == "revoke" && c.SourceType == "migration")) || c.SourceRef == "" || len(c.SourceRef) > 128 || c.StartsAt < 0 || c.ExpiresAt < 0 || (c.ExpiresAt > 0 && c.ExpiresAt <= c.StartsAt) {
			return nil, ErrRoutingDefaultInvalid
		}
	case "default":
		if c.GroupID <= 0 || c.GroupKey == "" {
			return nil, ErrRoutingDefaultInvalid
		}
	case "public_access":
		if c.PublicGroupAccess != "all" && c.PublicGroupAccess != "explicit_only" {
			return nil, ErrRoutingDefaultInvalid
		}
	default:
		return nil, ErrRoutingDefaultInvalid
	}
	if err = r.UpdateRoutingAccess(ctx, c); err != nil {
		return nil, err
	}
	return r.UserRoutingFacts(ctx, c.UserID)
}
func (uc *IdentityUsecase) SetTokenRouting(ctx context.Context, userID, tokenID int64, mode string, groupID, revision int64, groupIDs []int64) (int64, error) {
	r, err := uc.routingAccessRepo()
	if err != nil {
		return 0, err
	}
	if userID <= 0 || tokenID <= 0 || revision <= 0 {
		return 0, ErrRoutingDefaultInvalid
	}
	valid := routing.ValidPolicy(mode, groupID)
	if mode == "ordered" {
		valid = routing.ValidOrderedPolicy(mode, groupID, groupIDs)
	} else if len(groupIDs) > 0 {
		valid = false
	}
	if !valid {
		return 0, ErrRoutingDefaultInvalid
	}
	return r.SetTokenRouting(ctx, userID, tokenID, mode, groupID, revision, groupIDs)
}
