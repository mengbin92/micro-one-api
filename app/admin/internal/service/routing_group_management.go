package service

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	channelv1 "micro-one-api/api/channel/v1"
)

func (s *AdminService) ArchiveRoutingGroup(ctx context.Context, req *channelv1.ArchiveRoutingGroupRequest) (*channelv1.RoutingGroupMutationReply, error) {
	if s.channelClient == nil {
		return nil, status.Error(codes.Unavailable, "channel service unavailable")
	}
	return s.channelClient.ArchiveRoutingGroup(operatorRPCContext(ctx), req)
}
func (s *AdminService) ReplaceRoutingGroupMembers(ctx context.Context, req *channelv1.ReplaceRoutingGroupMembersRequest) (*channelv1.RoutingGroupMutationReply, error) {
	if s.channelClient == nil {
		return nil, status.Error(codes.Unavailable, "channel service unavailable")
	}
	return s.channelClient.ReplaceRoutingGroupMembers(operatorRPCContext(ctx), req)
}

func (s *AdminService) MutateRoutingGroupState(ctx context.Context, id int64, r RoutingGroupStateRequest) (*channelv1.RoutingGroupDetail, error) {
	if s.channelClient == nil {
		return nil, status.Error(codes.Unavailable, "channel service unavailable")
	}
	return s.channelClient.SetRoutingGroupState(operatorRPCContext(ctx), &channelv1.SetRoutingGroupStateRequest{Id: id, ExpectedRevision: r.ExpectedRevision, Status: r.Status, AccessMode: r.AccessMode, Reason: requestWriteReason(ctx, r.Reason)})
}
