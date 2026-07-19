package biz

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"go.uber.org/zap"

	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"
)

// AsyncBillingUsecase provides a non-blocking billing path.
// It uses a local quota check + async settlement.
type AsyncBillingUsecase struct {
	syncUc       *BillingUsecase    // Fallback to sync billing
	localCache   *QuotaCache        // L1: in-memory quota snapshot
	redis        *redis.Client      // L2: distributed quota counter
	settleQueue  chan *SettleTask   // async settlement queue
	batchWriter  *BatchLedgerWriter // batch ledger persistence
	workerWg     sync.WaitGroup
	workerCtx    context.Context
	workerCancel context.CancelFunc
	// closed is set by Close() before workerCancel fires. Once true, Settle
	// stops enqueuing onto settleQueue (the worker is draining / has exited)
	// and falls back to the synchronous commit pipeline so no settlement is
	// silently lost when the process is shutting down. atomic so Settle (hot
	// path, many goroutines) reads it lock-free.
	closed         atomic.Bool
	quotaLuaScript *string // Cached Lua script
}

// QuotaCache provides fast local quota checking.
type QuotaCache struct {
	mu    sync.RWMutex
	quota map[int64]*UserQuota // userID → quota
}

// UserQuota represents a user's quota information.
type UserQuota struct {
	UserID     int64
	Available  int64 // Available amount in cents
	Frozen     int64 // Frozen/Reserved amount
	LastUpdate time.Time
}

// SettleTask represents a settlement task to be processed asynchronously.
// The task carries the full commit inputs so the background worker can run
// the complete CommitQuotaWithUsageAndSplit pipeline (reservation lifecycle,
// wallet settlement, ledger, subscription usage) instead of writing raw
// ledger entries that would bypass the reservation state machine.
type SettleTask struct {
	RequestID string
	// ReservationID is the authoritative key for the commit pipeline.
	ReservationID         string
	UserID                string
	Model                 string
	ChannelID             string
	SubscriptionAccountID int64
	ActualTokens          int64
	// Success mirrors CommitQuotaRequest.Success: false → release reservation.
	Success bool
	// Usage carries the per-request usage detail forwarded from the relay.
	Usage     LedgerUsage
	Cost      int64
	Timestamp time.Time
}

// BatchLedgerWriter batches ledger writes for efficiency. Entries are flushed
// to the provided LedgerRepo in batches; if no repo is configured, Flush
// returns an explicit error rather than silently dropping entries.
type BatchLedgerWriter struct {
	batch     []*LedgerEntry
	batchMu   sync.Mutex
	flushChan chan struct{}
	size      int
	interval  time.Duration
	stopChan  chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
	ledger    LedgerRepo // destination for flushed entries; may be nil

	// dropped counts entries dropped because no ledger repo is configured
	// (surfaces the misconfiguration in metrics instead of silent loss).
	dropped atomic.Int64
}

// LedgerEntry represents a single ledger entry.
type LedgerEntry struct {
	UserID                string
	ChannelID             string
	SubscriptionAccountID int64
	Model                 string
	TokenAmount           int64
	Cost                  int64
	CreatedAt             time.Time
}

// NewAsyncBillingUsecase creates a new async billing use case. The sync use
// case's ledger repo is wired into the batch writer so flushed entries are
// actually persisted (REVIEW_v1 P1-6).
func NewAsyncBillingUsecase(
	syncUc *BillingUsecase,
	redisClient *redis.Client,
	queueSize int,
	batchSize int,
	batchInterval time.Duration,
) *AsyncBillingUsecase {
	ctx, cancel := context.WithCancel(context.Background())

	bw := NewBatchLedgerWriter(batchSize, batchInterval)
	// Best-effort: pull the ledger repo from the sync use case. This keeps the
	// existing constructor signature stable; callers that need a different
	// destination can use SetLedgerRepo on the returned use case.
	if syncUc != nil {
		bw.SetLedgerRepo(syncUc.ledgerRepo)
	}

	uc := &AsyncBillingUsecase{
		syncUc:       syncUc,
		redis:        redisClient,
		localCache:   NewQuotaCache(),
		settleQueue:  make(chan *SettleTask, queueSize),
		batchWriter:  bw,
		workerCtx:    ctx,
		workerCancel: cancel,
	}

	// Start background workers
	uc.startWorkers()

	return uc
}

// SetLedgerRepo overrides the batch writer's destination ledger repo.
func (uc *AsyncBillingUsecase) SetLedgerRepo(repo LedgerRepo) {
	if uc == nil || uc.batchWriter == nil {
		return
	}
	uc.batchWriter.SetLedgerRepo(repo)
}

// NewQuotaCache creates a new quota cache.
func NewQuotaCache() *QuotaCache {
	return &QuotaCache{
		quota: make(map[int64]*UserQuota),
	}
}

// Get retrieves quota from cache.
func (c *QuotaCache) Get(userID int64) (*UserQuota, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	q, ok := c.quota[userID]
	if !ok {
		return nil, false
	}
	// Check if stale (5 seconds)
	if time.Since(q.LastUpdate) > 5*time.Second {
		return nil, false
	}
	return q, true
}

// Set stores quota in cache.
func (c *QuotaCache) Set(userID int64, available, frozen int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.quota[userID] = &UserQuota{
		UserID:     userID,
		Available:  available,
		Frozen:     frozen,
		LastUpdate: time.Now(),
	}
}

// Invalidate removes quota from cache.
func (c *QuotaCache) Invalidate(userID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.quota, userID)
}

// PreCheck performs a fast local quota check without DB round-trip.
// This is the "fast path" — actual deduction happens asynchronously.
func (uc *AsyncBillingUsecase) PreCheck(
	ctx context.Context,
	userID string,
	requestID string,
	estimatedTokens int64,
	model string,
	channelID string,
	subscriptionAccountID int64,
) error {
	start := time.Now()
	defer func() {
		metrics.BillingReserveDuration.WithLabelValues("async").Observe(time.Since(start).Seconds())
	}()

	// Convert userID to int64
	uid := parseUserID(userID)

	// L1 check: local cache
	if quota, ok := uc.localCache.Get(uid); ok {
		cost := estimateCost(model, estimatedTokens)
		if quota.Available < cost {
			metrics.QuotaCheckFallback.WithLabelValues("insufficient_quota").Inc()
			return ErrInsufficientQuota
		}

		// Optimistic deduction in cache
		quota.Available -= cost
		quota.Frozen += cost
		uc.localCache.Set(uid, quota.Available, quota.Frozen)

		metrics.QuotaCacheHits.WithLabelValues("l1").Inc()
		return nil
	}

	// L2 check: Redis atomic check-and-deduct
	if uc.redis != nil {
		return uc.preCheckRedis(ctx, uid, model, estimatedTokens)
	}

	// Fallback to sync path
	metrics.QuotaCheckFallback.WithLabelValues("cache_miss").Inc()
	_, err := uc.syncUc.ReserveQuota(ctx, userID, requestID, estimatedTokens, model, channelID, subscriptionAccountID)
	return err
}

// preCheckRedis performs quota check and deduction in Redis using Lua script.
func (uc *AsyncBillingUsecase) preCheckRedis(
	ctx context.Context,
	userID int64,
	model string,
	estimatedTokens int64,
) error {
	cost := estimateCost(model, estimatedTokens)
	key := fmt.Sprintf("quota:%d", userID)

	// Load or generate Lua script
	script := uc.getCheckAndDeductScript()

	// Execute Lua script atomically
	result, err := uc.redis.Eval(ctx, *script, []string{key}, cost).Result()
	if err != nil {
		metrics.QuotaCheckFallback.WithLabelValues("redis_error").Inc()
		return fmt.Errorf("redis quota check failed: %w", err)
	}

	// Result: 1 = success, 0 = insufficient quota
	if result.(int64) == 0 {
		return ErrInsufficientQuota
	}

	metrics.QuotaCacheHits.WithLabelValues("l2").Inc()
	return nil
}

// getCheckAndDeductScript returns the Lua script for atomic check-and-deduct.
func (uc *AsyncBillingUsecase) getCheckAndDeductScript() *string {
	if uc.quotaLuaScript != nil {
		return uc.quotaLuaScript
	}

	script := `
		local key = KEYS[1]
		local cost = tonumber(ARGV[1])

		-- Get current quota
		local quota = tonumber(redis.call('HGET', key, 'available')) or 0

		-- Check if sufficient
		if quota < cost then
			return 0
		end

		-- Deduct
		redis.call('HINCRBY', key, 'available', -cost)
		redis.call('HINCRBY', key, 'frozen', cost)
		redis.call('EXPIRE', key, 300)  -- 5 minute TTL

		return 1
	`
	uc.quotaLuaScript = &script
	return uc.quotaLuaScript
}

// Settle performs the actual billing asynchronously. The provided ctx is
// preserved for the fallback (queue-full) synchronous path so tracing and
// deadlines are not lost (REVIEW_v1 P1-5). The background worker runs the
// full CommitQuotaWithUsageAndSplit pipeline so the reservation lifecycle,
// wallet settlement, ledger entry and subscription usage write all happen
// exactly as on the synchronous path; only the gRPC caller is unblocked
// before the DB work completes.
func (uc *AsyncBillingUsecase) Settle(ctx context.Context, task *SettleTask) {
	if task == nil {
		return
	}
	// After Close() the worker has exited (or is about to). Any further
	// Settle must not rely on the queue: there is no consumer, so an
	// enqueued task would be silently dropped. Fall back to the
	// synchronous commit pipeline instead. The pipeline is idempotent
	// (see CommitQuotaWithUsageAndSplit -> CASReservationStatus), so
	// concurrent retries are safe.
	if uc.closed.Load() {
		metrics.AsyncBillingFallbackToSync.WithLabelValues().Inc()
		uc.settleSync(ctx, task)
		return
	}
	select {
	case uc.settleQueue <- task:
		metrics.AsyncBillingQueueSize.WithLabelValues().Set(float64(len(uc.settleQueue)))
	default:
		// Queue full: fallback to synchronous settle, preserving ctx.
		metrics.AsyncBillingFallbackToSync.WithLabelValues().Inc()
		uc.settleSync(ctx, task)
	}
}

// settleSync performs synchronous settlement as fallback. It runs the full
// commit pipeline so the reservation reaches the same terminal state as the
// background worker would produce. It is nil-safe: if no sync use case is
// configured (e.g. in tests / partial wiring), it records the drop via
// metrics rather than nil-panic'ing.
func (uc *AsyncBillingUsecase) settleSync(ctx context.Context, task *SettleTask) {
	start := time.Now()
	defer func() {
		lag := time.Since(task.Timestamp)
		metrics.BillingSettlementLag.Observe(lag.Seconds())
		metrics.AsyncBillingSettlementDuration.WithLabelValues("sync").Observe(time.Since(start).Seconds())
	}()

	if uc.syncUc == nil {
		metrics.AsyncBillingDroppedFlushes.Inc()
		applogger.Log.Warn("async settlement skipped (no sync use case)", zap.String("reservation_id", task.ReservationID))
		return
	}
	uc.runCommitPipeline(ctx, task)
}

// runCommitPipeline runs the authoritative CommitQuotaWithUsageAndSplit
// pipeline for a settle task. It is shared by the background worker and the
// queue-full synchronous fallback so both paths produce identical state.
// Errors are logged and counted via metrics; they cannot be returned to the
// original gRPC caller, which has already been released with a provisional
// response.
//
// Idempotency / exactly-once settlement:
//
//	The relay returns a provisional success to the client BEFORE the worker
//	runs this pipeline, so a worker crash or a retry (e.g. queue-full sync
//	fallback racing with the worker draining the same task) could in
//	principle call CommitQuotaWithUsageAndSplit twice for the same
//	reservation id. The pipeline is safe under such re-entry because the
//	reservation state machine transitions through Compare-And-Set calls
//	(CASReservationStatus):
//
//	  reserved -> committing  (CAS: only one caller wins)
//	  committing -> committed (CAS: only the winner of the first CAS)
//
//	The losing CAS path reads the current row status and returns the stored
//	result without re-applying wallet deduction, ledger write or subscription
//	usage. As a result the ledger and wallet are mutated at most once even if
//	runCommitPipeline is invoked multiple times. The relevant code lives in
//	BillingUsecase.commitQuotaInternal (see the "Concurrent or retry" branch).
//	If that CAS guard is ever weakened, this async path becomes unsafe.
func (uc *AsyncBillingUsecase) runCommitPipeline(ctx context.Context, task *SettleTask) {
	if _, _, _, err := uc.syncUc.CommitQuotaWithUsageAndSplit(
		ctx,
		task.ReservationID,
		task.ActualTokens,
		task.Success,
		task.Usage,
	); err != nil {
		metrics.AsyncBillingDroppedFlushes.Inc()
		applogger.Log.Warn("async settlement error", zap.String("reservation_id", task.ReservationID), zap.Error(err))
	}
}

// startWorkers starts background settlement workers.
func (uc *AsyncBillingUsecase) startWorkers() {
	// Settlement processor
	uc.workerWg.Add(1)
	go func() {
		defer uc.workerWg.Done()
		uc.settlementWorker()
	}()

	// Start batch flusher
	uc.batchWriter.Start()
}

// settlementWorker processes settlement tasks from the queue.
func (uc *AsyncBillingUsecase) settlementWorker() {
	defer func() {
		// On shutdown, drain anything still in the queue so we do not
		// leave reservations stuck in reserved. Close() also drains but
		// doing it here means a process that exits without calling Close
		// (e.g. a crashed test or an os.Exit path) still flushes.
		for {
			select {
			case task := <-uc.settleQueue:
				uc.processSettlement(task)
			default:
				return
			}
		}
	}()
	for {
		select {
		case <-uc.workerCtx.Done():
			return
		case task := <-uc.settleQueue:
			metrics.AsyncBillingQueueSize.WithLabelValues().Set(float64(len(uc.settleQueue)))
			uc.processSettlement(task)
		}
	}
}

// processSettlement processes a single settlement task on the background
// worker. It runs the full CommitQuotaWithUsageAndSplit pipeline so the
// reservation, wallet, ledger and subscription usage all advance exactly as
// on the synchronous path. The earlier implementation wrote raw ledger
// entries via BatchLedgerWriter, which bypassed the reservation state
// machine and left reservations stuck in the reserved state; this version
// delegates to the authoritative pipeline.
func (uc *AsyncBillingUsecase) processSettlement(task *SettleTask) {
	start := time.Now()
	defer func() {
		lag := time.Since(task.Timestamp)
		metrics.BillingSettlementLag.Observe(lag.Seconds())
		metrics.AsyncBillingSettlementDuration.WithLabelValues("async").Observe(time.Since(start).Seconds())
	}()

	if uc.syncUc == nil {
		metrics.AsyncBillingDroppedFlushes.Inc()
		applogger.Log.Warn("async settlement skipped (no sync use case)", zap.String("reservation_id", task.ReservationID))
		return
	}
	// Use the worker's detached context so the settlement survives the
	// caller's request scope. The commit pipeline is idempotent; a retried
	// reservation short-circuits inside the state machine.
	uc.runCommitPipeline(uc.workerCtx, task)
}

// Close closes the async billing use case and waits for workers to finish.
func (uc *AsyncBillingUsecase) Close() error {
	// Flip the drain flag first so concurrent Settle callers stop
	// enqueuing and fall back to the synchronous path. Without this,
	// a task enqueued in the window between workerCancel() and
	// wg.Wait() would sit in settleQueue with no consumer and be lost.
	uc.closed.Store(true)
	uc.workerCancel()
	uc.workerWg.Wait()
	// Drain any tasks that raced in after closed was set but before the
	// worker exited. The worker also drains on its way out, but a task
	// could have arrived between the worker's final select and
	// wg.Wait() returning. Process inline so Close never returns with
	// un-settled work in the queue.
	for {
		select {
		case task := <-uc.settleQueue:
			uc.processSettlement(task)
		default:
			uc.batchWriter.Stop()
			return nil
		}
	}
}

// NewBatchLedgerWriter creates a new batch ledger writer. The writer is only
// useful once SetLedgerRepo has been called; without a repo, Flush reports
// the dropped count via metrics rather than silently losing entries.
func NewBatchLedgerWriter(size int, interval time.Duration) *BatchLedgerWriter {
	if size <= 0 {
		size = 100
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &BatchLedgerWriter{
		batch:     make([]*LedgerEntry, 0, size),
		flushChan: make(chan struct{}, 1),
		size:      size,
		interval:  interval,
		stopChan:  make(chan struct{}),
	}
}

// SetLedgerRepo wires the destination repo. Must be called before Start.
func (w *BatchLedgerWriter) SetLedgerRepo(repo LedgerRepo) {
	w.batchMu.Lock()
	w.ledger = repo
	w.batchMu.Unlock()
}

// Start starts the batch flusher worker.
func (w *BatchLedgerWriter) Start() {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()

		for {
			select {
			case <-w.stopChan:
				// Flush remaining before exit
				w.Flush()
				return
			case <-ticker.C:
				w.Flush()
			case <-w.flushChan:
				// Manual flush triggered
				w.Flush()
			}
		}
	}()
}

// Stop stops the batch flusher worker. It is idempotent: a second call
// (e.g. from AsyncBillingUsecase.Close after the writer was already
// stopped by an explicit Stop) is a no-op rather than panicking on
// close of an already-closed channel.
func (w *BatchLedgerWriter) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopChan)
	})
	w.wg.Wait()
}

// Add adds a ledger entry to the batch.
func (w *BatchLedgerWriter) Add(entry *LedgerEntry) {
	w.batchMu.Lock()
	w.batch = append(w.batch, entry)
	if len(w.batch) >= w.size {
		w.batchMu.Unlock()
		w.Flush()
		return
	}
	w.batchMu.Unlock()
}

// Flush writes all pending entries to the ledger repo. If no repo is
// configured, entries are counted as dropped (surfaced via metrics) so the
// misconfiguration is observable instead of silent data loss.
func (w *BatchLedgerWriter) Flush() {
	w.batchMu.Lock()
	if len(w.batch) == 0 {
		w.batchMu.Unlock()
		return
	}

	batch := w.batch
	w.batch = make([]*LedgerEntry, 0, w.size)
	ledger := w.ledger
	w.batchMu.Unlock()

	if ledger == nil {
		// Misconfiguration: the writer is running without a destination.
		// Count and discard so the problem is visible in metrics/logs rather
		// than silently swallowed.
		w.dropped.Add(int64(len(batch)))
		metrics.AsyncBillingDroppedFlushes.Add(float64(len(batch)))
		applogger.Log.Warn("BatchLedgerWriter: dropped entries (no ledger repo configured)", zap.Int("dropped", len(batch)))
		return
	}

	// Persist each entry. A real batch INSERT would be more efficient, but
	// LedgerRepo currently exposes single-entry CreateLedger; correctness
	// over premature optimization.
	for _, entry := range batch {
		led := &Ledger{
			UserID:                entry.UserID,
			Amount:                entry.Cost,
			Quota:                 entry.TokenAmount,
			PromptTokens:          0,
			CompletionTokens:      entry.TokenAmount,
			ModelName:             entry.Model,
			ChannelID:             parseInt64Default(entry.ChannelID, 0),
			SubscriptionAccountID: entry.SubscriptionAccountID,
			Type:                  LedgerTypeConsume,
			IsStream:              false,
			Endpoint:              "",
			CreatedAt:             entry.CreatedAt,
		}
		if err := ledger.CreateLedger(context.Background(), led); err != nil {
			w.dropped.Add(1)
			metrics.AsyncBillingDroppedFlushes.Inc()
			applogger.Log.Warn("BatchLedgerWriter: failed to persist ledger entry", zap.Error(err))
		}
	}
}

// estimateCost estimates the cost in cents for a given model and token count.
// This is a simplified version - real implementation would use model pricing.
func estimateCost(model string, tokens int64) int64 {
	// Simplified pricing: $0.001 per 1K tokens
	return (tokens * 1) / 1000
}

// parseUserID converts string userID to int64.
func parseUserID(userID string) int64 {
	// Simple parsing - real implementation would handle different formats
	var uid int64
	fmt.Sscanf(userID, "%d", &uid)
	return uid
}

// DroppedCount returns the number of ledger entries dropped by the batch
// writer (e.g. when no repo is configured or persistence failed).
func (w *BatchLedgerWriter) DroppedCount() int64 {
	return w.dropped.Load()
}

// AsyncBillingDroppedFlushes is a last-resort counter for entries that could
// not be persisted by the batch writer. It is registered lazily via the
// metrics package if already declared; otherwise it is a package-level var
// guarded by a sync.Once to avoid duplicate-registration panics.
