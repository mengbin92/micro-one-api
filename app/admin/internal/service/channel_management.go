package service

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	channelv1 "micro-one-api/api/channel/v1"
)

func (s *AdminService) BatchDeleteChannels(ctx context.Context, req *channelv1.BatchDeleteChannelsRequest) (*channelv1.BatchDeleteChannelsResponse, error) {
	if s.channelClient == nil {
		return nil, status.Error(codes.Unavailable, "channel service unavailable")
	}
	return s.channelClient.BatchDeleteChannels(operatorRPCContext(ctx), req)
}
func (s *AdminService) ExportChannels(ctx context.Context, req *channelv1.ListChannelsRequest) (*channelv1.ChannelExportArtifact, error) {
	if s.channelClient == nil {
		return nil, status.Error(codes.Unavailable, "channel service unavailable")
	}
	return s.channelClient.ExportChannels(operatorRPCContext(ctx), req)
}
func (s *AdminService) ClearSubscriptionAccountError(ctx context.Context, req *channelv1.ClearSubscriptionAccountErrorRequest) (*channelv1.ClearSubscriptionAccountErrorResponse, error) {
	if s.channelClient == nil {
		return nil, status.Error(codes.Unavailable, "channel service unavailable")
	}
	return s.channelClient.ClearSubscriptionAccountError(operatorRPCContext(ctx), req)
}
