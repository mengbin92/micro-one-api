package testutil

import (
	"gorm.io/gorm"
	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/app/log/internal/biz"
	"micro-one-api/app/log/internal/data"
	"micro-one-api/app/log/internal/service"
)

// NewServiceTierLog borrows migrated scratch storage for the relay acceptance
// chain, including the real transport-to-domain and domain-to-storage mappings.
func NewServiceTierLog(db *gorm.DB) logv1.LogServiceServer {
	return service.NewLogService(biz.NewLogUsecase(data.NewRepositoryWithDB(db)))
}
