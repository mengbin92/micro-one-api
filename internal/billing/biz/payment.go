package biz

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

const (
	PaymentOrderStatusPending = "pending"
	PaymentOrderStatusPaid    = "paid"
	PaymentOrderStatusClosed  = "closed"

	PaymentAssetIssueStatusPending = "pending"
	PaymentAssetIssueStatusIssued  = "issued"

	PaymentAssetTypeBalance      = "balance"
	PaymentAssetTypeSubscription = "subscription"

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
	GroupID          int64 // Subscription group ID for auto-assignment after payment
	PlanID           int64 // Subscription plan ID for plan-based auto-assignment
	// PlanSnapshot holds the immutable purchase-time view of the plan. It is
	// populated at order creation so fulfillment does not depend on the live
	// plan row (which may be taken off-shelf or edited while the order is
	// pending). See plan_snapshot.go. Empty for non-plan orders.
	PlanSnapshot string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	PaidAt       *time.Time
}

type CreatePaymentOrderRequest struct {
	UserID      string
	Channel     string
	AssetType   string
	AssetAmount int64
	MoneyCents  int64
	Currency    string
	GroupID     int64 // Optional: subscription group ID for auto-assignment
	PlanID      int64 // Optional: subscription plan ID for auto-assignment
}

type ListPaymentOrdersRequest struct {
	Page      int32
	PageSize  int32
	UserID    string
	Status    string
	Channel   string
	TradeNo   string
	StartTime int64
	EndTime   int64
}

type PaymentProviderOrder struct {
	PayURL          string
	Payload         string
	ProviderTradeNo string
}

type PaymentProviderStatus struct {
	TradeNo         string
	ProviderTradeNo string
	TradeStatus     string
	Paid            bool
	Closed          bool
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
	ListOrders(ctx context.Context, req ListPaymentOrdersRequest) ([]*PaymentOrder, int64, error)
	MarkOrderPaid(ctx context.Context, tradeNo, providerTradeNo string, issue func(*PaymentOrder) error) (*PaymentOrder, bool, error)
	MarkOrderClosed(ctx context.Context, tradeNo, providerTradeNo string) (*PaymentOrder, bool, error)
	// MarkOrderRefunded transitions a paid order to refunded, running the
	// revert callback inside the same transaction. Idempotent.
	MarkOrderRefunded(ctx context.Context, tradeNo, reason string, revert func(*PaymentOrder, *gorm.DB) error) (*PaymentOrder, bool, error)
}

type PaymentProvider interface {
	CreateOrder(ctx context.Context, order *PaymentOrder) (*PaymentProviderOrder, error)
}

type PaymentStatusQuerier interface {
	QueryOrder(ctx context.Context, order *PaymentOrder) (*PaymentProviderStatus, error)
}

type PaymentNotifyVerifier interface {
	VerifyNotify(ctx context.Context, params map[string]string) (*PaymentNotify, error)
}

type PaymentUsecase struct {
	repo        PaymentRepo
	provider    PaymentProvider
	issuer      PaymentAssetIssuer
	assigner    SubscriptionAssigner // Optional: assigns subscription after payment
	snapshotter PlanSnapshotter      // Optional: captures plan snapshot at order creation
}

type PaymentAssetIssuer interface {
	IssueBalance(ctx context.Context, order *PaymentOrder) error
}

// SubscriptionAssigner is an optional interface that payment issuers can
// implement to automatically assign subscriptions after payment.
type SubscriptionAssigner interface {
	AssignSubscriptionAfterPayment(ctx context.Context, order *PaymentOrder) error
}

func NewPaymentUsecase(repo PaymentRepo, provider PaymentProvider, issuer PaymentAssetIssuer) *PaymentUsecase {
	return &PaymentUsecase{repo: repo, provider: provider, issuer: issuer}
}

// NewPaymentUsecaseWithSnapshotter wires a plan snapshotter so subscription
// orders capture an immutable plan view at creation time. The snapshot makes
// fulfillment independent of later on/off-shelf changes to the plan row.
func NewPaymentUsecaseWithSnapshotter(repo PaymentRepo, provider PaymentProvider, issuer PaymentAssetIssuer, snapshotter PlanSnapshotter) *PaymentUsecase {
	return &PaymentUsecase{repo: repo, provider: provider, issuer: issuer, snapshotter: snapshotter}
}

// NewPaymentUsecaseWithAssigner creates a PaymentUsecase with subscription assignment support.
func NewPaymentUsecaseWithAssigner(repo PaymentRepo, provider PaymentProvider, issuer PaymentAssetIssuer, assigner SubscriptionAssigner) *PaymentUsecase {
	return &PaymentUsecase{repo: repo, provider: provider, issuer: issuer, assigner: assigner}
}

// NewPaymentUsecaseWithAssignerAndSnapshotter wires both the subscription
// assigner and the plan snapshotter. This is the production constructor used
// by billing-service so plan-backed subscription orders are off-shelf-safe.
func NewPaymentUsecaseWithAssignerAndSnapshotter(repo PaymentRepo, provider PaymentProvider, issuer PaymentAssetIssuer, assigner SubscriptionAssigner, snapshotter PlanSnapshotter) *PaymentUsecase {
	return &PaymentUsecase{repo: repo, provider: provider, issuer: issuer, assigner: assigner, snapshotter: snapshotter}
}

// SetPlanSnapshotter installs a plan snapshotter on an already-constructed
// usecase. Used by wiring paths that build the usecase before the snapshotter.
func (uc *PaymentUsecase) SetPlanSnapshotter(snapshotter PlanSnapshotter) {
	if uc == nil {
		return
	}
	uc.snapshotter = snapshotter
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
		GroupID:          req.GroupID,
		PlanID:           req.PlanID,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	// Phase 2: capture the plan snapshot at creation time so fulfillment is
	// decoupled from later on/off-shelf changes. Only subscription orders with
	// a plan_id carry a snapshot; balance orders and group-only orders leave
	// the column NULL.
	if order.AssetType == PaymentAssetTypeSubscription && order.PlanID > 0 && uc.snapshotter != nil {
		snapshot, snapErr := uc.snapshotter.CapturePlanSnapshot(ctx, order.PlanID)
		if snapErr != nil {
			return nil, fmt.Errorf("capture plan snapshot: %w", snapErr)
		}
		ApplyPlanSnapshotToOrder(order, snapshot)
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
	order, err := uc.repo.GetOrderByTradeNo(ctx, tradeNo)
	if err != nil || order == nil {
		return order, err
	}
	return uc.refreshProviderStatus(ctx, order)
}

func (uc *PaymentUsecase) ListOrders(ctx context.Context, req ListPaymentOrdersRequest) ([]*PaymentOrder, int64, error) {
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 {
		req.PageSize = 20
	}
	if req.PageSize > 100 {
		req.PageSize = 100
	}
	return uc.repo.ListOrders(ctx, req)
}

func (uc *PaymentUsecase) MarkOrderPaid(ctx context.Context, tradeNo, providerTradeNo string) (*PaymentOrder, error) {
	if tradeNo == "" {
		return nil, errors.New("trade_no is required")
	}
	order, _, err := uc.repo.MarkOrderPaid(ctx, tradeNo, providerTradeNo, func(order *PaymentOrder) error {
		if order.AssetType == PaymentAssetTypeBalance {
			if err := uc.issuer.IssueBalance(ctx, order); err != nil {
				return err
			}
		}
		if order.AssetType == PaymentAssetTypeSubscription {
			if order.PlanID <= 0 && order.GroupID <= 0 {
				return errors.New("subscription group_id is required")
			}
			if uc.assigner == nil {
				return errors.New("subscription assigner is not configured")
			}
			if err := uc.assigner.AssignSubscriptionAfterPayment(ctx, order); err != nil {
				return fmt.Errorf("assign subscription after payment: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if order == nil {
		return nil, errors.New("payment order not found")
	}
	return order, nil
}

func (uc *PaymentUsecase) refreshProviderStatus(ctx context.Context, order *PaymentOrder) (*PaymentOrder, error) {
	if order.Status != PaymentOrderStatusPending || order.Channel != PaymentChannelAlipay {
		return order, nil
	}
	querier, ok := uc.provider.(PaymentStatusQuerier)
	if !ok {
		return order, nil
	}
	status, err := querier.QueryOrder(ctx, order)
	if err != nil || status == nil {
		return order, nil
	}
	providerTradeNo := firstNonEmptyString(status.ProviderTradeNo, order.ProviderTradeNo)
	if status.Paid {
		paid, err := uc.MarkOrderPaid(ctx, order.TradeNo, providerTradeNo)
		if err != nil {
			return nil, err
		}
		return paid, nil
	}
	if status.Closed {
		closed, _, err := uc.repo.MarkOrderClosed(ctx, order.TradeNo, providerTradeNo)
		if err != nil {
			return nil, err
		}
		if closed != nil {
			return closed, nil
		}
	}
	return order, nil
}

func validateCreatePaymentOrderRequest(req CreatePaymentOrderRequest) error {
	if req.UserID == "" {
		return errors.New("user_id is required")
	}
	if req.AssetType != PaymentAssetTypeBalance && req.AssetType != PaymentAssetTypeSubscription {
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

type balancePaymentAssetIssuer struct {
	billing *BillingUsecase
}

func NewPaymentAssetIssuer(billing *BillingUsecase) PaymentAssetIssuer {
	return &balancePaymentAssetIssuer{billing: billing}
}

func (i *balancePaymentAssetIssuer) IssueBalance(ctx context.Context, order *PaymentOrder) error {
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

func (p *routedPaymentProvider) QueryOrder(ctx context.Context, order *PaymentOrder) (*PaymentProviderStatus, error) {
	if p == nil || order == nil {
		return nil, nil
	}
	provider, ok := p.providers[order.Channel]
	if !ok {
		return nil, nil
	}
	querier, ok := provider.(PaymentStatusQuerier)
	if !ok {
		return nil, nil
	}
	return querier.QueryOrder(ctx, order)
}
