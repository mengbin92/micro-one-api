package data

import (
	"context"
	"testing"

	"micro-one-api/app/billing/internal/biz"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newPricingSnapshotTestRepo(t *testing.T) (*pricingSnapshotRepo, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&pricingSnapshotModel{}))
	return &pricingSnapshotRepo{data: &Data{db: db}}, db
}

func TestPricingSnapshotRepo_ClaimIsIdempotentByHash(t *testing.T) {
	repo, db := newPricingSnapshotTestRepo(t)
	snapshot := &biz.PricingSnapshot{
		ConfigHash:        "a" + "b64chars-hash-placeholder-0000000000000000000000000",
		ModelName:         "public-model",
		InputPrice:        0.001,
		OutputPrice:       0.002,
		CacheReadPrice:    0.0001,
		GroupRatio:        1,
		CacheCreationMode: "observe",
		SnapshotVersion:   biz.PricingSnapshotVersion,
	}

	require.NoError(t, repo.ClaimPricingSnapshot(context.Background(), snapshot))
	// A concurrent writer claiming the identical hash reuses the evidence.
	require.NoError(t, repo.ClaimPricingSnapshot(context.Background(), snapshot))

	var count int64
	require.NoError(t, db.Model(&pricingSnapshotModel{}).Count(&count).Error)
	assert.Equal(t, int64(1), count, "identical config_hash must collapse to one snapshot")

	got, err := repo.GetPricingSnapshotByHash(context.Background(), snapshot.ConfigHash)
	require.NoError(t, err)
	assert.Equal(t, snapshot.ConfigHash, got.ConfigHash)
	assert.InDelta(t, 0.001, got.InputPrice, 1e-12)
	assert.InDelta(t, 0.0001, got.CacheReadPrice, 1e-12)
	assert.Equal(t, "public-model", got.ModelName)
	assert.Equal(t, "observe", got.CacheCreationMode)
}

func TestPricingSnapshotRepo_GetMissingHash(t *testing.T) {
	repo, _ := newPricingSnapshotTestRepo(t)
	_, err := repo.GetPricingSnapshotByHash(context.Background(), "does-not-exist")
	assert.ErrorIs(t, err, biz.ErrPricingSnapshotNotFound)
	// Empty hash is not an error: legacy rows simply have no evidence.
	snap, err := repo.GetPricingSnapshotByHash(context.Background(), "")
	require.NoError(t, err)
	assert.Nil(t, snap)
}
