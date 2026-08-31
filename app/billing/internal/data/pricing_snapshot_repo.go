package data

import (
	"context"
	"errors"
	"time"

	"micro-one-api/app/billing/internal/biz"

	subscriptionbiz "micro-one-api/domain/subscription/biz"

	"gorm.io/gorm"
)

// pricingSnapshotModel is the migration 088 billing_pricing_snapshots row:
// immutable pricing evidence referenced by billing_ledgers.pricing_config_hash.
type pricingSnapshotModel struct {
	ID                   uint      `gorm:"primaryKey;column:id"`
	ConfigHash           string    `gorm:"column:config_hash;uniqueIndex:uk_billing_pricing_snapshots_hash"`
	ModelName            string    `gorm:"column:model_name"`
	InputPrice           float64   `gorm:"column:input_price"`
	OutputPrice          float64   `gorm:"column:output_price"`
	CacheReadPrice       float64   `gorm:"column:cache_read_price"`
	CacheCreation5mPrice float64   `gorm:"column:cache_creation_5m_price"`
	CacheCreation1hPrice float64   `gorm:"column:cache_creation_1h_price"`
	GroupRatio           float64   `gorm:"column:group_ratio"`
	CacheCreationMode    string    `gorm:"column:cache_creation_mode"`
	SnapshotVersion      int32     `gorm:"column:snapshot_version"`
	CreatedAt            time.Time `gorm:"column:created_at"`
}

func (pricingSnapshotModel) TableName() string { return "billing_pricing_snapshots" }

type pricingSnapshotRepo struct {
	data *Data
}

func NewPricingSnapshotRepo(data *Data) biz.PricingSnapshotRepo {
	return &pricingSnapshotRepo{data: data}
}

// ClaimPricingSnapshotInTx inserts the snapshot inside the caller's commit
// transaction, mirroring the billing_ledger_dedupe_claims pattern: a unique
// violation on config_hash is a benign reuse of an already-claimed snapshot,
// any other error fails the transaction so a ledger row can never commit
// without its pricing evidence.
func (r *pricingSnapshotRepo) ClaimPricingSnapshotInTx(ctx context.Context, tx subscriptionbiz.Tx, snapshot *biz.PricingSnapshot) error {
	if snapshot == nil || snapshot.ConfigHash == "" {
		return nil
	}
	db := txDB(tx)
	if db == nil {
		if r.data == nil || r.data.db == nil {
			return errors.New("pricing snapshot repo: no database available")
		}
		db = r.data.db
	}
	return claimPricingSnapshot(ctx, db, snapshot)
}

func (r *pricingSnapshotRepo) ClaimPricingSnapshot(ctx context.Context, snapshot *biz.PricingSnapshot) error {
	if snapshot == nil || snapshot.ConfigHash == "" {
		return nil
	}
	if r.data == nil || r.data.db == nil {
		return errors.New("pricing snapshot repo: no database available")
	}
	return r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return claimPricingSnapshot(ctx, tx, snapshot)
	})
}

func claimPricingSnapshot(ctx context.Context, db *gorm.DB, snapshot *biz.PricingSnapshot) error {
	model := &pricingSnapshotModel{
		ConfigHash:           snapshot.ConfigHash,
		ModelName:            snapshot.ModelName,
		InputPrice:           snapshot.InputPrice,
		OutputPrice:          snapshot.OutputPrice,
		CacheReadPrice:       snapshot.CacheReadPrice,
		CacheCreation5mPrice: snapshot.CacheCreation5mPrice,
		CacheCreation1hPrice: snapshot.CacheCreation1hPrice,
		GroupRatio:           snapshot.GroupRatio,
		CacheCreationMode:    snapshot.CacheCreationMode,
		SnapshotVersion:      snapshot.SnapshotVersion,
	}
	if model.SnapshotVersion == 0 {
		model.SnapshotVersion = biz.PricingSnapshotVersion
	}
	if err := db.WithContext(ctx).Create(model).Error; err != nil {
		if isUniqueConstraintError(err) {
			// Identical charge inputs were already frozen; reuse the evidence.
			return nil
		}
		return err
	}
	snapshot.ID = model.ID
	snapshot.CreatedAt = model.CreatedAt
	return nil
}

// GetPricingSnapshotByHash returns the snapshot a ledger row references.
func (r *pricingSnapshotRepo) GetPricingSnapshotByHash(ctx context.Context, configHash string) (*biz.PricingSnapshot, error) {
	if configHash == "" {
		return nil, nil
	}
	if r == nil || r.data == nil || r.data.db == nil {
		return nil, errors.New("pricing snapshot repo: no database available")
	}
	var model pricingSnapshotModel
	if err := r.data.db.WithContext(ctx).
		Where("config_hash = ?", configHash).
		First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrPricingSnapshotNotFound
		}
		return nil, err
	}
	return &biz.PricingSnapshot{
		ID:                   model.ID,
		ConfigHash:           model.ConfigHash,
		ModelName:            model.ModelName,
		InputPrice:           model.InputPrice,
		OutputPrice:          model.OutputPrice,
		CacheReadPrice:       model.CacheReadPrice,
		CacheCreation5mPrice: model.CacheCreation5mPrice,
		CacheCreation1hPrice: model.CacheCreation1hPrice,
		GroupRatio:           model.GroupRatio,
		CacheCreationMode:    model.CacheCreationMode,
		SnapshotVersion:      model.SnapshotVersion,
		CreatedAt:            model.CreatedAt,
	}, nil
}
