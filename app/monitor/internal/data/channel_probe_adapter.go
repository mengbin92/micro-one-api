package data

import (
	"context"

	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
	"micro-one-api/app/monitor/internal/biz"
)

// ChannelProbeAdapter implements biz.ChannelProbeClient by wrapping the
// proto-generated channel-service gRPC client. This keeps proto DTO imports
// in the data layer, out of the biz layer.
type ChannelProbeAdapter struct {
	client channelv1.ChannelServiceClient
}

// NewChannelProbeAdapter creates a new adapter wrapping the given gRPC client.
func NewChannelProbeAdapter(client channelv1.ChannelServiceClient) *ChannelProbeAdapter {
	return &ChannelProbeAdapter{client: client}
}

func (a *ChannelProbeAdapter) ListEnabledChannels(ctx context.Context, page, pageSize int32) ([]biz.ChannelProbeSummary, error) {
	resp, err := a.client.ListChannels(ctx, &channelv1.ListChannelsRequest{
		Page:     page,
		PageSize: pageSize,
		Status:   1,
	})
	if err != nil || resp == nil {
		return nil, err
	}
	channels := resp.GetChannels()
	result := make([]biz.ChannelProbeSummary, 0, len(channels))
	for _, ch := range channels {
		if ch == nil {
			continue
		}
		result = append(result, biz.ChannelProbeSummary{
			ID:     ch.GetId(),
			Type:   ch.GetType(),
			Status: ch.GetStatus(),
		})
	}
	return result, nil
}

func (a *ChannelProbeAdapter) GetChannelDetail(ctx context.Context, channelID int64) (*biz.ChannelProbeDetail, error) {
	resp, err := a.client.GetChannel(ctx, &channelv1.GetChannelRequest{ChannelId: channelID})
	if err != nil || resp == nil || resp.GetChannel() == nil {
		return nil, err
	}
	ch := resp.GetChannel()
	return &biz.ChannelProbeDetail{
		ID:         ch.GetId(),
		Type:       ch.GetType(),
		BaseURL:    ch.GetBaseUrl(),
		Key:        ch.GetKey(),
		APIVersion: ch.GetConfig().GetApiVersion(),
	}, nil
}

func (a *ChannelProbeAdapter) RecordChannelHealth(ctx context.Context, channelID int64, success bool, errMsg string, responseTimeMs int64) error {
	resp, err := a.client.RecordChannelHealth(ctx, &channelv1.RecordChannelHealthRequest{
		ChannelId:    channelID,
		Success:      success,
		Error:        errMsg,
		ResponseTime: responseTimeMs,
	})
	if err != nil {
		return err
	}
	if resp != nil && !resp.GetSuccess() {
		return nil
	}
	return nil
}

// Ensure the adapter satisfies the biz interface.
var _ biz.ChannelProbeClient = (*ChannelProbeAdapter)(nil)

// Suppress unused import warning for commonv1 (used for type assertions in tests).
var _ = commonv1.ChannelSummary{}
