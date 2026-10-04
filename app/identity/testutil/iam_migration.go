package testutil

import (
	"gorm.io/gorm"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/app/identity/internal/data"
)

type MigrationRequest = biz.IAMMigrationRequest
type MigrationManifest = biz.IAMMigrationManifest
type CutoverEvidence = biz.IAMCutoverEvidence

// NewIAMMigrationStack is a scratch-DB assembly seam for cross-service tests.
func NewIAMMigrationStack(db *gorm.DB) *biz.IAMMigrationUsecase {
	repo := data.NewRoutingBackfillRepository(db)
	return biz.NewIAMMigrationUsecase(data.NewIAMMigrationRepo(repo.Data), data.NewIAMTxRunner(repo.Data))
}

func MigrationManifestDigest(manifest MigrationManifest) string {
	return biz.IAMMigrationDigest(manifest)
}
