package service

import (
	"context"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/routing"
	"micro-one-api/platform/routingdto"
)

func (s *IdentityService) GetUserRoutingFacts(ctx context.Context, req *identityv1.GetUserRoutingFactsRequest) (*identityv1.GetUserRoutingFactsReply, error) {
	f, err := s.uc.UserRoutingFacts(ctx, req.UserId)
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	return routingFactsReply(f), nil
}
func (s *IdentityService) UpdateUserRoutingAccess(ctx context.Context, req *identityv1.UpdateUserRoutingAccessRequest) (*identityv1.GetUserRoutingFactsReply, error) {
	f, err := s.uc.UpdateRoutingAccess(ctx, biz.RoutingAccessChange{UserID: req.UserId, ExpectedRevision: req.ExpectedRevision, GroupID: req.RoutingGroupId, Operation: req.Operation, GroupKey: req.GroupKey, SourceType: req.SourceType, SourceRef: req.SourceRef, StartsAt: req.StartsAt, ExpiresAt: req.ExpiresAt, PublicGroupAccess: req.PublicGroupAccess})
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	return routingFactsReply(f), nil
}
func (s *IdentityService) SetTokenRouting(ctx context.Context, req *identityv1.SetTokenRoutingRequest) (*identityv1.SetTokenRoutingReply, error) {
	rev, err := s.uc.SetTokenRouting(ctx, req.UserId, req.TokenId, req.RoutingMode, req.RoutingGroupId, req.ExpectedRevision)
	if err != nil {
		return nil, mapIdentityErrorToGRPC(err)
	}
	return &identityv1.SetTokenRoutingReply{Revision: rev}, nil
}

func routingFactsReply(f *routing.SubjectFacts) *identityv1.GetUserRoutingFactsReply {
	p := &identityv1.GetUserRoutingFactsReply{Facts: routingdto.FactsToProto(f)}
	for _, t := range f.TokenReferences {
		p.Tokens = append(p.Tokens, &identityv1.RoutingTokenReference{Id: t.ID, Name: t.Name, Mode: t.Mode, GroupId: t.GroupID, Revision: t.Revision})
	}
	return p
}
