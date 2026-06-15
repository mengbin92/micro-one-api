package data

import (
	"context"
	"testing"
	"time"

	"micro-one-api/internal/notify/biz"
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

	if err := repo.RecordFailure(context.Background(), n.ID, 2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := repo.Get(context.Background(), n.ID)
	if got.Status != biz.NotifyStatusPending || got.RetryCount != 1 {
		t.Fatalf("expected pending retry 1, got status=%s retry=%d", got.Status, got.RetryCount)
	}

	if err := repo.RecordFailure(context.Background(), n.ID, 2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ = repo.Get(context.Background(), n.ID)
	if got.Status != biz.NotifyStatusFailed || got.RetryCount != 2 {
		t.Fatalf("expected failed retry 2, got status=%s retry=%d", got.Status, got.RetryCount)
	}
}
