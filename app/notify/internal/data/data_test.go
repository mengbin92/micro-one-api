package data

import (
	"context"
	"testing"
	"time"

	"micro-one-api/app/notify/internal/biz"
)

func TestMemoryRepository_CreateAndGet(t *testing.T) {
	repo := newMemoryRepository()

	n := &biz.Notification{
		Type:      biz.NotifyTypeWebhook,
		Recipient: "https://example.com/hook",
		Subject:   "alert",
		Content:   "service down",
		Status:    biz.NotifyStatusPending,
		CreatedAt: time.Now(),
	}

	err := repo.Create(context.Background(), n)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n.ID == 0 {
		t.Fatal("expected ID to be assigned")
	}

	got, err := repo.Get(context.Background(), n.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Subject != "alert" {
		t.Fatalf("expected alert, got %s", got.Subject)
	}
}

func TestMemoryRepository_GetNotFound(t *testing.T) {
	repo := newMemoryRepository()
	_, err := repo.Get(context.Background(), 999)
	if err != biz.ErrNotificationNotFound {
		t.Fatalf("expected ErrNotificationNotFound, got %v", err)
	}
}

func TestMemoryRepository_List(t *testing.T) {
	repo := newMemoryRepository()
	_ = repo.Create(context.Background(), &biz.Notification{Type: biz.NotifyTypeWebhook, Recipient: "a", Status: biz.NotifyStatusPending, CreatedAt: time.Now()})
	_ = repo.Create(context.Background(), &biz.Notification{Type: biz.NotifyTypeEmail, Recipient: "b", Status: biz.NotifyStatusSent, CreatedAt: time.Now()})

	t.Run("all", func(t *testing.T) {
		items, total, err := repo.List(context.Background(), 1, 20, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// 1 from default + 2 created
		if total != 3 {
			t.Fatalf("expected 3, got %d", total)
		}
		if len(items) != 3 {
			t.Fatalf("expected 3, got %d", len(items))
		}
	})

	t.Run("filter by type", func(t *testing.T) {
		items, total, err := repo.List(context.Background(), 1, 20, biz.NotifyTypeEmail, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected 1, got %d", total)
		}
		if len(items) != 1 || items[0].Type != biz.NotifyTypeEmail {
			t.Fatalf("unexpected items: %+v", items)
		}
	})

	t.Run("filter by status", func(t *testing.T) {
		items, total, err := repo.List(context.Background(), 1, 20, "", biz.NotifyStatusPending)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected 1, got %d", total)
		}
		if len(items) != 1 || items[0].Status != biz.NotifyStatusPending {
			t.Fatalf("unexpected items: %+v", items)
		}
	})
}

func TestMemoryRepository_UpdateStatus(t *testing.T) {
	repo := newMemoryRepository()

	n := &biz.Notification{
		Type:      biz.NotifyTypeWebhook,
		Recipient: "https://example.com",
		Status:    biz.NotifyStatusPending,
		CreatedAt: time.Now(),
	}
	_ = repo.Create(context.Background(), n)

	t.Run("mark sent", func(t *testing.T) {
		err := repo.UpdateStatus(context.Background(), n.ID, biz.NotifyStatusSent)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, _ := repo.Get(context.Background(), n.ID)
		if got.Status != biz.NotifyStatusSent {
			t.Fatalf("expected sent, got %s", got.Status)
		}
		if got.SentAt.IsZero() {
			t.Fatal("expected SentAt to be set")
		}
	})

	t.Run("not found", func(t *testing.T) {
		err := repo.UpdateStatus(context.Background(), 999, biz.NotifyStatusSent)
		if err != biz.ErrNotificationNotFound {
			t.Fatalf("expected ErrNotificationNotFound, got %v", err)
		}
	})
}

func TestMemoryRepository_ListPendingAndRecordFailure(t *testing.T) {
	repo := newMemoryRepository()
	n := &biz.Notification{
		Type:      biz.NotifyTypeWebhook,
		Recipient: "https://example.com",
		Status:    biz.NotifyStatusPending,
		CreatedAt: time.Now(),
	}
	_ = repo.Create(context.Background(), n)

	items, err := repo.ListPending(context.Background(), 20, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 || items[0].ID != n.ID {
		t.Fatalf("expected created pending notification, got %+v", items)
	}

	if err := repo.RecordFailure(context.Background(), n.ID, 2, "webhook returned status 500"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := repo.Get(context.Background(), n.ID)
	if got.Status != biz.NotifyStatusPending || got.RetryCount != 1 || got.LastError != "webhook returned status 500" {
		t.Fatalf("expected pending retry 1 with error, got status=%s retry=%d error=%q", got.Status, got.RetryCount, got.LastError)
	}

	if err := repo.RecordFailure(context.Background(), n.ID, 2, "timeout"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ = repo.Get(context.Background(), n.ID)
	if got.Status != biz.NotifyStatusFailed || got.RetryCount != 2 || got.LastError != "timeout" {
		t.Fatalf("expected failed retry 2 with error, got status=%s retry=%d error=%q", got.Status, got.RetryCount, got.LastError)
	}

	if err := repo.UpdateStatus(context.Background(), n.ID, biz.NotifyStatusSent); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ = repo.Get(context.Background(), n.ID)
	if got.LastError != "" {
		t.Fatalf("expected last error to be cleared after sent, got %q", got.LastError)
	}
}

func TestMemoryRepository_ClaimAndCompleteProcessingLease(t *testing.T) {
	repo := newMemoryRepository()
	n := &biz.Notification{
		Type:      biz.NotifyTypeWebhook,
		Recipient: "https://example.com",
		Status:    biz.NotifyStatusPending,
		CreatedAt: time.Now(),
	}
	if err := repo.Create(context.Background(), n); err != nil {
		t.Fatal(err)
	}

	claimed, err := repo.ClaimPending(context.Background(), 20, 3)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimPending() = %d, %v; want one claim", len(claimed), err)
	}
	if _, err := repo.ClaimPending(context.Background(), 20, 3); err != nil {
		t.Fatal(err)
	} else if got, _ := repo.Get(context.Background(), n.ID); got.Status != biz.NotifyStatusProcessing {
		t.Fatalf("second claim changed status to %q", got.Status)
	}

	stale := claimed[0].ProcessingAt.Add(-time.Second)
	if err := repo.CompleteProcessing(context.Background(), n.ID, stale, biz.NotifyStatusSent, 3, ""); err != biz.ErrNotificationLeaseLost {
		t.Fatalf("stale completion error = %v, want ErrNotificationLeaseLost", err)
	}
	if err := repo.CompleteProcessing(context.Background(), n.ID, claimed[0].ProcessingAt, biz.NotifyStatusSent, 3, ""); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Get(context.Background(), n.ID)
	if got.Status != biz.NotifyStatusSent || !got.ProcessingAt.IsZero() {
		t.Fatalf("completion = status %q processing_at %v", got.Status, got.ProcessingAt)
	}
}
