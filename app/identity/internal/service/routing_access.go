package service

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/routing"
	"micro-one-api/platform/routingdto"
	"micro-one-api/platform/security/serviceidentity"
)

func (s *IdentityService) GetUserRoutingFacts(ctx context.Context, req *identityv1.GetUserRoutingFactsRequest) (*identityv1.GetUserRoutingFactsReply, error) {
	mode, err := s.uc.AuthorizationMode(ctx)
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	var f *routing.SubjectFacts
	if mode == "iam" && !serviceidentity.FromContext(ctx).SystemCapability(identityv1.IdentityService_GetUserRoutingFacts_FullMethodName) {
		ctx, err = s.managedIdentityContext(ctx)
		if err != nil {
			return nil, err
		}
		f, err = s.uc.ManagedUserRoutingFacts(ctx, req.UserId)
	} else {
		f, err = s.uc.UserRoutingFacts(ctx, req.UserId)
	}
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	return routingFactsReply(f), nil
}
func (s *IdentityService) UpdateUserRoutingAccess(ctx context.Context, req *identityv1.UpdateUserRoutingAccessRequest) (*identityv1.GetUserRoutingFactsReply, error) {
	ctx, err := s.managedIdentityContext(ctx)
	if err != nil {
		return nil, err
	}
	f, err := s.uc.UpdateRoutingAccess(ctx, biz.RoutingAccessChange{ExpectedUserRevision: req.ExpectedUserRevision, ExpectedPolicyRevision: req.ExpectedPolicyRevision, Reason: req.Reason, UserID: req.UserId, ExpectedRevision: req.ExpectedRevision, GroupID: req.RoutingGroupId, Operation: req.Operation, GroupKey: req.GroupKey, SourceType: req.SourceType, SourceRef: req.SourceRef, StartsAt: req.StartsAt, ExpiresAt: req.ExpiresAt, PublicGroupAccess: req.PublicGroupAccess})
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	return routingFactsReply(f), nil
}
func (s *IdentityService) SetTokenRouting(ctx context.Context, req *identityv1.SetTokenRoutingRequest) (*identityv1.SetTokenRoutingReply, error) {
	if err := s.intrinsicUserContext(ctx, req.UserId); err != nil {
		return nil, err
	}
	rev, err := s.uc.SetTokenRouting(ctx, req.UserId, req.TokenId, req.RoutingMode, req.RoutingGroupId, req.ExpectedRevision, req.RoutingGroupIds)
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	return &identityv1.SetTokenRoutingReply{Revision: rev}, nil
}

func routingFactsReply(f *routing.SubjectFacts) *identityv1.GetUserRoutingFactsReply {
	p := &identityv1.GetUserRoutingFactsReply{Facts: routingdto.FactsToProto(f), AuthorizationRevision: f.AuthorizationRevision, AuthorizationPolicyRevision: f.AuthorizationPolicyRevision}
	for _, t := range f.TokenReferences {
		p.Tokens = append(p.Tokens, &identityv1.RoutingTokenReference{Id: t.ID, Name: t.Name, Mode: t.Mode, GroupId: t.GroupID, Revision: t.Revision, GroupIds: t.GroupIDs})
	}
	return p
}

func (s *IdentityService) intrinsicUserContext(ctx context.Context, userID int64) error {
	mode, err := s.uc.AuthorizationMode(ctx)
	if err != nil {
		return mapIdentityErrorToGRPC(err)
	}
	if mode == "legacy" {
		return nil
	}
	if !serviceidentity.FromContext(ctx).Dedicated || serviceidentity.FromContext(ctx).Name != "admin" {
		return status.Error(codes.PermissionDenied, "dedicated caller required")
	}
	raw, system := operatorCredential(ctx)
	if raw == "" || system {
		return status.Error(codes.Unauthenticated, "user session required")
	}
	snapshot, err := s.uc.GetSessionAuthorization(ctx, raw, authorization.Platform())
	if err != nil {
		return mapIdentityErrorToGRPC(err)
	}
	if snapshot.Actor.UserID != userID {
		return status.Error(codes.PermissionDenied, "self operation only")
	}
	return nil
}
