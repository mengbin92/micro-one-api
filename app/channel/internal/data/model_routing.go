package data

import (
	"context"
	"sort"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/pkg/wildcard"

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
	}
}

// ── Repository methods ──────────────────────────────────────────────────────

func (r *Repository) ListModelRoutings(ctx context.Context, group, model, platform string) ([]*biz.ModelRouting, error) {
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
	if group != "" {
		query = query.Where("group_name = ?", group)
	}
	if model != "" {
		query = query.Where("model = ?", model)
	}
	if platform != "" {
		query = query.Where("platform = ?", platform)
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
		if platform != "" && row.Platform != platform {
			continue
		}
		clone := *row
		result = append(result, &clone)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (r *Repository) UpsertModelRouting(ctx context.Context, do *biz.ModelRouting) error {
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
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing modelRoutingModel
		err := tx.Where("group_name = ? AND model = ? AND platform = ? AND subscription_account_id = ?",
			po.GroupName, po.Model, po.Platform, po.SubscriptionAccountID).First(&existing).Error
		if err == nil {
			do.ID = existing.ID
			return tx.Model(&modelRoutingModel{}).Where("id = ?", existing.ID).Updates(map[string]any{
				"enabled":    po.Enabled,
				"priority":   po.Priority,
				"updated_at": po.UpdatedAt,
			}).Error
		}
		if !isGormNotFound(err) {
			return err
		}
		if err := tx.Create(po).Error; err != nil {
			if isDuplicateEntry(err) {
				// Race: another tx inserted the same unique key; reload and update.
				var retry modelRoutingModel
				if relErr := tx.Where("group_name = ? AND model = ? AND platform = ? AND subscription_account_id = ?",
					po.GroupName, po.Model, po.Platform, po.SubscriptionAccountID).First(&retry).Error; relErr == nil {
					do.ID = retry.ID
					return tx.Model(&modelRoutingModel{}).Where("id = ?", retry.ID).Updates(map[string]any{
						"enabled":    po.Enabled,
						"priority":   po.Priority,
						"updated_at": po.UpdatedAt,
					}).Error
				}
				// Reload also failed: surface the original create error.
			}
			return err
		}
		do.ID = po.ID
		return nil
	})
}

func (r *Repository) upsertModelRoutingMemory(do *biz.ModelRouting) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	for _, row := range r.modelRoutings {
		if row.GroupName == do.GroupName && row.Model == do.Model &&
			row.Platform == do.Platform && row.SubscriptionAccountID == do.SubscriptionAccountID {
			row.Enabled = do.Enabled
			row.Priority = do.Priority
			row.UpdatedAt = do.UpdatedAt
			return nil
		}
	}
	if do.ID == 0 {
		r.modelRoutingNextID++
		do.ID = r.modelRoutingNextID
	}
	clone := *do
	r.modelRoutings[do.ID] = &clone
	return nil
}

func (r *Repository) DeleteModelRouting(ctx context.Context, id int64) error {
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
