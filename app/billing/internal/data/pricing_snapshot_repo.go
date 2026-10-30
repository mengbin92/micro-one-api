package data

import (
	"context"
	"errors"
	"micro-one-api/domain/authorization"
	"time"

	"micro-one-api/app/billing/internal/biz"

	subscriptionbiz "micro-one-api/domain/subscription/biz"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// pricingSnapshotModel is the migration 088 billing_pricing_snapshots row:
// immutable pricing evidence referenced by billing_ledgers.pricing_config_hash.
type pricingSnapshotModel struct {
	PricingTimeBand string `gorm:"column:pricing_time_band"`
	PricingTimezone string `gorm:"column:pricing_timezone"`

	LongContextPolicy    string `gorm:"column:long_context_policy"`
	LongContextThreshold int64  `gorm:"column:long_context_threshold"`
	LongContextTier      string `gorm:"column:long_context_tier"`

	ServiceTier           string    `gorm:"column:service_tier"`
	ServiceTierPolicy     string    `gorm:"column:service_tier_policy"`
	ServiceTierConfigHash string    `gorm:"column:service_tier_config_hash"`
	ID                    uint      `gorm:"primaryKey;column:id"`
	ConfigHash            string    `gorm:"column:config_hash;uniqueIndex:uk_billing_pricing_snapshots_hash"`
	ModelName             string    `gorm:"column:model_name"`
	InputPrice            float64   `gorm:"column:input_price"`
	OutputPrice           float64   `gorm:"column:output_price"`
	CacheReadPrice        float64   `gorm:"column:cache_read_price"`
	CacheCreation5mPrice  float64   `gorm:"column:cache_creation_5m_price"`
	CacheCreation1hPrice  float64   `gorm:"column:cache_creation_1h_price"`
	GroupRatio            float64   `gorm:"column:group_ratio"`
	CacheCreationMode     string    `gorm:"column:cache_creation_mode"`
	SnapshotVersion       int32     `gorm:"column:snapshot_version"`
	CreatedAt             time.Time `gorm:"column:created_at"`
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
		ConfigHash:            snapshot.ConfigHash,
		ServiceTier:           snapshot.ServiceTier,
		ServiceTierPolicy:     snapshot.ServiceTierPolicy,
		ServiceTierConfigHash: snapshot.ServiceTierConfigHash,
		LongContextPolicy:     snapshot.LongContextPolicy,
		PricingTimeBand:       snapshot.PricingTimeBand,
		PricingTimezone:       snapshot.PricingTimezone,

		LongContextThreshold: snapshot.LongContextThreshold,
		LongContextTier:      snapshot.LongContextTier,
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
	create := db.WithContext(ctx)
	var omitted []string
	if snapshot.PricingTimeBand == "" {
		omitted = append(omitted, "PricingTimeBand", "PricingTimezone")
	}
	if snapshot.LongContextPolicy == "" {
		omitted = append(omitted, "LongContextPolicy", "LongContextThreshold", "LongContextTier")
	}
	if snapshot.ServiceTierConfigHash == "" {
		omitted = append(omitted, "ServiceTier", "ServiceTierPolicy", "ServiceTierConfigHash")
	}
	create = create.Omit(omitted...)
	// PostgreSQL marks a transaction failed after a unique violation, even
	// when Go swallows it. Conflict-free insertion keeps the caller's money
	// transaction usable when another ledger reuses an immutable snapshot.
	if err := create.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "config_hash"}}, DoNothing: true}).Create(model).Error; err != nil {
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
	if _, iam := authorization.QueryScopeFromContext(ctx, "billing.pricing.read"); iam {
		id, err := r.ModelResourceID(ctx, model.ModelName)
		if err != nil {
			return nil, err
		}
		if authorization.Require(ctx, "billing.pricing.read", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: id}) != nil {
			return nil, nil
		}
	}
	return &biz.PricingSnapshot{
		ID:                    model.ID,
		ConfigHash:            model.ConfigHash,
		ServiceTier:           model.ServiceTier,
		ServiceTierPolicy:     model.ServiceTierPolicy,
		ServiceTierConfigHash: model.ServiceTierConfigHash,
		LongContextPolicy:     model.LongContextPolicy,
		PricingTimeBand:       model.PricingTimeBand,
		PricingTimezone:       model.PricingTimezone,

		LongContextThreshold: model.LongContextThreshold,
		LongContextTier:      model.LongContextTier,
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

func (r *pricingSnapshotRepo) ModelResourceID(ctx context.Context, model string) (int64, error) {
	var row struct{ ID int64 }
	err := r.data.db.WithContext(ctx).Table("models").Select("id").Where("model_id = ?", model).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	return row.ID, err
}
