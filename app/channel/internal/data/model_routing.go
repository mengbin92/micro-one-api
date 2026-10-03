package data

import (
	"context"
	"sort"
	"strings"

	"gorm.io/gorm/clause"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/wildcard"
	"micro-one-api/platform/database/authzquery"

	"gorm.io/gorm"
)

// modelRoutingModel is the PO for the model_routings table (P2 #3). Stays
// inside data; never crosses into biz or service.
type modelRoutingModel struct {
	ID                    int64  `gorm:"column:id;primaryKey;autoIncrement"`
	GroupName             string `gorm:"column:group_name"`
	Model                 string `gorm:"column:model"`
	Platform              string `gorm:"column:platform"`
	SubscriptionAccountID int64  `gorm:"column:subscription_account_id"`
	Enabled               bool   `gorm:"column:enabled"`
	Priority              int32  `gorm:"column:priority"`
	CreatedAt             int64  `gorm:"column:created_at"`
	UpdatedAt             int64  `gorm:"column:updated_at"`
	Revision              int64  `gorm:"column:revision;default:1"`
}

func (modelRoutingModel) TableName() string { return "model_routings" }

// ── DO ↔ PO conversion helpers (free functions, data-only) ─────────────────

func newModelRoutingPO(do *biz.ModelRouting) *modelRoutingModel {
	if do == nil {
		return nil
	}
	return &modelRoutingModel{
		ID:                    do.ID,
		GroupName:             do.GroupName,
		Model:                 do.Model,
		Platform:              do.Platform,
		SubscriptionAccountID: do.SubscriptionAccountID,
		Enabled:               do.Enabled,
		Priority:              do.Priority,
		CreatedAt:             do.CreatedAt,
		UpdatedAt:             do.UpdatedAt,
		Revision:              do.Revision,
	}
}

func toModelRoutingDO(po *modelRoutingModel) *biz.ModelRouting {
	if po == nil {
		return nil
	}
	return &biz.ModelRouting{
		ID:                    po.ID,
		GroupName:             po.GroupName,
		Model:                 po.Model,
		Platform:              po.Platform,
		SubscriptionAccountID: po.SubscriptionAccountID,
		Enabled:               po.Enabled,
		Priority:              po.Priority,
		CreatedAt:             po.CreatedAt,
		UpdatedAt:             po.UpdatedAt,
		Revision:              po.Revision,
	}
}

// ── Repository methods ──────────────────────────────────────────────────────

func (r *Repository) ListModelRoutings(ctx context.Context, group, model, platform string) ([]*biz.ModelRouting, error) {
	if r.db == nil {
		if _, scoped := authorization.QueryScopeFromContext(ctx, "channel.model_routing.read"); scoped {
			return nil, authorization.ErrDenied
		}
	}
	if r.db != nil {
		return r.listModelRoutingsDB(ctx, group, model, platform)
	}
	return r.listModelRoutingsMemory(group, model, platform)
}

func (r *Repository) ListModelRoutingsForSelect(ctx context.Context, group, model, platform string) ([]*biz.ModelRouting, error) {
	// Fetch all enabled rows for the group, then let the biz
	// RoutingMatchForSelect helper apply exact-before-wildcard precedence.
	// Returning exact-first is an optimisation the DB path does by ordering.
	rows, err := r.ListModelRoutings(ctx, group, "", platform)
	if err != nil {
		return nil, err
	}
	// Keep only enabled rows and order: exact (non-wildcard) first, then
	// specific wildcards, then "*". The biz helper re-applies precedence but
	// this ordering makes the DB query result deterministic.
	enabled := make([]*biz.ModelRouting, 0, len(rows))
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		enabled = append(enabled, row)
	}
	sort.SliceStable(enabled, func(i, j int) bool {
		pi, pj := wildcard.IsPattern(enabled[i].Model), wildcard.IsPattern(enabled[j].Model)
		if pi != pj {
			return !pi // exact (non-pattern) first
		}
		if enabled[i].Model == "*" {
			return false
		}
		if enabled[j].Model == "*" {
			return true
		}
		return enabled[i].Model < enabled[j].Model
	})
	return enabled, nil
}

func (r *Repository) listModelRoutingsDB(ctx context.Context, group, model, platform string) ([]*biz.ModelRouting, error) {
	query := r.db.WithContext(ctx).Model(&modelRoutingModel{})
	query, scopeErr := r.modelRoutingQuery(ctx, query)
	if scopeErr != nil {
		return nil, scopeErr
	}
	if group != "" {
		query = r.mappingGroupScope(query, group, "")
	}
	if model != "" {
		query = query.Where("model = ?", model)
	}
	if platform != "" {
		// P2 #3 review fix: an empty platform on a routing row means "any
		// platform". The relay always infers a concrete platform (e.g. codex),
		// so an equality filter platform = ? would never match a row left
		// empty (the UI-recommended default). Match both the exact platform
		// and empty-platform rows.
		query = query.Where("platform = ? OR platform = ?", platform, "")
	}
	var rows []modelRoutingModel
	if err := query.Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]*biz.ModelRouting, 0, len(rows))
	for i := range rows {
		result = append(result, toModelRoutingDO(&rows[i]))
	}
	return result, nil
}

func (r *Repository) listModelRoutingsMemory(group, model, platform string) ([]*biz.ModelRouting, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	result := make([]*biz.ModelRouting, 0)
	for _, row := range r.modelRoutings {
		if group != "" && row.GroupName != group {
			continue
		}
		if model != "" && row.Model != model {
			continue
		}
		if platform != "" && row.Platform != platform && row.Platform != "" {
			// P2 #3 review fix: empty platform means "any platform" (see DB path).
			continue
		}
		clone := *row
		result = append(result, &clone)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (r *Repository) UpsertModelRouting(ctx context.Context, do *biz.ModelRouting) error {
	if ctx.Value(modelWriteKey{}) == nil {
		_, createScoped := authorization.QueryScopeFromContext(ctx, "channel.model_routing.create")
		_, updateScoped := authorization.QueryScopeFromContext(ctx, "channel.model_routing.update")
		if createScoped || updateScoped {
			if r.db == nil {
				return authorization.ErrDenied
			}
			expected, present := authorization.ExpectedResourceRevision(ctx)
			if !present || authorization.WriteReason(ctx) == "" {
				return authorization.ErrWritePrecondition
			}
			return authzquery.RunInTx(ctx, r.db, 3, func(current context.Context, tx *gorm.DB) error {
				owner := &Repository{db: tx, encKey: r.encKey, routingGroupRelations: r.routingGroupRelations, routingGroupDualWrite: r.routingGroupDualWrite}
				ids, err := owner.RoutingGroupAuthorizationIDs(current, do.GroupName)
				if err != nil {
					return err
				}
				accountFacts, err := owner.SubscriptionAccountAuthorizationFacts(current, do.SubscriptionAccountID)
				if err != nil {
					return err
				}
				for _, id := range ids {
					found := false
					for _, member := range accountFacts.RoutingGroupIDs {
						if id == member {
							found = true
						}
					}
					if !found {
						return authorization.ErrDenied
					}
				}
				var existing modelRoutingModel
				err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("group_name = ? AND model = ? AND platform = ? AND subscription_account_id = ?", do.GroupName, do.Model, do.Platform, do.SubscriptionAccountID).First(&existing).Error
				op := "channel.model_routing.create"
				if err == nil {
					op = "channel.model_routing.update"
				} else if !isGormNotFound(err) {
					return err
				}
				facts := authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: existing.ID, RoutingGroupIDs: ids}
				if err := authorization.Require(current, op, facts); err != nil {
					return err
				}
				if op == "channel.model_routing.create" {
					if expected != 0 {
						return authorization.ErrWriteConflict
					}
				} else if expected == 0 || int64(expected) != existing.Revision {
					return authorization.ErrWriteConflict
				}
				if err := owner.UpsertModelRouting(context.WithValue(current, modelWriteKey{}, true), do); err != nil {
					return err
				}
				return authzquery.AppendWriteAudit(current, tx, op, do.ID)
			})
		}
	}
	if r.db != nil {
		return r.upsertModelRoutingDB(ctx, do)
	}
	return r.upsertModelRoutingMemory(do)
}

func (r *Repository) upsertModelRoutingDB(ctx context.Context, do *biz.ModelRouting) error {
	po := newModelRoutingPO(do)
	// Read-then-write upsert (matches the existing UpsertChannelMapping /
	// UpsertSubscriptionMapping pattern and works across MySQL/SQLite/Postgres
	// without relying on driver-specific ON CONFLICT column matching).
	return r.routingMappingTransaction(ctx, "model_routings", map[string]any{"group_name": po.GroupName, "model": po.Model, "platform": po.Platform, "subscription_account_id": po.SubscriptionAccountID}, po.GroupName, func(tx *gorm.DB) error {
		var existing modelRoutingModel
		err := tx.Where("group_name = ? AND model = ? AND platform = ? AND subscription_account_id = ?",
			po.GroupName, po.Model, po.Platform, po.SubscriptionAccountID).First(&existing).Error
		if err == nil {
			do.ID = existing.ID
			do.Revision = existing.Revision + 1
			updates := map[string]any{
				"priority":   po.Priority,
				"updated_at": po.UpdatedAt,
				"revision":   do.Revision,
			}
			if do.EnabledHasValue {
				updates["enabled"] = po.Enabled
			}
			return tx.Model(&modelRoutingModel{}).Where("id = ?", existing.ID).Updates(updates).Error
		}
		if !isGormNotFound(err) {
			return err
		}
		if !do.EnabledHasValue {
			po.Enabled = true
		}
		po.Revision = 1
		if err := tx.Create(po).Error; err != nil {
			if isDuplicateEntry(err) {
				// Race: another tx inserted the same unique key; reload and update.
				var retry modelRoutingModel
				if relErr := tx.Where("group_name = ? AND model = ? AND platform = ? AND subscription_account_id = ?",
					po.GroupName, po.Model, po.Platform, po.SubscriptionAccountID).First(&retry).Error; relErr == nil {
					return authorization.ErrWriteConflict
				}
				// Reload also failed: surface the original create error.
			}
			return err
		}
		do.ID = po.ID
		do.Revision = 1
		return nil
	})
}

func (r *Repository) upsertModelRoutingMemory(do *biz.ModelRouting) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	for _, row := range r.modelRoutings {
		if row.GroupName == do.GroupName && row.Model == do.Model &&
			row.Platform == do.Platform && row.SubscriptionAccountID == do.SubscriptionAccountID {
			if do.EnabledHasValue {
				row.Enabled = do.Enabled
			}
			row.Priority = do.Priority
			row.UpdatedAt = do.UpdatedAt
			row.Revision++
			do.Revision = row.Revision
			return nil
		}
	}
	if do.ID == 0 {
		r.modelRoutingNextID++
		do.ID = r.modelRoutingNextID
	}
	if !do.EnabledHasValue {
		do.Enabled = true
	}
	if do.Revision <= 0 {
		do.Revision = 1
	}
	clone := *do
	r.modelRoutings[do.ID] = &clone
	return nil
}

func (r *Repository) DeleteModelRouting(ctx context.Context, id int64) error {
	if _, scoped := authorization.QueryScopeFromContext(ctx, "channel.model_routing.delete"); scoped {
		if r.db == nil {
			return authorization.ErrDenied
		}
		return authzquery.RunInTx(ctx, r.db, 3, func(current context.Context, tx *gorm.DB) error {
			owner := &Repository{db: tx, encKey: r.encKey, routingGroupRelations: r.routingGroupRelations, routingGroupDualWrite: r.routingGroupDualWrite}
			var existing modelRoutingModel
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&existing, id).Error; err != nil {
				if isGormNotFound(err) {
					return biz.ErrModelRoutingNotFound
				}
				return err
			}
			ids, err := owner.RoutingGroupAuthorizationIDs(current, existing.GroupName)
			if err != nil {
				return err
			}
			if err := authorization.Require(current, "channel.model_routing.delete", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: id, RoutingGroupIDs: ids}); err != nil {
				return err
			}
			expected, present := authorization.ExpectedResourceRevision(current)
			if !present || authorization.WriteReason(current) == "" {
				return authorization.ErrWritePrecondition
			}
			if expected == 0 || int64(expected) != existing.Revision {
				return authorization.ErrWriteConflict
			}
			if err := tx.Delete(&existing).Error; err != nil {
				return err
			}
			if err := owner.syncRoutingMappingTx(tx, "model_routings", map[string]any{"id": id}, existing.GroupName); err != nil {
				return err
			}
			return authzquery.AppendWriteAudit(current, tx, "channel.model_routing.delete", id)
		})
	}
	if id <= 0 {
		return biz.ErrModelRoutingNotFound
	}
	if r.db != nil {
		return r.deleteModelRoutingDB(ctx, id)
	}
	return r.deleteModelRoutingMemory(id)
}

func (r *Repository) deleteModelRoutingDB(ctx context.Context, id int64) error {
	tx := r.db.WithContext(ctx).Where("id = ?", id).Delete(&modelRoutingModel{})
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return biz.ErrModelRoutingNotFound
	}
	return nil
}

func (r *Repository) deleteModelRoutingMemory(id int64) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	if _, ok := r.modelRoutings[id]; !ok {
		return biz.ErrModelRoutingNotFound
	}
	delete(r.modelRoutings, id)
	return nil
}

func (r *Repository) modelRoutingQuery(ctx context.Context, query *gorm.DB) (*gorm.DB, error) {
	var key strings.Builder
	r.db.Dialector.QuoteTo(&key, "key")
	groups := "SELECT 1 FROM routing_groups rg WHERE rg.id IN ? AND rg." + key.String() + " = model_routings.group_name"
	return authzquery.ApplyContext(ctx, query, authzquery.Columns{Resource: "model_routings.id", Groups: groups}, "channel.model_routing.read")
}
