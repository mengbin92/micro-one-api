package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"micro-one-api/app/billing/internal/biz"

	subscriptionbiz "micro-one-api/domain/subscription/biz"
)

// F17 故障子项在 notify HTTP 边界的隔离验收：验签失败、非成功状态吞掉、
// app_id / 金额交叉校验拒绝、重复签名回调幂等。验签器与 usecase 的
// repo/issuer 均为 fake，故障由测试注入。

// stubNotifyVerifier returns a canned verified notify regardless of params,
// or an error to model signature verification failure.
type stubNotifyVerifier struct {
	notify *biz.PaymentNotify
	err    error
}

func (v *stubNotifyVerifier) VerifyNotify(ctx context.Context, params map[string]string) (*biz.PaymentNotify, error) {
	return v.notify, v.err
}

// notifyPaymentRepo backs the notify handler path with in-memory order state.
// Only GetOrderByTradeNo and MarkOrderPaid are exercised by HandleAlipayNotify;
// every other repo method panics via the embedded nil interface if reached.
type notifyPaymentRepo struct {
	biz.PaymentRepo
	order *biz.PaymentOrder
}

func (r *notifyPaymentRepo) GetOrderByTradeNo(ctx context.Context, tradeNo string) (*biz.PaymentOrder, error) {
	if r.order == nil || r.order.TradeNo != tradeNo {
		return nil, nil
	}
	copy := *r.order
	return &copy, nil
}

func (r *notifyPaymentRepo) MarkOrderPaid(ctx context.Context, tradeNo, providerTradeNo string, issue func(*biz.PaymentOrder, subscriptionbiz.Tx) error) (*biz.PaymentOrder, bool, error) {
	if r.order == nil || r.order.TradeNo != tradeNo {
		return nil, false, nil
	}
	if r.order.Status == biz.PaymentOrderStatusPaid {
		return r.order, false, nil
	}
	if err := issue(r.order, nil); err != nil {
		return nil, false, err
	}
	r.order.Status = biz.PaymentOrderStatusPaid
	r.order.ProviderTradeNo = providerTradeNo
	r.order.AssetIssueStatus = biz.PaymentAssetIssueStatusIssued
	return r.order, true, nil
}

// notifyCountingIssuer counts balance issues so duplicate-callback tests can
// assert the asset is granted exactly once.
type notifyCountingIssuer struct{ issued int }

func (i *notifyCountingIssuer) IssueBalance(ctx context.Context, order *biz.PaymentOrder) error {
	i.issued++
	return nil
}

func (i *notifyCountingIssuer) IssueBalanceInTx(ctx context.Context, tx subscriptionbiz.Tx, order *biz.PaymentOrder) error {
	return i.IssueBalance(ctx, order)
}

func postNotify(svc *BillingService) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/payments/alipay/notify", strings.NewReader("out_trade_no=PAY-1&sign=stub"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	svc.HandleAlipayNotify(rec, req)
	return rec
}

func notifyService(order *biz.PaymentOrder) (*BillingService, *notifyPaymentRepo, *notifyCountingIssuer) {
	repo := &notifyPaymentRepo{order: order}
	issuer := &notifyCountingIssuer{}
	uc := biz.NewPaymentUsecase(repo, nil, issuer)
	svc := NewBillingService(nil, nil, uc, &stubNotifyVerifier{notify: &biz.PaymentNotify{
		TradeNo:         order.TradeNo,
		ProviderTradeNo: "ALI-1",
		Success:         true,
		TotalAmount:     order.MoneyCents,
		AppID:           "app-1",
	}})
	svc.SetExpectedAlipayAppID("app-1")
	return svc, repo, issuer
}

func pendingNotifyOrder() *biz.PaymentOrder {
	return &biz.PaymentOrder{
		TradeNo:          "PAY-1",
		UserID:           "42",
		Channel:          biz.PaymentChannelAlipay,
		AssetType:        biz.PaymentAssetTypeBalance,
		AssetAmount:      1000000,
		MoneyCents:       1000,
		Status:           biz.PaymentOrderStatusPending,
		AssetIssueStatus: biz.PaymentAssetIssueStatusPending,
	}
}

func TestHandleAlipayNotifyVerifyFailureRespondsFail(t *testing.T) {
	svc := NewBillingService(nil, nil, nil, &stubNotifyVerifier{err: errors.New("bad signature")})
	rec := postNotify(svc)
	if rec.Body.String() != "fail" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestHandleAlipayNotifyNonSuccessIsSwallowed(t *testing.T) {
	svc := NewBillingService(nil, nil, nil, &stubNotifyVerifier{notify: &biz.PaymentNotify{
		TradeNo: "PAY-1",
		Success: false, // e.g. TRADE_CLOSED — not a delivery failure
	}})
	rec := postNotify(svc)
	if rec.Body.String() != "success" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestHandleAlipayNotifyMarksPaidAndIssuesOnce(t *testing.T) {
	svc, repo, issuer := notifyService(pendingNotifyOrder())
	rec := postNotify(svc)
	if rec.Body.String() != "success" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if repo.order.Status != biz.PaymentOrderStatusPaid {
		t.Fatalf("status = %q", repo.order.Status)
	}
	if issuer.issued != 1 {
		t.Fatalf("issued = %d", issuer.issued)
	}
}

// F17-3 重复签名回调：支付宝重发同一笔已处理通知时仍回 "success"（不再
// 触发支付宝重发风暴），且余额恰好发放一次。
func TestHandleAlipayNotifyDuplicateSignedCallbackIdempotent(t *testing.T) {
	svc, repo, issuer := notifyService(pendingNotifyOrder())

	first := postNotify(svc)
	if first.Body.String() != "success" {
		t.Fatalf("first body = %q", first.Body.String())
	}
	second := postNotify(svc)
	if second.Body.String() != "success" {
		t.Fatalf("second body = %q", second.Body.String())
	}
	if repo.order.Status != biz.PaymentOrderStatusPaid {
		t.Fatalf("status = %q", repo.order.Status)
	}
	if issuer.issued != 1 {
		t.Fatalf("issued = %d", issuer.issued)
	}
}

// 交叉校验（billing-L5）：签名通过但金额不符的回调必须拒绝（回 "fail"
// 让支付宝重试），订单不得被推进。
func TestHandleAlipayNotifyAmountMismatchRespondsFail(t *testing.T) {
	repo := &notifyPaymentRepo{order: pendingNotifyOrder()}
	issuer := &notifyCountingIssuer{}
	uc := biz.NewPaymentUsecase(repo, nil, issuer)
	svc := NewBillingService(nil, nil, uc, &stubNotifyVerifier{notify: &biz.PaymentNotify{
		TradeNo:     "PAY-1",
		Success:     true,
		TotalAmount: 2000, // 本地订单是 1000 分
		AppID:       "app-1",
	}})
	svc.SetExpectedAlipayAppID("app-1")

	rec := postNotify(svc)
	if rec.Body.String() != "fail" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if repo.order.Status != biz.PaymentOrderStatusPending {
		t.Fatalf("status = %q", repo.order.Status)
	}
	if issuer.issued != 0 {
		t.Fatalf("issued = %d", issuer.issued)
	}
}

func TestHandleAlipayNotifyAppIDMismatchRespondsFail(t *testing.T) {
	repo := &notifyPaymentRepo{order: pendingNotifyOrder()}
	issuer := &notifyCountingIssuer{}
	uc := biz.NewPaymentUsecase(repo, nil, issuer)
	svc := NewBillingService(nil, nil, uc, &stubNotifyVerifier{notify: &biz.PaymentNotify{
		TradeNo:     "PAY-1",
		Success:     true,
		TotalAmount: 1000,
		AppID:       "attacker-app",
	}})
	svc.SetExpectedAlipayAppID("app-1")

	rec := postNotify(svc)
	if rec.Body.String() != "fail" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if repo.order.Status != biz.PaymentOrderStatusPending {
		t.Fatalf("status = %q", repo.order.Status)
	}
	if issuer.issued != 0 {
		t.Fatalf("issued = %d", issuer.issued)
	}
}
