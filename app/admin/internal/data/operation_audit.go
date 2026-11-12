package data

import (
	"context"
	"database/sql"
	"strings"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"micro-one-api/app/admin/internal/biz"
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

func escapeAuditPrefix(value string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(value) + "%"
}

func (r *operationAuditRepo) List(ctx context.Context, filter biz.OperationAuditFilter) ([]biz.OperationAudit, int64, error) {
	base := func() *gorm.DB {
		query := r.db.WithContext(ctx).Table("resource_write_audits")
		if filter.ActorUserID > 0 {
			query = query.Where("actor_user_id = ?", filter.ActorUserID)
		}
		if filter.Operation != "" {
			query = query.Where("operation LIKE ? ESCAPE '!'", escapeAuditPrefix(filter.Operation))
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
		return nil, 0, err
	}
	var rows []operationAuditRow
	if err := base().Select("event_id, actor_user_id, operation, resource_id, decision_versions, reason, occurred_at, result").
		Order("occurred_at DESC, event_id DESC").Offset(filter.Offset).Limit(filter.Limit).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	items := make([]biz.OperationAudit, 0, len(rows))
	for _, row := range rows {
		items = append(items, biz.OperationAudit{
			EventID: row.EventID, ActorUserID: row.ActorUserID, Operation: row.Operation,
			ResourceID: row.ResourceID, DecisionVersions: row.DecisionVersions,
			Reason: row.Reason, OccurredAt: row.OccurredAt, Result: row.Result,
		})
	}
	return items, total, nil
}
