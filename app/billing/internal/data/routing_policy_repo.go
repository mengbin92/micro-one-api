package data

import (
	"context"
	"errors"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/domain/routing"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	subscriptiondata "micro-one-api/domain/subscription/data"
	applogger "micro-one-api/platform/logging"
	"time"
)

type routingPolicyModel struct {
	RoutingGroupID int64
	Version        int64
	BillingMode    string
	PriceRatio     float64
	EffectiveAt    int64
}

func (routingPolicyModel) TableName() string { return "routing_billing_policies" }

type routingPolicyHead struct {
	RoutingGroupID int64 `gorm:"primaryKey"`
	Version        int64
}

func (routingPolicyHead) TableName() string { return "routing_billing_policy_heads" }

type routingPolicyRepo struct{ db *gorm.DB }

func NewRoutingPolicyRepo(d *Data) biz.RoutingPolicyRepo { return &routingPolicyRepo{db: d.DB()} }
func (r *routingPolicyRepo) Get(ctx context.Context, id int64) (*routing.BillingPolicy, error) {
	var row routingPolicyModel
	err := r.db.WithContext(ctx).Table("routing_billing_policies p").Select("p.*").Joins("JOIN routing_billing_policy_heads h ON h.routing_group_id=p.routing_group_id AND h.version=p.version").Where("p.routing_group_id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, biz.ErrRequestSnapshotUnavailable
	}
	return &routing.BillingPolicy{GroupID: row.RoutingGroupID, Version: row.Version, BillingMode: row.BillingMode, PriceRatio: row.PriceRatio, EffectiveAt: row.EffectiveAt}, nil
}
func (r *routingPolicyRepo) Publish(ctx context.Context, p *routing.BillingPolicy, expected int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := subscriptiondata.LockContractReferences(tx); err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&routingPolicyHead{RoutingGroupID: p.GroupID}).Error; err != nil {
			return err
		}
		var head routingPolicyHead
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&head, p.GroupID).Error; err != nil {
			return err
		}
		if head.Version != expected {
			return biz.ErrRoutingContextConflict
		}
		mode := routing.SubscriptionFirst
		if head.Version > 0 {
			var old routingPolicyModel
			if err := tx.Where("routing_group_id = ? AND version = ?", p.GroupID, head.Version).Take(&old).Error; err != nil {
				return err
			}
			mode = old.BillingMode
		}
		if mode != p.BillingMode {
			blocked, err := routingPolicyHasContracts(tx, p.GroupID)
			if err != nil {
				return err
			}
			if blocked {
				return biz.ErrRoutingContextConflict
			}
		}
		row := routingPolicyModel{RoutingGroupID: p.GroupID, Version: expected + 1, BillingMode: p.BillingMode, PriceRatio: p.PriceRatio, EffectiveAt: time.Now().Unix()}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		result := tx.Model(&routingPolicyHead{}).Where("routing_group_id = ? AND version = ?", p.GroupID, expected).Update("version", expected+1)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return biz.ErrRoutingContextConflict
		}
		p.Version = row.Version
		p.EffectiveAt = row.EffectiveAt
		return nil
	})
}

// Conservative reference check: legacy contracts may cover any authorized group.
func routingPolicyHasContracts(tx *gorm.DB, id int64) (bool, error) {
	var n int64
	err := tx.Table("user_subscriptions s").Where("s.status = ? AND s.expires_at > ?", "active", time.Now().Unix()).Where("s.contract_snapshot IS NULL OR s.contract_snapshot = '' OR EXISTS (SELECT 1 FROM subscription_routing_entitlements e WHERE e.subscription_id=s.id AND e.routing_group_id=?)", id).Count(&n).Error
	if err != nil || n > 0 {
		return n > 0, err
	}
	err = tx.Table("subscription_plans p").Where("p.for_sale = ?", true).Where("p.contract_snapshot IS NULL OR p.contract_snapshot = '' OR EXISTS (SELECT 1 FROM subscription_plan_routing_groups e WHERE e.plan_id=p.id AND e.routing_group_id=?)", id).Count(&n).Error
	if err != nil || n > 0 {
		return n > 0, err
	}
	var orders []struct {
		ID           int64
		PlanSnapshot string
	}
	if err := tx.Table("payment_orders").Select("id, plan_snapshot").Where("status = ? AND (plan_id > 0 OR group_id > 0)", "pending").Find(&orders).Error; err != nil {
		return false, err
	}
	for _, o := range orders {
		snapshot, err := subscriptionbiz.DecodePlanSnapshot(o.PlanSnapshot)
		if err != nil {
			// A single corrupt pending order must not block every policy
			// publish; treat the unknown snapshot as possibly covering the
			// group (the check is conservative by design). Log the offender so
			// the cause stays visible instead of surfacing only as an
			// unexplained "referenced by contracts" rejection.
			applogger.Log.Warn("undecodable pending order snapshot; treating it as covering every routing group",
				zap.String("component", "billing.data"),
				zap.Int64("order_id", o.ID),
				zap.Int64("routing_group_id", id),
				zap.Error(err))
			return true, nil
		}
		if snapshot.Contract.Covers(id) {
			return true, nil
		}
	}
	return false, nil
}
