package authzquery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"gorm.io/gorm"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
	"strconv"
	"time"
)

// AppendWriteAudit must be called on the same transaction as the mutation.
// Each actual operation gets a separate record; only verified actor/version
// facts and a resource identifier are stored, never resource contents.
func AppendWriteAudit(ctx context.Context, tx *gorm.DB, operation string, resourceID int64) error {
	return appendAudit(ctx, tx, operation, resourceID, "success")
}

// AppendFailureAudit uses the owner DB after rollback. Its error must be
// surfaced by the caller alongside the original mutation error.
func AppendFailureAudit(ctx context.Context, db *gorm.DB, operation string, resourceID int64) error {
	if db == nil {
		return appendAudit(ctx, nil, operation, resourceID, "failure")
	}
	return appendAudit(ctx, db.WithContext(ctx), operation, resourceID, "failure")
}

func appendAudit(ctx context.Context, tx *gorm.DB, operation string, resourceID int64, result string) error {
	q, iam := authorization.QueryScopeFromContext(ctx, operation)
	if !iam {
		return nil
	}
	if tx == nil || q.ActorID <= 0 {
		return authorization.ErrDenied
	}
	if result == "success" && !q.ValidUntil.IsZero() && !time.Now().Before(q.ValidUntil) {
		return authorization.ErrDenied
	}
	versions, err := jsonx.Marshal(q.Versions)
	if err != nil {
		return err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	return tx.Table("resource_write_audits").Create(map[string]any{
		"event_id": hex.EncodeToString(id[:]), "actor_user_id": q.ActorID,
		"operation": operation, "resource_id": strconv.FormatInt(resourceID, 10),
		"decision_versions": string(versions), "reason": authorization.WriteReason(ctx),
		"occurred_at": time.Now().UnixMilli(), "result": result,
	}).Error
}

// RecordWriteFailure appends after rollback and preserves both errors when the
// audit storage itself is unavailable. A successful write is returned directly.
func RecordWriteFailure(ctx context.Context, db *gorm.DB, operation string, resourceID int64, writeErr error) error {
	if writeErr == nil {
		return nil
	}
	if auditErr := AppendFailureAudit(ctx, db, operation, resourceID); auditErr != nil {
		return errors.Join(writeErr, auditErr)
	}
	return writeErr
}
