package service

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/domain/authorization"
)

func (s *ChannelService) ExecuteChannelAction(ctx context.Context, req *channelv1.ExecuteChannelActionRequest) (*channelv1.ExecuteChannelActionReply, error) {
	ctx = ownerWriteContext(ctx, req, "channel", req.ChannelId, "expected_revision")
	if req.ChannelId <= 0 || (req.Action != "test" && req.Action != "balance_refresh") {
		return nil, status.Error(codes.InvalidArgument, "valid channel and action required")
	}
	if req.Reason != "" {
		ctx = authorization.WithWriteReason(ctx, req.Reason)
	}
	result, err := s.uc.ExecuteChannelAction(ctx, req.ChannelId, req.Action)
	if err != nil {
		return nil, err
	}
	return &channelv1.ExecuteChannelActionReply{Success: result.Success, Skipped: result.Skipped, ChannelId: result.ChannelID, Message: result.Message, Provider: result.Provider, HealthStatus: result.HealthStatus, ResponseTime: result.ResponseTime, StatusCode: result.StatusCode, Balance: result.Balance, BalanceUpdatedTime: result.BalanceUpdatedTime, BalanceRefreshLastError: result.BalanceRefreshLastError, BalanceRefreshLastSuccessTime: result.BalanceRefreshLastSuccessTime, ConsecutiveBalanceRefreshFailures: result.ConsecutiveBalanceRefreshFailures}, nil
}
