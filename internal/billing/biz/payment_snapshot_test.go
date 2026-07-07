package biz

import (
	"context"
	"testing"

	subscriptionbiz "micro-one-api/internal/subscription/biz"

	"gorm.io/gorm"
)

// stubPlanGetterForCreate returns a configured plan so CreateOrder can capture
// a snapshot. It also counts calls to assert CreateOrder reads the plan exactly
// once at creation.
type stubPlanGetterForCreate struct {
	plan  *subscriptionbiz.SubscriptionPlan
	calls int
}

func (s *stubPlanGetterForCreate) GetPlanByID(ctx context.Context, planID int64) (*subscriptionbiz.SubscriptionPlan, error) {
	s.calls++
	return s.plan, nil
}

type capturingPaymentRepo struct {
	created *PaymentOrder
}

func (r *capturingPaymentRepo) CreateOrder(ctx context.Context, order *PaymentOrder) (*PaymentOrder, error) {
	r.created = order
	return order, nil
}
func (r *capturingPaymentRepo) GetOrderByTradeNo(ctx context.Context, tradeNo string) (*PaymentOrder, error) {
	return nil, nil
}
func (r *capturingPaymentRepo) ListOrders(ctx context.Context, req ListPaymentOrdersRequest) ([]*PaymentOrder, int64, error) {
	return nil, 0, nil
}
func (r *capturingPaymentRepo) MarkOrderPaid(ctx context.Context, tradeNo, providerTradeNo string, issue func(*PaymentOrder) error) (*PaymentOrder, bool, error) {
	return nil, false, nil
}
func (r *capturingPaymentRepo) MarkOrderClosed(ctx context.Context, tradeNo, providerTradeNo string) (*PaymentOrder, bool, error) {
	return nil, false, nil
}
func (r *capturingPaymentRepo) MarkOrderRefunded(ctx context.Context, tradeNo, reason string, revert func(*PaymentOrder, *gorm.DB) error) (*PaymentOrder, bool, error) {
	return nil, false, nil
}

func TestCreateOrder_CapturesPlanSnapshotForSubscriptionPlanOrder(t *testing.T) {
	plan := &subscriptionbiz.SubscriptionPlan{
		ID: 7, GroupID: 3, Name: "Pro", ProductName: "codex-pro",
		PriceQuota: 2000, ValidityDays: 30,
	}
	getter := &stubPlanGetterForCreate{plan: plan}
	snap := NewPaymentPlanSnapshotter(getter)
	repo := &capturingPaymentRepo{}
	uc := NewPaymentUsecaseWithSnapshotter(repo, NewMockPaymentProvider(), &countingPaymentIssuer{}, snap)

	order, err := uc.CreateOrder(context.Background(), CreatePaymentOrderRequest{
		UserID:      "42",
		Channel:     PaymentChannelMock,
		AssetType:   PaymentAssetTypeSubscription,
		AssetAmount: 30,
		MoneyCents:  200000,
		Currency:    "CNY",
		GroupID:     3,
		PlanID:      7,
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if order.PlanSnapshot == "" {
		t.Fatal("plan snapshot not captured on subscription+plan order")
	}
	decoded, err := DecodePlanSnapshot(order.PlanSnapshot)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.PlanID != 7 || decoded.GroupID != 3 || decoded.PriceQuota != 2000 || decoded.ValidityDays != 30 {
		t.Fatalf("decoded snapshot = %#v", decoded)
	}
	if decoded.Name != "Pro" || decoded.ProductName != "codex-pro" {
		t.Fatalf("decoded names = %q/%q", decoded.Name, decoded.ProductName)
	}
	if getter.calls != 1 {
		t.Fatalf("expected 1 plan read at creation, got %d", getter.calls)
	}
	// The persisted order must also carry the snapshot.
	if repo.created == nil || repo.created.PlanSnapshot != order.PlanSnapshot {
		t.Fatalf("repo did not persist snapshot")
	}
}

func TestCreateOrder_NoSnapshotForBalanceOrder(t *testing.T) {
	getter := &stubPlanGetterForCreate{plan: &subscriptionbiz.SubscriptionPlan{ID: 7}}
	snap := NewPaymentPlanSnapshotter(getter)
	repo := &capturingPaymentRepo{}
	uc := NewPaymentUsecaseWithSnapshotter(repo, NewMockPaymentProvider(), &countingPaymentIssuer{}, snap)

	order, err := uc.CreateOrder(context.Background(), CreatePaymentOrderRequest{
		UserID:      "42",
		Channel:     PaymentChannelMock,
		AssetType:   PaymentAssetTypeBalance,
		AssetAmount: 1000,
		MoneyCents:  100000,
		PlanID:      7, // balance order with a stray plan_id must not capture a snapshot
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if order.PlanSnapshot != "" {
		t.Fatalf("balance order captured snapshot = %q, want empty", order.PlanSnapshot)
	}
	if getter.calls != 0 {
		t.Fatalf("balance order should not read plan repo, got %d calls", getter.calls)
	}
}

func TestCreateOrder_NoSnapshotWhenSnapshotterAbsent(t *testing.T) {
	repo := &capturingPaymentRepo{}
	uc := NewPaymentUsecase(repo, NewMockPaymentProvider(), &countingPaymentIssuer{})

	order, err := uc.CreateOrder(context.Background(), CreatePaymentOrderRequest{
		UserID:      "42",
		Channel:     PaymentChannelMock,
		AssetType:   PaymentAssetTypeSubscription,
		AssetAmount: 30,
		MoneyCents:  200000,
		GroupID:     3,
		PlanID:      7,
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if order.PlanSnapshot != "" {
		t.Fatalf("order captured snapshot without snapshotter = %q", order.PlanSnapshot)
	}
}
