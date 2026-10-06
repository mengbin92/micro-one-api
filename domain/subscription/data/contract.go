package data

import (
	"context"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/subscription/biz"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/routingoutbox"
)

func encodeContract(c *biz.SubscriptionContract) *string {
	if c == nil {
		return nil
	}
	b, _ := jsonx.Marshal(c)
	s := string(b)
	return &s
}
func decodeContract(raw *string) *biz.SubscriptionContract {
	if raw == nil || *raw == "" {
		return nil
	}
	c := &biz.SubscriptionContract{}
	if jsonx.Unmarshal([]byte(*raw), c) != nil {
		return &biz.SubscriptionContract{}
	}
	return c // Invalid persisted contracts stay non-nil and fail domain validation.
}
func contractCoverage(raw *string) []biz.RoutingCoverage {
	c := decodeContract(raw)
	if c == nil {
		return nil
	}
	return c.Coverage
}
func syncContractCoverage(tx *gorm.DB, table, column string, id int64, c *biz.SubscriptionContract) error {
	if c != nil && c.Validate() != nil {
		return biz.ErrSubscriptionContractInvalid
	}
	if err := tx.Table(table).Where(column+" = ?", id).Delete(map[string]any{}).Error; err != nil {
		return err
	}
	if c == nil {
		return nil
	}
	for _, g := range c.Coverage {
		grant := 0
		if g.GrantsAccess {
			grant = 1
		}
		if err := tx.Table(table).Create(map[string]any{column: id, "routing_group_id": g.GroupID, "grants_access": grant}).Error; err != nil {
			return err
		}
	}
	return nil
}

func subscriptionMutation(ctx context.Context, db *gorm.DB, s *biz.UserSubscription, write func(*gorm.DB) error) error {
	iam := false
	for _, op := range subscriptionWriteOps {
		if _, ok := authorization.QueryScopeFromContext(ctx, op); ok {
			iam = true
		}
	}
	if !biz.EntitlementsEnabled() && s.Contract == nil && !iam {
		return write(db.WithContext(ctx))
	}
	var revision int64
	err := runSubscriptionTx(ctx, db, func(ctx context.Context, tx *gorm.DB) error {
		var current subscriptionModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, s.ID).Error; err != nil {
			return err
		}
		if err := requireOperations(ctx, current.ID, current.UserID, subscriptionWriteOps...); err != nil {
			return err
		}
		if err := requireOperations(ctx, s.ID, s.UserID, subscriptionWriteOps...); err != nil {
			return err
		}
		for _, operation := range []string{"subscription.user_subscription.assign", "subscription.user_subscription.change"} {
			if _, iam := authorization.QueryScopeFromContext(ctx, operation); iam {
				if err := validateEnabledQuotaPolicy(tx, s.GroupID); err != nil {
					return err
				}
				break
			}
		}
		if iam && s.EntitlementRevision != current.EntitlementRevision {
			return biz.ErrSubscriptionContractConflict
		}
		if expected, supplied := biz.ExpectedRevision(ctx); iam && supplied && expected != current.EntitlementRevision {
			return biz.ErrSubscriptionContractConflict
		}
		if !biz.EntitlementsEnabled() && s.Contract == nil {
			if err := write(tx); err != nil {
				return err
			}
			revision = current.EntitlementRevision + 1
			if err := tx.Model(&subscriptionModel{}).Where("id = ?", s.ID).Update("entitlement_revision", revision).Error; err != nil {
				return err
			}
			return auditOperations(ctx, tx, s.ID, subscriptionWriteOps...)
		}
		if s.EntitlementRevision > 0 && s.EntitlementRevision != current.EntitlementRevision {
			return biz.ErrSubscriptionContractConflict
		}
		if current.ContractSnapshot != nil && s.Contract == nil {
			return biz.ErrSubscriptionContractConflict
		}
		if s.Contract != nil && s.Contract.Validate() != nil {
			return biz.ErrSubscriptionContractInvalid
		}
		oldContract := decodeContract(current.ContractSnapshot)
		if s.Contract != nil && (oldContract == nil || oldContract.Digest != s.Contract.Digest) {
			if err := LockContractReferences(tx); err != nil {
				return err
			}
			if err := ValidateContractBillingModes(tx, s.Contract); err != nil {
				return err
			}
		}
		if err := write(tx); err != nil {
			return err
		}
		revision = current.EntitlementRevision + 1
		if err := tx.Model(&subscriptionModel{}).Where("id = ?", s.ID).Update("entitlement_revision", revision).Error; err != nil {
			return err
		}
		if err := syncContractCoverage(tx, "subscription_routing_entitlements", "subscription_id", s.ID, s.Contract); err != nil {
			return err
		}
		if err := routingoutbox.Enqueue(tx, "subscription", "subscription", s.ID, revision); err != nil {
			return err
		}
		return auditOperations(ctx, tx, s.ID, subscriptionWriteOps...)
	})
	if err == nil {
		s.EntitlementRevision = revision
	}
	return err
}
func (r *Repository) StartEntitlementOutbox() func() {
	return routingoutbox.Start(r.db, r.redis, "subscription", routingoutbox.Report)
}

// LockContractReferences serializes contract publication with billing-mode changes.
// The caller owns the transaction; lifecycle writes use this lock before exposing
// a new reference. Request usage updates do not take this publication lock.
func LockContractReferences(tx *gorm.DB) error {
	return tx.Exec("UPDATE subscription_contract_guard SET revision = revision + 1 WHERE id = 1").Error
}
func ValidateContractBillingModes(tx *gorm.DB, c *biz.SubscriptionContract) error {
	if c == nil {
		return nil
	}
	if c.Validate() != nil {
		return biz.ErrSubscriptionContractInvalid
	}
	ids := make([]int64, 0, len(c.Coverage))
	for _, g := range c.Coverage {
		ids = append(ids, g.GroupID)
	}
	iamReferences := false
	for _, op := range []string{"subscription.plan.create", "subscription.plan.update", "subscription.plan.publish", "subscription.user_subscription.assign", "subscription.user_subscription.change", "billing.account.read"} {
		if _, ok := authorization.QueryScopeFromContext(tx.Statement.Context, op); ok {
			iamReferences = true
		}
	}
	if iamReferences {
		var validGroups int64
		if err := tx.Table("routing_groups").Where("id IN ? AND status = ?", ids, "enabled").Count(&validGroups).Error; err != nil {
			return err
		}
		if validGroups != int64(len(ids)) {
			return biz.ErrSubscriptionContractInvalid
		}

	}
	var n int64
	if err := tx.Table("routing_billing_policies p").Joins("JOIN routing_billing_policy_heads h ON h.routing_group_id=p.routing_group_id AND h.version=p.version").Where("p.routing_group_id IN ? AND p.billing_mode = ?", ids, "wallet_only").Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return biz.ErrSubscriptionContractInvalid
	}
	return nil
}
