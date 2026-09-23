package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	billingv1 "micro-one-api/api/billing/v1"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"micro-one-api/pkg/jsonx"
)

type usageBillingClient struct {
	billingv1.BillingServiceClient
	usage *billingv1.SubscriptionUsage
	err   error
}

func (c usageBillingClient) GetSubscriptionUsage(_ context.Context, req *billingv1.GetSubscriptionUsageRequest, _ ...grpc.CallOption) (*billingv1.GetSubscriptionUsageResponse, error) {
	if req.UserId != 42 {
		return nil, errors.New("wrong user")
	}
	return &billingv1.GetSubscriptionUsageResponse{Usage: c.usage}, c.err
}

func TestSubscriptionUsageUsesBillingAndFailsClosed(t *testing.T) {
	for _, tt := range []struct {
		name    string
		client  usageBillingClient
		status  int
		success bool
	}{
		{"authoritative frozen", usageBillingClient{usage: &billingv1.SubscriptionUsage{Id: 1, Status: "active", UsageSource: "billing", RateMultiplier: 2, DailyUsed: &billingv1.SubscriptionUsageDimension{Used: 1, Settled: 1, Frozen: new(2.0), Limit: new(5.0), Available: new(2.0), Remaining: 4}}}, 200, true},
		{"no active subscription", usageBillingClient{}, 200, false},
		{"authority unavailable", usageBillingClient{err: errors.New("billing unavailable")}, 502, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := NewHTTPServer(rawIdentityClient{}, nil, tt.client, nil, nil)
			s.subscriptionUsecase = subscriptionbiz.NewSubscriptionUsecase(failingSubscriptionRepo{err: errors.New("local reads forbidden")}, nil)
			req := httptest.NewRequest(http.MethodGet, "/v1/subscription/usage", nil)
			req.Header.Set("Authorization", "Bearer test-key")
			rec := httptest.NewRecorder()
			s.handleSubscriptionUsage(rec, req)
			require.Equal(t, tt.status, rec.Code, rec.Body.String())
			if tt.status != 200 {
				return
			}
			var response struct {
				Success bool                                  `json:"success"`
				Data    *subscriptionbiz.SubscriptionProgress `json:"data"`
			}
			require.NoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &response))
			require.Equal(t, tt.success, response.Success)
			if tt.success {
				require.Equal(t, "billing", response.Data.UsageSource)
				require.Equal(t, 2.0, *response.Data.DailyUsed.Frozen)
				require.Equal(t, 2.0, *response.Data.DailyUsed.Available)
				require.Equal(t, 4.0, response.Data.DailyUsed.Remaining)
			}
		})
	}
}
