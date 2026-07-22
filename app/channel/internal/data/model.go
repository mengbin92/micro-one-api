package data

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"micro-one-api/app/channel/internal/biz"

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
	ID        int64  `gorm:"column:id;primaryKey;autoIncrement"`
	ChannelID int64  `gorm:"column:channel_id"`
	ModelPK   int64  `gorm:"column:model_id"`
	Enabled   bool   `gorm:"column:enabled"`
	Priority  int32  `gorm:"column:priority"`
	Config    string `gorm:"column:config"`
	CreatedAt int64  `gorm:"column:created_at"`
	UpdatedAt int64  `gorm:"column:updated_at"`
}

func (modelChannelMappingModel) TableName() string { return "model_channel_mapping" }

type modelSubscriptionMappingModel struct {
	ID                    int64  `gorm:"column:id;primaryKey;autoIncrement"`
	SubscriptionAccountID int64  `gorm:"column:subscription_account_id"`
	ModelPK               int64  `gorm:"column:model_id"`
	GroupName             string `gorm:"column:group_name"`
	Enabled               bool   `gorm:"column:enabled"`
	Priority              int32  `gorm:"column:priority"`
	CreatedAt             int64  `gorm:"column:created_at"`
	UpdatedAt             int64  `gorm:"column:updated_at"`
}

func (modelSubscriptionMappingModel) TableName() string { return "model_subscription_mapping" }

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
		ID:        do.ID,
		ChannelID: do.ChannelID,
		ModelPK:   do.ModelPK,
		Enabled:   do.Enabled,
		Priority:  do.Priority,
		Config:    do.Config,
		CreatedAt: do.CreatedAt,
		UpdatedAt: do.UpdatedAt,
	}
}

func toChannelMappingDO(po *modelChannelMappingModel) *biz.ModelChannelMapping {
	return &biz.ModelChannelMapping{
		ID:        po.ID,
		ChannelID: po.ChannelID,
		ModelPK:   po.ModelPK,
		Enabled:   po.Enabled,
		Priority:  po.Priority,
		Config:    po.Config,
		CreatedAt: po.CreatedAt,
		UpdatedAt: po.UpdatedAt,
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
		CreatedAt:             po.CreatedAt,
		UpdatedAt:             po.UpdatedAt,
	}
}

// ── JSON helpers for capabilities/tags arrays ──────────────────────────────

func jsonStringArray(in []string) string {
	if len(in) == 0 {
		return "[]"
	}
	b, err := json.Marshal(in)
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
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
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
		query = query.Where("model_id LIKE ? OR display_name LIKE ?", like, like)
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

	// Channel counts: one query with GROUP BY model_id.
	type countRow struct {
		ModelID int64
		Count   int64
	}
	var chRows []countRow
	_ = r.db.WithContext(ctx).Model(&modelChannelMappingModel{}).
		Select("model_id as model_id, count(*) as count").
		Where("model_id IN ? AND enabled = ?", ids, true).
		Group("model_id").
		Scan(&chRows).Error
	chMap := make(map[int64]int32, len(chRows))
	for _, row := range chRows {
		chMap[row.ModelID] = int32(row.Count)
	}

	// Subscription counts: one query with GROUP BY model_id.
	var subRows []countRow
	_ = r.db.WithContext(ctx).Model(&modelSubscriptionMappingModel{}).
		Select("model_id as model_id, count(*) as count").
		Where("model_id IN ? AND enabled = ?", ids, true).
		Group("model_id").
		Scan(&subRows).Error
	subMap := make(map[int64]int32, len(subRows))
	for _, row := range subRows {
		subMap[row.ModelID] = int32(row.Count)
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
	if err := r.db.WithContext(ctx).Where("model_id = ?", modelID).First(&po).Error; err != nil {
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
	return int32(res.RowsAffected), nil
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
		affected = int32(res.RowsAffected)
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
			return tx.Model(&modelChannelMappingModel{}).Where("id = ?", existing.ID).Updates(map[string]interface{}{
				"enabled":    po.Enabled,
				"priority":   po.Priority,
				"config":     po.Config,
				"updated_at": po.UpdatedAt,
			}).Error
		}
		if !isGormNotFound(err) {
			return err
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
			return tx.Model(&modelSubscriptionMappingModel{}).Where("id = ?", existing.ID).Updates(map[string]interface{}{
				"enabled":    po.Enabled,
				"priority":   po.Priority,
				"updated_at": po.UpdatedAt,
			}).Error
		}
		if !isGormNotFound(err) {
			return err
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
		if m.ModelID == modelID {
			return cloneModel(m), nil
		}
	}
	return nil, biz.ErrModelNotFound
}

func (r *Repository) createModelMemory(do *biz.Model) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	for _, m := range r.models {
		if m.ModelID == do.ModelID {
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
		if a.Alias == do.Alias {
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
			m.Enabled = do.Enabled
			m.Priority = do.Priority
			m.Config = do.Config
			m.UpdatedAt = do.UpdatedAt
			return nil
		}
	}
	r.modelMappingNextID++
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
			m.Enabled = do.Enabled
			m.Priority = do.Priority
			m.UpdatedAt = do.UpdatedAt
			return nil
		}
	}
	r.modelSubMappingNextID++
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
