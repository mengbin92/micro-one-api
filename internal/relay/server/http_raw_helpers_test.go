package server

import (
	"context"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
	logv1 "micro-one-api/api/log/v1"

	"google.golang.org/grpc"
)

type rawIdentityClient struct {
	identityv1.IdentityServiceClient
	userIDByToken map[string]int64
}

func (c rawIdentityClient) GetAuthSnapshot(ctx context.Context, req *identityv1.GetAuthSnapshotRequest, opts ...grpc.CallOption) (*identityv1.GetAuthSnapshotReply, error) {
	userID := int64(42)
	if c.userIDByToken != nil {
		if mapped, ok := c.userIDByToken[req.Token]; ok {
			userID = mapped
		}
	}
	return &identityv1.GetAuthSnapshotReply{
		UserId:        userID,
		TokenId:       7,
		Group:         "default",
		AllowedModels: []string{},
		UserEnabled:   true,
		TokenEnabled:  true,
	}, nil
}

type rawChannelClient struct {
	channelv1.ChannelServiceClient
	baseURL    string
	key        string
	chType     int32
	apiVersion string
}

func (c rawChannelClient) SelectChannel(ctx context.Context, req *channelv1.SelectChannelRequest, opts ...grpc.CallOption) (*channelv1.SelectChannelReply, error) {
	chType := c.chType
	if chType == 0 {
		chType = 1
	}
	return &channelv1.SelectChannelReply{
		Channel: &commonv1.ChannelInfo{
			Id:      11,
			Type:    chType,
			Name:    "openai-compatible",
			Status:  1,
			BaseUrl: c.baseURL,
			Key:     c.key,
			Group:   req.Group,
			Models:  req.Model,
			Config:  &commonv1.ChannelConfig{ApiVersion: c.apiVersion},
		},
	}, nil
}

func (c rawChannelClient) GetChannel(ctx context.Context, req *channelv1.GetChannelRequest, opts ...grpc.CallOption) (*channelv1.GetChannelReply, error) {
	chType := c.chType
	if chType == 0 {
		chType = 1
	}
	return &channelv1.GetChannelReply{
		Channel: &commonv1.ChannelInfo{
			Id:      req.ChannelId,
			Type:    chType,
			Name:    "openai-compatible",
			Status:  1,
			BaseUrl: c.baseURL,
			Key:     c.key,
			Group:   "default",
			Models:  "gpt-3.5-turbo",
			Config:  &commonv1.ChannelConfig{ApiVersion: c.apiVersion},
		},
	}, nil
}

type rawLogClient struct {
	logv1.LogServiceClient
	entries []*logv1.IngestLogRequest
}

func (c *rawLogClient) IngestLog(ctx context.Context, req *logv1.IngestLogRequest, opts ...grpc.CallOption) (*logv1.IngestLogResponse, error) {
	c.entries = append(c.entries, req)
	return &logv1.IngestLogResponse{Id: int64(len(c.entries))}, nil
}

type rawBillingClient struct {
	billingv1.BillingServiceClient
	commits        int
	commitRequests []*billingv1.CommitQuotaRequest
	releases       int
	reserveSuccess bool
	reserveMessage string
	commitSuccess  bool
	commitMessage  string
	releaseSuccess bool
	releaseMessage string
}

func (c *rawBillingClient) ReserveQuota(ctx context.Context, req *billingv1.ReserveQuotaRequest, opts ...grpc.CallOption) (*billingv1.ReserveQuotaResponse, error) {
	success := c.reserveSuccess
	if !success && c.reserveMessage == "" {
		success = true
	}
	return &billingv1.ReserveQuotaResponse{
		Success:        success,
		ErrorMessage:   c.reserveMessage,
		ReservationId:  "reservation-1",
		ReservedAmount: req.EstimatedTokens,
	}, nil
}

func (c *rawBillingClient) CommitQuota(ctx context.Context, req *billingv1.CommitQuotaRequest, opts ...grpc.CallOption) (*billingv1.CommitQuotaResponse, error) {
	c.commits++
	c.commitRequests = append(c.commitRequests, req)
	success := c.commitSuccess
	if !success && c.commitMessage == "" {
		success = true
	}
	return &billingv1.CommitQuotaResponse{Success: success, ErrorMessage: c.commitMessage, CommittedAmount: req.ActualTokens}, nil
}

func (c *rawBillingClient) ReleaseQuota(ctx context.Context, req *billingv1.ReleaseQuotaRequest, opts ...grpc.CallOption) (*billingv1.ReleaseQuotaResponse, error) {
	c.releases++
	success := c.releaseSuccess
	if !success && c.releaseMessage == "" {
		success = true
	}
	return &billingv1.ReleaseQuotaResponse{Success: success, ErrorMessage: c.releaseMessage}, nil
}
