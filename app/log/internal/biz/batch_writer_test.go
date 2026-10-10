package biz

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestBatchLogWriterConcurrentStopKeepsAcceptedEntries(t *testing.T) {
	for attempt := range 2000 {
		repo := newBatchMockLogRepo()
		writer := NewBatchLogWriter(repo, 100, time.Hour)
		writer.Start()
		start := make(chan struct{})
		var callers sync.WaitGroup
		for range 16 {
			callers.Go(func() {
				<-start
				if err := writer.IngestLog(context.Background(), &LogEntry{Message: "shutdown"}); err != nil {
					t.Errorf("IngestLog: %v", err)
				}
			})
		}
		close(start)
		writer.Stop()
		callers.Wait()
		repo.mockLogRepo.mu.Lock()
		stored := len(repo.entries)
		repo.mockLogRepo.mu.Unlock()
		if stored != 16 {
			t.Fatalf("attempt %d: persisted %d of 16 accepted entries", attempt, stored)
		}
	}
}

type blockingBatchLogRepo struct {
	*batchMockLogRepo
	started chan struct{}
	release chan struct{}
}

func (r *blockingBatchLogRepo) CreateBatch(ctx context.Context, entries []*LogEntry) error {
	close(r.started)
	<-r.release
	return r.batchMockLogRepo.CreateBatch(ctx, entries)
}

func TestBatchLogWriterFlushWaitsForActiveWrite(t *testing.T) {
	repo := &blockingBatchLogRepo{newBatchMockLogRepo(), make(chan struct{}), make(chan struct{})}
	writer := NewBatchLogWriter(repo, 100, time.Hour)
	writer.Start()
	if err := writer.IngestLog(context.Background(), &LogEntry{Message: "flush"}); err != nil {
		t.Fatal(err)
	}
	writer.inflight.Wait()
	backgroundDone := make(chan struct{})
	go func() {
		writer.flush()
		close(backgroundDone)
	}()
	<-repo.started
	done := make(chan struct{})
	defer func() {
		close(repo.release)
		<-backgroundDone
		<-done
		writer.Stop()
	}()
	go func() {
		writer.Flush()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("Flush returned before the accepted entry was persisted")
	case <-time.After(20 * time.Millisecond):
	}
}
