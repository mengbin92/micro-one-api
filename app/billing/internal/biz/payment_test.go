package biz

import (
	subscriptionbiz "micro-one-api/domain/subscription/biz"

	"context"
	"fmt"
	"strings"
	"testing"

)

type memoryPaymentRepo struct {
	order *PaymentOrder
}

func (r *memoryPaymentRepo) CreateOrder(ctx context.Context, order *PaymentOrder) (*PaymentOrder, error) {
	r.order = order
	return order, nil
}

func (r *memoryPaymentRepo) GetOrderByTradeNo(ctx context.Context, tradeNo string) (*PaymentOrder, error) {
	if r.order == nil || r.order.TradeNo != tradeNo {
		return nil, nil
	}
	copy := *r.order
	return &copy, nil
}

func (r *memoryPaymentRepo) ListOrders(ctx context.Context, req ListPaymentOrdersRequest) ([]*PaymentOrder, int64, error) {
	if r.order == nil {
		return nil, 0, nil
	}
	copy := *r.order
	return []*PaymentOrder{&copy}, 1, nil
}

func (r *memoryPaymentRepo) MarkOrderPaid(ctx context.Context, tradeNo, providerTradeNo string, issue func(*PaymentOrder, subscriptionbiz.Tx) error) (*PaymentOrder, bool, error) {
	if r.order == nil || r.order.TradeNo != tradeNo {
		return nil, false, nil
	}
	if r.order.Status == PaymentOrderStatusPaid {
		return r.order, false, nil
	}
	if err := issue(r.order, nil); err != nil {
		return nil, false, err
	}
	r.order.Status = PaymentOrderStatusPaid
	r.order.ProviderTradeNo = providerTradeNo
	r.order.AssetIssueStatus = PaymentAssetIssueStatusIssued
	return r.order, true, nil
}

func (r *memoryPaymentRepo) MarkOrderClosed(ctx context.Context, tradeNo, providerTradeNo string) (*PaymentOrder, bool, error) {
	if r.order == nil || r.order.TradeNo != tradeNo {
		return nil, false, nil
	}
	if r.order.Status == PaymentOrderStatusClosed {
		return r.order, false, nil
	}
	r.order.Status = PaymentOrderStatusClosed
	r.order.ProviderTradeNo = providerTradeNo
	return r.order, true, nil
}

func (r *memoryPaymentRepo) MarkOrderRefunded(ctx context.Context, tradeNo, reason string, revert func(*PaymentOrder, subscriptionbiz.Tx) error) (*PaymentOrder, bool, error) {
	if r.order == nil || r.order.TradeNo != tradeNo {
		return nil, false, nil
	}
	if r.order.Status == PaymentOrderStatusRefunded {
		return r.order, false, nil
	}
	if r.order.Status != PaymentOrderStatusPaid {
		return nil, false, fmt.Errorf("payment order status %q cannot be refunded", r.order.Status)
	}
	if revert != nil {
		if err := revert(r.order, nil); err != nil {
			return nil, false, err
		}
	}
	r.order.Status = PaymentOrderStatusRefunded
	r.order.ProviderPayload = reason
	return r.order, true, nil
}

func (r *memoryPaymentRepo) MarkOrderAssetIssued(ctx context.Context, tradeNo, userID string) (*PaymentOrder, bool, error) {
	if r.order == nil || r.order.TradeNo != tradeNo {
		return nil, false, nil
	}
	copy := *r.order
	if userID != "" && r.order.UserID != userID {
		return &copy, false, nil
	}
	if r.order.Status != PaymentOrderStatusPaid || r.order.AssetIssueStatus != PaymentAssetIssueStatusPending {
		return &copy, false, nil
	}
	r.order.AssetIssueStatus = PaymentAssetIssueStatusIssued
	return r.order, true, nil
}

func (r *memoryPaymentRepo) UnmarkOrderAssetIssued(ctx context.Context, tradeNo string) (*PaymentOrder, bool, error) {
	if r.order == nil || r.order.TradeNo != tradeNo {
		return nil, false, nil
	}
	copy := *r.order
	if r.order.AssetIssueStatus != PaymentAssetIssueStatusIssued {
		return &copy, false, nil
	}
	r.order.AssetIssueStatus = PaymentAssetIssueStatusPending
	return r.order, true, nil
}

type statusPaymentProvider struct {
	status *PaymentProviderStatus
	err    error
}

func (p *statusPaymentProvider) CreateOrder(ctx context.Context, order *PaymentOrder) (*PaymentProviderOrder, error) {
	return &PaymentProviderOrder{}, nil
}

func (p *statusPaymentProvider) QueryOrder(ctx context.Context, order *PaymentOrder) (*PaymentProviderStatus, error) {
	return p.status, p.err
}

type countingPaymentIssuer struct {
	issued int
}

func (i *countingPaymentIssuer) IssueBalance(ctx context.Context, order *PaymentOrder) error {
	i.issued++
	return nil
}

// IssueBalanceInTx mirrors the production fallback: tests use in-memory
// mocks with no shared DB, so the tx argument is unused and the standalone
// IssueBalance path is exercised instead.
func (i *countingPaymentIssuer) IssueBalanceInTx(ctx context.Context, tx subscriptionbiz.Tx, order *PaymentOrder) error {
	return i.IssueBalance(ctx, order)
}

type countingSubscriptionAssigner struct {
	assigned int
	order    *PaymentOrder
	err      error
}

func (a *countingSubscriptionAssigner) AssignSubscriptionAfterPayment(ctx context.Context, order *PaymentOrder) error {
	a.assigned++
	a.order = order
	return a.err
}

// AssignSubscriptionAfterPaymentInTx mirrors the production in-tx path for
// the in-memory test double: tx is nil so the non-tx AssignSubscriptionAfterPayment
// path is exercised.
func (a *countingSubscriptionAssigner) AssignSubscriptionAfterPaymentInTx(ctx context.Context, tx subscriptionbiz.Tx, order *PaymentOrder) error {
	return a.AssignSubscriptionAfterPayment(ctx, order)
}

func TestPaymentUsecaseGetOrderRefreshesPaidAlipayOrder(t *testing.T) {
	repo := &memoryPaymentRepo{order: &PaymentOrder{
		TradeNo:          "PAY-1",
		Channel:          PaymentChannelAlipay,
		AssetType:        PaymentAssetTypeBalance,
		Status:           PaymentOrderStatusPending,
		AssetIssueStatus: PaymentAssetIssueStatusPending,
	}}
	issuer := &countingPaymentIssuer{}
	uc := NewPaymentUsecase(repo, &statusPaymentProvider{status: &PaymentProviderStatus{
		ProviderTradeNo: "ALI-1",
		TradeStatus:     "TRADE_SUCCESS",
		Paid:            true,
	}}, issuer)

	order, err := uc.GetOrderByTradeNo(context.Background(), "PAY-1")
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != PaymentOrderStatusPaid {
		t.Fatalf("status = %q", order.Status)
	}
	if order.ProviderTradeNo != "ALI-1" {
		t.Fatalf("provider_trade_no = %q", order.ProviderTradeNo)
	}
	if issuer.issued != 1 {
		t.Fatalf("issued = %d", issuer.issued)
	}
}

func TestPaymentUsecaseGetOrderRefreshesClosedAlipayOrder(t *testing.T) {
	repo := &memoryPaymentRepo{order: &PaymentOrder{
		TradeNo:          "PAY-1",
		Channel:          PaymentChannelAlipay,
		AssetType:        PaymentAssetTypeBalance,
		Status:           PaymentOrderStatusPending,
		AssetIssueStatus: PaymentAssetIssueStatusPending,
	}}
	issuer := &countingPaymentIssuer{}
	uc := NewPaymentUsecase(repo, &statusPaymentProvider{status: &PaymentProviderStatus{
		ProviderTradeNo: "ALI-1",
		TradeStatus:     "TRADE_CLOSED",
		Closed:          true,
	}}, issuer)

	order, err := uc.GetOrderByTradeNo(context.Background(), "PAY-1")
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != PaymentOrderStatusClosed {
		t.Fatalf("status = %q", order.Status)
	}
	if issuer.issued != 0 {
		t.Fatalf("issued = %d", issuer.issued)
	}
}

func TestPaymentUsecasePaidSubscriptionOrderRequiresAssigner(t *testing.T) {
	repo := &memoryPaymentRepo{order: &PaymentOrder{
		TradeNo:          "PAY-SUB-1",
		Channel:          PaymentChannelAlipay,
		AssetType:        PaymentAssetTypeSubscription,
		AssetAmount:      1,
		GroupID:          9,
		Status:           PaymentOrderStatusPending,
		AssetIssueStatus: PaymentAssetIssueStatusPending,
	}}
	issuer := &countingPaymentIssuer{}
	uc := NewPaymentUsecase(repo, &statusPaymentProvider{status: &PaymentProviderStatus{
		ProviderTradeNo: "ALI-SUB-1",
		Paid:            true,
	}}, issuer)

	order, err := uc.GetOrderByTradeNo(context.Background(), "PAY-SUB-1")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "subscription assigner is not configured") {
		t.Fatalf("err = %v", err)
	}
	if order != nil {
		t.Fatalf("order = %#v", order)
	}
	if repo.order.Status != PaymentOrderStatusPending {
		t.Fatalf("status = %q", repo.order.Status)
	}
	if issuer.issued != 0 {
		t.Fatalf("issued = %d", issuer.issued)
	}
}

func TestPaymentUsecasePaidSubscriptionOrderAssignsSubscription(t *testing.T) {
	repo := &memoryPaymentRepo{order: &PaymentOrder{
		TradeNo:          "PAY-SUB-1",
		Channel:          PaymentChannelAlipay,
		AssetType:        PaymentAssetTypeSubscription,
		AssetAmount:      1,
		GroupID:          9,
		Status:           PaymentOrderStatusPending,
		AssetIssueStatus: PaymentAssetIssueStatusPending,
	}}
	issuer := &countingPaymentIssuer{}
	assigner := &countingSubscriptionAssigner{}
	uc := NewPaymentUsecaseWithAssigner(repo, &statusPaymentProvider{status: &PaymentProviderStatus{
		ProviderTradeNo: "ALI-SUB-1",
		Paid:            true,
	}}, issuer, assigner)

	order, err := uc.GetOrderByTradeNo(context.Background(), "PAY-SUB-1")
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != PaymentOrderStatusPaid {
		t.Fatalf("status = %q", order.Status)
	}
	if order.AssetIssueStatus != PaymentAssetIssueStatusIssued {
		t.Fatalf("asset_issue_status = %q", order.AssetIssueStatus)
	}
	if order.ProviderTradeNo != "ALI-SUB-1" {
		t.Fatalf("provider_trade_no = %q", order.ProviderTradeNo)
	}
	if issuer.issued != 0 {
		t.Fatalf("issued = %d", issuer.issued)
	}
	if assigner.assigned != 1 {
		t.Fatalf("assigned = %d", assigner.assigned)
	}
	if assigner.order == nil || assigner.order.GroupID != 9 {
		t.Fatalf("assigner order = %#v", assigner.order)
	}
}

func TestPaymentUsecaseMarkOrderAssetIssuedClaimsOnce(t *testing.T) {
	repo := &memoryPaymentRepo{order: &PaymentOrder{
		TradeNo:          "PAY-CLAIM-1",
		UserID:           "42",
		Channel:          PaymentChannelAlipay,
		AssetType:        PaymentAssetTypeSubscription,
		Status:           PaymentOrderStatusPaid,
		AssetIssueStatus: PaymentAssetIssueStatusPending,
	}}
	uc := NewPaymentUsecase(repo, &statusPaymentProvider{}, &countingPaymentIssuer{})

	// First completion wins the claim.
	order, claimed, err := uc.MarkOrderAssetIssued(context.Background(), "PAY-CLAIM-1", "42")
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("first claim should win")
	}
	if order == nil || order.AssetIssueStatus != PaymentAssetIssueStatusIssued {
		t.Fatalf("order after claim = %#v", order)
	}

	// A replayed completion observes issued and cannot claim again.
	_, claimed, err = uc.MarkOrderAssetIssued(context.Background(), "PAY-CLAIM-1", "42")
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("second claim must lose (already issued)")
	}

	// Fulfilment-failure compensation releases the claim so it can be retried.
	order, unmarked, err := uc.UnmarkOrderAssetIssued(context.Background(), "PAY-CLAIM-1")
	if err != nil {
		t.Fatal(err)
	}
	if !unmarked || order == nil || order.AssetIssueStatus != PaymentAssetIssueStatusPending {
		t.Fatalf("unmark order = %#v unmarked=%v", order, unmarked)
	}
	_, claimed, err = uc.MarkOrderAssetIssued(context.Background(), "PAY-CLAIM-1", "42")
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("claim after unmark should win again")
	}
}

func TestPaymentUsecaseMarkOrderAssetIssuedRejectsWrongUser(t *testing.T) {
	repo := &memoryPaymentRepo{order: &PaymentOrder{
		TradeNo:          "PAY-CLAIM-2",
		UserID:           "42",
		Status:           PaymentOrderStatusPaid,
		AssetIssueStatus: PaymentAssetIssueStatusPending,
	}}
	uc := NewPaymentUsecase(repo, &statusPaymentProvider{}, &countingPaymentIssuer{})

	order, claimed, err := uc.MarkOrderAssetIssued(context.Background(), "PAY-CLAIM-2", "999")
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("claim by a different user must be refused")
	}
	if order == nil || order.AssetIssueStatus != PaymentAssetIssueStatusPending {
		t.Fatalf("order must stay pending, got %#v", order)
	}
}

func TestPaymentUsecaseMarkOrderAssetIssuedRequiresTradeNo(t *testing.T) {
	uc := NewPaymentUsecase(&memoryPaymentRepo{}, &statusPaymentProvider{}, &countingPaymentIssuer{})
	if _, _, err := uc.MarkOrderAssetIssued(context.Background(), "", "42"); err == nil {
		t.Fatal("empty trade_no must error")
	}
	if _, _, err := uc.UnmarkOrderAssetIssued(context.Background(), ""); err == nil {
		t.Fatal("empty trade_no must error")
	}
}
