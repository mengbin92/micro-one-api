package biz

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	subscriptionbiz "micro-one-api/domain/subscription/biz"
)

// racingPaymentRepo models the payment_orders unique trade_no key: the second
// insert of the same deterministic trade no fails, and the row is visible to
// GetOrderByTradeNo immediately (what the real repo guarantees inside its
// transaction).
type racingPaymentRepo struct {
	mu     sync.Mutex
	orders map[string]*PaymentOrder
}

func (r *racingPaymentRepo) CreateOrder(ctx context.Context, order *PaymentOrder) (*PaymentOrder, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.orders == nil {
		r.orders = map[string]*PaymentOrder{}
	}
	if _, exists := r.orders[order.TradeNo]; exists {
		return nil, fmt.Errorf("duplicate key 'uk_payment_orders_trade_no'")
	}
	stored := *order
	r.orders[order.TradeNo] = &stored
	c := stored
	return &c, nil
}

func (r *racingPaymentRepo) GetOrderByTradeNo(ctx context.Context, tradeNo string) (*PaymentOrder, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if order, ok := r.orders[tradeNo]; ok {
		c := *order
		return &c, nil
	}
	return nil, nil
}

func (r *racingPaymentRepo) AttachProviderResult(ctx context.Context, order *PaymentOrder) (*PaymentOrder, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored, ok := r.orders[order.TradeNo]
	if !ok {
		return nil, fmt.Errorf("order %s not found", order.TradeNo)
	}
	stored.PayURL = order.PayURL
	stored.ProviderPayload = order.ProviderPayload
	stored.ProviderTradeNo = order.ProviderTradeNo
	c := *stored
	return &c, nil
}

func (r *racingPaymentRepo) DeletePendingOrder(ctx context.Context, tradeNo string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.orders, tradeNo)
	return nil
}

func (r *racingPaymentRepo) ListOrders(ctx context.Context, req ListPaymentOrdersRequest) ([]*PaymentOrder, int64, error) {
	return nil, 0, nil
}
func (r *racingPaymentRepo) MarkOrderPaid(ctx context.Context, tradeNo, providerTradeNo string, issue func(*PaymentOrder, subscriptionbiz.Tx) error) (*PaymentOrder, bool, error) {
	return nil, false, nil
}
func (r *racingPaymentRepo) MarkOrderClosed(ctx context.Context, tradeNo, providerTradeNo string) (*PaymentOrder, bool, error) {
	return nil, false, nil
}
func (r *racingPaymentRepo) MarkOrderRefunded(ctx context.Context, tradeNo, reason string, revert func(*PaymentOrder, subscriptionbiz.Tx) error) (*PaymentOrder, bool, error) {
	return nil, false, nil
}
func (r *racingPaymentRepo) MarkOrderAssetIssued(ctx context.Context, tradeNo, userID string) (*PaymentOrder, bool, error) {
	return nil, false, nil
}
func (r *racingPaymentRepo) UnmarkOrderAssetIssued(ctx context.Context, tradeNo string) (*PaymentOrder, bool, error) {
	return nil, false, nil
}

// slowCountingProvider sleeps on every call so a concurrent duplicate is
// guaranteed to attempt its insert while the first call is still inside the
// provider, then reports how often the provider was invoked.
type slowCountingProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *slowCountingProvider) CreateOrder(ctx context.Context, order *PaymentOrder) (*PaymentProviderOrder, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	return &PaymentProviderOrder{PayURL: "pay://mock", Payload: "{}", ProviderTradeNo: "PTN-1"}, nil
}

func (p *slowCountingProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type stubPurchaseValidator struct{}

func (stubPurchaseValidator) ValidateRenewalContract(context.Context, int64, int64, *subscriptionbiz.SubscriptionContract) error {
	return nil
}

// lockedPlanGetter is a goroutine-safe variant of stubPlanGetterForCreate for
// the concurrent CreateOrder test.
type lockedPlanGetter struct {
	mu   sync.Mutex
	plan *subscriptionbiz.SubscriptionPlan
}

func (g *lockedPlanGetter) GetPlanByID(context.Context, int64) (*subscriptionbiz.SubscriptionPlan, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.plan, nil
}

// TestCreateOrder_KeyedConcurrentDuplicateCallsProviderOnce proves the
// insert-first ordering: two concurrent CreateOrder calls with the same
// (user, request) deterministically share one trade_no, so the unique key
// rejects the second insert BEFORE the provider is invoked — exactly one
// provider order exists, and both callers receive it.
func TestCreateOrder_KeyedConcurrentDuplicateCallsProviderOnce(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "true")
	repo := &racingPaymentRepo{}
	provider := &slowCountingProvider{}
	snap := NewPaymentPlanSnapshotter(&lockedPlanGetter{plan: &subscriptionbiz.SubscriptionPlan{
		ID: 7, GroupID: 3, Name: "Pro", ProductName: "codex-pro", PriceQuota: 2000, ValidityDays: 30,
		ForSale: true,
		Group:   &subscriptionbiz.SubscriptionGroup{ID: 3, Status: subscriptionbiz.SubscriptionGroupStatusEnabled},
	}})
	uc := NewPaymentUsecaseWithSnapshotter(repo, provider, &countingPaymentIssuer{}, snap)
	uc.SetSubscriptionPurchaseValidator(stubPurchaseValidator{})

	req := CreatePaymentOrderRequest{
		UserID:      "42",
		Channel:     PaymentChannelMock,
		AssetType:   PaymentAssetTypeSubscription,
		AssetAmount: 30,
		MoneyCents:  200000,
		Currency:    "CNY",
		GroupID:     3,
		PlanID:      7,
		RequestID:   "req-dedupe-1",
	}
	results := make([]*PaymentOrder, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = uc.CreateOrder(context.Background(), req)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d CreateOrder: %v", i, err)
		}
	}
	if results[0].TradeNo == "" || results[0].TradeNo != results[1].TradeNo {
		t.Fatalf("trade nos differ: %q vs %q", results[0].TradeNo, results[1].TradeNo)
	}
	if got := provider.callCount(); got != 1 {
		t.Fatalf("provider called %d times, want exactly 1 (orphan provider orders otherwise)", got)
	}
}
