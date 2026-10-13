package service

import (
	"context"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/biz"
)

func (s *ChannelService) RecordUpstreamInventory(ctx context.Context, req *channelv1.RecordUpstreamInventoryRequest) (*channelv1.RecordUpstreamInventoryResponse, error) {
	d, err := s.uc.RecordUpstreamInventory(ctx, biz.UpstreamInventory{SourceKind: req.GetSourceKind(), SourceID: req.GetSourceId(), ExactModelIDs: append([]string(nil), req.GetExactModelIds()...), AttemptID: req.GetAttemptId(), ObservedAt: req.GetObservedAt(), Manual: req.GetManual()})
	if err != nil {
		return nil, err
	}
	return &channelv1.RecordUpstreamInventoryResponse{Received: true, Added: d.Added, Missing: d.Missing}, nil
}

func (s *ChannelService) GetUsageCapabilities(ctx context.Context, _ *channelv1.GetUsageCapabilitiesRequest) (*channelv1.GetUsageCapabilitiesResponse, error) {
	version := int32(0)
	if s.uc.UsageV2Available(ctx) {
		version = 2
	}
	return &channelv1.GetUsageCapabilitiesResponse{UsageContractVersion: version}, nil
}
