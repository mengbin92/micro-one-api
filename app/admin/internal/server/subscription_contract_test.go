package server

import (
	"context"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	billingv1 "micro-one-api/api/billing/v1"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type contractCommerceClient struct {
	adminHTTPBillingClient
	request *billingv1.SubscriptionCommerceRequest
}

func (c *contractCommerceClient) ExecuteSubscriptionCommerce(_ context.Context, req *billingv1.SubscriptionCommerceRequest, _ ...grpc.CallOption) (*billingv1.SubscriptionCommerceReply, error) {
	c.request = req
	return &billingv1.SubscriptionCommerceReply{ResultJson: `{"Change":{"SubscriptionID":3,"Applied":true}}`}, nil
}
func TestSelfContractChangeUsesAuthenticatedBuyer(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "true")
	client := &contractCommerceClient{}
	server := newAdminHTTPSubscriptionPaymentTestServer(&adminHTTPIdentityClient{validateValid: true, validateUserID: 42}, client)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions/change", strings.NewReader(`{"user_id":999,"from_subscription_id":3,"to_plan_id":8,"old_price_quota":0,"new_price_quota":0}`))
	req.Header.Set("Authorization", "Bearer session")
	req.Header.Set("Idempotency-Key", "change-key")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, client.request)
	require.EqualValues(t, 42, client.request.UserId)
	require.EqualValues(t, 8, client.request.PlanId)
	require.Equal(t, "change-key", client.request.RequestId)
}
func TestContractPaymentDelegatesReplayWithoutLivePlanLookup(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "true")
	client := &adminHTTPBillingClient{}
	server := newAdminHTTPSubscriptionPaymentTestServer(&adminHTTPIdentityClient{validateValid: true, validateUserID: 42}, client)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions/purchase/payment", strings.NewReader(`{"plan_id":8,"money_cents":1}`))
	req.Header.Set("Authorization", "Bearer session")
	req.Header.Set("Idempotency-Key", "purchase-key")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, client.paymentCreateReq)
	require.Equal(t, "purchase-key", client.paymentCreateReq.RequestId)
	require.EqualValues(t, 8, client.paymentCreateReq.PlanId)
}
