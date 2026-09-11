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
	var rows []record
	if err := db.WithContext(ctx).Where("owner = ? AND delivered_at = 0", owner).Order("created_at, id").Limit(100).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if err := publish(ctx, Topic, Change{Owner: row.Owner, Kind: row.Kind, AggregateID: row.AggregateID, Revision: row.Revision}); err != nil {
			return err
		}
		if err := db.WithContext(ctx).Model(&record{}).Where("id = ? AND delivered_at = 0", row.ID).Update("delivered_at", time.Now().Unix()).Error; err != nil {
			return err
		}
	}
	return nil
}

// Start deliberately uses Redis Streams even when the optional general event bus
// uses memory. Without Redis the durable rows remain pending for a later restart.
func Start(db *gorm.DB, redisClient *redis.Client, owner string, report func(error)) func() {
	if db == nil || redisClient == nil {
		return func() {}
	}
	bus := events.NewStreamEventBus(redisClient, owner+"-routing-outbox")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			runCtx, runCancel := context.WithTimeout(ctx, 5*time.Second)
			err := Dispatch(runCtx, db, owner, bus.Publish)
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
	return func() { cancel(); <-done; bus.Close() }
}

// Invalidator serializes revision checks with eviction. Failed evictions remain
// retryable; delayed/duplicate events cannot replace a newer cached snapshot.
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
		return nil
	}
}
