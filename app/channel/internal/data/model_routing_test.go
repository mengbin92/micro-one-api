package data

import (
	"context"
	"testing"

	"micro-one-api/app/channel/internal/biz"
)

func TestRepository_ModelRoutings_Database(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()

	// Upsert two routings (one exact, one wildcard).
	r1 := &biz.ModelRouting{GroupName: "default", Model: "gpt-5", Platform: "", SubscriptionAccountID: 2, Enabled: true, Priority: 0}
	if err := repo.UpsertModelRouting(ctx, r1); err != nil {
		t.Fatalf("UpsertModelRouting r1: %v", err)
	}
	if r1.ID == 0 {
		t.Fatal("expected non-zero id after upsert")
	}
	r2 := &biz.ModelRouting{GroupName: "default", Model: "claude-*", Platform: "", SubscriptionAccountID: 3, Enabled: true, Priority: 0}
	if err := repo.UpsertModelRouting(ctx, r2); err != nil {
		t.Fatalf("UpsertModelRouting r2: %v", err)
	}

	// List all routings for the group.
	rows, err := repo.ListModelRoutings(ctx, "default", "", "")
	if err != nil {
		t.Fatalf("ListModelRoutings: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 routings, got %d", len(rows))
	}

	// ForSelect returns enabled rows ordered exact-first.
	sel, err := repo.ListModelRoutingsForSelect(ctx, "default", "gpt-5", "")
	if err != nil {
		t.Fatalf("ListModelRoutingsForSelect: %v", err)
	}
	if len(sel) != 2 {
		t.Fatalf("expected 2 rows for select, got %d", len(sel))
	}
	// Exact ("gpt-5", non-pattern) must come before wildcard ("claude-*").
	if sel[0].Model != "gpt-5" {
		t.Fatalf("expected exact row first, got %q", sel[0].Model)
	}

	// Update via upsert (same unique key) flips enabled.
	r1.Enabled = false
	if err := repo.UpsertModelRouting(ctx, r1); err != nil {
		t.Fatalf("UpsertModelRouting update: %v", err)
	}
	sel2, _ := repo.ListModelRoutingsForSelect(ctx, "default", "gpt-5", "")
	for _, r := range sel2 {
		if r.ID == r1.ID && r.Enabled {
			t.Fatal("r1 should be disabled after update")
		}
	}

	// Delete.
	if err := repo.DeleteModelRouting(ctx, r1.ID); err != nil {
		t.Fatalf("DeleteModelRouting: %v", err)
	}
	if err := repo.DeleteModelRouting(ctx, r1.ID); err != biz.ErrModelRoutingNotFound {
		t.Fatalf("second delete want ErrModelRoutingNotFound, got %v", err)
	}
}

func TestRepository_ModelRoutings_Memory(t *testing.T) {
	repo := newMemoryRepository()
	ctx := context.Background()

	r := &biz.ModelRouting{GroupName: "default", Model: "claude-*", SubscriptionAccountID: 5, Enabled: true}
	if err := repo.UpsertModelRouting(ctx, r); err != nil {
		t.Fatalf("UpsertModelRouting: %v", err)
	}
	rows, err := repo.ListModelRoutings(ctx, "default", "", "")
	if err != nil {
		t.Fatalf("ListModelRoutings: %v", err)
	}
	if len(rows) != 1 || rows[0].SubscriptionAccountID != 5 {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	// ForSelect filters by enabled.
	sel, _ := repo.ListModelRoutingsForSelect(ctx, "default", "claude-sonnet-4", "")
	if len(sel) != 1 {
		t.Fatalf("expected 1 enabled row for select, got %d", len(sel))
	}
	// Delete.
	if err := repo.DeleteModelRouting(ctx, r.ID); err != nil {
		t.Fatalf("DeleteModelRouting: %v", err)
	}
}
