package data

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"micro-one-api/pkg/jsonx"

	"micro-one-api/app/channel/internal/biz"

	"micro-one-api/pkg/safecast"

	"gorm.io/gorm"
)

// ── Persistent Objects (PO) ────────────────────────────────────────────────
// PO types stay inside data. Driver-specific GORM tags never leave this file.

type modelModel struct {
	ID            int64   `gorm:"column:id;primaryKey;autoIncrement"`
	ModelID       string  `gorm:"column:model_id"`
	DisplayName   string  `gorm:"column:display_name"`
	Description   *string `gorm:"column:description"`
	Provider      string  `gorm:"column:provider"`
	ModelType     string  `gorm:"column:model_type"`
	ContextWindow int32   `gorm:"column:context_window"`
	PricingInput  float64 `gorm:"column:pricing_input"`
	PricingOutput float64 `gorm:"column:pricing_output"`
	Status        int32   `gorm:"column:status"`
	IsPublic      bool    `gorm:"column:is_public"`
	Capabilities  string  `gorm:"column:capabilities"` // JSON array
	Tags          string  `gorm:"column:tags"`         // JSON array
	Category      string  `gorm:"column:category"`
	Tier          string  `gorm:"column:tier"`
	Metadata      *string `gorm:"column:metadata"` // JSON object
	CreatedAt     int64   `gorm:"column:created_at"`
	UpdatedAt     int64   `gorm:"column:updated_at"`
}

func (modelModel) TableName() string { return "models" }

type modelAliasModel struct {
	ID        int64 `gorm:"column:id;primaryKey;autoIncrement"`
	ModelPK   int64 `gorm:"column:model_id"`
	Alias     string
	IsPrimary bool  `gorm:"column:is_primary"`
	CreatedAt int64 `gorm:"column:created_at"`
}

func (modelAliasModel) TableName() string { return "model_aliases" }

type modelChannelMappingModel struct {
	ID              int64  `gorm:"column:id;primaryKey;autoIncrement"`
	ChannelID       int64  `gorm:"column:channel_id"`
	ModelPK         int64  `gorm:"column:model_id"`
	Enabled         bool   `gorm:"column:enabled"`
	Priority        int32  `gorm:"column:priority"`
	Config          string `gorm:"column:config"`
	UpstreamModelID string `gorm:"column:upstream_model_id"`
	CreatedAt       int64  `gorm:"column:created_at"`
	UpdatedAt       int64  `gorm:"column:updated_at"`
}

func (modelChannelMappingModel) TableName() string { return "model_channel_mapping" }

type modelSubscriptionMappingModel struct {
	ID                    int64  `gorm:"column:id;primaryKey;autoIncrement"`
	SubscriptionAccountID int64  `gorm:"column:subscription_account_id"`
	ModelPK               int64  `gorm:"column:model_id"`
	GroupName             string `gorm:"column:group_name"`
	Enabled               bool   `gorm:"column:enabled"`
	Priority              int32  `gorm:"column:priority"`
	UpstreamModelID       string `gorm:"column:upstream_model_id"`
	CreatedAt             int64  `gorm:"column:created_at"`
	UpdatedAt             int64  `gorm:"column:updated_at"`
}

func (modelSubscriptionMappingModel) TableName() string { return "model_subscription_mapping" }

type modelUsageStatModel struct {
	ID           int64  `gorm:"column:id;primaryKey;autoIncrement"`
	ModelPK      int64  `gorm:"column:model_id"`
	Date         string `gorm:"column:date"`
	RequestCount int32  `gorm:"column:request_count"`
	TokenCount   int64  `gorm:"column:token_count"`
	ErrorCount   int32  `gorm:"column:error_count"`
	AvgLatency   int32  `gorm:"column:avg_latency"`
}

func (modelUsageStatModel) TableName() string { return "model_usage_stats" }

// ── DO ↔ PO conversion helpers (free functions, data-only) ─────────────────

func newModelPO(do *biz.Model) *modelModel {
	po := &modelModel{
		ID:            do.ID,
		ModelID:       do.ModelID,
		DisplayName:   do.DisplayName,
		Provider:      do.Provider,
		ModelType:     do.ModelType,
		ContextWindow: do.ContextWindow,
		PricingInput:  do.PricingInput,
		PricingOutput: do.PricingOutput,
		Status:        do.Status,
		IsPublic:      do.IsPublic,
		Capabilities:  jsonStringArray(do.Capabilities),
		Tags:          jsonStringArray(do.Tags),
		Category:      do.Category,
		Tier:          do.Tier,
		CreatedAt:     do.CreatedAt,
		UpdatedAt:     do.UpdatedAt,
	}
	if do.Description != "" {
		d := do.Description
		po.Description = &d
	}
	if do.Metadata != "" {
		m := do.Metadata
		po.Metadata = &m
	}
	return po
}

func toModelDO(po *modelModel) *biz.Model {
	return &biz.Model{
		ID:            po.ID,
		ModelID:       po.ModelID,
		DisplayName:   po.DisplayName,
		Description:   derefString(po.Description),
		Provider:      po.Provider,
		ModelType:     po.ModelType,
		ContextWindow: po.ContextWindow,
		PricingInput:  po.PricingInput,
		PricingOutput: po.PricingOutput,
		Status:        po.Status,
		IsPublic:      po.IsPublic,
		Capabilities:  parseStringArray(po.Capabilities),
		Tags:          parseStringArray(po.Tags),
		Category:      po.Category,
		Tier:          po.Tier,
		Metadata:      derefString(po.Metadata),
		CreatedAt:     po.CreatedAt,
		UpdatedAt:     po.UpdatedAt,
	}
}

func newModelAliasPO(do *biz.ModelAlias) *modelAliasModel {
	return &modelAliasModel{
		ID:        do.ID,
		ModelPK:   do.ModelPK,
		Alias:     do.Alias,
		IsPrimary: do.IsPrimary,
		CreatedAt: do.CreatedAt,
	}
}

func toModelAliasDO(po *modelAliasModel) *biz.ModelAlias {
	return &biz.ModelAlias{
		ID:        po.ID,
		ModelPK:   po.ModelPK,
		Alias:     po.Alias,
		IsPrimary: po.IsPrimary,
		CreatedAt: po.CreatedAt,
	}
}

func newChannelMappingPO(do *biz.ModelChannelMapping) *modelChannelMappingModel {
	return &modelChannelMappingModel{
		ID:              do.ID,
		ChannelID:       do.ChannelID,
		ModelPK:         do.ModelPK,
		Enabled:         do.Enabled,
		Priority:        do.Priority,
		Config:          do.Config,
		UpstreamModelID: do.UpstreamModelID,
		CreatedAt:       do.CreatedAt,
		UpdatedAt:       do.UpdatedAt,
	}
}

func toChannelMappingDO(po *modelChannelMappingModel) *biz.ModelChannelMapping {
	return &biz.ModelChannelMapping{
		ID:              po.ID,
		ChannelID:       po.ChannelID,
		ModelPK:         po.ModelPK,
		Enabled:         po.Enabled,
		Priority:        po.Priority,
		Config:          po.Config,
		UpstreamModelID: po.UpstreamModelID,
		CreatedAt:       po.CreatedAt,
		UpdatedAt:       po.UpdatedAt,
	}
}

func newSubscriptionMappingPO(do *biz.ModelSubscriptionMapping) *modelSubscriptionMappingModel {
	return &modelSubscriptionMappingModel{
		ID:                    do.ID,
		SubscriptionAccountID: do.SubscriptionAccountID,
		ModelPK:               do.ModelPK,
		GroupName:             do.GroupName,
		Enabled:               do.Enabled,
		Priority:              do.Priority,
		UpstreamModelID:       do.UpstreamModelID,
		CreatedAt:             do.CreatedAt,
		UpdatedAt:             do.UpdatedAt,
	}
}

func toSubscriptionMappingDO(po *modelSubscriptionMappingModel) *biz.ModelSubscriptionMapping {
	return &biz.ModelSubscriptionMapping{
		ID:                    po.ID,
		SubscriptionAccountID: po.SubscriptionAccountID,
		ModelPK:               po.ModelPK,
		GroupName:             po.GroupName,
		Enabled:               po.Enabled,
		Priority:              po.Priority,
		UpstreamModelID:       po.UpstreamModelID,
		CreatedAt:             po.CreatedAt,
		UpdatedAt:             po.UpdatedAt,
	}
}

func newUsageStatPO(do *biz.ModelUsageStat) *modelUsageStatModel {
	return &modelUsageStatModel{
		ID:           do.ID,
		ModelPK:      do.ModelPK,
		Date:         do.Date,
		RequestCount: do.RequestCount,
		TokenCount:   do.TokenCount,
		ErrorCount:   do.ErrorCount,
		AvgLatency:   do.AvgLatency,
	}
}

func toUsageStatDO(po *modelUsageStatModel) *biz.ModelUsageStat {
	return &biz.ModelUsageStat{
		ID:           po.ID,
		ModelPK:      po.ModelPK,
		Date:         po.Date,
		RequestCount: po.RequestCount,
		TokenCount:   po.TokenCount,
		ErrorCount:   po.ErrorCount,
		AvgLatency:   po.AvgLatency,
	}
}

// ── JSON helpers for capabilities/tags arrays ──────────────────────────────

func jsonStringArray(in []string) string {
	if len(in) == 0 {
		return "[]"
	}
	b, err := jsonx.Marshal(in)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func parseStringArray(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []string
	if err := jsonx.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// ── ModelRepo implementation on *Repository ────────────────────────────────

// Compile-time assertion: *Repository implements biz.ModelRepo.
var _ biz.ModelRepo = (*Repository)(nil)

// ListModels returns a page of model summaries (without mappings).
func (r *Repository) ListModels(ctx context.Context, page, pageSize int32, filter biz.ListModelsFilter) ([]*biz.Model, int64, error) {
	if r.db == nil {
		return r.listModelsMemory(page, pageSize, filter)
	}
	return r.listModelsDB(ctx, page, pageSize, filter)
}

func (r *Repository) listModelsDB(ctx context.Context, page, pageSize int32, filter biz.ListModelsFilter) ([]*biz.Model, int64, error) {
	query := r.db.WithContext(ctx).Model(&modelModel{})
	if filter.Keyword != "" {
		like := "%" + escapeLike(filter.Keyword) + "%"
		// v0.11.0 review L6: ESCAPE clause is required for SQLite to honour
		// backslash-escaped wildcards; MySQL/Postgres also accept it.
		query = query.Where("LOWER(model_id) LIKE ? ESCAPE '!' OR LOWER(display_name) LIKE ? ESCAPE '!'", strings.ToLower(like), strings.ToLower(like))
	}
	if filter.Provider != "" {
		query = query.Where("provider = ?", filter.Provider)
	}
	if filter.ModelType != "" {
		query = query.Where("model_type = ?", filter.ModelType)
	}
	if filter.Status != 0 {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.Category != "" {
		query = query.Where("category = ?", filter.Category)
	}
	if filter.Tier != "" {
		query = query.Where("tier = ?", filter.Tier)
	}
	if filter.PublicOnly {
		query = query.Where("is_public = ?", true)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	var pos []modelModel
	if err := query.Order("id DESC").Offset(int(offset)).Limit(int(pageSize)).Find(&pos).Error; err != nil {
		return nil, 0, err
	}
	result := make([]*biz.Model, 0, len(pos))
	for i := range pos {
		result = append(result, toModelDO(&pos[i]))
	}
	// Batch-fill channel/subscription counts in two queries (not N+1).
	r.batchFillModelCounts(ctx, result)
	return result, total, nil
}

// batchFillModelCounts populates ChannelCount and SubscriptionCount for a
// batch of models using two GROUP BY queries instead of 2*N individual
// COUNT queries. Models with no mappings get count 0 (left join semantics
// are handled by initialising counts to zero before merging).
func (r *Repository) batchFillModelCounts(ctx context.Context, models []*biz.Model) {
	if len(models) == 0 {
		return
	}
	ids := make([]int64, len(models))
	for i, m := range models {
		ids[i] = m.ID
	}

	type countRow struct {
		ModelID int64
		Count   int64
	}

	// Channel counts: join channels so orphaned mappings (parent deleted
	// without cascade) do not inflate the count.
	var chRows []countRow
	_ = r.db.WithContext(ctx).Model(&modelChannelMappingModel{}).
		Select("model_channel_mapping.model_id as model_id, count(*) as count").
		Joins("JOIN channels ON channels.id = model_channel_mapping.channel_id").
		Where("model_channel_mapping.model_id IN ? AND model_channel_mapping.enabled = ?", ids, true).
		Group("model_channel_mapping.model_id").
		Scan(&chRows).Error
	chMap := make(map[int64]int32, len(chRows))
	for _, row := range chRows {
		chMap[row.ModelID] = safecast.Int64ToInt32Saturating(row.Count)
	}

	// Subscription counts: join subscription_accounts so orphaned mappings
	// do not inflate the count.
	var subRows []countRow
	_ = r.db.WithContext(ctx).Model(&modelSubscriptionMappingModel{}).
		Select("model_subscription_mapping.model_id as model_id, count(*) as count").
		Joins("JOIN subscription_accounts ON subscription_accounts.id = model_subscription_mapping.subscription_account_id").
		Where("model_subscription_mapping.model_id IN ? AND model_subscription_mapping.enabled = ?", ids, true).
		Group("model_subscription_mapping.model_id").
		Scan(&subRows).Error
	subMap := make(map[int64]int32, len(subRows))
	for _, row := range subRows {
		subMap[row.ModelID] = safecast.Int64ToInt32Saturating(row.Count)
	}

	for _, m := range models {
		m.ChannelCount = chMap[m.ID]
		m.SubscriptionCount = subMap[m.ID]
	}
}

func (r *Repository) GetModel(ctx context.Context, modelPK int64) (*biz.Model, error) {
	if r.db == nil {
		return r.getModelMemory(modelPK)
	}
	var po modelModel
	if err := r.db.WithContext(ctx).Where("id = ?", modelPK).First(&po).Error; err != nil {
		if isGormNotFound(err) {
			return nil, biz.ErrModelNotFound
		}
		return nil, err
	}
	m := toModelDO(&po)
	r.batchFillModelCounts(ctx, []*biz.Model{m})
	return m, nil
}

func (r *Repository) GetModelByID(ctx context.Context, modelID string) (*biz.Model, error) {
	if r.db == nil {
		return r.getModelByIDMemory(modelID)
	}
	var po modelModel
	// Case-insensitive lookup: "GLM-5.2" and "glm-5.2" refer to the same model.
	if err := r.db.WithContext(ctx).Where("LOWER(model_id) = ?", strings.ToLower(modelID)).First(&po).Error; err != nil {
		if isGormNotFound(err) {
			return nil, biz.ErrModelNotFound
		}
		return nil, err
	}
	return toModelDO(&po), nil
}

func (r *Repository) CreateModel(ctx context.Context, do *biz.Model) error {
	if r.db == nil {
		return r.createModelMemory(do)
	}
	po := newModelPO(do)
	if err := r.db.WithContext(ctx).Create(po).Error; err != nil {
		if isDuplicateEntry(err) {
			return biz.ErrModelIDExists
		}
		return err
	}
	do.ID = po.ID
	return nil
}

func (r *Repository) UpdateModel(ctx context.Context, do *biz.Model) error {
	if r.db == nil {
		return r.updateModelMemory(do)
	}
	po := newModelPO(do)
	updates := map[string]interface{}{
		"display_name":   po.DisplayName,
		"description":    po.Description,
		"provider":       po.Provider,
		"model_type":     po.ModelType,
		"context_window": po.ContextWindow,
		"pricing_input":  po.PricingInput,
		"pricing_output": po.PricingOutput,
		"is_public":      po.IsPublic,
		"capabilities":   po.Capabilities,
		"tags":           po.Tags,
		"category":       po.Category,
		"tier":           po.Tier,
		"metadata":       po.Metadata,
		"updated_at":     po.UpdatedAt,
	}
	res := r.db.WithContext(ctx).Model(&modelModel{}).Where("id = ?", do.ID).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return biz.ErrModelNotFound
	}
	return nil
}

func (r *Repository) DeleteModel(ctx context.Context, modelPK int64) error {
	if r.db == nil {
		return r.deleteModelMemory(modelPK)
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("model_id = ?", modelPK).Delete(&modelAliasModel{}).Error; err != nil {
			return err
		}
		if err := tx.Where("model_id = ?", modelPK).Delete(&modelChannelMappingModel{}).Error; err != nil {
			return err
		}
		if err := tx.Where("model_id = ?", modelPK).Delete(&modelSubscriptionMappingModel{}).Error; err != nil {
			return err
		}
		res := tx.Where("id = ?", modelPK).Delete(&modelModel{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return biz.ErrModelNotFound
		}
		return nil
	})
}

func (r *Repository) ChangeModelStatus(ctx context.Context, modelPK int64, status int32) error {
	if r.db == nil {
		return r.changeModelStatusMemory(modelPK, status)
	}
	res := r.db.WithContext(ctx).Model(&modelModel{}).Where("id = ?", modelPK).
		Updates(map[string]interface{}{"status": status, "updated_at": time.Now().Unix()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return biz.ErrModelNotFound
	}
	return nil
}

func (r *Repository) BatchChangeStatus(ctx context.Context, modelPKs []int64, status int32) (int32, error) {
	if r.db == nil {
		return r.batchChangeStatusMemory(modelPKs, status)
	}
	res := r.db.WithContext(ctx).Model(&modelModel{}).Where("id IN ?", modelPKs).
		Updates(map[string]interface{}{"status": status, "updated_at": time.Now().Unix()})
	if res.Error != nil {
		return 0, res.Error
	}
	return safecast.Int64ToInt32Saturating(res.RowsAffected), nil
}

func (r *Repository) BatchDelete(ctx context.Context, modelPKs []int64) (int32, error) {
	if r.db == nil {
		return r.batchDeleteMemory(modelPKs)
	}
	var affected int32
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("model_id IN ?", modelPKs).Delete(&modelAliasModel{}).Error; err != nil {
			return err
		}
		if err := tx.Where("model_id IN ?", modelPKs).Delete(&modelChannelMappingModel{}).Error; err != nil {
			return err
		}
		if err := tx.Where("model_id IN ?", modelPKs).Delete(&modelSubscriptionMappingModel{}).Error; err != nil {
			return err
		}
		res := tx.Where("id IN ?", modelPKs).Delete(&modelModel{})
		if res.Error != nil {
			return res.Error
		}
		affected = safecast.Int64ToInt32Saturating(res.RowsAffected)
		return nil
	})
	return affected, err
}

// ── Aliases ────────────────────────────────────────────────────────────────

func (r *Repository) ListModelAliases(ctx context.Context, modelPK int64) ([]*biz.ModelAlias, error) {
	if r.db == nil {
		return r.listModelAliasesMemory(modelPK)
	}
	var pos []modelAliasModel
	q := r.db.WithContext(ctx).Model(&modelAliasModel{})
	if modelPK > 0 {
		q = q.Where("model_id = ?", modelPK)
	}
	if err := q.Order("id ASC").Find(&pos).Error; err != nil {
		return nil, err
	}
	result := make([]*biz.ModelAlias, 0, len(pos))
	for i := range pos {
		result = append(result, toModelAliasDO(&pos[i]))
	}
	return result, nil
}

func (r *Repository) CreateModelAlias(ctx context.Context, do *biz.ModelAlias) error {
	if r.db == nil {
		return r.createModelAliasMemory(do)
	}
	po := newModelAliasPO(do)
	if err := r.db.WithContext(ctx).Create(po).Error; err != nil {
		if isDuplicateEntry(err) {
			return biz.ErrAliasExists
		}
		return err
	}
	do.ID = po.ID
	return nil
}

func (r *Repository) DeleteModelAlias(ctx context.Context, aliasID int64) error {
	if r.db == nil {
		return r.deleteModelAliasMemory(aliasID)
	}
	res := r.db.WithContext(ctx).Where("id = ?", aliasID).Delete(&modelAliasModel{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return biz.ErrAliasNotFound
	}
	return nil
}

// ── Channel mappings ───────────────────────────────────────────────────────

func (r *Repository) ListChannelMappings(ctx context.Context, channelID int64) ([]*biz.ModelChannelMapping, error) {
	if r.db == nil {
		return r.listChannelMappingsMemory(channelID)
	}
	var pos []modelChannelMappingModel
	q := r.db.WithContext(ctx).Model(&modelChannelMappingModel{})
	if channelID > 0 {
		q = q.Where("channel_id = ?", channelID)
	}
	if err := q.Order("priority DESC, id ASC").Find(&pos).Error; err != nil {
		return nil, err
	}
	result := make([]*biz.ModelChannelMapping, 0, len(pos))
	for i := range pos {
		result = append(result, toChannelMappingDO(&pos[i]))
	}
	return result, nil
}

// ListChannelMappingsByModel returns all channel mappings for a given model,
// avoiding the need to load all mappings and filter in Go.
func (r *Repository) ListChannelMappingsByModel(ctx context.Context, modelPK int64) ([]*biz.ModelChannelMapping, error) {
	if r.db == nil {
		return r.listChannelMappingsByModelMemory(modelPK)
	}
	var pos []modelChannelMappingModel
	if err := r.db.WithContext(ctx).Where("model_id = ?", modelPK).
		Order("priority DESC, id ASC").Find(&pos).Error; err != nil {
		return nil, err
	}
	result := make([]*biz.ModelChannelMapping, 0, len(pos))
	for i := range pos {
		result = append(result, toChannelMappingDO(&pos[i]))
	}
	return result, nil
}

func (r *Repository) UpsertChannelMapping(ctx context.Context, do *biz.ModelChannelMapping) error {
	if r.db == nil {
		return r.upsertChannelMappingMemory(do)
	}
	po := newChannelMappingPO(do)
	// ON CONFLICT (channel_id, model_id) DO UPDATE.
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing modelChannelMappingModel
		err := tx.Where("channel_id = ? AND model_id = ?", po.ChannelID, po.ModelPK).First(&existing).Error
		if err == nil {
			// enabled is proto3 optional; only write it when the
			// caller set it, so a priority-only update does not disable the row.
			updates := map[string]interface{}{
				"priority":          po.Priority,
				"config":            po.Config,
				"upstream_model_id": po.UpstreamModelID,
				"updated_at":        po.UpdatedAt,
			}
			if do.EnabledHasValue {
				updates["enabled"] = po.Enabled
			}
			return tx.Model(&modelChannelMappingModel{}).Where("id = ?", existing.ID).Updates(updates).Error
		}
		if !isGormNotFound(err) {
			return err
		}
		// Insert: default enabled=true (DB DEFAULT 1) when the caller did not
		// set it, mirroring the migration's DEFAULT 1.
		if !do.EnabledHasValue {
			po.Enabled = true
		}
		return tx.Create(po).Error
	})
}

func (r *Repository) DeleteChannelMapping(ctx context.Context, channelID, modelPK int64) error {
	if r.db == nil {
		return r.deleteChannelMappingMemory(channelID, modelPK)
	}
	res := r.db.WithContext(ctx).Where("channel_id = ? AND model_id = ?", channelID, modelPK).
		Delete(&modelChannelMappingModel{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return biz.ErrMappingNotFound
	}
	return nil
}

// ── Subscription mappings ──────────────────────────────────────────────────

func (r *Repository) ListSubscriptionMappings(ctx context.Context, accountID int64) ([]*biz.ModelSubscriptionMapping, error) {
	if r.db == nil {
		return r.listSubscriptionMappingsMemory(accountID)
	}
	var pos []modelSubscriptionMappingModel
	q := r.db.WithContext(ctx).Model(&modelSubscriptionMappingModel{})
	if accountID > 0 {
		q = q.Where("subscription_account_id = ?", accountID)
	}
	if err := q.Order("priority DESC, id ASC").Find(&pos).Error; err != nil {
		return nil, err
	}
	result := make([]*biz.ModelSubscriptionMapping, 0, len(pos))
	for i := range pos {
		result = append(result, toSubscriptionMappingDO(&pos[i]))
	}
	return result, nil
}

// ListSubscriptionMappingsByModel returns all subscription mappings for a
// given model, avoiding the need to load all mappings and filter in Go.
func (r *Repository) ListSubscriptionMappingsByModel(ctx context.Context, modelPK int64) ([]*biz.ModelSubscriptionMapping, error) {
	if r.db == nil {
		return r.listSubscriptionMappingsByModelMemory(modelPK)
	}
	var pos []modelSubscriptionMappingModel
	if err := r.db.WithContext(ctx).Where("model_id = ?", modelPK).
		Order("priority DESC, id ASC").Find(&pos).Error; err != nil {
		return nil, err
	}
	result := make([]*biz.ModelSubscriptionMapping, 0, len(pos))
	for i := range pos {
		result = append(result, toSubscriptionMappingDO(&pos[i]))
	}
	return result, nil
}

func (r *Repository) UpsertSubscriptionMapping(ctx context.Context, do *biz.ModelSubscriptionMapping) error {
	if r.db == nil {
		return r.upsertSubscriptionMappingMemory(do)
	}
	po := newSubscriptionMappingPO(do)
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing modelSubscriptionMappingModel
		err := tx.Where("subscription_account_id = ? AND model_id = ? AND group_name = ?",
			po.SubscriptionAccountID, po.ModelPK, po.GroupName).First(&existing).Error
		if err == nil {
			updates := map[string]interface{}{
				"priority":          po.Priority,
				"upstream_model_id": po.UpstreamModelID,
				"updated_at":        po.UpdatedAt,
			}
			if do.EnabledHasValue {
				updates["enabled"] = po.Enabled
			}
			return tx.Model(&modelSubscriptionMappingModel{}).Where("id = ?", existing.ID).Updates(updates).Error
		}
		if !isGormNotFound(err) {
			return err
		}
		if !do.EnabledHasValue {
			po.Enabled = true
		}
		return tx.Create(po).Error
	})
}

func (r *Repository) DeleteSubscriptionMapping(ctx context.Context, accountID, modelPK int64, groupName string) error {
	if r.db == nil {
		return r.deleteSubscriptionMappingMemory(accountID, modelPK, groupName)
	}
	q := r.db.WithContext(ctx).Where("subscription_account_id = ? AND model_id = ?", accountID, modelPK)
	if groupName != "" {
		q = q.Where("group_name = ?", groupName)
	}
	res := q.Delete(&modelSubscriptionMappingModel{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return biz.ErrMappingNotFound
	}
	return nil
}

// ── Sprint 4: Usage statistics ─────────────────────────────────────────────

func (r *Repository) RecordModelUsage(ctx context.Context, modelPK int64, stat *biz.ModelUsageStat) error {
	if r.db == nil {
		return r.recordModelUsageMemory(modelPK, stat)
	}
	po := newUsageStatPO(stat)
	po.ModelPK = modelPK
	// Upsert: if a row for (model_id, date) already exists, accumulate.
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing modelUsageStatModel
		err := tx.Where("model_id = ? AND date = ?", modelPK, po.Date).First(&existing).Error
		if err == nil {
			return tx.Model(&modelUsageStatModel{}).Where("id = ?", existing.ID).Updates(map[string]interface{}{
				"request_count": existing.RequestCount + po.RequestCount,
				"token_count":   existing.TokenCount + po.TokenCount,
				"error_count":   existing.ErrorCount + po.ErrorCount,
				"avg_latency":   po.AvgLatency, // overwrite with latest
			}).Error
		}
		if !isGormNotFound(err) {
			return err
		}
		return tx.Create(po).Error
	})
}

func (r *Repository) ListModelUsageStats(ctx context.Context, modelPK int64, startDate, endDate string, page, pageSize int32) ([]*biz.ModelUsageStat, int64, error) {
	if r.db == nil {
		return r.listModelUsageStatsMemory(modelPK, startDate, endDate, page, pageSize)
	}
	query := r.db.WithContext(ctx).Model(&modelUsageStatModel{})
	if modelPK > 0 {
		query = query.Where("model_id = ?", modelPK)
	}
	if startDate != "" {
		query = query.Where("date >= ?", startDate)
	}
	if endDate != "" {
		query = query.Where("date <= ?", endDate)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	var pos []modelUsageStatModel
	if err := query.Order("date DESC, id DESC").Offset(int(offset)).Limit(int(pageSize)).Find(&pos).Error; err != nil {
		return nil, 0, err
	}
	result := make([]*biz.ModelUsageStat, 0, len(pos))
	for i := range pos {
		result = append(result, toUsageStatDO(&pos[i]))
	}
	return result, total, nil
}

// ── GORM error helpers ─────────────────────────────────────────────────────

func isGormNotFound(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "record not found") || strings.Contains(err.Error(), "no rows"))
}

func isDuplicateEntry(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate entry") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "constraint failed: unique")
}

// ── In-memory fallback implementations ─────────────────────────────────────
// Used when the repository has no DB (lite/single-binary deployment), mirroring
// the channels/abilities memory mode. Kept simple: sufficient for tests and
// local development.

func (r *Repository) listModelsMemory(page, pageSize int32, filter biz.ListModelsFilter) ([]*biz.Model, int64, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	var filtered []*biz.Model
	for _, m := range r.models {
		if matchesModelFilter(m, filter) {
			filtered = append(filtered, cloneModel(m))
		}
	}
	total := int64(len(filtered))
	start := int((page - 1) * pageSize)
	if start >= len(filtered) {
		return []*biz.Model{}, total, nil
	}
	end := start + int(pageSize)
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[start:end], total, nil
}

func (r *Repository) getModelMemory(modelPK int64) (*biz.Model, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	m, ok := r.models[modelPK]
	if !ok {
		return nil, biz.ErrModelNotFound
	}
	return cloneModel(m), nil
}

func (r *Repository) getModelByIDMemory(modelID string) (*biz.Model, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	for _, m := range r.models {
		if strings.EqualFold(m.ModelID, modelID) {
			return cloneModel(m), nil
		}
	}
	return nil, biz.ErrModelNotFound
}

func (r *Repository) createModelMemory(do *biz.Model) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	for _, m := range r.models {
		if strings.EqualFold(m.ModelID, do.ModelID) {
			return biz.ErrModelIDExists
		}
	}
	if r.models == nil {
		r.models = make(map[int64]*biz.Model)
	}
	r.modelNextID++
	do.ID = r.modelNextID
	r.models[do.ID] = cloneModel(do)
	return nil
}

func (r *Repository) updateModelMemory(do *biz.Model) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	existing, ok := r.models[do.ID]
	if !ok {
		return biz.ErrModelNotFound
	}
	existing.DisplayName = do.DisplayName
	existing.Description = do.Description
	existing.Provider = do.Provider
	existing.ModelType = do.ModelType
	existing.ContextWindow = do.ContextWindow
	existing.PricingInput = do.PricingInput
	existing.PricingOutput = do.PricingOutput
	existing.IsPublic = do.IsPublic
	existing.Capabilities = append([]string(nil), do.Capabilities...)
	existing.Tags = append([]string(nil), do.Tags...)
	existing.Category = do.Category
	existing.Tier = do.Tier
	existing.Metadata = do.Metadata
	existing.UpdatedAt = do.UpdatedAt
	return nil
}

func (r *Repository) deleteModelMemory(modelPK int64) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	if _, ok := r.models[modelPK]; !ok {
		return biz.ErrModelNotFound
	}
	delete(r.models, modelPK)
	for id, a := range r.modelAliases {
		if a.ModelPK == modelPK {
			delete(r.modelAliases, id)
		}
	}
	for id, m := range r.modelChannelMappings {
		if m.ModelPK == modelPK {
			delete(r.modelChannelMappings, id)
		}
	}
	for id, m := range r.modelSubscriptionMappings {
		if m.ModelPK == modelPK {
			delete(r.modelSubscriptionMappings, id)
		}
	}
	return nil
}

func (r *Repository) changeModelStatusMemory(modelPK int64, status int32) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	m, ok := r.models[modelPK]
	if !ok {
		return biz.ErrModelNotFound
	}
	m.Status = status
	return nil
}

func (r *Repository) batchChangeStatusMemory(modelPKs []int64, status int32) (int32, error) {
	r.lock.Lock()
	defer r.lock.Unlock()
	var affected int32
	for _, pk := range modelPKs {
		if m, ok := r.models[pk]; ok {
			m.Status = status
			affected++
		}
	}
	return affected, nil
}

func (r *Repository) batchDeleteMemory(modelPKs []int64) (int32, error) {
	r.lock.Lock()
	defer r.lock.Unlock()
	var affected int32
	for _, pk := range modelPKs {
		if _, ok := r.models[pk]; ok {
			delete(r.models, pk)
			affected++
		}
	}
	return affected, nil
}

func (r *Repository) listModelAliasesMemory(modelPK int64) ([]*biz.ModelAlias, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	var result []*biz.ModelAlias
	for _, a := range r.modelAliases {
		if modelPK == 0 || a.ModelPK == modelPK {
			result = append(result, cloneModelAlias(a))
		}
	}
	return result, nil
}

func (r *Repository) createModelAliasMemory(do *biz.ModelAlias) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	for _, a := range r.modelAliases {
		if strings.EqualFold(a.Alias, do.Alias) {
			return biz.ErrAliasExists
		}
	}
	if r.modelAliases == nil {
		r.modelAliases = make(map[int64]*biz.ModelAlias)
	}
	r.modelAliasNextID++
	do.ID = r.modelAliasNextID
	r.modelAliases[do.ID] = cloneModelAlias(do)
	return nil
}

func (r *Repository) deleteModelAliasMemory(aliasID int64) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	if _, ok := r.modelAliases[aliasID]; !ok {
		return biz.ErrAliasNotFound
	}
	delete(r.modelAliases, aliasID)
	return nil
}

func (r *Repository) listChannelMappingsMemory(channelID int64) ([]*biz.ModelChannelMapping, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	var result []*biz.ModelChannelMapping
	for _, m := range r.modelChannelMappings {
		if channelID == 0 || m.ChannelID == channelID {
			result = append(result, cloneChannelMapping(m))
		}
	}
	return result, nil
}

func (r *Repository) upsertChannelMappingMemory(do *biz.ModelChannelMapping) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.modelChannelMappings == nil {
		r.modelChannelMappings = make(map[int64]*biz.ModelChannelMapping)
	}
	for _, m := range r.modelChannelMappings {
		if m.ChannelID == do.ChannelID && m.ModelPK == do.ModelPK {
			if do.EnabledHasValue {
				m.Enabled = do.Enabled
			}
			m.Priority = do.Priority
			m.Config = do.Config
			m.UpstreamModelID = do.UpstreamModelID
			m.UpdatedAt = do.UpdatedAt
			return nil
		}
	}
	r.modelMappingNextID++
	// Insert default: enabled=true when caller did not set it.
	if !do.EnabledHasValue {
		do.Enabled = true
	}
	do.ID = r.modelMappingNextID
	r.modelChannelMappings[do.ID] = cloneChannelMapping(do)
	return nil
}

func (r *Repository) deleteChannelMappingMemory(channelID, modelPK int64) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	for id, m := range r.modelChannelMappings {
		if m.ChannelID == channelID && m.ModelPK == modelPK {
			delete(r.modelChannelMappings, id)
			return nil
		}
	}
	return biz.ErrMappingNotFound
}

func (r *Repository) listSubscriptionMappingsMemory(accountID int64) ([]*biz.ModelSubscriptionMapping, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	var result []*biz.ModelSubscriptionMapping
	for _, m := range r.modelSubscriptionMappings {
		if accountID == 0 || m.SubscriptionAccountID == accountID {
			result = append(result, cloneSubscriptionMapping(m))
		}
	}
	return result, nil
}

func (r *Repository) upsertSubscriptionMappingMemory(do *biz.ModelSubscriptionMapping) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.modelSubscriptionMappings == nil {
		r.modelSubscriptionMappings = make(map[int64]*biz.ModelSubscriptionMapping)
	}
	for _, m := range r.modelSubscriptionMappings {
		if m.SubscriptionAccountID == do.SubscriptionAccountID && m.ModelPK == do.ModelPK && m.GroupName == do.GroupName {
			if do.EnabledHasValue {
				m.Enabled = do.Enabled
			}
			m.Priority = do.Priority
			m.UpstreamModelID = do.UpstreamModelID
			m.UpdatedAt = do.UpdatedAt
			return nil
		}
	}
	r.modelSubMappingNextID++
	if !do.EnabledHasValue {
		do.Enabled = true
	}
	do.ID = r.modelSubMappingNextID
	r.modelSubscriptionMappings[do.ID] = cloneSubscriptionMapping(do)
	return nil
}

func (r *Repository) deleteSubscriptionMappingMemory(accountID, modelPK int64, groupName string) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	for id, m := range r.modelSubscriptionMappings {
		if m.SubscriptionAccountID == accountID && m.ModelPK == modelPK && (groupName == "" || m.GroupName == groupName) {
			delete(r.modelSubscriptionMappings, id)
			return nil
		}
	}
	return biz.ErrMappingNotFound
}

// listChannelMappingsByModelMemory returns channel mappings for a model from
// the in-memory store.
func (r *Repository) listChannelMappingsByModelMemory(modelPK int64) ([]*biz.ModelChannelMapping, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	var result []*biz.ModelChannelMapping
	for _, m := range r.modelChannelMappings {
		if m.ModelPK == modelPK {
			result = append(result, cloneChannelMapping(m))
		}
	}
	return result, nil
}

// listSubscriptionMappingsByModelMemory returns subscription mappings for a
// model from the in-memory store.
func (r *Repository) listSubscriptionMappingsByModelMemory(modelPK int64) ([]*biz.ModelSubscriptionMapping, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	var result []*biz.ModelSubscriptionMapping
	for _, m := range r.modelSubscriptionMappings {
		if m.ModelPK == modelPK {
			result = append(result, cloneSubscriptionMapping(m))
		}
	}
	return result, nil
}

// ── Sprint 4: Usage statistics (memory) ─────────────────────────────────────

func (r *Repository) recordModelUsageMemory(modelPK int64, stat *biz.ModelUsageStat) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.modelUsageStats == nil {
		r.modelUsageStats = make(map[int64]*biz.ModelUsageStat)
	}
	for _, s := range r.modelUsageStats {
		if s.ModelPK == modelPK && s.Date == stat.Date {
			s.RequestCount += stat.RequestCount
			s.TokenCount += stat.TokenCount
			s.ErrorCount += stat.ErrorCount
			s.AvgLatency = stat.AvgLatency
			return nil
		}
	}
	r.modelUsageStatNextID++
	stat.ID = r.modelUsageStatNextID
	stat.ModelPK = modelPK
	r.modelUsageStats[stat.ID] = &biz.ModelUsageStat{
		ID:           stat.ID,
		ModelPK:      modelPK,
		Date:         stat.Date,
		RequestCount: stat.RequestCount,
		TokenCount:   stat.TokenCount,
		ErrorCount:   stat.ErrorCount,
		AvgLatency:   stat.AvgLatency,
	}
	return nil
}

func (r *Repository) listModelUsageStatsMemory(modelPK int64, startDate, endDate string, page, pageSize int32) ([]*biz.ModelUsageStat, int64, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	var filtered []*biz.ModelUsageStat
	for _, s := range r.modelUsageStats {
		if modelPK > 0 && s.ModelPK != modelPK {
			continue
		}
		if startDate != "" && s.Date < startDate {
			continue
		}
		if endDate != "" && s.Date > endDate {
			continue
		}
		filtered = append(filtered, &biz.ModelUsageStat{
			ID:           s.ID,
			ModelPK:      s.ModelPK,
			Date:         s.Date,
			RequestCount: s.RequestCount,
			TokenCount:   s.TokenCount,
			ErrorCount:   s.ErrorCount,
			AvgLatency:   s.AvgLatency,
		})
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Date != filtered[j].Date {
			return filtered[i].Date > filtered[j].Date
		}
		return filtered[i].ID > filtered[j].ID
	})
	total := int64(len(filtered))
	start := int((page - 1) * pageSize)
	if start >= len(filtered) {
		return []*biz.ModelUsageStat{}, total, nil
	}
	end := start + int(pageSize)
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[start:end], total, nil
}

// ── Memory helpers ─────────────────────────────────────────────────────────

func matchesModelFilter(m *biz.Model, f biz.ListModelsFilter) bool {
	if f.Provider != "" && m.Provider != f.Provider {
		return false
	}
	if f.ModelType != "" && m.ModelType != f.ModelType {
		return false
	}
	if f.Status != 0 && m.Status != f.Status {
		return false
	}
	if f.Category != "" && m.Category != f.Category {
		return false
	}
	if f.Tier != "" && m.Tier != f.Tier {
		return false
	}
	if f.PublicOnly && !m.IsPublic {
		return false
	}
	if f.Keyword != "" {
		kw := strings.ToLower(f.Keyword)
		if !strings.Contains(strings.ToLower(m.ModelID), kw) &&
			!strings.Contains(strings.ToLower(m.DisplayName), kw) {
			return false
		}
	}
	return true
}

func cloneModel(m *biz.Model) *biz.Model {
	if m == nil {
		return nil
	}
	c := *m
	c.Capabilities = append([]string(nil), m.Capabilities...)
	c.Tags = append([]string(nil), m.Tags...)
	return &c
}

func cloneModelAlias(a *biz.ModelAlias) *biz.ModelAlias {
	if a == nil {
		return nil
	}
	c := *a
	return &c
}

func cloneChannelMapping(m *biz.ModelChannelMapping) *biz.ModelChannelMapping {
	if m == nil {
		return nil
	}
	c := *m
	return &c
}

func cloneSubscriptionMapping(m *biz.ModelSubscriptionMapping) *biz.ModelSubscriptionMapping {
	if m == nil {
		return nil
	}
	c := *m
	return &c
}

// ── v0.11.0 Phase 2 §2.1: canonical model ID preflight & merge ─────────────

// CanonicalModelPreflight scans the models table and reports every set of
// rows whose model_id collides after biz.NormalizeModelID, together with
// the dependent-row counts (aliases, channel/subscription mappings, usage
// stats) the operator needs to plan the merge. Read-only.
func (r *Repository) CanonicalModelPreflight(ctx context.Context) (*biz.PreflightReport, error) {
	if r.db == nil {
		return r.canonicalPreflightMemory(ctx)
	}
	return r.canonicalPreflightDB(ctx)
}

func (r *Repository) canonicalPreflightDB(ctx context.Context) (*biz.PreflightReport, error) {
	// 1. Find every (LOWER(TRIM(model_id)), id) pair, then keep only the
	//    canonical ids that have > 1 row. Sorting by id makes the survivor
	//    selection deterministic.
	type idRow struct {
		ID      int64
		ModelID string
	}
	var rows []idRow
	if err := r.db.WithContext(ctx).
		Model(&modelModel{}).
		Select("id, model_id").
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	byCanonical := make(map[string][]idRow)
	for _, row := range rows {
		c := biz.NormalizeModelID(row.ModelID)
		byCanonical[c] = append(byCanonical[c], row)
	}

	// 2. Collect dependent counts per model PK in bulk queries.
	type depCount struct {
		ModelPK int64
		Count   int64
	}
	aliasCounts := map[int64]int64{}
	chCounts := map[int64]int64{}
	subCounts := map[int64]int64{}
	statCounts := map[int64]int64{}
	statReqTotals := map[int64]int64{}
	statTokTotals := map[int64]int64{}
	{
		var xs []depCount
		_ = r.db.WithContext(ctx).Model(&modelAliasModel{}).
			Select("model_id as model_pk, count(*) as count").
			Group("model_id").Scan(&xs).Error
		for _, x := range xs {
			aliasCounts[x.ModelPK] = x.Count
		}
	}
	{
		var xs []depCount
		_ = r.db.WithContext(ctx).Model(&modelChannelMappingModel{}).
			Select("model_id as model_pk, count(*) as count").
			Group("model_id").Scan(&xs).Error
		for _, x := range xs {
			chCounts[x.ModelPK] = x.Count
		}
	}
	{
		var xs []depCount
		_ = r.db.WithContext(ctx).Model(&modelSubscriptionMappingModel{}).
			Select("model_id as model_pk, count(*) as count").
			Group("model_id").Scan(&xs).Error
		for _, x := range xs {
			subCounts[x.ModelPK] = x.Count
		}
	}
	{
		type statAgg struct {
			ModelPK      int64
			Days         int64
			RequestTotal int64
			TokenTotal   int64
		}
		var xs []statAgg
		_ = r.db.WithContext(ctx).Model(&modelUsageStatModel{}).
			Select("model_id as model_pk, count(*) as days, coalesce(sum(request_count),0) as request_total, coalesce(sum(token_count),0) as token_total").
			Group("model_id").Scan(&xs).Error
		for _, x := range xs {
			statCounts[x.ModelPK] = x.Days
			statReqTotals[x.ModelPK] = x.RequestTotal
			statTokTotals[x.ModelPK] = x.TokenTotal
		}
	}

	report := &biz.PreflightReport{}
	for canonical, members := range byCanonical {
		if len(members) < 2 {
			continue
		}
		g := biz.DuplicateModelGroup{CanonicalID: canonical}
		for _, mrow := range members {
			g.Members = append(g.Members, biz.DuplicateModelRef{
				ModelPK:              mrow.ID,
				ModelID:              mrow.ModelID,
				IsPrimary:            mrow.ModelID == canonical,
				Aliases:              safecast.Int64ToInt32Saturating(aliasCounts[mrow.ID]),
				ChannelMappings:      safecast.Int64ToInt32Saturating(chCounts[mrow.ID]),
				SubscriptionMappings: safecast.Int64ToInt32Saturating(subCounts[mrow.ID]),
				UsageStatDays:        safecast.Int64ToInt32Saturating(statCounts[mrow.ID]),
				UsageRequestTotal:    statReqTotals[mrow.ID],
				UsageTokenTotal:      statTokTotals[mrow.ID],
			})
		}
		// Deterministic member order by PK.
		sort.Slice(g.Members, func(i, j int) bool { return g.Members[i].ModelPK < g.Members[j].ModelPK })
		report.Groups = append(report.Groups, g)
	}
	sort.Slice(report.Groups, func(i, j int) bool { return report.Groups[i].CanonicalID < report.Groups[j].CanonicalID })
	return report, nil
}

// MergeCanonicalModels collapses one duplicate group onto SurvivingPK in a
// single transaction:
//  1. Re-point every dependent row on a loser onto the survivor. On a unique
//     key that already exists on the survivor, this would collide — we detect
//     that BEFORE the UPDATE by counting survivor/loser key overlap and abort
//     with biz.ErrCanonicalConflict (no partial writes).
//  2. Fold usage stats: sum request/token/error counts into the survivor's
//     existing (model_id, date) row, or move the loser's row if no survivor
//     row exists for that date.
//  3. Delete the loser model rows.
//  4. Rewrite the survivor's model_id to the canonical spelling.
//
// The whole thing runs under a single tx so a failure at any step leaves the
// registry unchanged. No INSERT IGNORE / ON CONFLICT DO NOTHING: a real key
// collision is a conflict the operator must resolve explicitly.
func (r *Repository) MergeCanonicalModels(ctx context.Context, group biz.DuplicateModelGroup) (*biz.MergeResult, error) {
	if r.db == nil {
		return r.mergeCanonicalModelsMemory(ctx, group)
	}
	survivor := group.SurvivingPK
	losers := make([]int64, 0, len(group.Members))
	survivorInMembers := false
	for _, m := range group.Members {
		if m.ModelPK == survivor {
			survivorInMembers = true
			continue
		}
		losers = append(losers, m.ModelPK)
	}
	if !survivorInMembers {
		return nil, fmt.Errorf("survivor pk %d is not a member of the group", survivor)
	}
	if len(losers) == 0 {
		return &biz.MergeResult{CanonicalID: group.CanonicalID, SurvivingPK: survivor}, nil
	}

	res := &biz.MergeResult{CanonicalID: group.CanonicalID, SurvivingPK: survivor, MergedModelPKs: append([]int64{}, losers...)}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Pre-check alias collisions: an alias string present on two or more
		// group members cannot be merged without dropping data — abort instead
		// of silently overwriting. (uk_alias is on alias alone.)
		if err := detectAliasKeyCollision(tx, append(losers, survivor)); err != nil {
			return err
		}
		// Pre-check channel-mapping collisions on (channel_id) within the group.
		if err := detectMappingKeyCollision(tx, "model_channel_mapping", "channel_id", losers, survivor); err != nil {
			return err
		}
		// Pre-check subscription-mapping collisions on (subscription_account_id, group_name).
		if err := detectMappingKeyCollision(tx, "model_subscription_mapping", "subscription_account_id", losers, survivor); err != nil {
			return err
		}

		// Re-point aliases onto the survivor. Capture the chained result so we
		// read the correct RowsAffected (the root tx's value is stale).
		aRes := tx.Model(&modelAliasModel{}).Where("model_id IN ?", losers).Update("model_id", survivor)
		if aRes.Error != nil {
			return aRes.Error
		}
		res.AliasesRepointed = safecast.Int64ToInt32Saturating(aRes.RowsAffected)

		cRes := tx.Model(&modelChannelMappingModel{}).Where("model_id IN ?", losers).Update("model_id", survivor)
		if cRes.Error != nil {
			return cRes.Error
		}
		res.ChannelMappingsRepointed = safecast.Int64ToInt32Saturating(cRes.RowsAffected)

		sRes := tx.Model(&modelSubscriptionMappingModel{}).Where("model_id IN ?", losers).Update("model_id", survivor)
		if sRes.Error != nil {
			return sRes.Error
		}
		res.SubscriptionMappingsRepointed = safecast.Int64ToInt32Saturating(sRes.RowsAffected)

		// Fold usage stats row-by-row so we never violate uk_model_date
		// (model_id, date). For each loser, for each date: if the survivor
		// already has a row, accumulate; otherwise move the loser's row.
		for _, loser := range losers {
			var loserStats []modelUsageStatModel
			if err := tx.Where("model_id = ?", loser).Find(&loserStats).Error; err != nil {
				return err
			}
			for _, ls := range loserStats {
				var surv modelUsageStatModel
				err := tx.Where("model_id = ? AND date = ?", survivor, ls.Date).First(&surv).Error
				if err == nil {
					if err := tx.Model(&modelUsageStatModel{}).Where("id = ?", surv.ID).Updates(map[string]interface{}{
						"request_count": surv.RequestCount + ls.RequestCount,
						"token_count":   surv.TokenCount + ls.TokenCount,
						"error_count":   surv.ErrorCount + ls.ErrorCount,
						// avg_latency: keep survivor's; loser's is already
						// overwrite-on-write, a weighted average is not meaningful
						// for a one-shot merge.
					}).Error; err != nil {
						return err
					}
					if err := tx.Where("id = ?", ls.ID).Delete(&modelUsageStatModel{}).Error; err != nil {
						return err
					}
					res.UsageStatsRepointed++
				} else if isGormNotFound(err) {
					if err := tx.Model(&modelUsageStatModel{}).Where("id = ?", ls.ID).
						Update("model_id", survivor).Error; err != nil {
						return err
					}
					res.UsageStatsRepointed++
				} else {
					return err
				}
			}
		}

		// Delete the now-orphaned loser model rows. Their FK ON DELETE CASCADE
		// has nothing left to cascade (we already re-pointed every dependent).
		if err := tx.Where("id IN ?", losers).Delete(&modelModel{}).Error; err != nil {
			return err
		}

		// Finally, normalise the survivor's stored model_id to the canonical
		// spelling. This is what lets the post-merge unique constraint apply.
		if err := tx.Model(&modelModel{}).Where("id = ?", survivor).
			Update("model_id", group.CanonicalID).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// detectAliasKeyCollision returns biz.ErrCanonicalConflict if any alias string
// is shared by two or more of the given model PKs. The alias unique key is on
// the alias column alone, so a shared alias cannot be merged without dropping
// a row.
func detectAliasKeyCollision(tx *gorm.DB, pks []int64) error {
	type dup struct {
		Alias string
		N     int64
	}
	var dups []dup
	if err := tx.Model(&modelAliasModel{}).
		Select("alias, count(*) as n").
		Where("model_id IN ?", pks).
		Group("alias").
		Having("count(*) > 1").
		Scan(&dups).Error; err != nil {
		return err
	}
	if len(dups) == 0 {
		return nil
	}
	names := make([]string, 0, len(dups))
	for _, d := range dups {
		names = append(names, d.Alias)
	}
	return fmt.Errorf("%w: alias(es) %s shared by multiple group members", biz.ErrCanonicalConflict, strings.Join(names, ", "))
}

// detectMappingKeyCollision returns biz.ErrCanonicalConflict if re-pointing
// losers onto the survivor would collide on the mapping table's natural key.
// For channel mappings the key is channel_id; for subscription mappings it is
// (subscription_account_id, group_name). A collision means the same partner
// already serves this canonical model through two members — a real conflict.
// Uses Rows()+Scan instead of an ad-hoc struct so the partner/group columns
// are read by explicit name regardless of driver quoting or collation.
func detectMappingKeyCollision(tx *gorm.DB, table, partnerCol string, losers []int64, survivor int64) error {
	hasGroup := table == "model_subscription_mapping"
	collect := func(modelID int64) (map[string]struct{}, error) {
		q := tx.Table(table).
			Select(partnerCol).
			Where("model_id = ?", modelID)
		if hasGroup {
			q = tx.Table(table).
				Select(partnerCol+", group_name").
				Where("model_id = ?", modelID)
		}
		rows, err := q.Rows()
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := make(map[string]struct{})
		for rows.Next() {
			if hasGroup {
				var partner int64
				var group string
				if err := rows.Scan(&partner, &group); err != nil {
					return nil, err
				}
				out[mappingKey(partner, group)] = struct{}{}
			} else {
				var partner int64
				if err := rows.Scan(&partner); err != nil {
					return nil, err
				}
				out[mappingKey(partner, "")] = struct{}{}
			}
		}
		return out, rows.Err()
	}

	survSet, err := collect(survivor)
	if err != nil {
		return err
	}
	loserSet := make(map[string]struct{})
	for _, loser := range losers {
		keys, err := collect(loser)
		if err != nil {
			return err
		}
		for kk := range keys {
			if _, dup := loserSet[kk]; dup {
				return fmt.Errorf("%w: partner key %s appears on multiple members of table %s", biz.ErrCanonicalConflict, kk, table)
			}
			loserSet[kk] = struct{}{}
			if _, hit := survSet[kk]; hit {
				return fmt.Errorf("%w: partner key %s already served by survivor in table %s", biz.ErrCanonicalConflict, kk, table)
			}
		}
	}
	return nil
}

func mappingKey(partner int64, group string) string {
	return fmt.Sprintf("%d|%s", partner, group)
}

// ── canonical preflight & merge — memory fallback ──────────────────────────
// Used when the repository has no DB (lite/single-binary deployment). Mirrors
// the DB path so the same behaviour is exercised by SQLite and unit tests.

func (r *Repository) canonicalPreflightMemory(ctx context.Context) (*biz.PreflightReport, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	byCanonical := make(map[string][]*biz.Model)
	for _, m := range r.models {
		c := biz.NormalizeModelID(m.ModelID)
		byCanonical[c] = append(byCanonical[c], m)
	}
	report := &biz.PreflightReport{}
	for canonical, members := range byCanonical {
		if len(members) < 2 {
			continue
		}
		g := biz.DuplicateModelGroup{CanonicalID: canonical}
		for _, mrow := range members {
			ref := biz.DuplicateModelRef{
				ModelPK:   mrow.ID,
				ModelID:   mrow.ModelID,
				IsPrimary: mrow.ModelID == canonical,
			}
			for _, a := range r.modelAliases {
				if a.ModelPK == mrow.ID {
					ref.Aliases++
				}
			}
			for _, c := range r.modelChannelMappings {
				if c.ModelPK == mrow.ID {
					ref.ChannelMappings++
				}
			}
			for _, s := range r.modelSubscriptionMappings {
				if s.ModelPK == mrow.ID {
					ref.SubscriptionMappings++
				}
			}
			for _, u := range r.modelUsageStats {
				if u.ModelPK == mrow.ID {
					ref.UsageStatDays++
					ref.UsageRequestTotal += int64(u.RequestCount)
					ref.UsageTokenTotal += u.TokenCount
				}
			}
			g.Members = append(g.Members, ref)
		}
		sort.Slice(g.Members, func(i, j int) bool { return g.Members[i].ModelPK < g.Members[j].ModelPK })
		report.Groups = append(report.Groups, g)
	}
	sort.Slice(report.Groups, func(i, j int) bool { return report.Groups[i].CanonicalID < report.Groups[j].CanonicalID })
	return report, nil
}

func (r *Repository) mergeCanonicalModelsMemory(ctx context.Context, group biz.DuplicateModelGroup) (*biz.MergeResult, error) {
	r.lock.Lock()
	defer r.lock.Unlock()
	survivor := group.SurvivingPK
	if _, ok := r.models[survivor]; !ok {
		return nil, biz.ErrModelNotFound
	}
	var losers []int64
	for _, m := range group.Members {
		if m.ModelPK != survivor {
			losers = append(losers, m.ModelPK)
		}
	}
	if len(losers) == 0 {
		return &biz.MergeResult{CanonicalID: group.CanonicalID, SurvivingPK: survivor}, nil
	}
	loserSet := make(map[int64]struct{}, len(losers))
	for _, l := range losers {
		loserSet[l] = struct{}{}
	}
	allPKs := append([]int64{survivor}, losers...)
	aliasOwnerSet := make(map[int64]struct{}, len(allPKs))
	for _, pk := range allPKs {
		aliasOwnerSet[pk] = struct{}{}
	}

	// Pre-check alias collisions.
	aliasOwners := make(map[string]int64)
	for _, a := range r.modelAliases {
		if _, ok := aliasOwnerSet[a.ModelPK]; !ok {
			continue
		}
		if prev, dup := aliasOwners[a.Alias]; dup {
			return nil, fmt.Errorf("%w: alias %q shared by models %d and %d", biz.ErrCanonicalConflict, a.Alias, prev, a.ModelPK)
		}
		aliasOwners[a.Alias] = a.ModelPK
	}
	// Pre-check channel-mapping collisions on (channel_id).
	chOwners := make(map[int64]int64)
	for _, c := range r.modelChannelMappings {
		if _, ok := aliasOwnerSet[c.ModelPK]; !ok {
			continue
		}
		if prev, dup := chOwners[c.ChannelID]; dup {
			return nil, fmt.Errorf("%w: channel %d served by models %d and %d", biz.ErrCanonicalConflict, c.ChannelID, prev, c.ModelPK)
		}
		chOwners[c.ChannelID] = c.ModelPK
	}
	// Pre-check subscription-mapping collisions on (account, group).
	subOwners := make(map[string]int64)
	for _, s := range r.modelSubscriptionMappings {
		if _, ok := aliasOwnerSet[s.ModelPK]; !ok {
			continue
		}
		kk := mappingKey(s.SubscriptionAccountID, s.GroupName)
		if prev, dup := subOwners[kk]; dup {
			return nil, fmt.Errorf("%w: subscription %s served by models %d and %d", biz.ErrCanonicalConflict, kk, prev, s.ModelPK)
		}
		subOwners[kk] = s.ModelPK
	}

	res := &biz.MergeResult{CanonicalID: group.CanonicalID, SurvivingPK: survivor, MergedModelPKs: append([]int64{}, losers...)}

	// Re-point aliases.
	for _, a := range r.modelAliases {
		if _, ok := loserSet[a.ModelPK]; ok {
			a.ModelPK = survivor
			res.AliasesRepointed++
		}
	}
	// Re-point channel mappings.
	for _, c := range r.modelChannelMappings {
		if _, ok := loserSet[c.ModelPK]; ok {
			c.ModelPK = survivor
			res.ChannelMappingsRepointed++
		}
	}
	// Re-point subscription mappings.
	for _, s := range r.modelSubscriptionMappings {
		if _, ok := loserSet[s.ModelPK]; ok {
			s.ModelPK = survivor
			res.SubscriptionMappingsRepointed++
		}
	}
	// Fold usage stats by date.
	survStatsByDate := make(map[string]*biz.ModelUsageStat)
	for _, u := range r.modelUsageStats {
		if u.ModelPK == survivor {
			survStatsByDate[u.Date] = u
		}
	}
	for _, u := range r.modelUsageStats {
		if _, ok := loserSet[u.ModelPK]; !ok {
			continue
		}
		if surv, ok := survStatsByDate[u.Date]; ok {
			surv.RequestCount += u.RequestCount
			surv.TokenCount += u.TokenCount
			surv.ErrorCount += u.ErrorCount
			delete(r.modelUsageStats, u.ID)
		} else {
			u.ModelPK = survivor
			survStatsByDate[u.Date] = u
		}
		res.UsageStatsRepointed++
	}
	// Delete loser model rows.
	for _, l := range losers {
		delete(r.models, l)
	}
	// Normalise survivor model_id.
	if surv, ok := r.models[survivor]; ok {
		surv.ModelID = group.CanonicalID
	}
	return res, nil
}
