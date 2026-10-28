package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/app/identity/internal/biz"
	identitydata "micro-one-api/app/identity/internal/data"
)

type paymentChannelsClient struct {
	*identityHTTPBillingClient
	channels []*billingv1.PaymentChannel
	err      error
	calls    int
}

func (c *paymentChannelsClient) ListPaymentChannels(context.Context, *billingv1.ListPaymentChannelsRequest, ...grpc.CallOption) (*billingv1.ListPaymentChannelsResponse, error) {
	c.calls++
	return &billingv1.ListPaymentChannelsResponse{Channels: c.channels}, c.err
}

func TestPaymentChannelsUsesBillingConfigurationAndFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		channels   []*billingv1.PaymentChannel
		err        error
		authorized bool
		status     int
		want       string
	}{
		{name: "enabled", channels: []*billingv1.PaymentChannel{{Channel: "wechat", Currency: "CNY", AssetTypes: []string{"balance"}}}, authorized: true, status: 200, want: `"channel":"wechat"`},
		{name: "none enabled", authorized: true, status: 200, want: `"data":{}`},
		{name: "owner unavailable", authorized: true, err: errors.New("offline"), status: 503, want: `"success":false`},
		{name: "owner permission denied", authorized: true, err: status.Error(codes.PermissionDenied, "owner denied"), status: 403, want: `"error":"Forbidden"`},
		{name: "owner session rejected", authorized: true, err: status.Error(codes.Unauthenticated, "session expired"), status: 401, want: `"error":"Unauthorized"`},
		{name: "unauthenticated", status: 401, want: `"success":false`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uc := biz.NewIdentityUsecase(identitydata.NewMemoryRepositoryForTest(), nil)
			_, token := registerAndLoginForHTTPTest(t, uc)
			client := &paymentChannelsClient{identityHTTPBillingClient: &identityHTTPBillingClient{}, channels: tc.channels, err: tc.err}
			srv := NewHTTPServer(":0", uc, nil, client)
			req := httptest.NewRequest(http.MethodGet, "/api/user/payment/channels", nil)
			if tc.authorized {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if !tc.authorized && client.calls != 0 {
				t.Fatal("unauthenticated request reached billing")
			}
		})
	}
}

func TestWechatRechargePreservesDecimalAmountAndIdempotencyKey(t *testing.T) {
	uc := biz.NewIdentityUsecase(identitydata.NewMemoryRepositoryForTest(), nil)
	_, token := registerAndLoginForHTTPTest(t, uc)
	client := &identityHTTPBillingClient{}
	srv := NewHTTPServer(":0", uc, nil, client)
	req := httptest.NewRequest(http.MethodPost, "/api/user/pay", strings.NewReader(`{"amount":1.005,"payment_method":"wechat"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", "wechat-attempt-1")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 || len(client.createOrderCalls) != 1 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	order := client.createOrderCalls[0]
	if order.GetMoneyCents() != 101 || order.GetRequestId() != "wechat-attempt-1" || order.GetChannel() != "wechat" || order.GetAssetType() != "balance" {
		t.Fatalf("order=%+v", order)
	}
}

func TestRechargeRejectsUnboundedDecimalExponents(t *testing.T) {
	uc := biz.NewIdentityUsecase(identitydata.NewMemoryRepositoryForTest(), nil)
	_, token := registerAndLoginForHTTPTest(t, uc)
	client := &identityHTTPBillingClient{}
	srv := NewHTTPServer(":0", uc, nil, client)
	for _, amount := range []string{"1e2147483647", "1e-2147483647", "1e1000000", "1e-1000000", `"1e2147483647"`, `"1e-2147483647"`, "12345678901234567890123456789012345"} {
		req := httptest.NewRequest(http.MethodPost, "/api/user/pay", strings.NewReader(`{"amount":`+amount+`,"payment_method":"wechat"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if (rec.Code != http.StatusOK && rec.Code != http.StatusBadRequest) || !strings.Contains(rec.Body.String(), `"success":false`) {
			t.Fatalf("amount=%s status=%d body=%s", amount, rec.Code, rec.Body.String())
		}
	}
	if len(client.createOrderCalls) != 0 {
		t.Fatal("invalid amount reached billing")
	}
}
