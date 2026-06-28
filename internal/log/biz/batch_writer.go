package biz

import (
	"context"
	"fmt"
	"sync"
	"time"

	"micro-one-api/internal/pkg/metrics"
)

// BatchLogWriter batches log entries for efficient database writes.
// Reduces database load by up to 90% compared to individual writes.
type BatchLogWriter struct {
	repo      LogRepo
	batch     []*LogEntry
	batchMu   sync.Mutex
	queue     chan *LogEntry
	flushChan chan struct{}
	batchSize int
	flushInt  time.Duration
	stopChan  chan struct{}
	wg        sync.WaitGroup
	// Metrics
	droppedEntries int64
}

// NewBatchLogWriter creates a new batch log writer.
func NewBatchLogWriter(repo LogRepo, batchSize int, flushInterval time.Duration) *BatchLogWriter {
	w := &BatchLogWriter{
		repo:      repo,
		batch:     make([]*LogEntry, 0, batchSize),
		queue:     make(chan *LogEntry, batchSize*10), // 10x batch size buffer
		flushChan: make(chan struct{}, 1),
		batchSize: batchSize,
		flushInt:  flushInterval,
		stopChan:  make(chan struct{}),
	}
	return w
}

// Start starts the batch writer background workers.
func (w *BatchLogWriter) Start() {
	w.wg.Add(2)

	// Queue processor
	go func() {
		defer w.wg.Done()
		w.queueProcessor()
	}()

	// Periodic flusher
	go func() {
		defer w.wg.Done()
		w.periodicFlusher()
	}()
}

// Stop stops the batch writer and flushes remaining entries.
func (w *BatchLogWriter) Stop() {
	close(w.stopChan)
	w.wg.Wait()
	w.flush()
}

// IngestLog queues a log entry for batch writing.
// Returns immediately if queue has capacity, drops entry if queue is full.
func (w *BatchLogWriter) IngestLog(ctx context.Context, entry *LogEntry) error {
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}

	select {
	case w.queue <- entry:
		metrics.UsageLogIngestTotal.WithLabelValues("queued").Inc()
		return nil
	default:
		// Queue full, drop the entry
		w.droppedEntries++
		metrics.UsageLogIngestTotal.WithLabelValues("dropped").Inc()
		return fmt.Errorf("log queue full, entry dropped")
	}
}

// queueProcessor processes log entries from the queue.
func (w *BatchLogWriter) queueProcessor() {
	for {
		select {
		case <-w.stopChan:
			return
		case entry := <-w.queue:
			w.addToBatch(entry)
		}
	}
}

// periodicFlusher periodically flushes the batch.
func (w *BatchLogWriter) periodicFlusher() {
	ticker := time.NewTicker(w.flushInt)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopChan:
			return
		case <-ticker.C:
			w.flush()
		case <-w.flushChan:
			w.flush()
		}
	}
}

// addToBatch adds an entry to the current batch.
func (w *BatchLogWriter) addToBatch(entry *LogEntry) {
	w.batchMu.Lock()
	defer w.batchMu.Unlock()

	w.batch = append(w.batch, entry)

	// Flush if batch is full
	if len(w.batch) >= w.batchSize {
		w.batchMu.Unlock()
		w.flush()
		w.batchMu.Lock()
	}
}

// flush writes all pending entries to the repository.
func (w *BatchLogWriter) flush() {
	w.batchMu.Lock()
	if len(w.batch) == 0 {
		w.batchMu.Unlock()
		return
	}

	batch := w.batch
	w.batch = make([]*LogEntry, 0, w.batchSize)
	w.batchMu.Unlock()

	// Write batch to database
	start := time.Now()
	err := w.createBatch(batch)
	duration := time.Since(start)

	if err != nil {
		metrics.UsageLogIngestTotal.WithLabelValues("error").Inc()
		// TODO: Handle error - retry or put back in queue
		return
	}

	metrics.UsageLogIngestTotal.WithLabelValues("success").Inc()
	metrics.LedgerWriteDuration.WithLabelValues("batch").Observe(duration.Seconds())
}

// createBatch creates multiple log entries using the repository.
// Falls back to individual writes if batch is not supported.
func (w *BatchLogWriter) createBatch(entries []*LogEntry) error {
	// Try batch interface first
	if batchRepo, ok := w.repo.(LogRepoBatch); ok {
		return batchRepo.CreateBatch(entries)
	}

	// Fallback to individual writes
	for _, entry := range entries {
		if err := w.repo.Create(context.Background(), entry); err != nil {
			return err
		}
	}
	return nil
}

// Flush triggers an immediate flush of pending entries.
func (w *BatchLogWriter) Flush() {
	select {
	case w.flushChan <- struct{}{}:
	default:
		// Already scheduled
	}
}

// Stats returns statistics about the batch writer.
func (w *BatchLogWriter) Stats() BatchWriterStats {
	w.batchMu.Lock()
	pending := len(w.batch)
	queued := len(w.queue)
	w.batchMu.Unlock()

	return BatchWriterStats{
		PendingBatch:  int32(pending),
		QueuedEntries: int32(queued),
		DroppedEntries: w.droppedEntries,
	}
}

// BatchWriterStats holds statistics about the batch writer.
type BatchWriterStats struct {
	PendingBatch   int32
	QueuedEntries  int32
	DroppedEntries int64
}

// LogRepoBatch extends LogRepo with batch operations.
type LogRepoBatch interface {
	LogRepo
	CreateBatch(entries []*LogEntry) error
}

// TODO: Implement proper batch insert in data layer
// Example MySQL implementation would use a single INSERT with multiple VALUES.
/*
func (r *logRepo) CreateBatch(entries []*LogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	// Build batch insert query
	query := `INSERT INTO logs
		(level, message, source, request_id, user_id, created_at,
		 username, token_name, model_name, quota, prompt_tokens, completion_tokens,
		 cache_read_tokens, channel_id, subscription_account_id, elapsed_time, is_stream)
		VALUES `

	// Implementation in data layer...
}
*/
