package biz

import (
	"context"
	"strings"
	"testing"
	"time"
)

type mockLogRepo struct {
	entries   map[int64]*LogEntry
	seq       int64
	getErr    error
	createErr error
	listErr   error
}

func (m *mockLogRepo) Get(ctx context.Context, id int64) (*LogEntry, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	e, ok := m.entries[id]
	if !ok {
		return nil, ErrLogNotFound
	}
	return e, nil
}

func (m *mockLogRepo) List(ctx context.Context, page, pageSize int32, level, source, keyword string) ([]*LogEntry, int64, error) {
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	var result []*LogEntry
	for _, e := range m.entries {
		if level != "" && e.Level != level {
			continue
		}
		if source != "" && e.Source != source {
			continue
		}
		result = append(result, e)
	}
	total := int64(len(result))
	start := int((page - 1) * pageSize)
	if start >= len(result) {
		return nil, total, nil
	}
	end := start + int(pageSize)
	if end > len(result) {
		end = len(result)
	}
	return result[start:end], total, nil
}

func (m *mockLogRepo) ListByUser(ctx context.Context, userID int64, page, pageSize int32, level, keyword string) ([]*LogEntry, int64, error) {
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	var result []*LogEntry
	for _, e := range m.entries {
		if e.UserID != userID {
			continue
		}
		if level != "" && e.Level != level {
			continue
		}
		if keyword != "" && !strings.Contains(e.Message, keyword) {
			continue
		}
		result = append(result, e)
	}
	total := int64(len(result))
	start := int((page - 1) * pageSize)
	if start >= len(result) {
		return nil, total, nil
	}
	end := start + int(pageSize)
	if end > len(result) {
		end = len(result)
	}
	return result[start:end], total, nil
}

func (m *mockLogRepo) Create(ctx context.Context, entry *LogEntry) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.seq++
	entry.ID = m.seq
	m.entries[entry.ID] = entry
	return nil
}

func newMockLogRepo(entries ...*LogEntry) *mockLogRepo {
	m := &mockLogRepo{entries: make(map[int64]*LogEntry)}
	for _, e := range entries {
		m.entries[e.ID] = e
		m.seq = e.ID
	}
	return m
}

func TestLogUsecase_GetLog(t *testing.T) {
	now := time.Now()
	repo := newMockLogRepo(&LogEntry{ID: 1, Level: "info", Message: "test", Source: "api", CreatedAt: now})
	uc := NewLogUsecase(repo)

	t.Run("success", func(t *testing.T) {
		e, err := uc.GetLog(context.Background(), 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e.Message != "test" {
			t.Fatalf("expected test, got %s", e.Message)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := uc.GetLog(context.Background(), 999)
		if err != ErrLogNotFound {
			t.Fatalf("expected ErrLogNotFound, got %v", err)
		}
	})
}

func TestLogUsecase_IngestLog(t *testing.T) {
	repo := newMockLogRepo()
	uc := NewLogUsecase(repo)

	t.Run("success", func(t *testing.T) {
		entry := &LogEntry{Level: "error", Message: "oops", Source: "relay"}
		err := uc.IngestLog(context.Background(), entry)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if entry.ID == 0 {
			t.Fatal("expected ID to be set")
		}
		if entry.CreatedAt.IsZero() {
			t.Fatal("expected CreatedAt to be set")
		}
	})

	t.Run("preserves existing CreatedAt", func(t *testing.T) {
		ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		entry := &LogEntry{Level: "info", Message: "old", Source: "api", CreatedAt: ts}
		_ = uc.IngestLog(context.Background(), entry)
		if !entry.CreatedAt.Equal(ts) {
			t.Fatalf("expected CreatedAt preserved, got %v", entry.CreatedAt)
		}
	})
}

func TestLogUsecase_ListLogs(t *testing.T) {
	repo := newMockLogRepo(
		&LogEntry{ID: 1, Level: "info", Message: "a", Source: "api"},
		&LogEntry{ID: 2, Level: "error", Message: "b", Source: "relay"},
		&LogEntry{ID: 3, Level: "info", Message: "c", Source: "api"},
	)
	uc := NewLogUsecase(repo)

	t.Run("all", func(t *testing.T) {
		entries, total, err := uc.ListLogs(context.Background(), 1, 50, "", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 3 {
			t.Fatalf("expected 3, got %d", total)
		}
		if len(entries) != 3 {
			t.Fatalf("expected 3 entries, got %d", len(entries))
		}
	})

	t.Run("filter by level", func(t *testing.T) {
		entries, total, err := uc.ListLogs(context.Background(), 1, 50, "error", "", "")
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

	t.Run("filter by source", func(t *testing.T) {
		entries, total, err := uc.ListLogs(context.Background(), 1, 50, "", "api", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 2 {
			t.Fatalf("expected 2, got %d", total)
		}
		if len(entries) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(entries))
		}
	})

	t.Run("normalizes page", func(t *testing.T) {
		_, total, err := uc.ListLogs(context.Background(), 0, 50, "", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 3 {
			t.Fatalf("expected 3, got %d", total)
		}
	})

	t.Run("normalizes pageSize", func(t *testing.T) {
		_, _, err := uc.ListLogs(context.Background(), 1, 0, "", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("pageSize capped at 200", func(t *testing.T) {
		_, _, err := uc.ListLogs(context.Background(), 1, 500, "", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
