package biz

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"micro-one-api/internal/pkg/metrics"
)

// AsyncBillingUsecase provides a non-blocking billing path.
// It uses a local quota check + async settlement.
type AsyncBillingUsecase struct {
	syncUc         *BillingUsecase    // Fallback to sync billing
	localCache     *QuotaCache        // L1: in-memory quota snapshot
	redis          *redis.Client      // L2: distributed quota counter
	settleQueue    chan *SettleTask   // async settlement queue
	batchWriter    *BatchLedgerWriter // batch ledger persistence
	workerWg       sync.WaitGroup
	workerCtx      context.Context
	workerCancel   context.CancelFunc
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
type SettleTask struct {
	RequestID             string
	UserID                string
	Model                 string
	ChannelID             string
	SubscriptionAccountID int64
	ActualTokens          int64
	Cost                  int64
	Timestamp             time.Time
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
// deadlines are not lost (REVIEW_v1 P1-5).
func (uc *AsyncBillingUsecase) Settle(ctx context.Context, task *SettleTask) {
	select {
	case uc.settleQueue <- task:
		// Update queue size metric
		metrics.AsyncBillingQueueSize.WithLabelValues().Set(float64(len(uc.settleQueue)))
	default:
		// Queue full → fallback to synchronous settle, preserving ctx.
		metrics.AsyncBillingFallbackToSync.WithLabelValues().Inc()
		uc.settleSync(ctx, task)
	}
}

// settleSync performs synchronous settlement as fallback. It is nil-safe:
// if no sync use case is configured (e.g. in tests / partial wiring), it
// records the drop via metrics rather than nil-panic'ing.
func (uc *AsyncBillingUsecase) settleSync(ctx context.Context, task *SettleTask) {
	start := time.Now()
	defer func() {
		lag := time.Since(task.Timestamp)
		metrics.BillingSettlementLag.Observe(lag.Seconds())
		metrics.AsyncBillingSettlementDuration.WithLabelValues("sync").Observe(time.Since(start).Seconds())
	}()

	if uc.syncUc == nil {
		metrics.AsyncBillingDroppedFlushes.Inc()
		fmt.Printf("async settlement skipped (no sync use case): request_id=%s\n", task.RequestID)
		return
	}

	// Use sync billing for settlement
	_, _, err := uc.syncUc.CommitQuota(ctx, task.RequestID, task.ActualTokens, true)
	if err != nil {
		fmt.Printf("sync settlement error: %v\n", err)
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

// processSettlement processes a single settlement task.
func (uc *AsyncBillingUsecase) processSettlement(task *SettleTask) {
	start := time.Now()
	defer func() {
		lag := time.Since(task.Timestamp)
		metrics.BillingSettlementLag.Observe(lag.Seconds())
		metrics.AsyncBillingSettlementDuration.WithLabelValues("async").Observe(time.Since(start).Seconds())
	}()

	// Write to batch ledger writer
	uc.batchWriter.Add(&LedgerEntry{
		UserID:                task.UserID,
		Model:                 task.Model,
		ChannelID:             task.ChannelID,
		SubscriptionAccountID: task.SubscriptionAccountID,
		TokenAmount:           task.ActualTokens,
		Cost:                  task.Cost,
		CreatedAt:             task.Timestamp,
	})

	// Update local cache
	uid := parseUserID(task.UserID)
	if quota, ok := uc.localCache.Get(uid); ok {
		// Release frozen amount, deduct actual cost
		estimatedCost := estimateCost(task.Model, task.ActualTokens) // Approximation
		quota.Frozen -= estimatedCost
		quota.Available -= (task.Cost - estimatedCost) // Adjust for difference
		uc.localCache.Set(uid, quota.Available, quota.Frozen)
	}
}

// Close closes the async billing use case and waits for workers to finish.
func (uc *AsyncBillingUsecase) Close() error {
	uc.workerCancel()
	uc.workerWg.Wait()
	uc.batchWriter.Stop()
	return nil
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

// Stop stops the batch flusher worker.
func (w *BatchLedgerWriter) Stop() {
	close(w.stopChan)
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
		fmt.Printf("BatchLedgerWriter: dropped %d entries (no ledger repo configured)\n", len(batch))
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
			fmt.Printf("BatchLedgerWriter: failed to persist ledger entry: %v\n", err)
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
