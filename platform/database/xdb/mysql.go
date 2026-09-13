package xdb

import (
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// PoolConfig holds database connection pool configuration
type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// DefaultPoolConfig returns sensible defaults for connection pooling
func DefaultPoolConfig() *PoolConfig {
	return &PoolConfig{
		MaxOpenConns:    100,
		MaxIdleConns:    10,
		ConnMaxLifetime: time.Hour,
		ConnMaxIdleTime: 10 * time.Minute,
	}
}

// OpenMySQL opens a MySQL connection with default pool settings
func OpenMySQL(dsn string) (*gorm.DB, error) {
	return OpenMySQLWithPool(dsn, DefaultPoolConfig())
}

// OpenMySQLWithPool opens a MySQL connection with custom pool settings
func OpenMySQLWithPool(dsn string, pool *PoolConfig) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(dsn), gormConfig())
	if err != nil {
		return nil, err
	}

	if pool == nil {
		pool = DefaultPoolConfig()
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	sqlDB.SetMaxOpenConns(pool.MaxOpenConns)
	sqlDB.SetMaxIdleConns(pool.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(pool.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(pool.ConnMaxIdleTime)

	return db, nil
}
