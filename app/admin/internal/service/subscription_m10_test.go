package service

import (
	"context"
	"strconv"
	"testing"

	billingv1 "micro-one-api/api/billing/v1"
	subscriptionbiz "micro-one-api/domain/subscription/biz"

	"google.golang.org/grpc"
)

// fakeSubscriptionRepo is a minimal in-memory SubscriptionRepository used to
// exercise the admin completion endpoint's M10 idempotency: the only behaviours
// the flow needs are active-lookup, create and narrow-update.
type fakeSubscriptionRepo struct {
	active        *subscriptionbiz.UserSubscription
	createdCount  int
	updatedCount  int
	createdByUser map[int64]*subscriptionbiz.UserSubscription
}

func (r *fakeSubscriptionRepo) CreateSubscription(_ context.Context, sub *subscriptionbiz.UserSubscription) error {
	r.createdCount++
	if r.createdByUser == nil {
		r.createdByUser = map[int64]*subscriptionbiz.UserSubscription{}
	}
	r.createdByUser[sub.UserID] = sub
	r.active = sub
	return nil
}
func (r *fakeSubscriptionRepo) CreateSubscriptionInTx(_ context.Context, _ subscriptionbiz.Tx, sub *subscriptionbiz.UserSubscription) error {
	return r.CreateSubscription(context.Background(), sub)
}
func (r *fakeSubscriptionRepo) UpdateSubscription(_ context.Context, _ *subscriptionbiz.UserSubscription) error { return nil }
func (r *fakeSubscriptionRepo) UpdateSubscriptionInTx(_ context.Context, _ subscriptionbiz.Tx, _ *subscriptionbiz.UserSubscription) error {
	return nil
}
func (r *fakeSubscriptionRepo) UpdateSubscriptionFields(_ context.Context, sub *subscriptionbiz.UserSubscription, _ []subscriptionbiz.SubscriptionField) error {
	r.updatedCount++
	r.active = sub
	return nil
}
func (r *fakeSubscriptionRepo) UpdateSubscriptionFieldsInTx(_ context.Context, _ subscriptionbiz.Tx, sub *subscriptionbiz.UserSubscription, _ []subscriptionbiz.SubscriptionField) error {
	r.updatedCount++
	r.active = sub
	return nil
}
func (r *fakeSubscriptionRepo) DeleteSubscription(_ context.Context, _ int64) error { return nil }
func (r *fakeSubscriptionRepo) GetSubscriptionByID(_ context.Context, _ int64) (*subscriptionbiz.UserSubscription, error) {
	return r.active, nil
}
func (r *fakeSubscriptionRepo) ListSubscriptionsByUser(_ context.Context, userID int64) ([]*subscriptionbiz.UserSubscription, error) {
	if r.active == nil || r.active.UserID != userID {
		return nil, nil
	}
	return []*subscriptionbiz.UserSubscription{r.active}, nil
}
func (r *fakeSubscriptionRepo) ListActiveSubscriptions(_ context.Context) ([]*subscriptionbiz.UserSubscription, error) {
	if r.active == nil {
		return nil, nil
	}
	return []*subscriptionbiz.UserSubscription{r.active}, nil
}
func (r *fakeSubscriptionRepo) ListAllSubscriptions(_ context.Context) ([]*subscriptionbiz.UserSubscription, error) {
	if r.active == nil {
		return nil, nil
	}
	return []*subscriptionbiz.UserSubscription{r.active}, nil
}
func (r *fakeSubscriptionRepo) GetActiveSubscriptionByUser(_ context.Context, userID int64) (*subscriptionbiz.UserSubscription, error) {
	if r.active == nil || r.active.UserID != userID {
		return nil, nil
	}
	return r.active, nil
}
func (r *fakeSubscriptionRepo) GetActiveSubscriptionByUserInTx(_ context.Context, _ subscriptionbiz.Tx, userID int64) (*subscriptionbiz.UserSubscription, error) {
	return r.GetActiveSubscriptionByUser(context.Background(), userID)
}
func (r *fakeSubscriptionRepo) AddUsage(_ context.Context, _ int64, _ float64, _ int64) error { return nil }
func (r *fakeSubscriptionRepo) AddUsageByIDInTx(_ context.Context, _ subscriptionbiz.Tx, _ int64, _ float64, _ int64) error {
	return nil
}
func (r *fakeSubscriptionRepo) GetByIDInTx(_ context.Context, _ subscriptionbiz.Tx, _ int64) (*subscriptionbiz.UserSubscription, error) {
	return r.active, nil
}

type fakeGroupRepo struct {
	group *subscriptionbiz.SubscriptionGroup
}

func (r *fakeGroupRepo) CreateGroup(_ context.Context, _ *subscriptionbiz.SubscriptionGroup) error { return nil }
func (r *fakeGroupRepo) UpdateGroup(_ context.Context, _ *subscriptionbiz.SubscriptionGroup) error { return nil }
func (r *fakeGroupRepo) DeleteGroup(_ context.Context, _ int64) error                             { return nil }
func (r *fakeGroupRepo) GetGroupByID(_ context.Context, _ int64) (*subscriptionbiz.SubscriptionGroup, error) {
	return r.group, nil
}
func (r *fakeGroupRepo) GetGroupByName(_ context.Context, _ string) (*subscriptionbiz.SubscriptionGroup, error) {
	return r.group, nil
}
func (r *fakeGroupRepo) ListGroups(_ context.Context) ([]*subscriptionbiz.SubscriptionGroup, error) {
	if r.group == nil {
		return nil, nil
	}
	return []*subscriptionbiz.SubscriptionGroup{r.group}, nil
}

// mockBillingClient is a scripted billingv1.BillingServiceClient. It embeds
// the generated interface so unimplemented methods panic if invoked, keeping
// the test focused on the completion endpoint's claim orchestration.
// MarkPaymentOrderAssetIssued simulates the billing-side CAS: the first call
// on a paid+pending order wins (flips the order to issued, claimed=true);
// every later call loses (claimed=false, order already issued).
type mockBillingClient struct {
	billingv1.BillingServiceClient
	order        *billingv1.PaymentOrder
	claimCalls   int
	unmarkCalls  int
	lastUnmarkNo string
}

func (m *mockBillingClient) GetPaymentOrderByTradeNo(_ context.Context, _ *billingv1.GetPaymentOrderByTradeNoRequest, _ ...grpc.CallOption) (*billingv1.PaymentOrderResponse, error) {
	return &billingv1.PaymentOrderResponse{Success: true, Order: m.order}, nil
}

func (m *mockBillingClient) MarkPaymentOrderAssetIssued(_ context.Context, _ *billingv1.MarkPaymentOrderAssetIssuedRequest, _ ...grpc.CallOption) (*billingv1.PaymentOrderResponse, error) {
	m.claimCalls++
	if m.order.GetStatus() == "paid" && m.order.GetAssetIssueStatus() == "pending" {
		m.order.AssetIssueStatus = "issued"
		return &billingv1.PaymentOrderResponse{Success: true, Order: m.order, Claimed: true}, nil
	}
	return &billingv1.PaymentOrderResponse{Success: true, Order: m.order, Claimed: false}, nil
}

func (m *mockBillingClient) UnmarkPaymentOrderAssetIssued(_ context.Context, req *billingv1.UnmarkPaymentOrderAssetIssuedRequest, _ ...grpc.CallOption) (*billingv1.PaymentOrderResponse, error) {
	m.unmarkCalls++
	m.lastUnmarkNo = req.GetTradeNo()
	if m.order.GetAssetIssueStatus() == "issued" {
		m.order.AssetIssueStatus = "pending"
	}
	return &billingv1.PaymentOrderResponse{Success: true, Order: m.order}, nil
}

func newAdminServiceForM10(billing *mockBillingClient, subRepo *fakeSubscriptionRepo, group *subscriptionbiz.SubscriptionGroup) *AdminService {
	groupRepo := &fakeGroupRepo{group: group}
	subUc := subscriptionbiz.NewSubscriptionUsecase(subRepo, groupRepo)
	groupUc := subscriptionbiz.NewGroupUsecase(groupRepo)
	svc := NewAdminService(billing, nil, nil, nil)
	svc.SetSubscriptionUsecases(subUc, groupUc)
	return svc
}

func paidGroupOrder(userID int64, tradeNo string) *billingv1.PaymentOrder {
	return &billingv1.PaymentOrder{
		TradeNo:          tradeNo,
		UserId:           strconv.FormatInt(userID, 10),
		Channel:          "alipay",
		AssetType:        "subscription",
		AssetAmount:      30,
		Status:           "paid",
		AssetIssueStatus: "pending",
		GroupId:          7,
	}
}

// TestCompleteSubscriptionPurchase_ReplayIsIdempotent is the M10 regression:
// a replayed completion after a successful fulfilment must NOT extend the
// entitlement again. First call wins the claim and fulfils (one subscription
// created); the replay loses the claim, observes issued and returns the
// current subscription without touching the repo.
func TestCompleteSubscriptionPurchase_ReplayIsIdempotent(t *testing.T) {
	subRepo := &fakeSubscriptionRepo{}
	group := &subscriptionbiz.SubscriptionGroup{ID: 7, Name: "pro", Status: subscriptionbiz.SubscriptionGroupStatusEnabled, PriceQuota: 100, DurationDays: 30}
	order := paidGroupOrder(42, "PAY-M10-1")
	billing := &mockBillingClient{order: order}
	svc := newAdminServiceForM10(billing, subRepo, group)

	// First completion: claim wins (claimed=true), fulfilment runs.
	sub1, err := svc.CompleteSubscriptionPurchase(context.Background(), 42, "PAY-M10-1")
	if err != nil {
		t.Fatalf("first completion: %v", err)
	}
	if sub1 == nil {
		t.Fatal("first completion returned nil subscription")
	}
	if subRepo.createdCount != 1 {
		t.Fatalf("createdCount = %d, want 1 (single fulfilment)", subRepo.createdCount)
	}
	if sub1.GroupID != 7 {
		t.Fatalf("sub group = %d, want 7", sub1.GroupID)
	}

	// Replay: claim loses (already issued), endpoint returns the current
	// subscription without fulfilling again.
	sub2, err := svc.CompleteSubscriptionPurchase(context.Background(), 42, "PAY-M10-1")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if sub2 == nil {
		t.Fatal("replay returned nil subscription")
	}
	if subRepo.createdCount != 1 {
		t.Fatalf("createdCount after replay = %d, want 1 (no double fulfilment)", subRepo.createdCount)
	}
	if billing.unmarkCalls != 0 {
		t.Fatalf("unmarkCalls = %d, want 0 (no failed fulfilment)", billing.unmarkCalls)
	}
}

// TestCompleteSubscriptionPurchase_FulfilFailureReleasesClaim is the M10
// compensation path: when fulfilment fails after winning the claim, the claim
// must be released (unmark) so the order can be retried.
func TestCompleteSubscriptionPurchase_FulfilFailureReleasesClaim(t *testing.T) {
	subRepo := &fakeSubscriptionRepo{}
	order := paidGroupOrder(42, "PAY-M10-2")
	billing := &mockBillingClient{order: order}
	// groupUc.Get fails: group is nil and the repo returns nil without error,
	// which surfaces as ErrSubscriptionNotPurchasable in fulfilPaidOrder.
	svc := newAdminServiceForM10(billing, subRepo, nil)

	_, err := svc.CompleteSubscriptionPurchase(context.Background(), 42, "PAY-M10-2")
	if err == nil {
		t.Fatal("completion with missing group must fail")
	}
	if billing.unmarkCalls != 1 {
		t.Fatalf("unmarkCalls = %d, want 1 (claim released on failure)", billing.unmarkCalls)
	}
	if billing.lastUnmarkNo != "PAY-M10-2" {
		t.Fatalf("unmark trade_no = %q, want PAY-M10-2", billing.lastUnmarkNo)
	}
	if subRepo.createdCount != 0 {
		t.Fatalf("createdCount = %d, want 0 (failed fulfilment)", subRepo.createdCount)
	}
}

// TestCompleteSubscriptionPurchase_RefusedClaimReturnsError ensures an order
// that is neither claimed nor issued (e.g. refunded) is surfaced as an error
// rather than being fulfilled.
func TestCompleteSubscriptionPurchase_RefusedClaimReturnsError(t *testing.T) {
	subRepo := &fakeSubscriptionRepo{}
	group := &subscriptionbiz.SubscriptionGroup{ID: 7, Name: "pro", Status: subscriptionbiz.SubscriptionGroupStatusEnabled, PriceQuota: 100, DurationDays: 30}
	order := paidGroupOrder(42, "PAY-M10-3")
	order.AssetIssueStatus = "refunded"
	billing := &mockBillingClient{order: order}
	svc := newAdminServiceForM10(billing, subRepo, group)

	if _, err := svc.CompleteSubscriptionPurchase(context.Background(), 42, "PAY-M10-3"); err == nil {
		t.Fatal("refused claim must surface an error")
	}
	if subRepo.createdCount != 0 {
		t.Fatalf("createdCount = %d, want 0", subRepo.createdCount)
	}
	if billing.unmarkCalls != 0 {
		t.Fatalf("unmarkCalls = %d, want 0", billing.unmarkCalls)
	}
}
