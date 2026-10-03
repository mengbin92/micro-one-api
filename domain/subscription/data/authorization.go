package data

import (
	"context"
	"gorm.io/gorm"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/database/authzquery"
)

var subscriptionWriteOps = []string{"subscription.user_subscription.assign", "subscription.user_subscription.change", "subscription.user_subscription.extend", "subscription.user_subscription.revoke", "subscription.user_subscription.quota.reset"}

func resourceFacts(id, user int64) authorization.ObjectFacts {
	return authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: id, OwnerUserID: user}
}
func requireOperations(ctx context.Context, id, user int64, ops ...string) error {
	for _, op := range ops {
		if _, iam := authorization.QueryScopeFromContext(ctx, op); iam && authorization.WriteReason(ctx) == "" {
			return authorization.ErrDenied
		}
		if err := authorization.Require(ctx, op, resourceFacts(id, user)); err != nil {
			return err
		}
	}
	return nil
}
func auditOperations(ctx context.Context, tx *gorm.DB, id int64, ops ...string) error {
	for _, op := range ops {
		if err := authzquery.AppendWriteAudit(ctx, tx, op, id); err != nil {
			return err
		}
	}
	return nil
}
func subscriptionQuery(ctx context.Context, db *gorm.DB) (*gorm.DB, error) {
	return authzquery.ApplyContext(ctx, db, authzquery.Columns{Resource: "id", User: "user_id"}, "subscription.user_subscription.list", "subscription.user_subscription.read", "subscription.user_subscription.report.read")
}
func groupQuery(ctx context.Context, db *gorm.DB) (*gorm.DB, error) {
	return authzquery.ApplyContext(ctx, db, authzquery.Columns{Resource: "id"}, "subscription.quota_policy.list", "subscription.quota_policy.read")
}
func planQuery(ctx context.Context, db *gorm.DB) (*gorm.DB, error) {
	return authzquery.ApplyContext(ctx, db, authzquery.Columns{Resource: "id"}, "subscription.plan.list", "subscription.plan.read")
}

func memoryVisible(ctx context.Context, id, user int64, operations ...string) bool {
	for _, operation := range operations {
		if authorization.Require(ctx, operation, resourceFacts(id, user)) != nil {
			return false
		}
	}
	return true
}
