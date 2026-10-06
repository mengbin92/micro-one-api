package data

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/domain/authorization"
	"strings"
)

// Composite handlers can check several operations on one row. Fence and
// increment that row once in the transaction, never once per permission.
func checkSourceWriteRevision(ctx context.Context, tx *gorm.DB, id int64, account bool, operations []string) error {
	if strings.TrimSpace(authorization.WriteReason(ctx)) == "" {
		return authorization.ErrWritePrecondition
	}
	if id == 0 {
		return nil
	}
	resource, table, column := "channel", "channels", "authorization_revision"
	if account {
		resource, table, column = "account", "subscription_accounts", "credential_revision"
	}
	key := fmt.Sprintf("iam.write_revision:%s:%d", resource, id)
	if _, checked := tx.Statement.Settings.Load(key); checked {
		return nil
	}
	var row struct{ Revision int64 }
	if err := tx.Table(table).Clauses(clause.Locking{Strength: "UPDATE"}).Select(column+" AS revision").Where("id = ?", id).Take(&row).Error; err != nil {
		return err
	}
	if err := authorization.CheckWriteRevision(ctx, resource, id, row.Revision); err != nil {
		return fmt.Errorf("revision context expected=%d actual=%d: %w", authorization.ExpectedRevision(ctx, resource, id), row.Revision, err)
	}
	// Ordinary account metadata updates already fence and increment their
	// credential revision in the SQL update; all other mutations do so here.
	accountUpdate := false
	for _, op := range operations {
		if op == "channel.account.update" {
			accountUpdate = true
		}
	}
	if !accountUpdate {
		if err := tx.Table(table).Where("id = ? AND "+column+" = ?", id, row.Revision).Update(column, gorm.Expr(column+" + 1")).Error; err != nil {
			return err
		}
	}
	tx.Statement.Settings.Store(key, true)
	return nil
}
