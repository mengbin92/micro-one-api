package biz

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	subscriptionbiz "micro-one-api/internal/subscription/biz"

	"gorm.io/gorm"
)

// renewingAssignmentUsecase counts AssignOrExtend calls so the idempotency
// test can assert a replayed payment callback does not extend the subscription
// a second time.
type renewingAssignmentUsecase struct {
	mu        sync.Mutex
	calls     int
	lastReq   *subscriptionbiz.AssignSubscriptionRequest
	extending bool
}

func (f *renewingAssignmentUsecase) Assign(ctx context.Context, req *subscriptionbiz.AssignSubscriptionRequest) (*subscriptionbiz.UserSubscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastReq = req
	return &subscriptionbiz.UserSubscription{ID: 1, ExpiresAt: req.ExpiresAt}, nil
}

func (f *renewingAssignmentUsecase) AssignOrExtend(ctx context.Context, req *subscriptionbiz.AssignSubscriptionRequest) (*subscriptionbiz.UserSubscription, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastReq = req
	return &subscriptionbiz.UserSubscription{ID: 1, ExpiresAt: req.ExpiresAt}, f.extending, nil
}

// inMemoryPaymentRepoForRenewal models the MarkOrderPaid idempotency guard:
// the first paid transition runs the issue callback; a second call on an
// already-paid order short-circuits and returns changed=false without
// invoking issue. Mirrors the DB transaction guard.
type inMemoryPaymentRepoForRenewal struct {
	mu    sync.Mutex
	order *PaymentOrder
}

func (r *inMemoryPaymentRepoForRenewal) CreateOrder(ctx context.Context, order *PaymentOrder) (*PaymentOrder, error) {
	r.order = order
	return order, nil
}
func (r *inMemoryPaymentRepoForRenewal) GetOrderByTradeNo(ctx context.Context, tradeNo string) (*PaymentOrder, error) {
	if r.order == nil || r.order.TradeNo != tradeNo {
		return nil, nil
	}
	c := *r.order
	return &c, nil
}
func (r *inMemoryPaymentRepoForRenewal) ListOrders(ctx context.Context, req ListPaymentOrdersRequest) ([]*PaymentOrder, int64, error) {
	return nil, 0, nil
}
func (r *inMemoryPaymentRepoForRenewal) MarkOrderPaid(ctx context.Context, tradeNo, providerTradeNo string, issue func(*PaymentOrder) error) (*PaymentOrder, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.order == nil || r.order.TradeNo != tradeNo {
		return nil, false, nil
	}
	if r.order.Status == PaymentOrderStatusPaid {
		c := *r.order
		return &c, false, nil
	}
	if err := issue(r.order); err != nil {
		return nil, false, err
	}
	r.order.Status = PaymentOrderStatusPaid
	r.order.ProviderTradeNo = providerTradeNo
	r.order.AssetIssueStatus = PaymentAssetIssueStatusIssued
	c := *r.order
	return &c, true, nil
}
func (r *inMemoryPaymentRepoForRenewal) MarkOrderClosed(ctx context.Context, tradeNo, providerTradeNo string) (*PaymentOrder, bool, error) {
	return nil, false, nil
}
func (r *inMemoryPaymentRepoForRenewal) MarkOrderRefunded(ctx context.Context, tradeNo, reason string, revert func(*PaymentOrder, *gorm.DB) error) (*PaymentOrder, bool, error) {
	return nil, false, nil
}

// newRenewalOrder builds a plan-backed order with a snapshot so the assigner
// fulfils from the snapshot without needing a live plan repo or group getter.
func newRenewalOrder(tradeNo string) *PaymentOrder {
	snap := PlanSnapshot{
		PlanID:       7,
		Name:         "Pro",
		ProductName:  "codex-pro",
		GroupID:      3,
		PriceQuota:   2000,
		ValidityDays: 30,
	}
	return &PaymentOrder{
		UserID:           "42",
		TradeNo:          tradeNo,
		Channel:          PaymentChannelAlipay,
		AssetType:        PaymentAssetTypeSubscription,
		AssetAmount:      30,
		GroupID:          3,
		PlanID:           7,
		PlanSnapshot:     EncodePlanSnapshot(snap),
		Status:           PaymentOrderStatusPending,
		AssetIssueStatus: PaymentAssetIssueStatusPending,
	}
}

func TestMarkOrderPaid_RenewalIsIdempotentAcrossReplays(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	assigner := &renewingAssignmentUsecase{extending: true}
	a := &paymentSubscriptionAssigner{
		subscriptions: assigner,
		now:           func() time.Time { return now },
	}
	repo := &inMemoryPaymentRepoForRenewal{order: newRenewalOrder("PAY-RENEW-1")}
	uc := NewPaymentUsecaseWithAssigner(repo, NewMockPaymentProvider(), &countingPaymentIssuer{}, a)

	order1, err := uc.MarkOrderPaid(context.Background(), "PAY-RENEW-1", "ALI-1")
	if err != nil {
		t.Fatalf("first MarkOrderPaid: %v", err)
	}
	if order1.Status != PaymentOrderStatusPaid {
		t.Fatalf("first status = %q", order1.Status)
	}
	if assigner.calls != 1 {
		t.Fatalf("assigner called %d times after first pay, want 1", assigner.calls)
	}
	firstExpires := assigner.lastReq.ExpiresAt

	// Replay the same callback (duplicate notify / retry). Idempotency guard
	// short-circuits: no second assignment, no second extension.
	order2, err := uc.MarkOrderPaid(context.Background(), "PAY-RENEW-1", "ALI-1")
	if err != nil {
		t.Fatalf("replay MarkOrderPaid: %v", err)
	}
	if order2.Status != PaymentOrderStatusPaid {
		t.Fatalf("replay status = %q", order2.Status)
	}
	if assigner.calls != 1 {
		t.Fatalf("assigner called %d times after replay, want 1 (idempotent)", assigner.calls)
	}
	if assigner.lastReq.ExpiresAt != firstExpires {
		t.Fatalf("expires_at changed on replay: %d -> %d", firstExpires, assigner.lastReq.ExpiresAt)
	}
}

func TestRenewal_TraceabilityMetadataLinksOrderToSubscription(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	assigner := &renewingAssignmentUsecase{extending: true}
	a := &paymentSubscriptionAssigner{
		subscriptions: assigner,
		now:           func() time.Time { return now },
	}
	repo := &inMemoryPaymentRepoForRenewal{order: newRenewalOrder("PAY-TRACE-1")}
	repo.order.ProviderTradeNo = "ALI-TRACE-1"
	uc := NewPaymentUsecaseWithAssigner(repo, NewMockPaymentProvider(), &countingPaymentIssuer{}, a)

	if _, err := uc.MarkOrderPaid(context.Background(), "PAY-TRACE-1", "ALI-TRACE-1"); err != nil {
		t.Fatalf("MarkOrderPaid: %v", err)
	}
	if assigner.lastReq == nil {
		t.Fatal("assigner not called")
	}
	// Subscription metadata must carry the payment trade_no so the order,
	// subscription, and billing ledger can be cross-referenced.
	if !strings.Contains(assigner.lastReq.Metadata, "PAY-TRACE-1") {
		t.Fatalf("metadata does not reference trade no: %s", assigner.lastReq.Metadata)
	}
	if !strings.Contains(assigner.lastReq.Metadata, "plan_id") {
		t.Fatalf("metadata does not reference plan_id: %s", assigner.lastReq.Metadata)
	}
}

func TestMarkOrderPaid_PlanSnapshotDoesNotRequireOrderGroupID(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	assigner := &renewingAssignmentUsecase{}
	a := &paymentSubscriptionAssigner{
		subscriptions: assigner,
		now:           func() time.Time { return now },
	}
	order := newRenewalOrder("PAY-NO-GROUP")
	order.GroupID = 0
	repo := &inMemoryPaymentRepoForRenewal{order: order}
	uc := NewPaymentUsecaseWithAssigner(repo, NewMockPaymentProvider(), &countingPaymentIssuer{}, a)

	if _, err := uc.MarkOrderPaid(context.Background(), "PAY-NO-GROUP", "ALI-NO-GROUP"); err != nil {
		t.Fatalf("MarkOrderPaid: %v", err)
	}
	if assigner.lastReq == nil || assigner.lastReq.GroupID != 3 {
		t.Fatalf("snapshot group was not used: %+v", assigner.lastReq)
	}
}

func TestRenewal_ExtendsExpiryForSameGroupActiveSubscription(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	assigner := &renewingAssignmentUsecase{extending: true}
	a := &paymentSubscriptionAssigner{
		subscriptions: assigner,
		now:           func() time.Time { return now },
	}
	repo := &inMemoryPaymentRepoForRenewal{order: newRenewalOrder("PAY-EXT-1")}
	uc := NewPaymentUsecaseWithAssigner(repo, NewMockPaymentProvider(), &countingPaymentIssuer{}, a)

	if _, err := uc.MarkOrderPaid(context.Background(), "PAY-EXT-1", "ALI-EXT-1"); err != nil {
		t.Fatalf("MarkOrderPaid: %v", err)
	}
	// Renewal extends expires_at by the plan's validity from now.
	wantExpires := now.Unix() + 30*subscriptionSecondsPerDay
	if assigner.lastReq.ExpiresAt != wantExpires {
		t.Fatalf("expires_at = %d, want %d (30d from now)", assigner.lastReq.ExpiresAt, wantExpires)
	}
}
