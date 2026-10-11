package biz

import (
	"context"
	"testing"

	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"micro-one-api/pkg/jsonx"
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
func (r *capturingPaymentRepo) AttachProviderResult(ctx context.Context, order *PaymentOrder) (*PaymentOrder, error) {
	return order, nil
}
func (r *capturingPaymentRepo) DeletePendingOrder(ctx context.Context, tradeNo string) error {
	return nil
}
func (r *capturingPaymentRepo) ListOrders(ctx context.Context, req ListPaymentOrdersRequest) ([]*PaymentOrder, int64, error) {
	return nil, 0, nil
}
func (r *capturingPaymentRepo) MarkOrderPaid(ctx context.Context, tradeNo, providerTradeNo string, issue func(*PaymentOrder, subscriptionbiz.Tx) error) (*PaymentOrder, bool, error) {
	return nil, false, nil
}
func (r *capturingPaymentRepo) MarkOrderClosed(ctx context.Context, tradeNo, providerTradeNo string) (*PaymentOrder, bool, error) {
	return nil, false, nil
}
func (r *capturingPaymentRepo) MarkOrderRefunded(ctx context.Context, tradeNo, reason string, revert func(context.Context, *PaymentOrder, subscriptionbiz.Tx) error) (*PaymentOrder, bool, error) {
	return nil, false, nil
}
func (r *capturingPaymentRepo) MarkOrderAssetIssued(ctx context.Context, tradeNo, userID string) (*PaymentOrder, bool, error) {
	return nil, false, nil
}
func (r *capturingPaymentRepo) UnmarkOrderAssetIssued(ctx context.Context, tradeNo string) (*PaymentOrder, bool, error) {
	return nil, false, nil
}

func TestCreateOrder_CapturesPlanSnapshotForSubscriptionPlanOrder(t *testing.T) {
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "10")
	plan := &subscriptionbiz.SubscriptionPlan{
		ID: 7, GroupID: 3, Name: "Pro", ProductName: "codex-pro",
		PriceQuota: 2000, ValidityDays: 30,
		ForSale: true, Group: &subscriptionbiz.SubscriptionGroup{ID: 3, Status: 1},
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
	var raw struct {
		PriceConversion *subscriptionbiz.PriceConversion `json:"price_conversion"`
	}
	if err := jsonx.Unmarshal([]byte(order.PlanSnapshot), &raw); err != nil {
		t.Fatalf("decode price conversion: %v", err)
	}
	if raw.PriceConversion == nil || raw.PriceConversion.Version != 1 || raw.PriceConversion.RechargeMultiplier != 10 || raw.PriceConversion.WalletAmount != 200_000_000 {
		t.Fatalf("new order did not freeze CNY 2000 -> USD 20000: %+v", raw.PriceConversion)
	}
	if order.MoneyCents != 200000 {
		t.Fatalf("Alipay amount changed: %d", order.MoneyCents)
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

func TestCreateOrder_CNYPlanPriceIsAuthoritative(t *testing.T) {
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "10")
	for _, currency := range []string{"CNY", "USD"} {
		t.Run(currency, func(t *testing.T) {
			getter := &stubPlanGetterForCreate{plan: &subscriptionbiz.SubscriptionPlan{ID: 7, GroupID: 3, PriceQuota: 100, ValidityDays: 30, ForSale: true, Group: &subscriptionbiz.SubscriptionGroup{ID: 3, Status: 1}}}
			repo := &capturingPaymentRepo{}
			uc := NewPaymentUsecaseWithSnapshotter(repo, NewMockPaymentProvider(), &countingPaymentIssuer{}, NewPaymentPlanSnapshotter(getter))
			order, err := uc.CreateOrder(context.Background(), CreatePaymentOrderRequest{UserID: "42", Channel: PaymentChannelMock, AssetType: PaymentAssetTypeSubscription, PlanID: 7, AssetAmount: 1, MoneyCents: 1, Currency: currency})
			if currency == "USD" {
				if err == nil || repo.created != nil {
					t.Fatal("CNY plan must reject a USD order before contacting the provider")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if order.MoneyCents != 10000 || order.Currency != "CNY" || order.AssetAmount != 30 {
				t.Fatalf("untrusted request changed the CNY plan charge: %+v", order)
			}
		})
	}
}

type stubGroupGetterForCreate struct {
	group *subscriptionbiz.SubscriptionGroup
}

func (s stubGroupGetterForCreate) GetGroupByID(context.Context, int64) (*subscriptionbiz.SubscriptionGroup, error) {
	return s.group, nil
}

func TestCreateOrder_CNYGroupPriceIsAuthoritative(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "10")
	for _, currency := range []string{"CNY", "USD"} {
		t.Run(currency, func(t *testing.T) {
			getter := stubGroupGetterForCreate{group: &subscriptionbiz.SubscriptionGroup{ID: 3, Status: 1, PriceQuota: 100, DurationDays: 30}}
			repo := &capturingPaymentRepo{}
			uc := NewPaymentUsecaseWithAssigner(repo, NewMockPaymentProvider(), nil, NewPaymentSubscriptionAssigner(nil, getter))
			order, err := uc.CreateOrder(context.Background(), CreatePaymentOrderRequest{UserID: "42", Channel: PaymentChannelMock, AssetType: PaymentAssetTypeSubscription, GroupID: 3, AssetAmount: 1, MoneyCents: 1, Currency: currency})
			if currency == "USD" {
				if err == nil || repo.created != nil {
					t.Fatal("CNY group must reject a USD order before contacting the provider")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if order.MoneyCents != 10000 || order.AssetAmount != 30 || order.PlanSnapshot == "" {
				t.Fatalf("untrusted request changed the canonical CNY group charge: %+v", order)
			}
		})
	}
}

func TestPaymentPlanSnapshotterRejectsUnsaleableLegacyPlan(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	for _, name := range []string{"zero duration", "off shelf", "disabled group"} {
		t.Run(name, func(t *testing.T) {
			plan := &subscriptionbiz.SubscriptionPlan{ID: 7, GroupID: 3, Group: &subscriptionbiz.SubscriptionGroup{ID: 3, Status: 1}, ForSale: true, PriceQuota: 100, ValidityDays: 30}
			switch name {
			case "zero duration":
				plan.ValidityDays = 0
			case "off shelf":
				plan.ForSale = false
			case "disabled group":
				plan.Group.Status = 2
			}
			_, err := NewPaymentPlanSnapshotter(&stubPlanGetterForCreate{plan: plan}).CapturePlanSnapshot(context.Background(), plan.ID)
			if err == nil {
				t.Fatal("new legacy cash order must be saleable and fulfillable before payment")
			}
		})
	}
}
