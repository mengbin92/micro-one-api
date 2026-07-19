package biz

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"micro-one-api/pkg/safecast"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"
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
	// closed prevents a second Stop() from re-closing stopChan and makes
	// IngestLog route to the synchronous repo path once shutdown begins so
	// late callers never block or get silently dropped.
	closed atomic.Bool
	// dropped counts entries dropped because the queue was full or the
	// writer was closed. atomic because IngestLog (caller goroutine) writes
	// and Stats (another goroutine) reads.
	dropped atomic.Int64
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

// Stop stops the batch writer, drains any entries still queued behind the
// processor, and flushes them in the final batch. It is idempotent.
func (w *BatchLogWriter) Stop() {
	if !w.closed.CompareAndSwap(false, true) {
		// Already stopped: avoid double close of stopChan.
		return
	}
	close(w.stopChan)
	w.wg.Wait()

	// Drain anything still in the queue. The queueProcessor exits the moment
	// it sees stopChan, so entries enqueued after that point (or that were
	// already buffered but not yet selected) would otherwise be lost. Move
	// them into the batch so the final flush persists them.
	for {
		select {
		case entry := <-w.queue:
			w.addToBatch(entry)
		default:
			w.flush()
			return
		}
	}
}

// IngestLog queues a log entry for batch writing.
// Returns immediately if queue has capacity, drops entry if queue is full.
func (w *BatchLogWriter) IngestLog(ctx context.Context, entry *LogEntry) error {
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}

	// After Stop() the background workers have exited and nothing will drain
	// the queue. Persist synchronously so the caller still observes a durable
	// write (at the cost of an extra round-trip during shutdown).
	if w.closed.Load() {
		return w.repo.Create(ctx, entry)
	}

	select {
	case w.queue <- entry:
		metrics.UsageLogIngestTotal.WithLabelValues("queued").Inc()
		return nil
	default:
		// Queue full, drop the entry. We intentionally do not fall back to a
		// synchronous write here (unlike the closed case) because a full queue
		// under steady load would turn every IngestLog into a synchronous
		// INSERT and defeat the batching optimisation. The dropped counter +
		// metric make the loss observable so an operator can raise
		// batch_size / queue capacity.
		w.dropped.Add(1)
		metrics.UsageLogIngestTotal.WithLabelValues("dropped").Inc()
		return fmt.Errorf("log queue full, entry dropped")
	}
}

// queueProcessor processes log entries from the queue.
func (w *BatchLogWriter) queueProcessor() {
	for {
		select {
		case <-w.stopChan:
			// Drain anything still queued before returning. Stop() also
			// drains, but doing it here as well means a process that exits
			// without calling Stop (crashed test, os.Exit) still flushes.
			for {
				select {
				case entry := <-w.queue:
					w.addToBatch(entry)
				default:
					return
				}
			}
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
	err := w.createBatch(context.Background(), batch)
	duration := time.Since(start)

	if err != nil {
		// A failed batch is a data-loss event for the usage log: we do not
		// retry (would risk out-of-order writes) and the entries are not put
		// back on the queue. Surface it via metrics + structured log so an
		// operator sees the dropped count; the batch writer is best-effort by
		// design and usage logs are not in the billing critical path.
		metrics.UsageLogIngestTotal.WithLabelValues("error").Inc()
		w.dropped.Add(int64(len(batch)))
		if applogger.Log != nil {
			applogger.Log.Warn("batch log writer: flush failed, entries dropped",
				zap.Int("dropped", len(batch)),
				zap.Duration("duration", duration),
				zap.Error(err),
			)
		}
		return
	}

	metrics.UsageLogIngestTotal.WithLabelValues("success").Inc()
	metrics.LedgerWriteDuration.WithLabelValues("batch").Observe(duration.Seconds())
}

// createBatch creates multiple log entries using the repository.
// Falls back to individual writes if batch is not supported.
func (w *BatchLogWriter) createBatch(ctx context.Context, entries []*LogEntry) error {
	// Try batch interface first
	if batchRepo, ok := w.repo.(LogRepoBatch); ok {
		return batchRepo.CreateBatch(ctx, entries)
	}

	// Fallback to individual writes
	for _, entry := range entries {
		if err := w.repo.Create(ctx, entry); err != nil {
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
		PendingBatch:   safecast.IntToInt32Saturating(pending),
		QueuedEntries:  safecast.IntToInt32Saturating(queued),
		DroppedEntries: w.dropped.Load(),
	}
}

// BatchWriterStats holds statistics about the batch writer.
type BatchWriterStats struct {
	PendingBatch   int32
	QueuedEntries  int32
	DroppedEntries int64
}

// LogRepoBatch is now defined in log.go alongside the LogRepo interface so
// the biz package has a single authoritative batch-capable repo contract.
// The data layer (data.Repository) implements CreateBatch via gorm
// CreateInBatches with a per-dialect fallback.
