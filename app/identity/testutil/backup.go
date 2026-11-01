package testutil

import (
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/app/identity/internal/data"
)

type MFARequired = biz.MFARequired

// NewBackupSecurity wires the production encryption and shared Redis seams
// over isolated recovery storage, without starting a public production server.
func NewBackupSecurity(db *gorm.DB, rdb *redis.Client, key []byte) *biz.IdentityUsecase {
	d := data.NewBackupRecoveryData(db, rdb)
	uc := biz.NewIdentityUsecase(data.NewRepository(d), nil)
	uc.SetIAMRuntime(data.NewIAMRuntimeRepo(d), data.NewIAMTxRunner(d))
	challenge := data.NewChallengeRepo(d)
	uc.SetSecurity(data.NewSecurityRepo(d, key), challenge, challenge.(biz.StepUpRepo))
	return uc
}
