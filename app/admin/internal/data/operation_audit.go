package data

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/platform/database/writeaudit"
)

type operationAuditRepo struct {
	db *gorm.DB
}

var _ biz.OperationAuditRepo = (*operationAuditRepo)(nil)

func NewOperationAuditRepoWithDriver(db *sql.DB, driver string) biz.OperationAuditRepo {
	d := strings.ToLower(strings.TrimSpace(driver))
	var dial gorm.Dialector
	switch d {
	case "postgres", "postgresql", "pgx":
		dial = postgres.New(postgres.Config{Conn: db})
	case "sqlite", "sqlite3":
		dial = sqlite.New(sqlite.Config{Conn: db})
	default:
		dial = mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true})
	}
	gdb, err := gorm.Open(dial, &gorm.Config{})
	if err != nil {
		return nil
	}
	return &operationAuditRepo{db: gdb}
}

type operationAuditRow struct {
	EventID          string
	ActorUserID      int64
	Operation        string
	ResourceID       string
	DecisionVersions string
	Reason           string
	OccurredAt       int64
	Result           string
}

func (r *operationAuditRepo) List(ctx context.Context, filter biz.OperationAuditFilter) (biz.OperationAuditBatch, error) {
	if r == nil || r.db == nil {
		return biz.OperationAuditBatch{}, biz.ErrOperationAuditUnavailable
	}
	page, err := writeaudit.ReadPage[operationAuditRow](ctx, r.db, writeaudit.Filter(filter))
	if err != nil {
		return biz.OperationAuditBatch{}, errors.Join(biz.ErrOperationAuditUnavailable, err)
	}
	items := make([]biz.OperationAudit, 0, len(page.Items))
	for _, row := range page.Items {
		items = append(items, biz.OperationAudit{
			EventID: row.EventID, ActorUserID: row.ActorUserID, Operation: row.Operation,
			ResourceID: row.ResourceID, DecisionVersions: row.DecisionVersions,
			Reason: row.Reason, OccurredAt: row.OccurredAt, Result: row.Result,
		})
	}
	return biz.OperationAuditBatch{Items: items, Total: page.Total, StoreID: page.StoreID}, nil
}
