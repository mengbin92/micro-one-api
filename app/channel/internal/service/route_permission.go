package service

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/domain/routing"
)

func (s *ChannelService) CheckRoute(ctx context.Context, req *channelv1.CheckRouteRequest) (*channelv1.CheckRouteReply, error) {
	if req == nil || req.SourceId <= 0 || req.Group == "" || req.Model == "" || (req.SourceKind != routing.Channel && req.SourceKind != routing.Subscription) {
		return nil, status.Error(codes.InvalidArgument, "group, model and a valid routing source are required")
	}
	if err := s.validateRoutingGroupPair(ctx, req.RoutingGroupId, req.Group); err != nil {
		return nil, err
	}
	permission, err := s.uc.CanRoute(ctx, req.Group, req.Model, routing.Source{Kind: req.SourceKind, ID: req.SourceId})
	if err != nil {
		return nil, err
	}
	return &channelv1.CheckRouteReply{Allowed: permission.Allowed, UpstreamModelId: permission.UpstreamModelID}, nil
}
