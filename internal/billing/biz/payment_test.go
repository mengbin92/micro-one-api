package biz

import (
	"context"
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

func (r *memoryPaymentRepo) MarkOrderPaid(ctx context.Context, tradeNo, providerTradeNo string, issue func(*PaymentOrder) error) (*PaymentOrder, bool, error) {
	if r.order == nil || r.order.TradeNo != tradeNo {
		return nil, false, nil
	}
	if r.order.Status == PaymentOrderStatusPaid {
		return r.order, false, nil
	}
	if err := issue(r.order); err != nil {
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

func TestPaymentUsecasePaidSubscriptionOrderDoesNotIssueBalance(t *testing.T) {
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
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != PaymentOrderStatusPaid {
		t.Fatalf("status = %q", order.Status)
	}
	if issuer.issued != 0 {
		t.Fatalf("issued = %d", issuer.issued)
	}
}
