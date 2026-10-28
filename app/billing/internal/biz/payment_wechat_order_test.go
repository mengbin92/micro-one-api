package biz

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type uncertainPaymentProvider struct{ calls int }

func (p *uncertainPaymentProvider) CreateOrder(context.Context, *PaymentOrder) (*PaymentProviderOrder, error) {
	p.calls++
	return nil, errors.New("deadline exceeded after request sent")
}

func TestWechatUncertainOrderRetainedAndIdempotent(t *testing.T) {
	repo := &racingPaymentRepo{}
	provider := &uncertainPaymentProvider{}
	uc := NewPaymentUsecase(repo, provider, nil)
	req := CreatePaymentOrderRequest{UserID: "123456789012345678901234567890", RequestID: "recharge-1", Channel: PaymentChannelWechat, AssetType: PaymentAssetTypeBalance, MoneyCents: 100, AssetAmount: 5000, Currency: "CNY"}
	order, err := uc.CreateOrder(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, PaymentOrderStatusPending, order.Status)
	require.LessOrEqual(t, len(order.TradeNo), 32)
	require.Empty(t, order.PayURL)
	retry, err := uc.CreateOrder(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, order.TradeNo, retry.TradeNo)
	require.Equal(t, 1, provider.calls)
	req.MoneyCents++
	_, err = uc.CreateOrder(context.Background(), req)
	require.Error(t, err)
	require.Equal(t, 1, provider.calls)
}

func TestWechatRejectsUnsupportedOrders(t *testing.T) {
	valid := CreatePaymentOrderRequest{UserID: "1", RequestID: "x", Channel: PaymentChannelWechat, AssetType: PaymentAssetTypeBalance, AssetAmount: 1, MoneyCents: 1, Currency: "CNY"}
	for _, change := range []func(*CreatePaymentOrderRequest){func(r *CreatePaymentOrderRequest) { r.Currency = "USD" }, func(r *CreatePaymentOrderRequest) { r.AssetType = PaymentAssetTypeSubscription }, func(r *CreatePaymentOrderRequest) { r.RequestID = "" }} {
		req := valid
		change(&req)
		require.Error(t, validateCreatePaymentOrderRequest(req))
	}
	require.NoError(t, validateCreatePaymentOrderRequest(valid))
}

func TestPaymentChannelsHideMockAndCredentials(t *testing.T) {
	uc := NewPaymentUsecase(nil, NewConfiguredPaymentProvider(PaymentConfig{}, nil), nil)
	require.Empty(t, uc.ListPaymentChannels())
	uc.provider = NewConfiguredPaymentProvider(PaymentConfig{Alipay: AlipayConfig{Enabled: true}}, nil)
	channels := uc.ListPaymentChannels()
	require.Len(t, channels, 1)
	require.Equal(t, PaymentChannelAlipay, channels[0].Channel)
	require.Equal(t, []string{PaymentAssetTypeBalance, PaymentAssetTypeSubscription}, channels[0].AssetTypes)
}

func (p *uncertainPaymentProvider) QueryOrder(context.Context, *PaymentOrder) (*PaymentProviderStatus, error) {
	return &PaymentProviderStatus{TradeNo: "kept-for-draining"}, nil
}
func (p *uncertainPaymentProvider) VerifyWebhook(context.Context, map[string]string, []byte) (*PaymentNotify, error) {
	return nil, nil
}

func TestWechatDisabledDrainsExistingWithoutAcceptingNewOrders(t *testing.T) {
	repo := &racingPaymentRepo{}
	provider := &uncertainPaymentProvider{}
	routed := NewConfiguredPaymentProvider(PaymentConfig{Wechat: WechatConfig{Enabled: false}}, provider)
	uc := NewPaymentUsecase(repo, routed, nil)
	require.Empty(t, uc.ListPaymentChannels())
	_, err := uc.CreateOrder(context.Background(), CreatePaymentOrderRequest{UserID: "1", RequestID: "x", Channel: PaymentChannelWechat, AssetType: PaymentAssetTypeBalance, AssetAmount: 1, MoneyCents: 1})
	require.Error(t, err)
	require.Empty(t, repo.orders)
	status, err := routed.(PaymentStatusQuerier).QueryOrder(context.Background(), &PaymentOrder{Channel: PaymentChannelWechat})
	require.NoError(t, err)
	require.Equal(t, "kept-for-draining", status.TradeNo)
	require.Zero(t, provider.calls)
}

type recoveringWechatProvider struct {
	calls    int
	status   string
	queryErr error
}

func (p *recoveringWechatProvider) CreateOrder(context.Context, *PaymentOrder) (*PaymentProviderOrder, error) {
	p.calls++
	if p.calls == 1 {
		return nil, errors.New("response lost")
	}
	return &PaymentProviderOrder{PayURL: "weixin://wxpay/bizpayurl/up?pr=recovered"}, nil
}
func (p *recoveringWechatProvider) QueryOrder(context.Context, *PaymentOrder) (*PaymentProviderStatus, error) {
	return &PaymentProviderStatus{TradeStatus: p.status}, p.queryErr
}

func TestWechatUncertainOrderRecoversQRCodeOnSameTrade(t *testing.T) {
	for _, state := range []string{"NOTPAY", "ORDER_NOT_EXIST", "USERPAYING", "unknown"} {
		t.Run(state, func(t *testing.T) {
			repo := &racingPaymentRepo{}
			provider := &recoveringWechatProvider{status: state}
			uc := NewPaymentUsecase(repo, provider, nil)
			req := CreatePaymentOrderRequest{UserID: "1", RequestID: "recharge", Channel: PaymentChannelWechat, AssetType: PaymentAssetTypeBalance, AssetAmount: 1, MoneyCents: 1, Currency: "CNY"}
			original, err := uc.CreateOrder(context.Background(), req)
			require.NoError(t, err)
			require.Empty(t, original.PayURL)
			recovered, err := uc.CreateOrder(context.Background(), req)
			require.NoError(t, err)
			require.Equal(t, original.TradeNo, recovered.TradeNo)
			require.Len(t, repo.orders, 1)
			if state == "NOTPAY" || state == "ORDER_NOT_EXIST" {
				require.NotEmpty(t, recovered.PayURL)
				require.Equal(t, 2, provider.calls)
				again, err := uc.CreateOrder(context.Background(), req)
				require.NoError(t, err)
				require.Equal(t, recovered.PayURL, again.PayURL)
				require.Equal(t, 2, provider.calls)
			} else {
				require.Empty(t, recovered.PayURL)
				require.Equal(t, 1, provider.calls)
			}
		})
	}
}

func (r *racingPaymentRepo) ClaimProviderOrder(ctx context.Context, tradeNo string) (*PaymentOrder, error) {
	order, err := r.GetOrderByTradeNo(ctx, tradeNo)
	if order != nil {
		order.ProviderClaim = "test-claim"
	}
	return order, err
}
