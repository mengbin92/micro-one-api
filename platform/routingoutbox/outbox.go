// Package routingoutbox persists routing invalidations in the owning service's
// transaction. Events contain identifiers and revisions only, never credentials.
package routingoutbox

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/events"
	"micro-one-api/platform/metrics"
)

const Topic = "routing.changed"

type Change struct {
	Owner       string `json:"owner"`
	Kind        string `json:"kind"`
	AggregateID int64  `json:"aggregate_id"`
	Revision    int64  `json:"revision"`
}

type record struct {
	ID          string `gorm:"primaryKey"`
	Owner       string
	Kind        string
	AggregateID int64
	Revision    int64
	CreatedAt   int64
	DeliveredAt int64
}

func (record) TableName() string { return "routing_change_outbox" }

// Enqueue must receive the same transaction that writes the authoritative row.
func Enqueue(tx *gorm.DB, owner, kind string, id, revision int64) error {
	return tx.Create(&record{ID: fmt.Sprintf("%s:%s:%d:%d", owner, kind, id, revision), Owner: owner, Kind: kind, AggregateID: id, Revision: revision, CreatedAt: time.Now().Unix()}).Error
}

// Dispatch acknowledges only successful durable publishes. A crash after publish
// may cause duplicates; consumers invalidate rather than installing event state.
func Dispatch(ctx context.Context, db *gorm.DB, owner string, publish func(context.Context, string, any) error) error {
	// Initialize zero-valued series even before the first publish/failure.
	metrics.RoutingOutboxLastSuccess.WithLabelValues(owner)
	for _, op := range []string{"scan", "load", "publish", "acknowledge", "gc"} {
		metrics.RoutingOutboxFailures.WithLabelValues(owner, op)
	}
	if err := observePending(ctx, db, owner); err != nil {
		return err
	}
	var rows []record
	if err := db.WithContext(ctx).Where("owner = ? AND delivered_at = 0", owner).Order("created_at, id").Limit(100).Find(&rows).Error; err != nil {
		return failure(owner, "load", "", err)
	}
	for _, row := range rows {
		if err := publish(ctx, Topic, Change{Owner: row.Owner, Kind: row.Kind, AggregateID: row.AggregateID, Revision: row.Revision}); err != nil {
			return failure(owner, "publish", row.ID, err)
		}
		if err := db.WithContext(ctx).Model(&record{}).Where("id = ? AND delivered_at = 0", row.ID).Update("delivered_at", time.Now().Unix()).Error; err != nil {
			return failure(owner, "acknowledge", row.ID, err)
		}
		metrics.RoutingOutboxLastSuccess.WithLabelValues(owner).SetToCurrentTime()
	}
	if len(rows) > 0 {
		return observePending(ctx, db, owner)
	}
	return nil
}

// DeliveryError keeps event identifiers in logs, never metric labels.
type DeliveryError struct {
	Owner, Operation, EventID string
	Err                       error
}

func (e *DeliveryError) Error() string {
	return fmt.Sprintf("routing outbox owner=%s operation=%s event_id=%s: %v", e.Owner, e.Operation, e.EventID, e.Err)
}
func (e *DeliveryError) Unwrap() error { return e.Err }
func failure(owner, operation, eventID string, err error) error {
	metrics.RoutingOutboxFailures.WithLabelValues(owner, operation).Inc()
	return &DeliveryError{Owner: owner, Operation: operation, EventID: eventID, Err: err}
}
func observePending(ctx context.Context, db *gorm.DB, owner string) error {
	metrics.RoutingOutboxLastScan.WithLabelValues(owner)
	var state struct {
		Pending int64
		Oldest  int64
	}
	if err := db.WithContext(ctx).Model(&record{}).Select("COUNT(*) AS pending, COALESCE(MIN(created_at), 0) AS oldest").Where("owner = ? AND delivered_at = 0", owner).Scan(&state).Error; err != nil {
		return failure(owner, "scan", "", err)
	}
	age := int64(0)
	if state.Pending > 0 {
		age = max(0, time.Now().Unix()-state.Oldest)
	}
	metrics.RoutingOutboxPending.WithLabelValues(owner).Set(float64(state.Pending))
	metrics.RoutingOutboxOldestAge.WithLabelValues(owner).Set(float64(age))
	metrics.RoutingOutboxLastScan.WithLabelValues(owner).SetToCurrentTime()
	return nil
}

// Start deliberately uses Redis Streams even when the optional general event bus
// uses memory. Without Redis the durable rows remain pending for a later restart.
func Start(db *gorm.DB, redisClient *redis.Client, owner string, report func(error)) func() {
	if db == nil {
		return func() {}
	}
	// Keep observing durable pending rows even when Redis is not configured.
	publish := func(context.Context, string, any) error { return fmt.Errorf("Redis client unavailable") }
	closeBus := func() {}
	if redisClient != nil {
		bus := events.NewStreamEventBus(redisClient, owner+"-routing-outbox")
		publish, closeBus = bus.Publish, func() { bus.Close() }
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		var gcCount int
		for {
			runCtx, runCancel := context.WithTimeout(ctx, 5*time.Second)
			err := Dispatch(runCtx, db, owner, publish)
			// Delivered rows are dead weight once consumers have acked; sweep
			// them so the pending scan stays cheap on high-churn deployments.
			gcCount++
			if gcCount >= 300 {
				gcCount = 0
				cutoff := time.Now().Add(-24 * time.Hour).Unix()
				if gcErr := db.WithContext(runCtx).Where("owner = ? AND delivered_at > 0 AND delivered_at < ?", owner, cutoff).Delete(&record{}).Error; gcErr != nil {
					gcErr = failure(owner, "gc", "", gcErr)
					if err == nil {
						err = gcErr
					}
				}
			}
			runCancel()
			if err != nil && ctx.Err() == nil && report != nil {
				report(err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done; closeBus() }
}

// Invalidator serializes revision checks with eviction. Failed evictions remain
// retryable; delayed/duplicate events cannot replace a newer cached snapshot.
// The versions map is a bounded guard only: dropping an entry at worst causes a
// redundant cache reload, never stale state, so it is randomly pruned at cap.
func Invalidator(evict func(context.Context, Change) error) events.Handler {
	var mu sync.Mutex
	versions := map[string]int64{}
	return func(ctx context.Context, event events.Event) error {
		raw, err := jsonx.Marshal(event.Payload)
		if err != nil {
			return err
		}
		var change Change
		if err := jsonx.Unmarshal(raw, &change); err != nil {
			return err
		}
		if change.AggregateID <= 0 || change.Revision <= 0 || (change.Owner != "identity" && change.Owner != "channel" && change.Owner != "subscription") {
			return fmt.Errorf("invalid routing event")
		}
		key := fmt.Sprintf("%s:%s:%d", change.Owner, change.Kind, change.AggregateID)
		mu.Lock()
		defer mu.Unlock()
		if versions[key] >= change.Revision {
			return nil
		}
		if err := evict(ctx, change); err != nil {
			return err
		}
		versions[key] = change.Revision
		if len(versions) > invalidatorVersionCap {
			for k := range versions {
				delete(versions, k)
				if len(versions) <= invalidatorVersionCap/2 {
					break
				}
			}
		}
		return nil
	}
}

const invalidatorVersionCap = 65536
