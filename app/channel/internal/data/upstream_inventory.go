package data

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/database/authzquery"
)

type upstreamInventoryModel struct {
	ID         string `gorm:"primaryKey;size:128"`
	Models     string `gorm:"type:text"`
	Diff       string `gorm:"type:text"`
	AttemptID  string `gorm:"size:128"`
	ObservedAt int64
	Manual     bool
}

func (upstreamInventoryModel) TableName() string { return "channel_upstream_inventory" }
func (r *Repository) SaveUpstreamInventory(ctx context.Context, i biz.UpstreamInventory) (diff biz.InventoryDiff, err error) {
	if r.db == nil || !r.db.Migrator().HasTable(&upstreamInventoryModel{}) {
		return diff, fmt.Errorf("inventory schema unavailable")
	}
	key := fmt.Sprintf("%s:%d", i.SourceKind, i.SourceID)
	err = authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&upstreamInventoryModel{ID: key, Models: "[]", Diff: "{}"}).Error; err != nil {
			return err
		}
		var row upstreamInventoryModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", key).First(&row).Error; err != nil {
			return err
		}
		if row.ObservedAt > i.ObservedAt {
			return nil
		}
		var before []string
		if jsonx.Unmarshal([]byte(row.Models), &before) != nil {
			return fmt.Errorf("invalid stored inventory")
		}
		old, newSet := map[string]bool{}, map[string]bool{}
		for _, id := range before {
			old[id] = true
		}
		for _, id := range i.ExactModelIDs {
			newSet[id] = true
			if !old[id] {
				diff.Added = append(diff.Added, id)
			}
		}
		for _, id := range before {
			if !newSet[id] {
				diff.Missing = append(diff.Missing, id)
			}
		}
		models, _ := jsonx.Marshal(i.ExactModelIDs)
		changes, _ := jsonx.Marshal(diff)
		if err := r.registerInventoryCandidatesTx(tx, i); err != nil {
			return err
		}
		return tx.Model(&row).Updates(map[string]any{"models": string(models), "diff": string(changes), "attempt_id": i.AttemptID, "observed_at": i.ObservedAt, "manual": i.Manual}).Error
	})
	return diff, err
}

// Candidates and the inventory receipt commit atomically. Existing catalog
// decisions, prices, manual models and source mapping states are never updated.
func (r *Repository) registerInventoryCandidatesTx(tx *gorm.DB, i biz.UpstreamInventory) error {
	if len(i.DiscoveredModels) == 0 {
		return nil
	}
	var source channelModel
	var account subscriptionAccountModel
	if i.SourceKind == "channel" {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&source, i.SourceID).Error; err != nil {
			return err
		}
	} else {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&account, i.SourceID).Error; err != nil {
			return err
		}
	}
	for _, candidate := range i.DiscoveredModels {
		po := newModelPO(candidate)
		po.AuthorizationRevision = 1
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(po).Error; err != nil {
			return err
		}
		var model modelModel
		if err := tx.Where("LOWER(model_id) = ?", biz.NormalizeModelID(candidate.ModelID)).First(&model).Error; err != nil {
			return err
		}
		if i.SourceKind == "channel" {
			mapping := &modelChannelMappingModel{ChannelID: source.ID, ModelPK: model.ID, UpstreamModelID: candidate.ModelID, Enabled: false, Config: "{}", CreatedAt: candidate.CreatedAt, UpdatedAt: candidate.UpdatedAt}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(mapping).Error; err != nil {
				return err
			}
		} else {
			for _, group := range biz.SplitCSV(account.Group) {
				mapping := &modelSubscriptionMappingModel{SubscriptionAccountID: account.ID, ModelPK: model.ID, GroupName: group, UpstreamModelID: candidate.ModelID, Enabled: false, CreatedAt: candidate.CreatedAt, UpdatedAt: candidate.UpdatedAt}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(mapping).Error; err != nil {
					return err
				}
			}
		}
	}
	return nil
}
