package data

import (
	"context"
	"testing"
	"time"

	"micro-one-api/internal/log/biz"
)

func TestMemoryRepository_CreateAndGet(t *testing.T) {
	repo := newMemoryRepository()

	entry := &biz.LogEntry{
		Level:     "error",
		Message:   "test error",
		Source:    "test",
		RequestID: "req-001",
		CreatedAt: time.Now(),
	}

	err := repo.Create(context.Background(), entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.ID == 0 {
		t.Fatal("expected ID to be assigned")
	}

	got, err := repo.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Message != "test error" {
		t.Fatalf("expected 'test error', got %s", got.Message)
	}
}

func TestMemoryRepository_GetNotFound(t *testing.T) {
	repo := newMemoryRepository()
	_, err := repo.Get(context.Background(), 999)
	if err != biz.ErrLogNotFound {
		t.Fatalf("expected ErrLogNotFound, got %v", err)
	}
}

func TestMemoryRepository_List(t *testing.T) {
	repo := newMemoryRepository()
	_ = repo.Create(context.Background(), &biz.LogEntry{Level: "info", Message: "connection established", Source: "s1", CreatedAt: time.Now()})
	_ = repo.Create(context.Background(), &biz.LogEntry{Level: "error", Message: "timeout error", Source: "s2", CreatedAt: time.Now()})

	t.Run("all", func(t *testing.T) {
		entries, total, err := repo.List(context.Background(), 1, 20, "", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// 1 from default + 2 created
		if total != 3 {
			t.Fatalf("expected 3, got %d", total)
		}
		if len(entries) != 3 {
			t.Fatalf("expected 3 entries, got %d", len(entries))
		}
	})

	t.Run("filter by level", func(t *testing.T) {
		entries, total, err := repo.List(context.Background(), 1, 20, "error", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected 1, got %d", total)
		}
		if len(entries) != 1 || entries[0].Level != "error" {
			t.Fatalf("unexpected entries: %+v", entries)
		}
	})

	t.Run("filter by keyword", func(t *testing.T) {
		entries, total, err := repo.List(context.Background(), 1, 20, "", "", "timeout")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected 1, got %d", total)
		}
		if len(entries) != 1 || entries[0].Message != "timeout error" {
			t.Fatalf("unexpected entries: %+v", entries)
		}
	})
}

func TestMemoryRepository_ListByUser(t *testing.T) {
	repo := newMemoryRepository()
	_ = repo.Create(context.Background(), &biz.LogEntry{Level: "info", Message: "user one request", Source: "relay", UserID: 1, CreatedAt: time.Now()})
	_ = repo.Create(context.Background(), &biz.LogEntry{Level: "error", Message: "user two failed", Source: "relay", UserID: 2, CreatedAt: time.Now()})
	_ = repo.Create(context.Background(), &biz.LogEntry{Level: "info", Message: "user two completed", Source: "relay", UserID: 2, CreatedAt: time.Now()})

	entries, total, err := repo.ListByUser(context.Background(), 2, 1, 20, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	for _, entry := range entries {
		if entry.UserID != 2 {
			t.Fatalf("entry user_id = %d, want 2", entry.UserID)
		}
	}
}

func TestMemoryRepository_ListByUserFiltersKeyword(t *testing.T) {
	repo := newMemoryRepository()
	_ = repo.Create(context.Background(), &biz.LogEntry{Level: "info", Message: "target message from user one", Source: "relay", UserID: 1, CreatedAt: time.Now()})
	_ = repo.Create(context.Background(), &biz.LogEntry{Level: "info", Message: "target message from user two", Source: "relay", UserID: 2, CreatedAt: time.Now()})
	_ = repo.Create(context.Background(), &biz.LogEntry{Level: "info", Message: "other message from user two", Source: "relay", UserID: 2, CreatedAt: time.Now()})

	entries, total, err := repo.ListByUser(context.Background(), 2, 1, 20, "", "target")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}
	if len(entries) != 1 || entries[0].UserID != 2 || entries[0].Message != "target message from user two" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}
