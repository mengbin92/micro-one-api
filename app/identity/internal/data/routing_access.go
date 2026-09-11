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

// tokenRoutingGroupOrderModel stores the token's explicit ordered candidate
// group list (routing_mode=ordered). position preserves user order.
type tokenRoutingGroupOrderModel struct {
	TokenID        int64 `gorm:"primaryKey"`
	RoutingGroupID int64 `gorm:"primaryKey"`
	Position       int
}

func (tokenRoutingGroupOrderModel) TableName() string { return "token_routing_group_orders" }

// loadTokenGroupOrders batch-loads ordered candidate lists keyed by token ID.
func loadTokenGroupOrders(tx *gorm.DB, tokenIDs []int64) (map[int64][]int64, error) {
	out := make(map[int64][]int64)
	if len(tokenIDs) == 0 {
		return out, nil
	}
	var rows []tokenRoutingGroupOrderModel
	if err := tx.Where("token_id IN ?", tokenIDs).Order("token_id, position").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.TokenID] = append(out[row.TokenID], row.RoutingGroupID)
	}
	return out, nil
}

// replaceTokenGroupOrders swaps the ordered candidate list for a token.
// An empty list deletes every row (mode != ordered).
func replaceTokenGroupOrders(tx *gorm.DB, tokenID int64, groupIDs []int64) error {
	if err := tx.Where("token_id = ?", tokenID).Delete(&tokenRoutingGroupOrderModel{}).Error; err != nil {
		return err
	}
	for i, gid := range groupIDs {
		row := tokenRoutingGroupOrderModel{TokenID: tokenID, RoutingGroupID: gid, Position: i}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

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
		var tokenIDs []int64
		for i := range tokens {
			tokenIDs = append(tokenIDs, tokens[i].ID)
		}
		orders, err := loadTokenGroupOrders(tx, tokenIDs)
		if err != nil {
			return err
		}
		for _, token := range tokens {
			var id int64
			if token.RoutingGroupID != nil {
				id = *token.RoutingGroupID
			}
			f.TokenReferences = append(f.TokenReferences, routing.TokenReference{ID: token.ID, Name: token.Name, Mode: token.RoutingMode, GroupID: id, Revision: token.RoutingRevision, GroupIDs: orders[token.ID]})
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
		// A revoke targeting a grant that is not active is a no-op: it must not
		// mint a dead 'revoked' row, bump the revision, or emit an outbox event.
		//
		// Probe read-only on purpose. Flipping the status here would take the
		// grant row before the user row, while the grant/revoke upsert below
		// takes the user row first — opposite lock orders that can deadlock when
		// the same user is concurrently granted and revoked. The status change
		// itself is applied by that upsert.
		if c.Operation == "revoke" {
			var active int64
			if err := tx.Model(&routingGrantModel{}).Where("user_id = ? AND routing_group_id = ? AND source_type = ? AND source_ref = ? AND status = ?", c.UserID, c.GroupID, c.SourceType, c.SourceRef, "active").Count(&active).Error; err != nil {
				return err
			}
			if active == 0 {
				return nil
			}
		}
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
		if c.Operation == "default" {
			// Mirror updateRoutingUserDB: moving the default group replaces the
			// migration grant, otherwise the stale grant keeps the old group
			// reachable forever.
			if err := tx.Where("user_id = ? AND source_type = ? AND source_ref = ?", c.UserID, "migration", "legacy_group").Delete(&routingGrantModel{}).Error; err != nil {
				return err
			}
			if err := tx.Create(migrationGrant(c.UserID, c.GroupID)).Error; err != nil {
				return err
			}
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
func (r *Repository) SetTokenRouting(ctx context.Context, userID, tokenID int64, mode string, groupID, revision int64, groupIDs []int64) (int64, error) {
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
		if err := replaceTokenGroupOrders(tx, tokenID, groupIDs); err != nil {
			return err
		}
		return routingoutbox.Enqueue(tx, "identity", "token", tokenID, revision+1)
	})
	if err != nil {
		return 0, err
	}
	return revision + 1, nil
}
