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
		return tx.Model(&row).Updates(map[string]any{"models": string(models), "diff": string(changes), "attempt_id": i.AttemptID, "observed_at": i.ObservedAt, "manual": i.Manual}).Error
	})
	return diff, err
}
