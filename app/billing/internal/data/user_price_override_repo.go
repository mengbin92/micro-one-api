package data

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"micro-one-api/app/billing/internal/biz"
)

// 097 user-specific routing-group price ratio overrides. The user override
// REPLACES (never multiplies) the published group ratio. Heads carry the live
// version per (user, group); history rows are append-only audit.

type userRoutingPriceHead struct {
	UserID         int64 `gorm:"primaryKey"`
	RoutingGroupID int64 `gorm:"primaryKey"`
	Version        int64
}

func (userRoutingPriceHead) TableName() string { return "user_routing_price_heads" }

type userRoutingPriceOverride struct {
	UserID         int64 `gorm:"primaryKey"`
	RoutingGroupID int64 `gorm:"primaryKey"`
	Version        int64 `gorm:"primaryKey"`
	PriceRatio     float64
	CreatedAt      int64
}

func (userRoutingPriceOverride) TableName() string { return "user_routing_price_overrides" }

type userPriceOverrideRepo struct{ db *gorm.DB }

func NewUserPriceOverrideRepo(d *Data) biz.UserPriceOverrideRepo {
	return &userPriceOverrideRepo{db: d.DB()}
}

func (r *userPriceOverrideRepo) Get(ctx context.Context, userID, groupID int64) (*biz.UserPriceOverride, error) {
	var row userRoutingPriceOverride
	err := r.db.WithContext(ctx).Table("user_routing_price_overrides o").Select("o.*").
		Joins("JOIN user_routing_price_heads h ON h.user_id=o.user_id AND h.routing_group_id=o.routing_group_id AND h.version=o.version").
		Where("o.user_id = ? AND o.routing_group_id = ?", userID, groupID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, biz.ErrRequestSnapshotUnavailable
	}
	return &biz.UserPriceOverride{UserID: row.UserID, GroupID: row.RoutingGroupID, Version: row.Version, PriceRatio: row.PriceRatio}, nil
}

// Set appends a new history version and bumps the head in one transaction.
// A re-set after Clear resumes from MAX(history) so versions stay monotone.
func (r *userPriceOverrideRepo) Set(ctx context.Context, userID, groupID int64, ratio float64) (int64, error) {
	var version int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&userRoutingPriceHead{UserID: userID, RoutingGroupID: groupID}).Error; err != nil {
			return err
		}
		var head userRoutingPriceHead
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND routing_group_id = ?", userID, groupID).Take(&head).Error; err != nil {
			return err
		}
		version = head.Version + 1
		if head.Version == 0 {
			// Fresh head after a Clear: keep the audit chain monotone.
			var maxHistory int64
			if err := tx.Table("user_routing_price_overrides").
				Select("COALESCE(MAX(version),0)").
				Where("user_id = ? AND routing_group_id = ?", userID, groupID).Scan(&maxHistory).Error; err != nil {
				return err
			}
			if maxHistory >= version {
				version = maxHistory + 1
			}
		}
		result := tx.Model(&userRoutingPriceHead{}).
			Where("user_id = ? AND routing_group_id = ?", userID, groupID).
			Update("version", version)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return biz.ErrRoutingContextConflict
		}
		return tx.Create(&userRoutingPriceOverride{UserID: userID, RoutingGroupID: groupID, Version: version, PriceRatio: ratio, CreatedAt: time.Now().Unix()}).Error
	})
	if err != nil {
		return 0, err
	}
	return version, nil
}

// Clear drops the live head and keeps history (idempotent, audit retained).
func (r *userPriceOverrideRepo) Clear(ctx context.Context, userID, groupID int64) error {
	result := r.db.WithContext(ctx).Where("user_id = ? AND routing_group_id = ?", userID, groupID).Delete(&userRoutingPriceHead{})
	if result.Error != nil {
		return biz.ErrRequestSnapshotUnavailable
	}
	return nil
}
