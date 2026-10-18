package data

import (
	"context"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/log/internal/biz"
)

const timingCommitBatchSize = 32

type timingWrite struct {
	ctx   context.Context
	entry *biz.LogEntry
	done  chan timingWriteResult
}

type timingWriteResult struct {
	id  int64
	err error
}

// Concurrent, individually authenticated ingests share a durable transaction.
// A caller sees success only after its summary, claim and refs have committed.
// At most 32 snapshots are retained; the drain goroutine exits when idle.
func (r *Repository) createTimingGrouped(ctx context.Context, entry *biz.LogEntry) error {
	r.timingInit.Do(func() {
		r.timingSlots = make(chan struct{}, timingCommitBatchSize)
		r.timingReady = make(chan struct{}, 1)
	})
	select {
	case r.timingSlots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	snapshot := *entry
	snapshot.RequestTiming = entry.RequestTiming.Clone()
	write := &timingWrite{ctx: ctx, entry: &snapshot, done: make(chan timingWriteResult, 1)}
	r.timingMu.Lock()
	r.timingPending = append(r.timingPending, write)
	if len(r.timingPending) == timingCommitBatchSize {
		select {
		case r.timingReady <- struct{}{}:
		default:
		}
	}
	if !r.timingRunning {
		r.timingRunning = true
		go r.drainTimingWrites()
	}
	r.timingMu.Unlock()
	select {
	case result := <-write.done:
		if result.err == nil {
			entry.ID = result.id
		}
		return result.err
	case <-ctx.Done():
		// The snapshot remains private if cancellation races a successful commit.
		// Its retry will resolve the same observation through the durable claim.
		return ctx.Err()
	}
}

func (r *Repository) drainTimingWrites() {
	for {
		r.timingMu.Lock()
		if len(r.timingPending) == 0 {
			r.timingRunning = false
			r.timingMu.Unlock()
			return
		}
		full := len(r.timingPending) == timingCommitBatchSize
		r.timingMu.Unlock()
		if !full {
			// Sparse arrivals need a short collection window; backlogged full
			// groups proceed immediately rather than paying a per-group sleep.
			timer := time.NewTimer(5 * time.Millisecond)
			select {
			case <-r.timingReady:
			case <-timer.C:
			}
			timer.Stop()
		}
		r.timingMu.Lock()
		pending := r.timingPending
		r.timingPending = nil
		// Consume only the notification for this snapshot while holding the
		// enqueue lock, so a stale signal cannot prematurely flush a new group.
		select {
		case <-r.timingReady:
		default:
		}
		r.timingMu.Unlock()
		writes := make([]*timingWrite, 0, len(pending))
		entries := make([]*biz.LogEntry, 0, len(pending))
		for _, write := range pending {
			if err := write.ctx.Err(); err != nil {
				write.done <- timingWriteResult{err: err}
				<-r.timingSlots
				continue
			}
			writes = append(writes, write)
			entries = append(entries, write.entry)
		}
		if len(writes) == 0 {
			continue
		}
		// One caller's timeout cannot abort unrelated authenticated ingests.
		// The shared SQL transaction has its own fixed deadline.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(writes[0].ctx), 2*time.Second)
		ids, err := r.createTimingBatchDB(ctx, entries)
		cancel()
		for i, write := range writes {
			result := timingWriteResult{err: err}
			if err == nil {
				result.id = ids[i]
			}
			write.done <- result
			<-r.timingSlots
		}
	}
}

func (r *Repository) createTimingBatchDB(ctx context.Context, entries []*biz.LogEntry) ([]int64, error) {
	if len(entries) == 0 || len(entries) > timingCommitBatchSize {
		return nil, biz.ErrInvalidRequestTiming
	}
	byKey := make(map[string]*biz.LogEntry, len(entries))
	for _, entry := range entries {
		if err := validateRequestTimingEntry(entry); err != nil || entry.RequestTiming == nil {
			return nil, biz.ErrInvalidRequestTiming
		}
		if byKey[entry.DedupeKey] == nil {
			byKey[entry.DedupeKey] = entry
		}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	// Deterministic claim lock order also protects overlapping batches from
	// different Log instances against the usual reverse-order deadlock.
	sort.Strings(keys)
	idsByKey := make(map[string]int64, len(keys))
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		claims := make([]logIngestDedupeClaimModel, 0, len(keys))
		for _, key := range keys {
			claims = append(claims, logIngestDedupeClaimModel{DedupeKey: key, CreatedAt: time.Now().Unix()})
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "dedupe_key"}}, DoNothing: true}).Create(&claims).Error; err != nil {
			return err
		}
		var stored []logIngestDedupeClaimModel
		if err := tx.Where("dedupe_key IN ?", keys).Order("dedupe_key").Find(&stored).Error; err != nil {
			return err
		}
		models := make([]logModel, 0, len(stored))
		freshKeys := make([]string, 0, len(stored))
		for _, claim := range stored {
			if claim.LogID != 0 {
				idsByKey[claim.DedupeKey] = claim.LogID
				continue
			}
			model, err := newLog(byKey[claim.DedupeKey])
			if err != nil {
				return err
			}
			models = append(models, *model)
			freshKeys = append(freshKeys, claim.DedupeKey)
		}
		if len(models) == 0 {
			return nil
		}
		if err := tx.Create(&models).Error; err != nil {
			return err
		}
		refs := make([]requestTraceAttemptRefModel, 0)
		updateSQL := "CASE dedupe_key"
		updateArgs := make([]any, 0, len(models)*2)
		for i, key := range freshKeys {
			idsByKey[key] = models[i].ID
			refs = append(refs, newRequestTraceAttemptRefs(byKey[key])...)
			updateSQL += " WHEN ? THEN ?"
			updateArgs = append(updateArgs, key, models[i].ID)
		}
		if len(refs) > 0 {
			// A summary can carry 128 refs; bound statement parameters on SQLite.
			if err := tx.CreateInBatches(&refs, 256).Error; err != nil {
				return err
			}
		}
		// The existing BIGINT column anchors CASE's result type on PostgreSQL;
		// otherwise all-parameter THEN branches can be inferred as text.
		updateSQL += " ELSE log_id END"
		return tx.Model(&logIngestDedupeClaimModel{}).Where("dedupe_key IN ?", freshKeys).
			Update("log_id", gorm.Expr(updateSQL, updateArgs...)).Error
	})
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(entries))
	for i, entry := range entries {
		ids[i] = idsByKey[entry.DedupeKey]
	}
	return ids, nil
}
