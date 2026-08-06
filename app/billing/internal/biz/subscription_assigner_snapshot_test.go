package biz

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	subscriptionbiz "micro-one-api/domain/subscription/biz"
)

// fakeAssignmentUsecase records the last AssignOrExtend request so the test can
// assert the assigner fulfilled from the snapshot, not a live plan read.
type fakeAssignmentUsecase struct {
	lastReq *subscriptionbiz.AssignSubscriptionRequest
	lastExt bool
	err     error
}

func (f *fakeAssignmentUsecase) Assign(ctx context.Context, req *subscriptionbiz.AssignSubscriptionRequest) (*subscriptionbiz.UserSubscription, error) {
	f.lastReq = req
	return &subscriptionbiz.UserSubscription{ID: 1}, f.err
}

func (f *fakeAssignmentUsecase) AssignOrExtend(ctx context.Context, req *subscriptionbiz.AssignSubscriptionRequest) (*subscriptionbiz.UserSubscription, bool, error) {
	f.lastReq = req
	return &subscriptionbiz.UserSubscription{ID: 1, ExpiresAt: req.ExpiresAt}, f.lastExt, f.err
}

func (f *fakeAssignmentUsecase) AssignOrExtendInTx(ctx context.Context, tx subscriptionbiz.Tx, req *subscriptionbiz.AssignSubscriptionRequest) (*subscriptionbiz.UserSubscription, bool, error) {
	return f.AssignOrExtend(ctx, req)
}

// stubPlanGetter returns the given plan (or error) on GetPlanByID. Used to
// verify the snapshot path does NOT call the live plan repo.
type stubPlanGetter struct {
	plan  *subscriptionbiz.SubscriptionPlan
	err   error
	calls int
}

func (s *stubPlanGetter) GetPlanByID(ctx context.Context, planID int64) (*subscriptionbiz.SubscriptionPlan, error) {
	s.calls++
	return s.plan, s.err
}

func newSnapshotAssigner(t *testing.T, plans SubscriptionPlanGetter, now time.Time) (*paymentSubscriptionAssigner, *fakeAssignmentUsecase) {
	t.Helper()
	fake := &fakeAssignmentUsecase{}
	a := &paymentSubscriptionAssigner{
		subscriptions: fake,
		groups:        nil,
		plans:         plans,
		now:           func() time.Time { return now },
	}
	return a, fake
}

func TestAssignPlan_FulfilsFromSnapshotWithoutLivePlan(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	// The live plan repo returns an off-shelf / deleted state. The snapshot
	// path must not consult it at all.
	live := &stubPlanGetter{plan: nil, err: subscriptionbiz.ErrSubscriptionPlanNotFound}
	a, fake := newSnapshotAssigner(t, live, now)

	snap := PlanSnapshot{
		PlanID:       7,
		Name:         "Codex Pro Monthly",
		ProductName:  "codex-pro",
		GroupID:      3,
		PriceQuota:   2000,
		ValidityDays: 30,
	}
	order := &PaymentOrder{
		UserID:       "42",
		TradeNo:      "PAY-SNAP-1",
		AssetType:    PaymentAssetTypeSubscription,
		AssetAmount:  30,
		GroupID:      3,
		PlanID:       7,
		PlanSnapshot: EncodePlanSnapshot(snap),
	}
	if err := a.AssignSubscriptionAfterPayment(context.Background(), order); err != nil {
		t.Fatalf("assign from snapshot: %v", err)
	}
	if live.calls != 0 {
		t.Fatalf("snapshot path must not read live plan repo, got %d calls", live.calls)
	}
	if fake.lastReq == nil {
		t.Fatal("AssignOrExtend was not called")
	}
	if fake.lastReq.GroupID != 3 || fake.lastReq.UserID != 42 {
		t.Fatalf("unexpected assign req: %+v", fake.lastReq)
	}
	wantExpires := now.Unix() + 30*subscriptionSecondsPerDay
	if fake.lastReq.ExpiresAt != wantExpires {
		t.Fatalf("expires_at = %d, want %d", fake.lastReq.ExpiresAt, wantExpires)
	}
	if fake.lastReq.SubscriptionName != "Codex Pro Monthly" {
		t.Fatalf("subscription_name = %q", fake.lastReq.SubscriptionName)
	}
	var meta map[string]string
	if err := json.Unmarshal([]byte(fake.lastReq.Metadata), &meta); err != nil {
		t.Fatalf("metadata not json: %v", err)
	}
	if meta["plan_snapshot"] != "true" || meta["plan_id"] != "7" {
		t.Fatalf("metadata = %v", meta)
	}
}

func TestAssignPlan_SnapshotUsesOrderAssetAmountWhenPresent(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	live := &stubPlanGetter{plan: nil, err: subscriptionbiz.ErrSubscriptionPlanNotFound}
	a, fake := newSnapshotAssigner(t, live, now)

	snap := PlanSnapshot{PlanID: 7, Name: "P", GroupID: 3, ValidityDays: 30}
	order := &PaymentOrder{
		UserID:       "42",
		TradeNo:      "PAY-SNAP-2",
		AssetType:    PaymentAssetTypeSubscription,
		AssetAmount:  45, // overrides snapshot validity
		GroupID:      3,
		PlanID:       7,
		PlanSnapshot: EncodePlanSnapshot(snap),
	}
	if err := a.AssignSubscriptionAfterPayment(context.Background(), order); err != nil {
		t.Fatalf("assign: %v", err)
	}
	wantExpires := now.Unix() + 45*subscriptionSecondsPerDay
	if fake.lastReq.ExpiresAt != wantExpires {
		t.Fatalf("expires_at = %d, want %d (45d from order)", fake.lastReq.ExpiresAt, wantExpires)
	}
}

func TestAssignPlan_FallsBackToLivePlanWhenNoSnapshot(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	plan := &subscriptionbiz.SubscriptionPlan{
		ID: 7, GroupID: 3, Name: "Live Plan", ValidityDays: 30,
	}
	live := &stubPlanGetter{plan: plan}
	a, fake := newSnapshotAssigner(t, live, now)

	order := &PaymentOrder{
		UserID:      "42",
		TradeNo:     "PAY-LIVE-1",
		AssetType:   PaymentAssetTypeSubscription,
		AssetAmount: 30,
		GroupID:     3,
		PlanID:      7,
		// PlanSnapshot intentionally empty: legacy / pre-snapshot order.
	}
	if err := a.AssignSubscriptionAfterPayment(context.Background(), order); err != nil {
		t.Fatalf("assign live: %v", err)
	}
	if live.calls != 1 {
		t.Fatalf("expected 1 live plan read, got %d", live.calls)
	}
	if fake.lastReq.SubscriptionName != "Live Plan" {
		t.Fatalf("name = %q", fake.lastReq.SubscriptionName)
	}
}

func TestAssignPlan_RejectsGarbageSnapshot(t *testing.T) {
	live := &stubPlanGetter{}
	a, _ := newSnapshotAssigner(t, live, time.Now())
	order := &PaymentOrder{
		UserID:       "42",
		AssetType:    PaymentAssetTypeSubscription,
		PlanID:       7,
		PlanSnapshot: "not-json",
	}
	err := a.AssignSubscriptionAfterPayment(context.Background(), order)
	if err == nil || !errors.Is(err, err) && err.Error() == "" {
		// any non-nil error is acceptable
	}
	if err == nil {
		t.Fatal("expected error for garbage snapshot")
	}
}

func TestPaymentPlanSnapshotter_CapturesPlan(t *testing.T) {
	plan := &subscriptionbiz.SubscriptionPlan{
		ID: 7, GroupID: 3, Name: "Pro", ProductName: "codex-pro",
		PriceQuota: 2000, ValidityDays: 30,
	}
	live := &stubPlanGetter{plan: plan}
	s := NewPaymentPlanSnapshotter(live)
	snap, err := s.CapturePlanSnapshot(context.Background(), 7)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if snap.PlanID != 7 || snap.GroupID != 3 || snap.PriceQuota != 2000 || snap.ValidityDays != 30 {
		t.Fatalf("snapshot = %#v", snap)
	}
	if snap.Name != "Pro" || snap.ProductName != "codex-pro" {
		t.Fatalf("snapshot names = %q/%q", snap.Name, snap.ProductName)
	}
}

func TestPaymentPlanSnapshotter_ZeroPlanIDReturnsEmpty(t *testing.T) {
	s := NewPaymentPlanSnapshotter(&stubPlanGetter{})
	snap, err := s.CapturePlanSnapshot(context.Background(), 0)
	if err != nil {
		t.Fatalf("capture zero: %v", err)
	}
	if snap.PlanID != 0 {
		t.Fatalf("zero plan_id snapshot = %#v", snap)
	}
}

// Compile-time assertion that strconv is used (avoids unused import in case
// the test body is trimmed later).
var _ = strconv.Itoa
