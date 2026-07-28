package data

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"micro-one-api/app/channel/internal/biz"

	"gorm.io/gorm"
)

// ── Model import/export data layer (v0.11.0 Phase 4) ──────────────────────
//
// ExportAllModels loads every model (paged internally to bound memory) with
// its aliases and mappings in three batched queries (not N+1). ImportModels
// applies the whole document inside one transaction: a create or an update of
// each model plus its aliases/mappings. On the first invalid record or
// conflict under the reject strategy the transaction is rolled back — no
// partial writes. Dry-run performs validation + diff without any write.

// ExportAllModels implements biz.ModelExchangeRepo.
func (r *Repository) ExportAllModels(ctx context.Context, filter biz.ListModelsFilter) ([]*biz.ModelExportModel, error) {
	if r.db == nil {
		return r.exportAllModelsMemory(filter)
	}
	// Page through the whole registry. The model count is bounded (hundreds),
	// so we collect all rows then batch-fill dependents in three queries.
	page := int32(1)
	pageSize := int32(500)
	var collected []*biz.Model
	for {
		models, total, err := r.listModelsDB(ctx, page, pageSize, filter)
		if err != nil {
			return nil, err
		}
		collected = append(collected, models...)
		if int64(len(collected)) >= total || len(models) == 0 {
			break
		}
		page++
	}
	if len(collected) == 0 {
		return []*biz.ModelExportModel{}, nil
	}
	// Batch-load all aliases and mappings for the collected model PKs.
	pks := make([]int64, 0, len(collected))
	for _, m := range collected {
		if m != nil {
			pks = append(pks, m.ID)
		}
	}
	aliasByModel, err := r.batchLoadAliasesByModel(ctx, pks)
	if err != nil {
		return nil, err
	}
	channelByModel, err := r.batchLoadChannelMappingsByModel(ctx, pks)
	if err != nil {
		return nil, err
	}
	subByModel, err := r.batchLoadSubscriptionMappingsByModel(ctx, pks)
	if err != nil {
		return nil, err
	}
	out := make([]*biz.ModelExportModel, 0, len(collected))
	for _, m := range collected {
		if m == nil {
			continue
		}
		out = append(out, &biz.ModelExportModel{
			ModelID:              m.ModelID,
			DisplayName:          m.DisplayName,
			Description:          m.Description,
			Provider:             m.Provider,
			ModelType:            m.ModelType,
			ContextWindow:        m.ContextWindow,
			PricingInput:         m.PricingInput,
			PricingOutput:        m.PricingOutput,
			Status:               m.Status,
			IsPublic:             m.IsPublic,
			Capabilities:         append([]string(nil), m.Capabilities...),
			Tags:                 append([]string(nil), m.Tags...),
			Category:             m.Category,
			Tier:                 m.Tier,
			Metadata:             m.Metadata,
			Aliases:              aliasByModel[m.ID],
			ChannelMappings:      channelByModel[m.ID],
			SubscriptionMappings: subByModel[m.ID],
		})
	}
	return out, nil
}

func (r *Repository) batchLoadAliasesByModel(ctx context.Context, pks []int64) (map[int64][]*biz.ModelAlias, error) {
	if len(pks) == 0 {
		return nil, nil
	}
	var pos []modelAliasModel
	if err := r.db.WithContext(ctx).Where("model_id IN ?", pks).Order("model_id ASC, id ASC").Find(&pos).Error; err != nil {
		return nil, err
	}
	out := make(map[int64][]*biz.ModelAlias, len(pks))
	for i := range pos {
		out[pos[i].ModelPK] = append(out[pos[i].ModelPK], toModelAliasDO(&pos[i]))
	}
	return out, nil
}

func (r *Repository) batchLoadChannelMappingsByModel(ctx context.Context, pks []int64) (map[int64][]*biz.ModelChannelMapping, error) {
	if len(pks) == 0 {
		return nil, nil
	}
	var pos []modelChannelMappingModel
	if err := r.db.WithContext(ctx).Where("model_id IN ?", pks).Order("model_id ASC, priority DESC, id ASC").Find(&pos).Error; err != nil {
		return nil, err
	}
	out := make(map[int64][]*biz.ModelChannelMapping, len(pks))
	for i := range pos {
		out[pos[i].ModelPK] = append(out[pos[i].ModelPK], toChannelMappingDO(&pos[i]))
	}
	return out, nil
}

func (r *Repository) batchLoadSubscriptionMappingsByModel(ctx context.Context, pks []int64) (map[int64][]*biz.ModelSubscriptionMapping, error) {
	if len(pks) == 0 {
		return nil, nil
	}
	var pos []modelSubscriptionMappingModel
	if err := r.db.WithContext(ctx).Where("model_id IN ?", pks).Order("model_id ASC, priority DESC, id ASC").Find(&pos).Error; err != nil {
		return nil, err
	}
	out := make(map[int64][]*biz.ModelSubscriptionMapping, len(pks))
	for i := range pos {
		out[pos[i].ModelPK] = append(out[pos[i].ModelPK], toSubscriptionMappingDO(&pos[i]))
	}
	return out, nil
}

// ImportModels implements biz.ModelExchangeRepo. The whole import runs in one
// transaction; a failure at any model rolls back every prior model in the
// batch so the registry is left untouched.
func (r *Repository) ImportModels(ctx context.Context, models []*biz.ModelExportModel, options biz.ImportOptions) (*biz.ImportSummary, error) {
	if r.db == nil {
		return r.importModelsMemory(models, options)
	}
	summary := &biz.ImportSummary{}
	if options.DryRun {
		return r.dryRunImportDB(ctx, models, options, summary)
	}
	// Build an index of existing models by canonical model_id so we can decide
	// create-vs-update and detect conflicts before the transaction.
	existing, err := r.loadExistingModelsByModelID(ctx, models)
	if err != nil {
		return nil, err
	}

	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().Unix()
		for _, em := range models {
			if em == nil {
				continue
			}
			exist := existing[biz.NormalizeModelID(em.ModelID)]
			outcome, err := r.applyImportModel(tx, em, exist, options, now)
			if err != nil {
				return err
			}
			summary.Results = append(summary.Results, outcome)
			switch outcome.Action {
			case "create":
				summary.Created++
			case "update":
				summary.Updated++
			case "skip":
				summary.Skipped++
			case "conflict":
				summary.Conflicts++
			case "error":
				summary.Errors++
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return summary, nil
}

// dryRunImportDB previews the import without writing. It reads the existing
// models, computes create/update/skip/conflict per record, and returns the
// summary. No transaction is opened.
func (r *Repository) dryRunImportDB(ctx context.Context, models []*biz.ModelExportModel, options biz.ImportOptions, summary *biz.ImportSummary) (*biz.ImportSummary, error) {
	existing, err := r.loadExistingModelsByModelID(ctx, models)
	if err != nil {
		return nil, err
	}
	for _, em := range models {
		if em == nil {
			continue
		}
		exist := existing[biz.NormalizeModelID(em.ModelID)]
		outcome := computeImportOutcome(em, exist, options)
		summary.Results = append(summary.Results, outcome)
		switch outcome.Action {
		case "create":
			summary.Created++
		case "update":
			summary.Updated++
		case "skip":
			summary.Skipped++
		case "conflict":
			summary.Conflicts++
		case "error":
			summary.Errors++
		}
	}
	return summary, nil
}

// loadExistingModelsByModelID loads the subset of existing models whose
// canonical id matches one in the import document. Returns a map keyed by
// canonical model id so the caller can decide create-vs-update.
func (r *Repository) loadExistingModelsByModelID(ctx context.Context, models []*biz.ModelExportModel) (map[string]*biz.Model, error) {
	if len(models) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(models))
	for _, m := range models {
		if m != nil {
			ids = append(ids, biz.NormalizeModelID(m.ModelID))
		}
	}
	var pos []modelModel
	if err := r.db.WithContext(ctx).Where("LOWER(model_id) IN ?", ids).Find(&pos).Error; err != nil {
		return nil, err
	}
	out := make(map[string]*biz.Model, len(pos))
	for i := range pos {
		out[biz.NormalizeModelID(pos[i].ModelID)] = toModelDO(&pos[i])
	}
	return out, nil
}

// computeImportOutcome decides the action for one record without writing. It
// is shared by dry-run and the per-record step of the real import so both see
// identical classification.
func computeImportOutcome(em *biz.ModelExportModel, existing *biz.Model, options biz.ImportOptions) biz.ImportRecordOutcome {
	if existing == nil {
		return biz.ImportRecordOutcome{ModelID: em.ModelID, Action: "create"}
	}
	if modelsContentEqual(em, existing, options) {
		return biz.ImportRecordOutcome{ModelID: em.ModelID, Action: "skip", Message: "identical content"}
	}
	if options.ConflictStrategy == biz.ConflictStrategyReject {
		return biz.ImportRecordOutcome{
			ModelID: em.ModelID,
			Action:  "conflict",
			Message: "model exists with different content; conflict_strategy=reject",
		}
	}
	return biz.ImportRecordOutcome{ModelID: em.ModelID, Action: "update"}
}

// modelsContentEqual reports whether the import record would leave the model
// unchanged. Prices are ignored when options.ImportPrices is false.
func modelsContentEqual(em *biz.ModelExportModel, existing *biz.Model, options biz.ImportOptions) bool {
	if em.DisplayName != existing.DisplayName {
		return false
	}
	if em.Description != existing.Description {
		return false
	}
	if em.Provider != existing.Provider {
		return false
	}
	if em.ModelType != existing.ModelType {
		return false
	}
	if em.ContextWindow != existing.ContextWindow {
		return false
	}
	if options.ImportPrices {
		if em.PricingInput != existing.PricingInput || em.PricingOutput != existing.PricingOutput {
			return false
		}
	}
	if em.Status != existing.Status {
		return false
	}
	if em.IsPublic != existing.IsPublic {
		return false
	}
	if em.Category != existing.Category {
		return false
	}
	if em.Tier != existing.Tier {
		return false
	}
	if em.Metadata != existing.Metadata {
		return false
	}
	if !stringSliceEqualSorted(em.Capabilities, existing.Capabilities) {
		return false
	}
	if !stringSliceEqualSorted(em.Tags, existing.Tags) {
		return false
	}
	return true
}

// applyImportModel writes one model (and its aliases/mappings) inside the
// caller's transaction. Returns the per-record outcome. On the reject
// strategy, a conflict aborts the transaction (so prior models roll back).
func (r *Repository) applyImportModel(tx *gorm.DB, em *biz.ModelExportModel, existing *biz.Model, options biz.ImportOptions, now int64) (biz.ImportRecordOutcome, error) {
	outcome := computeImportOutcome(em, existing, options)
	switch outcome.Action {
	case "skip":
		return outcome, nil
	case "conflict":
		// Under reject, a conflict is a hard error: the whole import must
		// roll back. Return a typed error so the summary is not built.
		return outcome, fmt.Errorf("%w: %s", biz.ErrImportConflict, outcome.Message)
	case "create":
		po := importModelToPO(em, options, now)
		if err := tx.Create(po).Error; err != nil {
			return outcome, fmt.Errorf("%w: create %s: %v", biz.ErrImportInvalidRecord, em.ModelID, err)
		}
		modelPK := po.ID
		if err := r.applyImportAliases(tx, modelPK, em.Aliases, now); err != nil {
			return outcome, fmt.Errorf("%w: aliases %s: %v", biz.ErrImportInvalidRecord, em.ModelID, err)
		}
		if err := r.applyImportChannelMappings(tx, modelPK, em.ChannelMappings, now); err != nil {
			return outcome, fmt.Errorf("%w: channel mappings %s: %v", biz.ErrImportInvalidRecord, em.ModelID, err)
		}
		if err := r.applyImportSubscriptionMappings(tx, modelPK, em.SubscriptionMappings, now); err != nil {
			return outcome, fmt.Errorf("%w: subscription mappings %s: %v", biz.ErrImportInvalidRecord, em.ModelID, err)
		}
		return outcome, nil
	case "update":
		po := importModelToPO(em, options, now)
		po.ID = existing.ID
		updates := map[string]interface{}{
			"display_name":   po.DisplayName,
			"description":    po.Description,
			"provider":       po.Provider,
			"model_type":     po.ModelType,
			"context_window": po.ContextWindow,
			"is_public":      po.IsPublic,
			"capabilities":   po.Capabilities,
			"tags":           po.Tags,
			"category":       po.Category,
			"tier":           po.Tier,
			"metadata":       po.Metadata,
			"status":         po.Status,
			"updated_at":     po.UpdatedAt,
		}
		if options.ImportPrices {
			updates["pricing_input"] = po.PricingInput
			updates["pricing_output"] = po.PricingOutput
		}
		if err := tx.Model(&modelModel{}).Where("id = ?", existing.ID).Updates(updates).Error; err != nil {
			return outcome, fmt.Errorf("%w: update %s: %v", biz.ErrImportInvalidRecord, em.ModelID, err)
		}
		// Replace aliases/mappings wholesale: delete then re-insert so the
		// imported set is authoritative. This mirrors how the operator would
		// expect a config-migration import to behave.
		if err := tx.Where("model_id = ?", existing.ID).Delete(&modelAliasModel{}).Error; err != nil {
			return outcome, fmt.Errorf("%w: clear aliases %s: %v", biz.ErrImportInvalidRecord, em.ModelID, err)
		}
		if err := r.applyImportAliases(tx, existing.ID, em.Aliases, now); err != nil {
			return outcome, fmt.Errorf("%w: aliases %s: %v", biz.ErrImportInvalidRecord, em.ModelID, err)
		}
		if err := tx.Where("model_id = ?", existing.ID).Delete(&modelChannelMappingModel{}).Error; err != nil {
			return outcome, fmt.Errorf("%w: clear channel mappings %s: %v", biz.ErrImportInvalidRecord, em.ModelID, err)
		}
		if err := r.applyImportChannelMappings(tx, existing.ID, em.ChannelMappings, now); err != nil {
			return outcome, fmt.Errorf("%w: channel mappings %s: %v", biz.ErrImportInvalidRecord, em.ModelID, err)
		}
		if err := tx.Where("model_id = ?", existing.ID).Delete(&modelSubscriptionMappingModel{}).Error; err != nil {
			return outcome, fmt.Errorf("%w: clear subscription mappings %s: %v", biz.ErrImportInvalidRecord, em.ModelID, err)
		}
		if err := r.applyImportSubscriptionMappings(tx, existing.ID, em.SubscriptionMappings, now); err != nil {
			return outcome, fmt.Errorf("%w: subscription mappings %s: %v", biz.ErrImportInvalidRecord, em.ModelID, err)
		}
		return outcome, nil
	}
	return outcome, fmt.Errorf("%w: unknown action %s", biz.ErrImportInvalidRecord, outcome.Action)
}

func (r *Repository) applyImportAliases(tx *gorm.DB, modelPK int64, aliases []*biz.ModelAlias, now int64) error {
	for _, a := range aliases {
		if a == nil || strings.TrimSpace(a.Alias) == "" {
			continue
		}
		po := &modelAliasModel{
			ModelPK:   modelPK,
			Alias:     biz.NormalizeModelID(a.Alias),
			IsPrimary: a.IsPrimary,
			CreatedAt: now,
		}
		if err := tx.Create(po).Error; err != nil {
			if isDuplicateEntry(err) {
				// An alias that already belongs to another model is a real
				// conflict the operator must resolve.
				return fmt.Errorf("%w: alias %s already exists", biz.ErrImportConflict, po.Alias)
			}
			return err
		}
	}
	return nil
}

func (r *Repository) applyImportChannelMappings(tx *gorm.DB, modelPK int64, mappings []*biz.ModelChannelMapping, now int64) error {
	for _, m := range mappings {
		if m == nil {
			continue
		}
		if m.ChannelID == 0 {
			return fmt.Errorf("%w: channel mapping has no channel_id", biz.ErrImportInvalidRecord)
		}
		po := &modelChannelMappingModel{
			ChannelID:       m.ChannelID,
			ModelPK:         modelPK,
			Enabled:         m.Enabled,
			Priority:        m.Priority,
			Config:          m.Config,
			UpstreamModelID: m.UpstreamModelID,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if err := tx.Create(po).Error; err != nil {
			if isDuplicateEntry(err) {
				return fmt.Errorf("%w: channel mapping for channel %d already exists", biz.ErrImportConflict, po.ChannelID)
			}
			return err
		}
	}
	return nil
}

func (r *Repository) applyImportSubscriptionMappings(tx *gorm.DB, modelPK int64, mappings []*biz.ModelSubscriptionMapping, now int64) error {
	for _, m := range mappings {
		if m == nil {
			continue
		}
		if m.SubscriptionAccountID == 0 {
			return fmt.Errorf("%w: subscription mapping has no subscription_account_id", biz.ErrImportInvalidRecord)
		}
		group := m.GroupName
		if group == "" {
			group = "default"
		}
		po := &modelSubscriptionMappingModel{
			SubscriptionAccountID: m.SubscriptionAccountID,
			ModelPK:               modelPK,
			GroupName:             group,
			Enabled:               m.Enabled,
			Priority:              m.Priority,
			UpstreamModelID:       m.UpstreamModelID,
			CreatedAt:             now,
			UpdatedAt:             now,
		}
		if err := tx.Create(po).Error; err != nil {
			if isDuplicateEntry(err) {
				return fmt.Errorf("%w: subscription mapping for account %d group %s already exists", biz.ErrImportConflict, po.SubscriptionAccountID, po.GroupName)
			}
			return err
		}
	}
	return nil
}

// importModelToPO converts an export model to a PO for insert/update. Pricing
// is only populated when options.ImportPrices is true; otherwise the PO keeps
// zero pricing on create and the update map omits price columns.
func importModelToPO(em *biz.ModelExportModel, options biz.ImportOptions, now int64) *modelModel {
	po := &modelModel{
		ModelID:       biz.NormalizeModelID(em.ModelID),
		DisplayName:   em.DisplayName,
		Provider:      em.Provider,
		ModelType:     em.ModelType,
		ContextWindow: em.ContextWindow,
		Status:        em.Status,
		IsPublic:      em.IsPublic,
		Capabilities:  jsonStringArray(em.Capabilities),
		Tags:          jsonStringArray(em.Tags),
		Category:      em.Category,
		Tier:          em.Tier,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if em.DisplayName == "" {
		po.DisplayName = po.ModelID
	}
	if po.ModelType == "" {
		po.ModelType = "chat"
	}
	if em.Description != "" {
		desc := em.Description
		po.Description = &desc
	}
	if em.Metadata != "" {
		meta := em.Metadata
		po.Metadata = &meta
	}
	if options.ImportPrices {
		po.PricingInput = em.PricingInput
		po.PricingOutput = em.PricingOutput
	}
	return po
}

func stringSliceEqualSorted(a, b []string) bool {
	a = sortedNonEmpty(a)
	b = sortedNonEmpty(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortedNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// ── In-memory fallback for export/import ───────────────────────────────────

func (r *Repository) exportAllModelsMemory(filter biz.ListModelsFilter) ([]*biz.ModelExportModel, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	// Reuse the existing memory list path which already applies the filter.
	models, _, err := r.listModelsMemory(1, int32(biz.MaxImportRecords), filter)
	if err != nil {
		return nil, err
	}
	out := make([]*biz.ModelExportModel, 0, len(models))
	for _, m := range models {
		if m == nil {
			continue
		}
		aliases, _ := r.listModelAliasesMemory(m.ID)
		channelMappings, _ := r.listChannelMappingsByModelMemory(m.ID)
		subMappings, _ := r.listSubscriptionMappingsByModelMemory(m.ID)
		out = append(out, &biz.ModelExportModel{
			ModelID:              m.ModelID,
			DisplayName:          m.DisplayName,
			Description:          m.Description,
			Provider:             m.Provider,
			ModelType:            m.ModelType,
			ContextWindow:        m.ContextWindow,
			PricingInput:         m.PricingInput,
			PricingOutput:        m.PricingOutput,
			Status:               m.Status,
			IsPublic:             m.IsPublic,
			Capabilities:         append([]string(nil), m.Capabilities...),
			Tags:                 append([]string(nil), m.Tags...),
			Category:             m.Category,
			Tier:                 m.Tier,
			Metadata:             m.Metadata,
			Aliases:              aliases,
			ChannelMappings:      channelMappings,
			SubscriptionMappings: subMappings,
		})
	}
	return out, nil
}

func (r *Repository) importModelsMemory(models []*biz.ModelExportModel, options biz.ImportOptions) (*biz.ImportSummary, error) {
	r.lock.Lock()
	defer r.lock.Unlock()
	summary := &biz.ImportSummary{}
	now := time.Now().Unix()
	// Build existing index.
	existing := make(map[string]*biz.Model)
	for _, m := range r.models {
		existing[biz.NormalizeModelID(m.ModelID)] = m
	}
	for _, em := range models {
		if em == nil {
			continue
		}
		exist := existing[biz.NormalizeModelID(em.ModelID)]
		outcome := computeImportOutcome(em, exist, options)
		switch outcome.Action {
		case "skip":
			// no-op
		case "conflict":
			summary.Results = append(summary.Results, outcome)
			summary.Conflicts++
			if options.ConflictStrategy == biz.ConflictStrategyReject {
				return nil, fmt.Errorf("%w: %s", biz.ErrImportConflict, outcome.Message)
			}
			continue
		case "create":
			r.modelNextID++
			do := importModelToDO(em, options, now)
			do.ID = r.modelNextID
			do.ModelID = biz.NormalizeModelID(do.ModelID)
			if r.models == nil {
				r.models = make(map[int64]*biz.Model)
			}
			r.models[do.ID] = do
			r.applyImportAliasesMemory(do.ID, em.Aliases, now)
			r.applyImportChannelMappingsMemory(do.ID, em.ChannelMappings, now)
			r.applyImportSubscriptionMappingsMemory(do.ID, em.SubscriptionMappings, now)
		case "update":
			do := importModelToDO(em, options, now)
			do.ID = exist.ID
			do.ModelID = biz.NormalizeModelID(em.ModelID)
			r.models[do.ID] = do
			// Clear and re-insert aliases/mappings.
			r.rebuildAliasesMemory(do.ID, em.Aliases, now)
			r.rebuildChannelMappingsMemory(do.ID, em.ChannelMappings, now)
			r.rebuildSubscriptionMappingsMemory(do.ID, em.SubscriptionMappings, now)
		}
		summary.Results = append(summary.Results, outcome)
		switch outcome.Action {
		case "create":
			summary.Created++
		case "update":
			summary.Updated++
		}
	}
	return summary, nil
}

func importModelToDO(em *biz.ModelExportModel, options biz.ImportOptions, now int64) *biz.Model {
	do := &biz.Model{
		ModelID:       em.ModelID,
		DisplayName:   em.DisplayName,
		Description:   em.Description,
		Provider:      em.Provider,
		ModelType:     em.ModelType,
		ContextWindow: em.ContextWindow,
		Status:        em.Status,
		IsPublic:      em.IsPublic,
		Capabilities:  append([]string(nil), em.Capabilities...),
		Tags:          append([]string(nil), em.Tags...),
		Category:      em.Category,
		Tier:          em.Tier,
		Metadata:      em.Metadata,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if do.DisplayName == "" {
		do.DisplayName = do.ModelID
	}
	if do.ModelType == "" {
		do.ModelType = "chat"
	}
	if options.ImportPrices {
		do.PricingInput = em.PricingInput
		do.PricingOutput = em.PricingOutput
	}
	return do
}

func (r *Repository) applyImportAliasesMemory(modelPK int64, aliases []*biz.ModelAlias, now int64) {
	if r.modelAliases == nil {
		r.modelAliases = make(map[int64]*biz.ModelAlias)
	}
	for _, a := range aliases {
		if a == nil || strings.TrimSpace(a.Alias) == "" {
			continue
		}
		r.modelAliasNextID++
		r.modelAliases[r.modelAliasNextID] = &biz.ModelAlias{
			ID:        r.modelAliasNextID,
			ModelPK:   modelPK,
			Alias:     biz.NormalizeModelID(a.Alias),
			IsPrimary: a.IsPrimary,
			CreatedAt: now,
		}
	}
}

func (r *Repository) rebuildAliasesMemory(modelPK int64, aliases []*biz.ModelAlias, now int64) {
	for id, a := range r.modelAliases {
		if a.ModelPK == modelPK {
			delete(r.modelAliases, id)
		}
	}
	r.applyImportAliasesMemory(modelPK, aliases, now)
}

func (r *Repository) applyImportChannelMappingsMemory(modelPK int64, mappings []*biz.ModelChannelMapping, now int64) {
	if r.modelChannelMappings == nil {
		r.modelChannelMappings = make(map[int64]*biz.ModelChannelMapping)
	}
	for _, m := range mappings {
		if m == nil {
			continue
		}
		r.modelMappingNextID++
		r.modelChannelMappings[r.modelMappingNextID] = &biz.ModelChannelMapping{
			ID:              r.modelMappingNextID,
			ChannelID:       m.ChannelID,
			ModelPK:         modelPK,
			Enabled:         m.Enabled,
			Priority:        m.Priority,
			Config:          m.Config,
			UpstreamModelID: m.UpstreamModelID,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
	}
}

func (r *Repository) rebuildChannelMappingsMemory(modelPK int64, mappings []*biz.ModelChannelMapping, now int64) {
	for id, m := range r.modelChannelMappings {
		if m.ModelPK == modelPK {
			delete(r.modelChannelMappings, id)
		}
	}
	r.applyImportChannelMappingsMemory(modelPK, mappings, now)
}

func (r *Repository) applyImportSubscriptionMappingsMemory(modelPK int64, mappings []*biz.ModelSubscriptionMapping, now int64) {
	if r.modelSubscriptionMappings == nil {
		r.modelSubscriptionMappings = make(map[int64]*biz.ModelSubscriptionMapping)
	}
	for _, m := range mappings {
		if m == nil {
			continue
		}
		group := m.GroupName
		if group == "" {
			group = "default"
		}
		r.modelSubMappingNextID++
		r.modelSubscriptionMappings[r.modelSubMappingNextID] = &biz.ModelSubscriptionMapping{
			ID:                    r.modelSubMappingNextID,
			SubscriptionAccountID: m.SubscriptionAccountID,
			ModelPK:               modelPK,
			GroupName:             group,
			Enabled:               m.Enabled,
			Priority:              m.Priority,
			UpstreamModelID:       m.UpstreamModelID,
			CreatedAt:             now,
			UpdatedAt:             now,
		}
	}
}

func (r *Repository) rebuildSubscriptionMappingsMemory(modelPK int64, mappings []*biz.ModelSubscriptionMapping, now int64) {
	for id, m := range r.modelSubscriptionMappings {
		if m.ModelPK == modelPK {
			delete(r.modelSubscriptionMappings, id)
		}
	}
	r.applyImportSubscriptionMappingsMemory(modelPK, mappings, now)
}
