package data

import (
	"context"
	"database/sql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/routing"
	"micro-one-api/platform/routingoutbox"
)

func (r *Repository) UserRoutingFacts(ctx context.Context, userID int64) (*routing.SubjectFacts, error) {
	if r.db == nil {
		return nil, biz.ErrRoutingFactsUnavailable
	}
	var f *routing.SubjectFacts
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var u userModel
		if err := tx.First(&u, userID).Error; err != nil {
			return biz.ErrUserNotFound
		}
		if u.Status != biz.UserStatusEnabled {
			return biz.ErrUserDisabled
		}
		if u.DefaultRoutingGroupID <= 0 || u.RoutingAccessRevision <= 0 {
			return biz.ErrRoutingFactsUnavailable
		}
		var grants []routingGrantModel
		if err := tx.Where("user_id = ?", userID).Order("routing_group_id, source_type, source_ref").Find(&grants).Error; err != nil {
			return err
		}
		f = &routing.SubjectFacts{DefaultGroupID: u.DefaultRoutingGroupID, AccessRevision: u.RoutingAccessRevision, PublicGroupAccess: u.PublicGroupAccess}
		var tokens []tokenModel
		if err := tx.Select("id", "name", "routing_mode", "routing_group_id", "routing_revision").Where("user_id = ? AND TRIM(COALESCE(name, '')) <> ''", userID).Find(&tokens).Error; err != nil {
			return err
		}
		for _, token := range tokens {
			var id int64
			if token.RoutingGroupID != nil {
				id = *token.RoutingGroupID
			}
			f.TokenReferences = append(f.TokenReferences, routing.TokenReference{ID: token.ID, Name: token.Name, Mode: token.RoutingMode, GroupID: id, Revision: token.RoutingRevision})
		}

		for _, g := range grants {
			f.Grants = append(f.Grants, routing.UserGroupGrant{GroupID: g.RoutingGroupID, SourceType: g.SourceType, SourceRef: g.SourceRef, StartsAt: g.StartsAt, ExpiresAt: g.ExpiresAt, Status: g.Status})
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return f, err
}
func (r *Repository) UpdateRoutingAccess(ctx context.Context, c biz.RoutingAccessChange) error {
	if r.db == nil {
		return biz.ErrRoutingFactsUnavailable
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// CAS is also a write lock on SQLite. Every grant mutation shares this row.
		updates := map[string]any{"routing_access_revision": c.ExpectedRevision + 1}
		if c.Operation == "default" {
			updates["default_routing_group_id"] = c.GroupID
			updates["group"] = c.GroupKey
		}
		if c.Operation == "public_access" {
			updates["public_group_access"] = c.PublicGroupAccess
		}
		result := tx.Model(&userModel{}).Where("id = ? AND routing_access_revision = ? AND status = ?", c.UserID, c.ExpectedRevision, biz.UserStatusEnabled).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return biz.ErrRoutingAccessConflict
		}
		if c.Operation == "grant" || c.Operation == "revoke" {
			status := "active"
			if c.Operation == "revoke" {
				status = "revoked"
			}
			g := routingGrantModel{UserID: c.UserID, RoutingGroupID: c.GroupID, SourceType: c.SourceType, SourceRef: c.SourceRef, StartsAt: c.StartsAt, ExpiresAt: c.ExpiresAt, Status: status}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "routing_group_id"}, {Name: "source_type"}, {Name: "source_ref"}}, DoUpdates: clause.AssignmentColumns([]string{"starts_at", "expires_at", "status"})}).Create(&g).Error; err != nil {
				return err
			}
		}
		return routingoutbox.Enqueue(tx, "identity", "user", c.UserID, c.ExpectedRevision+1)
	})
}
func (r *Repository) SetTokenRouting(ctx context.Context, userID, tokenID int64, mode string, groupID, revision int64) (int64, error) {
	if r.db == nil {
		return 0, biz.ErrRoutingFactsUnavailable
	}
	var id any
	if groupID > 0 {
		id = groupID
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&tokenModel{}).Where("id = ? AND user_id = ? AND routing_revision = ? AND TRIM(COALESCE(name, '')) <> ''", tokenID, userID, revision).Updates(map[string]any{"routing_mode": mode, "routing_group_id": id, "routing_revision": revision + 1})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return biz.ErrRoutingAccessConflict
		}
		return routingoutbox.Enqueue(tx, "identity", "token", tokenID, revision+1)
	})
	if err != nil {
		return 0, err
	}
	return revision + 1, nil
}
