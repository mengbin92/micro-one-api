package data

import (
	"context"
	"os"
	"sync"

	"micro-one-api/domain/subscription/biz"
	"micro-one-api/platform/database/xdb"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type Repository struct {
	db    *gorm.DB
	redis *redis.Client

	lock          sync.RWMutex
	groups        map[int64]*biz.SubscriptionGroup
	plans         map[int64]*biz.SubscriptionPlan
	subscriptions map[int64]*biz.UserSubscription
	nextGroupID   int64
	nextPlanID    int64
	nextSubID     int64
}

func NewRepositoryFromEnv(driver string, dsn ...string) (*Repository, error) {
	var dbDSN string
	if len(dsn) > 0 && dsn[0] != "" {
		dbDSN = dsn[0]
	} else {
		dbDSN = os.Getenv("SUBSCRIPTION_SQL_DSN")
		if dbDSN == "" {
			dbDSN = os.Getenv("SQL_DSN")
		}
		if dbDSN == "" {
			dbDSN = os.Getenv("DATABASE_DSN")
		}
	}
	if dbDSN == "" {
		return newMemoryRepository(), nil
	}
	// Schema isolation (Phase 2.4): effective schema comes from the wire
	// argument, the per-service env var, or the global DATABASE_SCHEMA fallback.
	schema := xdb.ResolveSchema("", "SUBSCRIPTION_SCHEMA", "DATABASE_SCHEMA")
	if len(dsn) > 1 && dsn[1] != "" {
		schema = dsn[1]
	}
	db, err := xdb.Open(xdb.DatabaseConfig{Driver: xdb.NormalizeDriver(driver, dbDSN), DSN: dbDSN, Schema: schema})
	if err != nil {
		return nil, err
	}
	redisAddr := os.Getenv("REDIS_ADDR")
	redisPassword := os.Getenv("REDIS_PASSWORD")
	rdb := xdb.NewRedisClient(redisAddr, redisPassword)
	if rdb != nil {
		if pingErr := rdb.Ping(context.Background()).Err(); pingErr != nil {
			rdb.Close()
			rdb = nil
		}
	}
	return &Repository{db: db, redis: rdb}, nil
}

func NewRepository(db *gorm.DB, redis *redis.Client) *Repository {
	return &Repository{db: db, redis: redis}
}

func newMemoryRepository() *Repository {
	return &Repository{
		groups:        make(map[int64]*biz.SubscriptionGroup),
		plans:         make(map[int64]*biz.SubscriptionPlan),
		subscriptions: make(map[int64]*biz.UserSubscription),
		nextGroupID:   1,
		nextPlanID:    1,
		nextSubID:     1,
	}
}

func NewMemoryRepositoryForTest() *Repository {
	return newMemoryRepository()
}

func (r *Repository) DB() *gorm.DB {
	if r == nil {
		return nil
	}
	return r.db
}

func (r *Repository) Redis() *redis.Client {
	if r == nil {
		return nil
	}
	return r.redis
}
