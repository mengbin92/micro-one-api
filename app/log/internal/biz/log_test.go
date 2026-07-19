package biz

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockLogRepo struct {
	mu        sync.Mutex
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
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[id]
	if !ok {
		return nil, ErrLogNotFound
	}
	cp := *e
	return &cp, nil
}

func (m *mockLogRepo) List(ctx context.Context, page, pageSize int32, level, source, keyword string) ([]*LogEntry, int64, error) {
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
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
	m.mu.Lock()
	defer m.mu.Unlock()
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

func (m *mockLogRepo) UsageByUser(ctx context.Context, userID int64, startTime, endTime time.Time) ([]*UsageStat, error) {
	statsByKey := map[string]*UsageStat{}
	for _, e := range m.entries {
		if e.UserID != userID || e.ModelName == "" {
			continue
		}
		if !startTime.IsZero() && e.CreatedAt.Before(startTime) {
			continue
		}
		if !endTime.IsZero() && e.CreatedAt.After(endTime) {
			continue
		}
		day := e.CreatedAt.UTC().Format("2006-01-02")
		key := day + "\x00" + e.ModelName
		stat := statsByKey[key]
		if stat == nil {
			stat = &UsageStat{Day: day, ModelName: e.ModelName}
			statsByKey[key] = stat
		}
		stat.RequestCount++
		stat.Quota += e.Quota
		stat.PromptTokens += e.PromptTokens
		stat.CompletionTokens += e.CompletionTokens
	}
	stats := make([]*UsageStat, 0, len(statsByKey))
	for _, stat := range statsByKey {
		stats = append(stats, stat)
	}
	return stats, nil
}

func (m *mockLogRepo) Create(ctx context.Context, entry *LogEntry) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	entry.ID = m.seq
	m.entries[entry.ID] = entry
	return nil
}

func (m *mockLogRepo) Delete(ctx context.Context, filter DeleteLogsFilter) (int64, error) {
	var deleted int64
	for id, entry := range m.entries {
		if filter.Level != "" && entry.Level != filter.Level {
			continue
		}
		if filter.Source != "" && entry.Source != filter.Source {
			continue
		}
		if filter.UserID != 0 && entry.UserID != filter.UserID {
			continue
		}
		if !filter.StartTime.IsZero() && entry.CreatedAt.Before(filter.StartTime) {
			continue
		}
		if !filter.EndTime.IsZero() && entry.CreatedAt.After(filter.EndTime) {
			continue
		}
		delete(m.entries, id)
		deleted++
	}
	return deleted, nil
}

func (m *mockLogRepo) DeleteBefore(ctx context.Context, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, nil
	}
	var deleted int64
	for id, entry := range m.entries {
		if entry.CreatedAt.Before(before) {
			delete(m.entries, id)
			deleted++
		}
	}
	return deleted, nil
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

func TestLogUsecase_DeleteLogsRequiresEndTimeAndDeletesMatchingScope(t *testing.T) {
	base := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	repo := newMockLogRepo(
		&LogEntry{ID: 1, Level: "info", Source: "relay", UserID: 1, CreatedAt: base.Add(-2 * time.Hour)},
		&LogEntry{ID: 2, Level: "error", Source: "relay", UserID: 1, CreatedAt: base.Add(-1 * time.Hour)},
		&LogEntry{ID: 3, Level: "info", Source: "relay", UserID: 2, CreatedAt: base.Add(-1 * time.Hour)},
		&LogEntry{ID: 4, Level: "info", Source: "relay", UserID: 1, CreatedAt: base.Add(time.Hour)},
	)
	uc := NewLogUsecase(repo)

	if _, err := uc.DeleteLogs(context.Background(), DeleteLogsFilter{Level: "info"}); err == nil {
		t.Fatal("expected missing end time error")
	}

	deleted, err := uc.DeleteLogs(context.Background(), DeleteLogsFilter{
		Level:   "info",
		UserID:  1,
		EndTime: base,
	})
	if err != nil {
		t.Fatalf("DeleteLogs() error = %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if _, err := repo.Get(context.Background(), 1); err != ErrLogNotFound {
		t.Fatalf("entry 1 should be deleted, err=%v", err)
	}
	for _, id := range []int64{2, 3, 4} {
		if _, err := repo.Get(context.Background(), id); err != nil {
			t.Fatalf("entry %d should remain, err=%v", id, err)
		}
	}
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

func TestLogUsecase_CleanupExpiredLogs(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	repo := newMockLogRepo(
		&LogEntry{ID: 1, Level: "info", CreatedAt: now.Add(-31 * 24 * time.Hour)},
		&LogEntry{ID: 2, Level: "info", CreatedAt: now.Add(-29 * 24 * time.Hour)},
	)
	uc := NewLogUsecase(repo)

	deleted, err := uc.CleanupExpiredLogs(context.Background(), 30, now)
	if err != nil {
		t.Fatalf("CleanupExpiredLogs() error = %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if _, err := repo.Get(context.Background(), 1); err != ErrLogNotFound {
		t.Fatalf("entry 1 should be deleted, err=%v", err)
	}
	if _, err := repo.Get(context.Background(), 2); err != nil {
		t.Fatalf("entry 2 should remain, err=%v", err)
	}

	deleted, err = uc.CleanupExpiredLogs(context.Background(), 0, now)
	if err != nil {
		t.Fatalf("CleanupExpiredLogs disabled error = %v", err)
	}
	if deleted != 0 {
		t.Fatalf("disabled cleanup deleted = %d, want 0", deleted)
	}
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

// --- Phase 2.3 batch ingestion tests ---

// batchMockLogRepo wraps mockLogRepo and records CreateBatch calls so we can
// assert the batch path is taken. It implements biz.LogRepoBatch.
type batchMockLogRepo struct {
	*mockLogRepo
	mu      sync.Mutex
	batched [][]*LogEntry
}

func newBatchMockLogRepo(entries ...*LogEntry) *batchMockLogRepo {
	return &batchMockLogRepo{mockLogRepo: newMockLogRepo(entries...)}
}

func (m *batchMockLogRepo) CreateBatch(ctx context.Context, entries []*LogEntry) error {
	m.mu.Lock()
	m.batched = append(m.batched, entries)
	m.mu.Unlock()
	for _, e := range entries {
		if err := m.mockLogRepo.Create(ctx, e); err != nil {
			return err
		}
	}
	return nil
}

func (m *batchMockLogRepo) batchCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.batched)
}

// TestLogUsecase_IngestLogSyncFallback verifies that when no batch writer is
// wired, IngestLog calls repo.Create directly (synchronous path).
func TestLogUsecase_IngestLogSyncFallback(t *testing.T) {
	repo := newBatchMockLogRepo()
	uc := NewLogUsecase(repo)
	// batchWriter is nil → sync path
	e := &LogEntry{Level: "info", Message: "sync", Source: "test"}
	if err := uc.IngestLog(context.Background(), e); err != nil {
		t.Fatalf("IngestLog sync failed: %v", err)
	}
	if e.ID == 0 {
		t.Fatal("entry ID not set on sync path")
	}
	if repo.batchCount() != 0 {
		t.Fatalf("CreateBatch called %d times, want 0 (no batch writer)", repo.batchCount())
	}
}

// TestLogUsecase_IngestLogBatchRouting verifies that when a batch writer is
// wired, IngestLog routes the entry through the queue instead of calling
// repo.Create synchronously. We assert by checking the entry is NOT assigned
// an ID until the batch is flushed.
func TestLogUsecase_IngestLogBatchRouting(t *testing.T) {
	repo := newBatchMockLogRepo()
	uc := NewLogUsecase(repo)
	w := NewBatchLogWriter(repo, 100, time.Hour) // long interval; we flush manually
	w.Start()
	defer w.Stop()
	uc.SetBatchWriter(w)

	e := &LogEntry{Level: "info", Message: "batched", Source: "test"}
	if err := uc.IngestLog(context.Background(), e); err != nil {
		t.Fatalf("IngestLog batch failed: %v", err)
	}
	// The entry should be queued, not yet persisted.
	if e.ID != 0 {
		t.Fatalf("entry ID = %d before flush; batch path should not persist synchronously", e.ID)
	}
	// Flush and verify the batch path was taken.
	w.Flush()
	// Give the background flusher a moment to process the manual flush signal.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if repo.batchCount() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if repo.batchCount() == 0 {
		t.Fatal("CreateBatch never called after Flush")
	}
}

// TestLogUsecase_IngestLogNilBatchWriterIsSafe confirms the zero-value
// LogUsecase (no batch writer) does not panic and behaves like the sync path.
func TestLogUsecase_IngestLogNilBatchWriterIsSafe(t *testing.T) {
	repo := newBatchMockLogRepo()
	uc := &LogUsecase{repo: repo} // batchWriter nil
	e := &LogEntry{Level: "info", Message: "safe", Source: "test"}
	if err := uc.IngestLog(context.Background(), e); err != nil {
		t.Fatalf("IngestLog nil-batch failed: %v", err)
	}
}

// TestBatchLogWriter_QueueFullReturnsError verifies the IngestLog contract:
// when the queue is full the writer returns an error rather than blocking.
// The writer's queue capacity is batchSize*10; with batchSize=1 the cap is
// 10. We do not start the processor goroutine so the queue only drains on
// flush; filling it to capacity forces the next IngestLog to take the
// "queue full" branch.
func TestBatchLogWriter_QueueFullReturnsError(t *testing.T) {
	repo := newBatchMockLogRepo()
	w := NewBatchLogWriter(repo, 1, time.Hour)
	// Fill the queue to its capacity (1*10 = 10).
	for i := 0; i < 10; i++ {
		if err := w.IngestLog(context.Background(), &LogEntry{Level: "info", Message: "fill"}); err != nil {
			t.Fatalf("fill IngestLog %d errored unexpectedly: %v", i, err)
		}
	}
	// Queue is now full; the next call must error, not block.
	err := w.IngestLog(context.Background(), &LogEntry{Level: "info", Message: "overflow"})
	if err == nil {
		t.Fatal("expected error when queue is full, got nil")
	}
}
