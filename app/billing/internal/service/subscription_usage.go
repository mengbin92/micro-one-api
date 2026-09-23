package service

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	billingv1 "micro-one-api/api/billing/v1"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"micro-one-api/platform/subscriptiondto"
)

func (s *BillingService) GetSubscriptionUsage(ctx context.Context, req *billingv1.GetSubscriptionUsageRequest) (*billingv1.GetSubscriptionUsageResponse, error) {
	if req.GetUserId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid user id")
	}
	progress, err := s.uc.GetSubscriptionUsage(ctx, req.UserId)
	if errors.Is(err, subscriptionbiz.ErrSubscriptionNotFound) {
		return &billingv1.GetSubscriptionUsageResponse{}, nil
	}
	if err != nil {
		return nil, err
	}
	usage, err := subscriptiondto.ProgressToProto(progress)
	if err != nil {
		return nil, err
	}
	return &billingv1.GetSubscriptionUsageResponse{Usage: usage}, nil
}
