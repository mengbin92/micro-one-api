package data

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
	"micro-one-api/platform/database/writeaudit"

	"micro-one-api/app/channel/internal/biz"
)

// writeAuditRepo reads the owner's resource_write_audits table for the admin
// audit console aggregation. Read-only: no writes ever go through here, so
// audit rows and business mutations keep their original atomicity in the
// caller's transaction.
type writeAuditRepo struct {
	db *gorm.DB
}

func NewWriteAuditRepo(r *Repository) biz.WriteAuditRepo {
	return &writeAuditRepo{db: r.db}
}

func escapeWriteAuditPrefix(value string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(value) + "%"
}

func (r *writeAuditRepo) ListWriteAudits(ctx context.Context, filter biz.WriteAuditFilter) (biz.WriteAuditPage, error) {
	if r == nil || r.db == nil {
		return biz.WriteAuditPage{}, biz.ErrWriteAuditUnavailable
	}
	base := func() *gorm.DB {
		query := r.db.WithContext(ctx).Table("resource_write_audits")
		if filter.ActorUserID > 0 {
			query = query.Where("actor_user_id = ?", filter.ActorUserID)
		}
		if filter.Operation != "" {
			query = query.Where("operation LIKE ? ESCAPE '!'", escapeWriteAuditPrefix(filter.Operation))
		}
		if filter.ResourceID != "" {
			query = query.Where("resource_id = ?", filter.ResourceID)
		}
		if filter.Result != "" {
			query = query.Where("result = ?", filter.Result)
		}
		if filter.StartTimeMS > 0 {
			query = query.Where("occurred_at >= ?", filter.StartTimeMS)
		}
		if filter.EndTimeMS > 0 {
			query = query.Where("occurred_at <= ?", filter.EndTimeMS)
		}
		return query
	}
	var total int64
	if err := base().Count(&total).Error; err != nil {
		return biz.WriteAuditPage{}, errors.Join(biz.ErrWriteAuditUnavailable, err)
	}
	items := make([]biz.WriteAudit, 0)
	if err := base().Select("event_id, actor_user_id, operation, resource_id, decision_versions, reason, occurred_at, result").
		Order("occurred_at DESC, event_id DESC").Offset(filter.Offset).Limit(filter.Limit).Scan(&items).Error; err != nil {
		return biz.WriteAuditPage{}, errors.Join(biz.ErrWriteAuditUnavailable, err)
	}
	storeID, err := writeaudit.StoreID(ctx, r.db)
	if err != nil {
		return biz.WriteAuditPage{}, errors.Join(biz.ErrWriteAuditUnavailable, err)
	}
	return biz.WriteAuditPage{Items: items, Total: total, StoreID: storeID}, nil
}
