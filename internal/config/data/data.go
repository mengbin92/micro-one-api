package data

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"micro-one-api/internal/config/biz"
	"micro-one-api/internal/pkg/xdb"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type Repository struct {
	db    *gorm.DB
	redis *redis.Client
	mu    sync.RWMutex
	mem   map[string]*biz.ConfigEntry // key = "namespace/key"
}

type configModel struct {
	ID        int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Namespace string `gorm:"column:namespace;index"`
	Key       string `gorm:"column:key;index"`
	Value     string `gorm:"column:value"`
	Comment   string `gorm:"column:comment"`
	UpdatedAt int64  `gorm:"column:updated_at"`
}

func (configModel) TableName() string { return "configs" }

func NewRepositoryFromEnv(dsn ...string) (*Repository, error) {
	var dbDSN string
	if len(dsn) > 0 && dsn[0] != "" {
		dbDSN = dsn[0]
	} else {
		dbDSN = os.Getenv("CONFIG_SQL_DSN")
		if dbDSN == "" {
			dbDSN = os.Getenv("SQL_DSN")
		}
	}
	if dbDSN == "" {
		return newMemoryRepository(), nil
	}
	db, err := xdb.OpenMySQL(dbDSN)
	if err != nil {
		return nil, err
	}
	redisAddr := os.Getenv("REDIS_ADDR")
	rdb := xdb.NewRedisClient(redisAddr)
	if rdb != nil {
		if pingErr := rdb.Ping(context.Background()).Err(); pingErr != nil {
			rdb.Close()
			rdb = nil
		}
	}
	return &Repository{db: db, redis: rdb}, nil
}

func newMemoryRepository() *Repository {
	return &Repository{
		mem: map[string]*biz.ConfigEntry{
			"default/theme": {
				ID:        1,
				Namespace: "default",
				Key:       "theme",
				Value:     "dark",
				Comment:   "UI theme setting",
				UpdatedAt: time.Now(),
			},
		},
	}
}

func (r *Repository) Get(ctx context.Context, namespace, key string) (*biz.ConfigEntry, error) {
	if r.db != nil {
		return r.getDB(ctx, namespace, key)
	}
	return r.getMemory(namespace, key)
}

func (r *Repository) List(ctx context.Context, namespace string, page, pageSize int32) ([]*biz.ConfigEntry, int64, error) {
	if r.db != nil {
		return r.listDB(ctx, namespace, page, pageSize)
	}
	return r.listMemory(namespace, page, pageSize)
}

func (r *Repository) Set(ctx context.Context, entry *biz.ConfigEntry) error {
	if r.db != nil {
		return r.setDB(ctx, entry)
	}
	return r.setMemory(entry)
}

func (r *Repository) Delete(ctx context.Context, namespace, key string) error {
	if r.db != nil {
		return r.deleteDB(ctx, namespace, key)
	}
	return r.deleteMemory(namespace, key)
}

// DB implementations

func (r *Repository) getDB(ctx context.Context, namespace, key string) (*biz.ConfigEntry, error) {
	var m configModel
	if err := r.db.WithContext(ctx).Where("namespace = ? AND `key` = ?", namespace, key).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrConfigNotFound
		}
		return nil, err
	}
	return &biz.ConfigEntry{
		ID:        m.ID,
		Namespace: m.Namespace,
		Key:       m.Key,
		Value:     m.Value,
		Comment:   m.Comment,
		UpdatedAt: time.Unix(m.UpdatedAt, 0),
	}, nil
}

func (r *Repository) listDB(ctx context.Context, namespace string, page, pageSize int32) ([]*biz.ConfigEntry, int64, error) {
	query := r.db.WithContext(ctx).Model(&configModel{})
	if namespace != "" {
		query = query.Where("namespace = ?", namespace)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	var models []configModel
	if err := query.Offset(int(offset)).Limit(int(pageSize)).Order("id DESC").Find(&models).Error; err != nil {
		return nil, 0, err
	}
	entries := make([]*biz.ConfigEntry, len(models))
	for i, m := range models {
		entries[i] = &biz.ConfigEntry{
			ID:        m.ID,
			Namespace: m.Namespace,
			Key:       m.Key,
			Value:     m.Value,
			Comment:   m.Comment,
			UpdatedAt: time.Unix(m.UpdatedAt, 0),
		}
	}
	return entries, total, nil
}

func (r *Repository) setDB(ctx context.Context, entry *biz.ConfigEntry) error {
	var existing configModel
	err := r.db.WithContext(ctx).Where("namespace = ? AND `key` = ?", entry.Namespace, entry.Key).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		m := configModel{
			Namespace: entry.Namespace,
			Key:       entry.Key,
			Value:     entry.Value,
			Comment:   entry.Comment,
			UpdatedAt: entry.UpdatedAt.Unix(),
		}
		return r.db.WithContext(ctx).Create(&m).Error
	}
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&existing).Updates(map[string]interface{}{
		"value":      entry.Value,
		"comment":    entry.Comment,
		"updated_at": entry.UpdatedAt.Unix(),
	}).Error
}

func (r *Repository) deleteDB(ctx context.Context, namespace, key string) error {
	return r.db.WithContext(ctx).Where("namespace = ? AND `key` = ?", namespace, key).Delete(&configModel{}).Error
}

// Memory implementations

func (r *Repository) getMemory(namespace, key string) (*biz.ConfigEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	k := namespace + "/" + key
	entry, ok := r.mem[k]
	if !ok {
		return nil, biz.ErrConfigNotFound
	}
	cloned := *entry
	return &cloned, nil
}

func (r *Repository) listMemory(namespace string, page, pageSize int32) ([]*biz.ConfigEntry, int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var all []*biz.ConfigEntry
	for _, entry := range r.mem {
		if namespace != "" && entry.Namespace != namespace {
			continue
		}
		cloned := *entry
		all = append(all, &cloned)
	}
	total := int64(len(all))
	start := int((page - 1) * pageSize)
	if start >= len(all) {
		return nil, total, nil
	}
	end := start + int(pageSize)
	if end > len(all) {
		end = len(all)
	}
	return all[start:end], total, nil
}

func (r *Repository) setMemory(entry *biz.ConfigEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := entry.Namespace + "/" + entry.Key
	if existing, ok := r.mem[k]; ok {
		entry.ID = existing.ID
	} else {
		entry.ID = int64(len(r.mem) + 1)
	}
	r.mem[k] = entry
	return nil
}

func (r *Repository) deleteMemory(namespace, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := namespace + "/" + key
	if _, ok := r.mem[k]; !ok {
		return biz.ErrConfigNotFound
	}
	delete(r.mem, k)
	return nil
}
