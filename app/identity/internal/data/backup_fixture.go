package data

import (
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// NewBackupRecoveryData borrows isolated caller-owned recovery clients.
func NewBackupRecoveryData(db *gorm.DB, rdb *redis.Client) *Data { return &Data{db: db, redis: rdb} }
