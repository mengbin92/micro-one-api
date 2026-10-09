package biz

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPaymentQueryRejectsDifferentOrder(t *testing.T) {
	repo := &memoryPaymentRepo{order: &PaymentOrder{TradeNo: "local", MoneyCents: 100, Channel: PaymentChannelAlipay, Status: PaymentOrderStatusPending, AssetType: PaymentAssetTypeBalance}}
	issuer := &countingPaymentIssuer{}
	uc := NewPaymentUsecase(repo, &statusPaymentProvider{status: &PaymentProviderStatus{TradeNo: "other", Paid: true}}, issuer)
	_, err := uc.GetOrderByTradeNo(context.Background(), "local")
	require.Error(t, err)
	require.Zero(t, issuer.issued)
}

func TestPaymentQueryRejectsDifferentAmount(t *testing.T) {
	repo := &memoryPaymentRepo{order: &PaymentOrder{TradeNo: "local", MoneyCents: 100, Channel: PaymentChannelAlipay, Status: PaymentOrderStatusPending, AssetType: PaymentAssetTypeBalance}}
	issuer := &countingPaymentIssuer{}
	uc := NewPaymentUsecase(repo, &statusPaymentProvider{status: &PaymentProviderStatus{TradeNo: "local", TotalAmount: 1, Paid: true}}, issuer)
	_, err := uc.GetOrderByTradeNo(context.Background(), "local")
	require.Error(t, err)
	require.Zero(t, issuer.issued)
}

type pagedReviewPaymentRepo struct {
	*memoryPaymentRepo
	orders []*PaymentOrder
	after  []int64
}

func (r *pagedReviewPaymentRepo) ListOrders(ctx context.Context, req ListPaymentOrdersRequest) ([]*PaymentOrder, int64, error) {
	r.after = append(r.after, req.AfterID)
	var rows []*PaymentOrder
	for _, order := range r.orders {
		if order.ID > req.AfterID {
			rows = append(rows, order)
			if len(rows) == int(req.PageSize) {
				break
			}
		}
	}
	return rows, int64(len(r.orders)), nil
}
func TestReconcilePendingOrdersAdvancesPastFirstPage(t *testing.T) {
	r := &pagedReviewPaymentRepo{memoryPaymentRepo: &memoryPaymentRepo{}}
	for i := int64(1); i <= 3; i++ {
		o := pendingAlipayBalanceOrder(fmt.Sprint(i))
		o.ID = i
		r.orders = append(r.orders, o)
	}
	uc := NewPaymentUsecase(r, &statusPaymentProvider{status: &PaymentProviderStatus{}}, nil)
	for i := 0; i < 3; i++ {
		_, err := uc.ReconcilePendingOrders(context.Background(), 2)
		require.NoError(t, err)
	}
	require.Equal(t, []int64{0, 2, 0}, r.after)
}
