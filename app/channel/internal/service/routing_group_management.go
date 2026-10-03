package service

import (
	"context"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/routing"
)

func (s *ChannelService) ArchiveRoutingGroup(ctx context.Context, req *channelv1.ArchiveRoutingGroupRequest) (*channelv1.RoutingGroupMutationReply, error) {
	if req == nil {
		return nil, biz.ErrRoutingGroupInvalid
	}
	uc, ok := s.routingGroupUC.(interface {
		Archive(context.Context, int64, int64, string) (*biz.RoutingGroup, error)
	})
	if !ok {
		return nil, biz.ErrRoutingGroupMigrationRequired
	}
	group, err := uc.Archive(ctx, req.Id, req.ExpectedRevision, req.Reason)
	if err != nil {
		return nil, err
	}
	return &channelv1.RoutingGroupMutationReply{Id: group.ID, Revision: group.Revision, Status: group.Status}, nil
}
func (s *ChannelService) ReplaceRoutingGroupMembers(ctx context.Context, req *channelv1.ReplaceRoutingGroupMembersRequest) (*channelv1.RoutingGroupMutationReply, error) {
	if req == nil {
		return nil, biz.ErrRoutingGroupInvalid
	}
	uc, ok := s.routingGroupUC.(interface {
		ReplaceMembers(context.Context, int64, int64, []routing.Source, string) (*biz.RoutingGroup, error)
	})
	if !ok {
		return nil, biz.ErrRoutingGroupMigrationRequired
	}
	members := make([]routing.Source, 0, len(req.Members))
	for _, source := range req.Members {
		if source == nil {
			return nil, biz.ErrRoutingGroupInvalid
		}
		members = append(members, routing.Source{Kind: source.SourceKind, ID: source.SourceId})
	}
	group, err := uc.ReplaceMembers(ctx, req.Id, req.ExpectedRevision, members, req.Reason)
	if err != nil {
		return nil, err
	}
	return &channelv1.RoutingGroupMutationReply{Id: group.ID, Revision: group.Revision, Status: group.Status}, nil
}
