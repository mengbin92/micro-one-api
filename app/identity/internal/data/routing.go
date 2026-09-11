package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/routing"
	"micro-one-api/platform/routingoutbox"
)

type routingGrantModel struct {
	UserID         int64  `gorm:"primaryKey;column:user_id"`
	RoutingGroupID int64  `gorm:"primaryKey;column:routing_group_id"`
	SourceType     string `gorm:"primaryKey;column:source_type"`
	SourceRef      string `gorm:"primaryKey;column:source_ref"`
	StartsAt       int64  `gorm:"column:starts_at"`
	ExpiresAt      int64  `gorm:"column:expires_at"`
	Status         string `gorm:"column:status"`
}

func (routingGrantModel) TableName() string { return "user_routing_group_grants" }

// NewRoutingBackfillRepository deliberately avoids startup token-hash backfill.
func NewRoutingBackfillRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

func (r *Repository) CheckRoutingSchema(ctx context.Context) error {
	if r.db == nil {
		return biz.ErrRoutingFactsUnavailable
	}
	for _, column := range []string{"default_routing_group_id", "routing_access_revision", "public_group_access"} {
		if !r.db.Migrator().HasColumn("users", column) {
			return biz.ErrRoutingFactsUnavailable
		}
	}
	for _, column := range []string{"routing_mode", "routing_group_id", "routing_revision"} {
		if !r.db.Migrator().HasColumn("tokens", column) {
			return biz.ErrRoutingFactsUnavailable
		}
	}
	if !r.db.Migrator().HasTable(&routingGrantModel{}) || !r.db.Migrator().HasTable("routing_change_outbox") {
		return biz.ErrRoutingFactsUnavailable
	}
	var count int64
	if err := r.db.WithContext(ctx).Table("users").Where("default_routing_group_id IS NULL OR routing_access_revision < 1").Count(&count).Error; err != nil {
		return biz.ErrRoutingFactsUnavailable
	}
	if count > 0 {
		return biz.ErrRoutingFactsUnavailable
	}
	return nil
}

func (r *Repository) GetRoutingFacts(ctx context.Context, userID, tokenID int64, expectedKey string) (*routing.SubjectFacts, error) {
	if r.db == nil {
		return nil, biz.ErrRoutingFactsUnavailable
	}
	var facts *routing.SubjectFacts
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user userModel
		if err := tx.First(&user, userID).Error; err != nil {
			return biz.ErrRoutingFactsUnavailable
		}
		if user.Group != expectedKey {
			return biz.ErrRoutingAccessConflict
		}
		if user.Status != biz.UserStatusEnabled {
			return biz.ErrUserDisabled
		}
		if user.DefaultRoutingGroupID <= 0 || user.RoutingAccessRevision <= 0 {
			return biz.ErrRoutingFactsUnavailable
		}
		var token struct {
			RoutingMode     string
			RoutingGroupID  int64
			RoutingRevision int64
			Status          int32
			ExpiredTime     int64
		}
		if err := tx.Table("tokens").Select("routing_mode, routing_group_id, routing_revision, status, expired_time").Where("id = ? AND user_id = ?", tokenID, userID).Take(&token).Error; err != nil {
			return biz.ErrRoutingFactsUnavailable
		}
		if token.Status != biz.TokenStatusEnabled {
			return biz.ErrTokenDisabled
		}
		if token.ExpiredTime > 0 && token.ExpiredTime <= time.Now().Unix() {
			return biz.ErrTokenExpired
		}
		if !routing.ValidPolicy(token.RoutingMode, token.RoutingGroupID) && token.RoutingMode != "ordered" || token.RoutingRevision <= 0 {
			return biz.ErrRoutingDefaultInvalid
		}
		var tokenGroupIDs []int64
		if token.RoutingMode == "ordered" {
			orders, err := loadTokenGroupOrders(tx, []int64{tokenID})
			if err != nil {
				return biz.ErrRoutingFactsUnavailable
			}
			tokenGroupIDs = orders[tokenID]
			if !routing.ValidOrderedPolicy(token.RoutingMode, token.RoutingGroupID, tokenGroupIDs) {
				return biz.ErrRoutingDefaultInvalid
			}
		}
		var grants []routingGrantModel
		if err := tx.Where("user_id = ?", userID).Order("routing_group_id, source_type, source_ref").Find(&grants).Error; err != nil {
			return biz.ErrRoutingFactsUnavailable
		}
		facts = &routing.SubjectFacts{DefaultGroupID: user.DefaultRoutingGroupID, PublicGroupAccess: user.PublicGroupAccess, AccessRevision: user.RoutingAccessRevision, TokenMode: token.RoutingMode, TokenGroupID: token.RoutingGroupID, TokenGroupIDs: tokenGroupIDs, TokenRevision: token.RoutingRevision}
		for _, g := range grants {
			facts.Grants = append(facts.Grants, routing.UserGroupGrant{GroupID: g.RoutingGroupID, SourceType: g.SourceType, SourceRef: g.SourceRef, StartsAt: g.StartsAt, ExpiresAt: g.ExpiresAt, Status: g.Status})
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return facts, err
}

func migrationGrant(userID, groupID int64) *routingGrantModel {
	return &routingGrantModel{UserID: userID, RoutingGroupID: groupID, SourceType: "migration", SourceRef: "legacy_group", Status: "active"}
}

func (r *Repository) createRoutingUserDB(ctx context.Context, user *biz.User, model *userModel) error {
	if user.DefaultRoutingGroupID <= 0 {
		return biz.ErrRoutingDefaultInvalid
	}
	model.DefaultRoutingGroupID, model.RoutingAccessRevision, model.PublicGroupAccess = user.DefaultRoutingGroupID, 1, "explicit_only"
	if err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(model).Error; err != nil {
			return err
		}
		return tx.Create(migrationGrant(model.ID, model.DefaultRoutingGroupID)).Error
	}); err != nil {
		return err
	}
	user.ID, user.RoutingAccessRevision, user.PublicGroupAccess = model.ID, 1, "explicit_only"
	return nil
}

func (r *Repository) updateRoutingUserDB(ctx context.Context, user *biz.User, updates map[string]any) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current userModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, user.ID).Error; err != nil {
			return err
		}
		if current.RoutingAccessRevision < 1 || current.RoutingAccessRevision != user.RoutingAccessRevision {
			return biz.ErrRoutingAccessConflict
		}
		if current.Group != user.Group {
			if user.DefaultRoutingGroupID <= 0 || user.DefaultRoutingGroupID == current.DefaultRoutingGroupID {
				return biz.ErrRoutingDefaultInvalid
			}
			updates["default_routing_group_id"] = user.DefaultRoutingGroupID
			updates["routing_access_revision"] = current.RoutingAccessRevision + 1
			if err := tx.Where("user_id = ? AND source_type = ? AND source_ref = ?", user.ID, "migration", "legacy_group").Delete(&routingGrantModel{}).Error; err != nil {
				return err
			}
			if err := tx.Create(migrationGrant(user.ID, user.DefaultRoutingGroupID)).Error; err != nil {
				return err
			}
		}
		updates["routing_access_revision"] = current.RoutingAccessRevision + 1
		if err := tx.Model(&userModel{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
			return err
		}
		return routingoutbox.Enqueue(tx, "identity", "user", user.ID, current.RoutingAccessRevision+1)
	})
}

// BackfillRoutingGroups consumes channel-owned facts through an explicit
// command. It never queries channel's schema or changes a group's state.
func (r *Repository) BackfillRoutingGroups(ctx context.Context, groups []*routing.Group, apply bool) (int, error) {
	if r.db == nil {
		return 0, biz.ErrRoutingFactsUnavailable
	}
	byKey := make(map[string]int64)
	byID := make(map[int64]bool)
	for _, g := range groups {
		if g == nil || g.ID <= 0 || g.Key == "" || byKey[g.Key] != 0 || byID[g.ID] {
			return 0, biz.ErrRoutingDefaultInvalid
		}
		byKey[g.Key], byID[g.ID] = g.ID, true
	}
	rehearsal := errors.New("routing backfill rehearsal")
	count := 0
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var users []userModel
		if err := tx.Select("id", "group", "default_routing_group_id", "routing_access_revision", "public_group_access").Order("id").Find(&users).Error; err != nil {
			return err
		}
		for _, u := range users {
			id := byKey[u.Group]
			if id <= 0 {
				return fmt.Errorf("user %d has an unmapped routing group", u.ID)
			}
			if u.DefaultRoutingGroupID != 0 {
				if u.DefaultRoutingGroupID != id || u.RoutingAccessRevision < 1 {
					return biz.ErrRoutingAccessConflict
				}
				var grant routingGrantModel
				if err := tx.Where(migrationGrant(u.ID, id)).First(&grant).Error; err != nil {
					return biz.ErrRoutingAccessConflict
				}
				continue
			}
			if err := tx.Model(&userModel{}).Where("id = ? AND default_routing_group_id IS NULL", u.ID).Updates(map[string]any{"default_routing_group_id": id, "routing_access_revision": 1, "public_group_access": "explicit_only"}).Error; err != nil {
				return err
			}
			if err := tx.Create(migrationGrant(u.ID, id)).Error; err != nil {
				return err
			}
			count++
		}
		if !apply {
			return rehearsal
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if errors.Is(err, rehearsal) {
		err = nil
	}
	return count, err
}
