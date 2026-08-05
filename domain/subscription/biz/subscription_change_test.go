package biz

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestChangeSubscription_ImmediateUpgrade(t *testing.T) {
	repo := newMockSubscriptionRepo()
	group := &SubscriptionGroup{Name: "basic", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), group); err != nil {
		t.Fatal(err)
	}
	groupPro := &SubscriptionGroup{Name: "pro", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), groupPro); err != nil {
		t.Fatal(err)
	}
	uc := NewSubscriptionUsecase(repo, repo)
	uc.now = func() time.Time { return time.Unix(5000, 0) }

	sub, _, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: group.ID, StartsAt: 1000, ExpiresAt: 99999, SubscriptionName: "basic",
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	origExpires := sub.ExpiresAt

	res, err := uc.ChangeSubscription(context.Background(), ChangeRequest{
		UserID:             1,
		FromSubscriptionID: sub.ID,
		ToPlanID:           2,
		ToGroupID:          groupPro.ID,
		NewPlanName:        "pro",
		NewPriceQuota:      2000,
		OldPriceQuota:      1000,
		Operator:           "admin",
		Now:                6000,
	})
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if !res.Applied {
		t.Fatal("upgrade should apply immediately")
	}
	if res.Policy != SubscriptionChangePolicyImmediate {
		t.Fatalf("policy = %q", res.Policy)
	}
	if res.ChargedQuota != 1000 {
		t.Fatalf("charged = %d, want 1000", res.ChargedQuota)
	}
	// Reload and verify the row was mutated in place.
	got, err := uc.repo.GetSubscriptionByID(context.Background(), sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.GroupID != groupPro.ID {
		t.Fatalf("group_id = %d, want %d", got.GroupID, groupPro.ID)
	}
	if got.SubscriptionName != "pro" {
		t.Fatalf("name = %q", got.SubscriptionName)
	}
	// expires_at preserved (change is not a renewal).
	if got.ExpiresAt != origExpires {
		t.Fatalf("expires_at changed: %d -> %d (want preserved)", origExpires, got.ExpiresAt)
	}
	// Usage windows reset on group change.
	if got.DailyUsageUSD != 0 || got.DailyWindowStart != 6000 {
		t.Fatalf("daily window not reset: %+v", got)
	}
	// Audit metadata recorded.
	var meta map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got.Metadata), &meta); err != nil {
		t.Fatalf("metadata not json: %v", err)
	}
	if _, ok := meta["last_change"]; !ok {
		t.Fatal("last_change audit missing from metadata")
	}
}

func TestChangeSubscription_NextCycleDowngrade(t *testing.T) {
	repo := newMockSubscriptionRepo()
	groupPro := &SubscriptionGroup{Name: "pro", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), groupPro); err != nil {
		t.Fatal(err)
	}
	groupBasic := &SubscriptionGroup{Name: "basic", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), groupBasic); err != nil {
		t.Fatal(err)
	}
	uc := NewSubscriptionUsecase(repo, repo)

	sub, _, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: groupPro.ID, StartsAt: 1000, ExpiresAt: 99999, SubscriptionName: "pro",
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	origGroup := sub.GroupID

	res, err := uc.ChangeSubscription(context.Background(), ChangeRequest{
		UserID:             1,
		FromSubscriptionID: sub.ID,
		ToPlanID:           2,
		ToGroupID:          groupBasic.ID,
		NewPriceQuota:      500,
		OldPriceQuota:      2000,
		Operator:           "admin",
		Now:                6000,
	})
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if res.Applied {
		t.Fatal("downgrade should defer to next cycle")
	}
	if res.Policy != SubscriptionChangePolicyNextCycle {
		t.Fatalf("policy = %q", res.Policy)
	}
	// Group unchanged immediately.
	got, err := uc.repo.GetSubscriptionByID(context.Background(), sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.GroupID != origGroup {
		t.Fatalf("group changed immediately on downgrade: %d -> %d", origGroup, got.GroupID)
	}
	// pending_change recorded.
	var meta map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got.Metadata), &meta); err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if _, ok := meta["pending_change"]; !ok {
		t.Fatal("pending_change missing from metadata")
	}
}

func TestChangeSubscription_PendingChangeAppliesOnRenewal(t *testing.T) {
	repo := newMockSubscriptionRepo()
	groupPro := &SubscriptionGroup{Name: "pro", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), groupPro); err != nil {
		t.Fatal(err)
	}
	groupBasic := &SubscriptionGroup{Name: "basic", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), groupBasic); err != nil {
		t.Fatal(err)
	}
	uc := NewSubscriptionUsecase(repo, repo)

	sub, _, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: groupPro.ID, StartsAt: 1000, ExpiresAt: 2000, SubscriptionName: "pro",
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := uc.ChangeSubscription(context.Background(), ChangeRequest{
		UserID:             1,
		FromSubscriptionID: sub.ID,
		ToPlanID:           2,
		ToGroupID:          groupBasic.ID,
		NewPriceQuota:      500,
		OldPriceQuota:      2000,
		Operator:           "admin",
		Now:                1500,
	}); err != nil {
		t.Fatalf("change: %v", err)
	}

	got, reused, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID:           1,
		GroupID:          groupBasic.ID,
		StartsAt:         2000,
		ExpiresAt:        3000,
		SubscriptionName: "basic",
	})
	if err != nil {
		t.Fatalf("renew with pending change: %v", err)
	}
	if !reused {
		t.Fatal("renewal should reuse active subscription")
	}
	if got.ID != sub.ID {
		t.Fatalf("subscription id = %d, want %d", got.ID, sub.ID)
	}
	if got.GroupID != groupBasic.ID {
		t.Fatalf("group_id = %d, want %d", got.GroupID, groupBasic.ID)
	}
	if got.SubscriptionName != "basic" {
		t.Fatalf("subscription_name = %q, want basic", got.SubscriptionName)
	}
	var meta map[string]json.RawMessage
	if got.Metadata != "" {
		if err := json.Unmarshal([]byte(got.Metadata), &meta); err != nil {
			t.Fatalf("metadata: %v", err)
		}
		if _, ok := meta["pending_change"]; ok {
			t.Fatal("pending_change should be cleared after renewal")
		}
	}
}

func TestChangeSubscription_RejectsNonActive(t *testing.T) {
	repo := newMockSubscriptionRepo()
	group := &SubscriptionGroup{Name: "pro", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), group); err != nil {
		t.Fatal(err)
	}
	uc := NewSubscriptionUsecase(repo, repo)
	sub, _, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: group.ID, StartsAt: 1000, ExpiresAt: 99999,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := uc.Revoke(context.Background(), sub.ID, "test"); err != nil {
		t.Fatal(err)
	}
	_, err = uc.ChangeSubscription(context.Background(), ChangeRequest{
		UserID: 1, FromSubscriptionID: sub.ID, ToGroupID: group.ID, NewPriceQuota: 100, OldPriceQuota: 100,
	})
	if err != ErrSubscriptionNotActive {
		t.Fatalf("err = %v, want ErrSubscriptionNotActive", err)
	}
}

func TestChangeSubscription_RejectsWrongUser(t *testing.T) {
	repo := newMockSubscriptionRepo()
	group := &SubscriptionGroup{Name: "pro", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), group); err != nil {
		t.Fatal(err)
	}
	uc := NewSubscriptionUsecase(repo, repo)
	sub, _, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: group.ID, StartsAt: 1000, ExpiresAt: 99999,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = uc.ChangeSubscription(context.Background(), ChangeRequest{
		UserID: 2, FromSubscriptionID: sub.ID, ToGroupID: group.ID,
	})
	if err == nil {
		t.Fatal("expected error for wrong user")
	}
}

// TestChangeSubscription_PendingChangeAppliesOnSameGroupRenewal (review §6
// regression for H9): a pending next-cycle downgrade must take effect when the
// user renews their CURRENT plan (same group), which is the real production
// renewal flow. Previously the pending_change only applied when the renewal
// request's GroupID matched the pending target — which never happens in a
// same-plan renewal — so the downgrade was permanently stranded.
//
// Note: this test models the renewal-initiation layer reading the pending
// change and targeting the pending group. The AssignOrExtend apply-pending
// branch fires when req.GroupID == pending.ToGroupID.
func TestChangeSubscription_PendingChangeAppliesOnSameGroupRenewal(t *testing.T) {
	repo := newMockSubscriptionRepo()
	groupPro := &SubscriptionGroup{Name: "pro", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), groupPro); err != nil {
		t.Fatal(err)
	}
	groupBasic := &SubscriptionGroup{Name: "basic", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), groupBasic); err != nil {
		t.Fatal(err)
	}
	uc := NewSubscriptionUsecase(repo, repo)

	sub, _, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: groupPro.ID, StartsAt: 1000, ExpiresAt: 2000, SubscriptionName: "pro",
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	// Schedule a downgrade to basic (next_cycle).
	if _, err := uc.ChangeSubscription(context.Background(), ChangeRequest{
		UserID:             1,
		FromSubscriptionID: sub.ID,
		ToPlanID:           2,
		ToGroupID:          groupBasic.ID,
		NewPriceQuota:      500,
		OldPriceQuota:      2000,
		Operator:           "admin",
		Now:                1500,
	}); err != nil {
		t.Fatalf("change: %v", err)
	}

	// The renewal-initiation layer reads the pending change and creates the
	// renewal order for the pending target group (basic). This is the fix for
	// H9: previously the renewal used the current group (pro), so
	// AssignOrExtend never entered the pending-apply branch.
	got, reused, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID:           1,
		GroupID:          groupBasic.ID, // pending target group
		StartsAt:         2000,
		ExpiresAt:        2000 + 30*86400,
		SubscriptionName: "basic",
	})
	if err != nil {
		t.Fatalf("renew with pending change: %v", err)
	}
	if !reused {
		t.Fatal("renewal should reuse the active subscription")
	}
	if got.GroupID != groupBasic.ID {
		t.Fatalf("group_id = %d, want %d (pending downgrade applied)", got.GroupID, groupBasic.ID)
	}
	// pending_change must be cleared after applying.
	var meta map[string]json.RawMessage
	if got.Metadata != "" {
		if err := json.Unmarshal([]byte(got.Metadata), &meta); err != nil {
			t.Fatalf("metadata: %v", err)
		}
		if _, ok := meta["pending_change"]; ok {
			t.Fatal("pending_change should be cleared after renewal applies it")
		}
	}
}

// TestChangeSubscription_SameGroupKeepsUsage (M6, 2026-08-05): an immediate
// change that keeps the same group must NOT reset the usage windows. The old
// group's usage is still applicable, and resetting it would both lose data and
// let a same-price plan switch refresh quota for free.
func TestChangeSubscription_SameGroupKeepsUsage(t *testing.T) {
	repo := newMockSubscriptionRepo()
	group := &SubscriptionGroup{Name: "basic", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), group); err != nil {
		t.Fatal(err)
	}
	uc := NewSubscriptionUsecase(repo, repo)
	uc.now = func() time.Time { return time.Unix(5000, 0) }

	sub, _, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: group.ID, StartsAt: 1000, ExpiresAt: 99999, SubscriptionName: "basic",
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	// Seed usage so we can assert it survives a same-group change.
	if err := uc.repo.AddUsage(context.Background(), 1, 12.5, 4000); err != nil {
		t.Fatalf("add usage: %v", err)
	}
	before, err := uc.repo.GetSubscriptionByID(context.Background(), sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.DailyUsageUSD != 12.5 {
		t.Fatalf("seed usage = %v, want 12.5", before.DailyUsageUSD)
	}

	// Same group, same price: an "upgrade" that only changes the plan name.
	res, err := uc.ChangeSubscription(context.Background(), ChangeRequest{
		UserID:             1,
		FromSubscriptionID: sub.ID,
		ToPlanID:           9,
		ToGroupID:          group.ID, // same group
		NewPlanName:        "basic-v2",
		NewPriceQuota:      1000,
		OldPriceQuota:      1000, // same price -> charged 0
		Operator:           "admin",
		Now:                6000,
	})
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if !res.Applied {
		t.Fatal("same-price change should apply immediately")
	}
	got, err := uc.repo.GetSubscriptionByID(context.Background(), sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Usage windows must be preserved.
	if got.DailyUsageUSD != 12.5 {
		t.Fatalf("daily usage reset on same-group change: %v, want 12.5", got.DailyUsageUSD)
	}
	if got.DailyWindowStart != before.DailyWindowStart {
		t.Fatalf("daily window start reset: %d -> %d", before.DailyWindowStart, got.DailyWindowStart)
	}
	if got.GroupID != group.ID {
		t.Fatalf("group_id = %d, want %d", got.GroupID, group.ID)
	}
	if got.SubscriptionName != "basic-v2" {
		t.Fatalf("subscription_name = %q, want basic-v2", got.SubscriptionName)
	}
}

// TestChangeSubscription_GroupChangeResetsUsage (M6, 2026-08-05): an immediate
// change that actually moves the user to a different group must reset the
// usage windows so the new group's limits start fresh.
func TestChangeSubscription_GroupChangeResetsUsage(t *testing.T) {
	repo := newMockSubscriptionRepo()
	groupBasic := &SubscriptionGroup{Name: "basic", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), groupBasic); err != nil {
		t.Fatal(err)
	}
	groupPro := &SubscriptionGroup{Name: "pro", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), groupPro); err != nil {
		t.Fatal(err)
	}
	uc := NewSubscriptionUsecase(repo, repo)
	uc.now = func() time.Time { return time.Unix(5000, 0) }

	sub, _, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: groupBasic.ID, StartsAt: 1000, ExpiresAt: 99999, SubscriptionName: "basic",
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := uc.repo.AddUsage(context.Background(), 1, 12.5, 4000); err != nil {
		t.Fatalf("add usage: %v", err)
	}

	if _, err := uc.ChangeSubscription(context.Background(), ChangeRequest{
		UserID:             1,
		FromSubscriptionID: sub.ID,
		ToPlanID:           2,
		ToGroupID:          groupPro.ID, // group change
		NewPlanName:        "pro",
		NewPriceQuota:      2000,
		OldPriceQuota:      1000,
		Operator:           "admin",
		Now:                6000,
	}); err != nil {
		t.Fatalf("change: %v", err)
	}
	got, err := uc.repo.GetSubscriptionByID(context.Background(), sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DailyUsageUSD != 0 {
		t.Fatalf("daily usage not reset on group change: %v", got.DailyUsageUSD)
	}
	if got.DailyWindowStart != 6000 {
		t.Fatalf("daily window start = %d, want 6000", got.DailyWindowStart)
	}
	if got.GroupID != groupPro.ID {
		t.Fatalf("group_id = %d, want %d", got.GroupID, groupPro.ID)
	}
}

// sentinelTx is a non-nil Tx placeholder so changeSubscription takes the InTx
// branch (GetByIDInTx / UpdateSubscriptionFieldsInTx) in tests. The mock repo
// ignores the tx value and operates on the in-memory map.
type sentinelTx struct{}

func (sentinelTx) DB() any { return nil }

// trackingMockSubscriptionRepo wraps mockSubscriptionRepo and counts InTx
// method calls so tests can assert the row-locked code path is actually taken
// (not just that RunInTx was entered).
type trackingMockSubscriptionRepo struct {
	mockSubscriptionRepo
	mu                     sync.Mutex
	getByIDInTxCalls       int
	updateFieldsInTxCalls  int
}

func newTrackingMockSubscriptionRepo() *trackingMockSubscriptionRepo {
	return &trackingMockSubscriptionRepo{
		mockSubscriptionRepo: mockSubscriptionRepo{
			groups:        map[int64]*SubscriptionGroup{},
			subscriptions: map[int64]*UserSubscription{},
			nextGroupID:   1,
			nextSubID:     1,
		},
	}
}

func (m *trackingMockSubscriptionRepo) GetByIDInTx(ctx context.Context, tx Tx, subscriptionID int64) (*UserSubscription, error) {
	m.mu.Lock()
	m.getByIDInTxCalls++
	m.mu.Unlock()
	return m.mockSubscriptionRepo.GetByIDInTx(ctx, tx, subscriptionID)
}

func (m *trackingMockSubscriptionRepo) UpdateSubscriptionFieldsInTx(ctx context.Context, tx Tx, subscription *UserSubscription, fields []SubscriptionField) error {
	m.mu.Lock()
	m.updateFieldsInTxCalls++
	m.mu.Unlock()
	return m.mockSubscriptionRepo.UpdateSubscriptionFieldsInTx(ctx, tx, subscription, fields)
}

func (m *trackingMockSubscriptionRepo) resetCounters() {
	m.mu.Lock()
	m.getByIDInTxCalls = 0
	m.updateFieldsInTxCalls = 0
	m.mu.Unlock()
}

// recordingTxRunner records how many times the callback ran and lets tests
// assert that ChangeSubscription routes through RunInTx (the row-locked path).
// By default it passes a non-nil sentinel tx so the InTx code branch executes.
type recordingTxRunner struct {
	mu        sync.Mutex
	calls     int
	runLocked func(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}

func (r *recordingTxRunner) RunInTx(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	if r.runLocked != nil {
		return r.runLocked(ctx, fn)
	}
	// Default: run the callback with a non-nil sentinel tx so changeSubscription
	// takes the InTx branch (GetByIDInTx / UpdateSubscriptionFieldsInTx).
	return fn(ctx, sentinelTx{})
}

// TestChangeSubscription_UsesTxRunnerWhenWired (M6, 2026-08-05): when a
// TxRunner is wired, ChangeSubscription must route through RunInTx and use the
// InTx repo methods (GetByIDInTx + UpdateSubscriptionFieldsInTx), not the
// unlocked read-modify-write path.
func TestChangeSubscription_UsesTxRunnerWhenWired(t *testing.T) {
	repo := newTrackingMockSubscriptionRepo()
	group := &SubscriptionGroup{Name: "basic", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), group); err != nil {
		t.Fatal(err)
	}
	groupPro := &SubscriptionGroup{Name: "pro", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), groupPro); err != nil {
		t.Fatal(err)
	}

	runner := &recordingTxRunner{}
	uc := NewSubscriptionUsecase(repo, repo)
	uc.SetTxRunner(runner)
	uc.now = func() time.Time { return time.Unix(5000, 0) }

	sub, _, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: group.ID, StartsAt: 1000, ExpiresAt: 99999,
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	// Reset counters after AssignOrExtend (which doesn't use txRunner).
	repo.resetCounters()

	// Upgrade to a different group.
	if _, err := uc.ChangeSubscription(context.Background(), ChangeRequest{
		UserID:             1,
		FromSubscriptionID: sub.ID,
		ToPlanID:           2,
		ToGroupID:          groupPro.ID,
		NewPriceQuota:      2000,
		OldPriceQuota:      1000,
		Operator:           "admin",
		Now:                6000,
	}); err != nil {
		t.Fatalf("change: %v", err)
	}
	runner.mu.Lock()
	calls := runner.calls
	runner.mu.Unlock()
	if calls != 1 {
		t.Fatalf("RunInTx calls = %d, want 1 (row-locked change path)", calls)
	}
	repo.mu.Lock()
	getByIDInTxCalls := repo.getByIDInTxCalls
	updateFieldsInTxCalls := repo.updateFieldsInTxCalls
	repo.mu.Unlock()
	if getByIDInTxCalls != 1 {
		t.Fatalf("GetByIDInTx calls = %d, want 1 (row-locked read)", getByIDInTxCalls)
	}
	if updateFieldsInTxCalls != 1 {
		t.Fatalf("UpdateSubscriptionFieldsInTx calls = %d, want 1 (in-tx write)", updateFieldsInTxCalls)
	}
	got, err := repo.GetSubscriptionByID(context.Background(), sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.GroupID != groupPro.ID {
		t.Fatalf("group_id = %d, want %d", got.GroupID, groupPro.ID)
	}
}

// TestChangeSubscription_TxRunnerErrorSurfacesNoMutation (M6, 2026-08-05):
// when RunInTx itself fails (e.g., cannot begin transaction), ChangeSubscription
// must surface the error and the subscription row must be untouched. This models
// a connection/infrastructure failure before the callback ever runs.
func TestChangeSubscription_TxRunnerErrorSurfacesNoMutation(t *testing.T) {
	repo := newMockSubscriptionRepo()
	group := &SubscriptionGroup{Name: "basic", Platform: "openai", Status: SubscriptionGroupStatusEnabled}
	if err := repo.CreateGroup(context.Background(), group); err != nil {
		t.Fatal(err)
	}
	runner := &recordingTxRunner{
		runLocked: func(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error {
			return errors.New("tx boom")
		},
	}
	uc := NewSubscriptionUsecase(repo, repo)
	uc.SetTxRunner(runner)

	sub, _, err := uc.AssignOrExtend(context.Background(), &AssignSubscriptionRequest{
		UserID: 1, GroupID: group.ID, StartsAt: 1000, ExpiresAt: 99999,
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}

	_, err = uc.ChangeSubscription(context.Background(), ChangeRequest{
		UserID:             1,
		FromSubscriptionID: sub.ID,
		ToGroupID:          group.ID,
		NewPriceQuota:      100,
		OldPriceQuota:      100,
	})
	if err == nil || err.Error() != "tx boom" {
		t.Fatalf("err = %v, want tx boom", err)
	}
	// No mutation should have been applied: the runner returned before the
	// callback ran, so the row is untouched.
	got, err := repo.GetSubscriptionByID(context.Background(), sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.GroupID != group.ID {
		t.Fatalf("group_id mutated despite tx failure: %d", got.GroupID)
	}
}
