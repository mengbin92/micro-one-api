package biz

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

const (
	PaymentOrderStatusPending = "pending"
	PaymentOrderStatusPaid    = "paid"
	PaymentOrderStatusClosed  = "closed"

	PaymentAssetIssueStatusPending = "pending"
	PaymentAssetIssueStatusIssued  = "issued"

	PaymentAssetTypeQuota = "quota"

	PaymentChannelMock   = "mock"
	PaymentChannelAlipay = "alipay"
)

type PaymentOrder struct {
	ID               int64
	UserID           string
	TradeNo          string
	Channel          string
	AssetType        string
	AssetAmount      int64
	MoneyCents       int64
	Currency         string
	Status           string
	ProviderTradeNo  string
	ProviderPayload  string
	PayURL           string
	AssetIssueStatus string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	PaidAt           *time.Time
}

type CreatePaymentOrderRequest struct {
	UserID      string
	Channel     string
	AssetType   string
	AssetAmount int64
	MoneyCents  int64
	Currency    string
}

type PaymentProviderOrder struct {
	PayURL          string
	Payload         string
	ProviderTradeNo string
}

type PaymentNotify struct {
	TradeNo         string
	ProviderTradeNo string
	Success         bool
	Channel         string
	Raw             map[string]string
}

type PaymentRepo interface {
	CreateOrder(ctx context.Context, order *PaymentOrder) (*PaymentOrder, error)
	GetOrderByTradeNo(ctx context.Context, tradeNo string) (*PaymentOrder, error)
	MarkOrderPaid(ctx context.Context, tradeNo, providerTradeNo string, issue func(*PaymentOrder) error) (*PaymentOrder, bool, error)
}

type PaymentProvider interface {
	CreateOrder(ctx context.Context, order *PaymentOrder) (*PaymentProviderOrder, error)
}

type PaymentNotifyVerifier interface {
	VerifyNotify(ctx context.Context, params map[string]string) (*PaymentNotify, error)
}

type PaymentUsecase struct {
	repo     PaymentRepo
	provider PaymentProvider
	issuer   PaymentAssetIssuer
}

type PaymentAssetIssuer interface {
	IssueQuota(ctx context.Context, order *PaymentOrder) error
}

func NewPaymentUsecase(repo PaymentRepo, provider PaymentProvider, issuer PaymentAssetIssuer) *PaymentUsecase {
	return &PaymentUsecase{repo: repo, provider: provider, issuer: issuer}
}

func (uc *PaymentUsecase) CreateOrder(ctx context.Context, req CreatePaymentOrderRequest) (*PaymentOrder, error) {
	if err := validateCreatePaymentOrderRequest(req); err != nil {
		return nil, err
	}
	currency := req.Currency
	if currency == "" {
		currency = "CNY"
	}
	order := &PaymentOrder{
		UserID:           req.UserID,
		TradeNo:          generatePaymentTradeNo(req.UserID),
		Channel:          req.Channel,
		AssetType:        req.AssetType,
		AssetAmount:      req.AssetAmount,
		MoneyCents:       req.MoneyCents,
		Currency:         currency,
		Status:           PaymentOrderStatusPending,
		AssetIssueStatus: PaymentAssetIssueStatusPending,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	providerOrder, err := uc.provider.CreateOrder(ctx, order)
	if err != nil {
		return nil, err
	}
	if providerOrder != nil {
		order.PayURL = providerOrder.PayURL
		order.ProviderPayload = providerOrder.Payload
		order.ProviderTradeNo = providerOrder.ProviderTradeNo
	}
	return uc.repo.CreateOrder(ctx, order)
}

func (uc *PaymentUsecase) GetOrderByTradeNo(ctx context.Context, tradeNo string) (*PaymentOrder, error) {
	if tradeNo == "" {
		return nil, errors.New("trade_no is required")
	}
	return uc.repo.GetOrderByTradeNo(ctx, tradeNo)
}

func (uc *PaymentUsecase) MarkOrderPaid(ctx context.Context, tradeNo, providerTradeNo string) (*PaymentOrder, error) {
	if tradeNo == "" {
		return nil, errors.New("trade_no is required")
	}
	order, _, err := uc.repo.MarkOrderPaid(ctx, tradeNo, providerTradeNo, func(order *PaymentOrder) error {
		return uc.issuer.IssueQuota(ctx, order)
	})
	if err != nil {
		return nil, err
	}
	if order == nil {
		return nil, errors.New("payment order not found")
	}
	return order, nil
}

func validateCreatePaymentOrderRequest(req CreatePaymentOrderRequest) error {
	if req.UserID == "" {
		return errors.New("user_id is required")
	}
	if req.AssetType != PaymentAssetTypeQuota {
		return fmt.Errorf("unsupported payment asset type %q", req.AssetType)
	}
	if req.AssetAmount <= 0 {
		return errors.New("asset_amount must be positive")
	}
	if req.MoneyCents <= 0 {
		return errors.New("money_cents must be positive")
	}
	switch req.Channel {
	case PaymentChannelMock, PaymentChannelAlipay:
		return nil
	default:
		return fmt.Errorf("unsupported payment channel %q", req.Channel)
	}
}

func generatePaymentTradeNo(userID string) string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("PAY%s%d", userID, time.Now().UnixNano())
	}
	return fmt.Sprintf("PAY%s%s%d", userID, hex.EncodeToString(b[:]), time.Now().Unix())
}

type quotaPaymentAssetIssuer struct {
	billing *BillingUsecase
}

func NewPaymentAssetIssuer(billing *BillingUsecase) PaymentAssetIssuer {
	return &quotaPaymentAssetIssuer{billing: billing}
}

func (i *quotaPaymentAssetIssuer) IssueQuota(ctx context.Context, order *PaymentOrder) error {
	if i == nil || i.billing == nil {
		return errors.New("payment asset issuer is not configured")
	}
	_, err := i.billing.TopUpQuota(ctx, order.UserID, "payment", order.AssetAmount, "payment:"+order.TradeNo)
	return err
}

type mockPaymentProvider struct{}

func NewMockPaymentProvider() PaymentProvider {
	return &mockPaymentProvider{}
}

func (p *mockPaymentProvider) CreateOrder(ctx context.Context, order *PaymentOrder) (*PaymentProviderOrder, error) {
	return &PaymentProviderOrder{
		PayURL:  fmt.Sprintf("mock://payment/%s", order.TradeNo),
		Payload: fmt.Sprintf(`{"trade_no":"%s","channel":"%s"}`, order.TradeNo, order.Channel),
	}, nil
}

type routedPaymentProvider struct {
	providers map[string]PaymentProvider
}

func NewConfiguredPaymentProvider(cfg PaymentConfig) PaymentProvider {
	routed := &routedPaymentProvider{providers: map[string]PaymentProvider{
		PaymentChannelMock: NewMockPaymentProvider(),
	}}
	if cfg.Alipay.Enabled {
		routed.providers[PaymentChannelAlipay] = NewAlipayPaymentProvider(cfg.Alipay)
	}
	return routed
}

func (p *routedPaymentProvider) CreateOrder(ctx context.Context, order *PaymentOrder) (*PaymentProviderOrder, error) {
	if p == nil || len(p.providers) == 0 {
		return nil, errors.New("payment provider is not configured")
	}
	provider, ok := p.providers[order.Channel]
	if !ok {
		return nil, fmt.Errorf("payment channel %q is not configured", order.Channel)
	}
	return provider.CreateOrder(ctx, order)
}
