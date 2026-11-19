package writeaudit

import (
	"context"
	"database/sql"
	"strings"

	"gorm.io/gorm"
)

type Filter struct {
	ActorUserID int64
	Operation   string
	ResourceID  string
	Result      string
	StartTimeMS int64
	EndTimeMS   int64
	Offset      int
	Limit       int
}

type Page[T any] struct {
	Items   []T
	Total   int64
	StoreID string
}

// ReadPage uses one snapshot so concurrent writes cannot change the page length after the count.
func ReadPage[T any](ctx context.Context, db *gorm.DB, filter Filter) (Page[T], error) {
	page := Page[T]{Items: make([]T, 0)}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		base := filter.query(tx)
		if err := base.Session(&gorm.Session{}).Count(&page.Total).Error; err != nil {
			return err
		}
		if err := base.Select("event_id, actor_user_id, operation, resource_id, decision_versions, reason, occurred_at, result").
			Order("occurred_at DESC, event_id DESC").Offset(filter.Offset).Limit(filter.Limit).Scan(&page.Items).Error; err != nil {
			return err
		}
		var err error
		page.StoreID, err = StoreID(ctx, tx)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return page, err
}

func (f Filter) query(db *gorm.DB) *gorm.DB {
	query := db.Table("resource_write_audits")
	if f.ActorUserID > 0 {
		query = query.Where("actor_user_id = ?", f.ActorUserID)
	}
	if f.Operation != "" {
		prefix := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(f.Operation) + "%"
		query = query.Where("operation LIKE ? ESCAPE '!'", prefix)
	}
	if f.ResourceID != "" {
		query = query.Where("resource_id = ?", f.ResourceID)
	}
	if f.Result != "" {
		query = query.Where("result = ?", f.Result)
	}
	if f.StartTimeMS > 0 {
		query = query.Where("occurred_at >= ?", f.StartTimeMS)
	}
	if f.EndTimeMS > 0 {
		query = query.Where("occurred_at <= ?", f.EndTimeMS)
	}
	return query
}
