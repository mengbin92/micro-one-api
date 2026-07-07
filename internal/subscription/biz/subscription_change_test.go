package biz

import (
	"context"
	"encoding/json"
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
