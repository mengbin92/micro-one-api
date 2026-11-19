package data

import (
	"context"
	"errors"

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

func (r *writeAuditRepo) ListWriteAudits(ctx context.Context, filter biz.WriteAuditFilter) (biz.WriteAuditPage, error) {
	if r == nil || r.db == nil {
		return biz.WriteAuditPage{}, biz.ErrWriteAuditUnavailable
	}
	page, err := writeaudit.ReadPage[biz.WriteAudit](ctx, r.db, writeaudit.Filter(filter))
	if err != nil {
		return biz.WriteAuditPage{}, errors.Join(biz.ErrWriteAuditUnavailable, err)
	}
	return biz.WriteAuditPage(page), nil
}
