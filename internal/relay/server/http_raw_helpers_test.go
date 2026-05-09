package server

import (
	"context"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"

	"google.golang.org/grpc"
)

type rawIdentityClient struct {
	identityv1.IdentityServiceClient
}

func (c rawIdentityClient) GetAuthSnapshot(ctx context.Context, req *identityv1.GetAuthSnapshotRequest, opts ...grpc.CallOption) (*identityv1.GetAuthSnapshotReply, error) {
	return &identityv1.GetAuthSnapshotReply{
		UserId:        42,
		TokenId:       7,
		Group:         "default",
		AllowedModels: []string{},
		UserEnabled:   true,
		TokenEnabled:  true,
	}, nil
}

type rawChannelClient struct {
	channelv1.ChannelServiceClient
	baseURL string
	key     string
}

func (c rawChannelClient) SelectChannel(ctx context.Context, req *channelv1.SelectChannelRequest, opts ...grpc.CallOption) (*channelv1.SelectChannelReply, error) {
	return &channelv1.SelectChannelReply{
		Channel: &commonv1.ChannelInfo{
			Id:      11,
			Type:    1,
			Name:    "openai-compatible",
			Status:  1,
			BaseUrl: c.baseURL,
			Key:     c.key,
			Group:   req.Group,
			Models:  req.Model,
		},
	}, nil
}

func (c rawChannelClient) GetChannel(ctx context.Context, req *channelv1.GetChannelRequest, opts ...grpc.CallOption) (*channelv1.GetChannelReply, error) {
	return &channelv1.GetChannelReply{
		Channel: &commonv1.ChannelInfo{
			Id:      req.ChannelId,
			Type:    1,
			Name:    "openai-compatible",
			Status:  1,
			BaseUrl: c.baseURL,
			Key:     c.key,
			Group:   "default",
			Models:  "gpt-3.5-turbo",
		},
	}, nil
}

type rawBillingClient struct {
	billingv1.BillingServiceClient
	commits  int
	releases int
}

func (c *rawBillingClient) ReserveQuota(ctx context.Context, req *billingv1.ReserveQuotaRequest, opts ...grpc.CallOption) (*billingv1.ReserveQuotaResponse, error) {
	return &billingv1.ReserveQuotaResponse{
		Success:        true,
		ReservationId:  "reservation-1",
		ReservedAmount: req.EstimatedTokens,
	}, nil
}

func (c *rawBillingClient) CommitQuota(ctx context.Context, req *billingv1.CommitQuotaRequest, opts ...grpc.CallOption) (*billingv1.CommitQuotaResponse, error) {
	c.commits++
	return &billingv1.CommitQuotaResponse{Success: true, CommittedAmount: req.ActualTokens}, nil
}

func (c *rawBillingClient) ReleaseQuota(ctx context.Context, req *billingv1.ReleaseQuotaRequest, opts ...grpc.CallOption) (*billingv1.ReleaseQuotaResponse, error) {
	c.releases++
	return &billingv1.ReleaseQuotaResponse{Success: true}, nil
}
