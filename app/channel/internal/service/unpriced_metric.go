package service

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/platform/authz"
	"micro-one-api/platform/security/serviceidentity"
)

// CountUnpricedRoutedModels is the admin worker's narrow system capability.
// Operator-facing model details remain protected by ListUnpricedRoutedModels.
func (s *ChannelService) CountUnpricedRoutedModels(ctx context.Context, req *channelv1.ListUnpricedRoutedModelsRequest) (*channelv1.CountUnpricedRoutedModelsResponse, error) {
	if !serviceidentity.HasSystemCapability(ctx, channelv1.ChannelService_CountUnpricedRoutedModels_FullMethodName) {
		return nil, status.Error(codes.PermissionDenied, "dedicated admin audit capability required")
	}
	uc := s.modelUc()
	if uc == nil {
		return nil, status.Error(codes.Unavailable, "model registry unavailable")
	}
	priced := make(map[string]struct{}, len(req.GetPricedModelIds()))
	for _, id := range req.GetPricedModelIds() {
		priced[biz.NormalizeModelID(id)] = struct{}{}
	}
	counts, err := uc.CountUnpricedRoutedModels(ctx, priced)
	if err != nil {
		if authz.IsAuthorizationError(err) {
			return nil, err
		}
		return nil, mapModelError(err)
	}
	return &channelv1.CountUnpricedRoutedModelsResponse{ChannelCount: counts.Channel, SubscriptionCount: counts.Subscription}, nil
}
