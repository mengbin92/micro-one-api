package service

import (
	"context"
	"fmt"
	billingv1 "micro-one-api/api/billing/v1"
)

func (s *AdminService) ListRequestAttempts(ctx context.Context, userID, rootID string, page, size int32) (*billingv1.ListRequestAttemptsResponse, error) {
	if s.billingClient == nil {
		return nil, fmt.Errorf("billing service unavailable")
	}
	return s.billingClient.ListRequestAttempts(ctx, &billingv1.ListRequestAttemptsRequest{UserId: userID, RootRequestId: rootID, Page: page, PageSize: size})
}
