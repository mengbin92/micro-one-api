package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/billing/internal/biz"
)

type stubWechatVerifier struct {
	biz.WechatPaymentProvider
	notification *biz.PaymentNotify
	err          error
	body         []byte
	headers      map[string]string
	calls        int
}

func (v *stubWechatVerifier) VerifyWebhook(_ context.Context, headers map[string]string, body []byte) (*biz.PaymentNotify, error) {
	v.calls++
	v.headers = headers
	v.body = append([]byte(nil), body...)
	return v.notification, v.err
}

func wechatNotifyService() (*BillingService, *stubWechatVerifier, *notifyPaymentRepo, *notifyCountingIssuer) {
	order := pendingNotifyOrder()
	order.Channel = biz.PaymentChannelWechat
	order.Currency = "CNY"
	repo := &notifyPaymentRepo{order: order}
	issuer := &notifyCountingIssuer{}
	uc := biz.NewPaymentUsecase(repo, nil, issuer)
	svc := NewBillingService(nil, nil, uc, nil)
	verifier := &stubWechatVerifier{notification: &biz.PaymentNotify{
		TradeNo: order.TradeNo, ProviderTradeNo: "wechat-transaction", Channel: biz.PaymentChannelWechat,
		Success: true, TotalAmount: order.MoneyCents, Currency: "CNY",
	}}
	svc.SetWechatVerifier(verifier)
	return svc, verifier, repo, issuer
}

func TestWechatNotifyPreservesSignedInputAndIssuesOnce(t *testing.T) {
	svc, verifier, repo, issuer := wechatNotifyService()
	const body = "{\n  \"resource\": {\"ciphertext\":\"unchanged\\nbytes\"}\n}\n"
	headers := map[string]string{
		"Wechatpay-Timestamp":      "1793116800",
		"Wechatpay-Nonce":          "signed-nonce",
		"Wechatpay-Serial":         "PUB_KEY_ID_test",
		"Wechatpay-Signature":      "signed-base64",
		"Wechatpay-Signature-Type": "WECHATPAY2-SHA256-RSA2048",
	}
	for range 2 {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/user/payments/wechat/notify", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		rec := httptest.NewRecorder()
		svc.HandleWechatNotify(rec, req)
		require.Equal(t, http.StatusNoContent, rec.Code)
		require.Empty(t, rec.Body.String())
	}
	require.Equal(t, []byte(body), verifier.body)
	require.Equal(t, headers, verifier.headers)
	require.Equal(t, 2, verifier.calls)
	require.Equal(t, biz.PaymentOrderStatusPaid, repo.order.Status)
	require.Equal(t, 1, issuer.issued)
}

func TestWechatNotifyRejectsUnverifiedOrMismatchedPayment(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*stubWechatVerifier)
	}{
		{name: "bad-signature", mutate: func(v *stubWechatVerifier) { v.err = errors.New("invalid signature") }},
		{name: "nil-notification", mutate: func(v *stubWechatVerifier) { v.notification = nil }},
		{name: "not-success", mutate: func(v *stubWechatVerifier) { v.notification.Success = false }},
		{name: "amount-mismatch", mutate: func(v *stubWechatVerifier) { v.notification.TotalAmount++ }},
		{name: "currency-mismatch", mutate: func(v *stubWechatVerifier) { v.notification.Currency = "USD" }},
		{name: "channel-mismatch", mutate: func(v *stubWechatVerifier) { v.notification.Channel = biz.PaymentChannelAlipay }},
		{name: "unknown-order", mutate: func(v *stubWechatVerifier) { v.notification.TradeNo = "unknown" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, verifier, repo, issuer := wechatNotifyService()
			test.mutate(verifier)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/user/payments/wechat/notify", strings.NewReader(`{"resource":{}}`))
			rec := httptest.NewRecorder()
			svc.HandleWechatNotify(rec, req)
			require.GreaterOrEqual(t, rec.Code, http.StatusBadRequest)
			require.Equal(t, biz.PaymentOrderStatusPending, repo.order.Status)
			require.Zero(t, issuer.issued)
		})
	}
}

func TestWechatNotifyRejectsUnavailableOrOversizedRequest(t *testing.T) {
	for _, test := range []struct {
		name    string
		method  string
		body    string
		disable bool
		status  int
	}{
		{name: "method", method: http.MethodGet, status: http.StatusMethodNotAllowed},
		{name: "disabled", method: http.MethodPost, disable: true, status: http.StatusServiceUnavailable},
		{name: "oversized", method: http.MethodPost, body: strings.Repeat("x", (64<<10)+1), status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, verifier, repo, issuer := wechatNotifyService()
			if test.disable {
				svc.SetWechatVerifier(nil)
			}
			req := httptest.NewRequest(test.method, "/api/v1/user/payments/wechat/notify", strings.NewReader(test.body))
			rec := httptest.NewRecorder()
			svc.HandleWechatNotify(rec, req)
			require.Equal(t, test.status, rec.Code)
			require.Zero(t, verifier.calls)
			require.Equal(t, biz.PaymentOrderStatusPending, repo.order.Status)
			require.Zero(t, issuer.issued)
		})
	}
}
