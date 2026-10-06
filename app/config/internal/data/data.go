package data

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"gorm.io/gorm/clause"
	"micro-one-api/app/config/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/database/authzquery"
	"micro-one-api/platform/database/xdb"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type Repository struct {
	db          *gorm.DB
	redis       *redis.Client
	mu          sync.RWMutex
	memVersions map[string]int64
	mem         map[string]*biz.ConfigEntry // key = "namespace/key"
}

type configModel struct {
	Deleted   xdb.Flag `gorm:"column:deleted"`
	ID        int64    `gorm:"column:id;primaryKey;autoIncrement"`
	Revision  int64    `gorm:"column:revision"`
	Namespace string   `gorm:"column:namespace;index"`
	Key       string   `gorm:"column:key;index"`
	Value     string   `gorm:"column:value"`
	Comment   string   `gorm:"column:comment"`
	UpdatedAt int64    `gorm:"column:updated_at"`
}

func (configModel) TableName() string { return "configs" }

// A key row exists independently of a config incarnation, so absent-key
// creation and tombstone resurrection serialize on every database dialect.
type configKeyLockModel struct {
	Namespace string `gorm:"column:namespace;primaryKey"`
	ConfigKey string `gorm:"column:config_key;primaryKey"`
}

func (configKeyLockModel) TableName() string { return "config_key_locks" }
func lockConfigKey(tx *gorm.DB, namespace, key string) error {
	row := configKeyLockModel{Namespace: namespace, ConfigKey: key}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return err
	}
	return tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("namespace = ? AND config_key = ?", namespace, key).First(&row).Error
}

func NewRepositoryFromEnv(driver string, dsn ...string) (*Repository, error) {
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
	// Schema isolation (Phase 2.4): effective schema comes from the wire
	// argument, the per-service env var, or the global DATABASE_SCHEMA fallback.
	schema := xdb.ResolveSchema("", "CONFIG_SCHEMA", "DATABASE_SCHEMA")
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
			_ = rdb.Close()
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

func (r *Repository) Redis() *redis.Client {
	if r == nil {
		return nil
	}
	return r.redis
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
		err := r.setDB(ctx, entry)
		return recordConfigFailure(ctx, r.db, entry.Namespace, entry.Key, entry.ID, err)
	}
	if err := authorization.RequireDurableWrite(ctx, biz.ConfigWriteOperation(entry.Namespace, entry.Key)); err != nil {
		return err
	}
	ctx, err := authorization.Refresh(ctx)
	if err != nil {
		return err
	}
	if err = requireConfigWrite(ctx, entry.Namespace, entry.Key); err != nil {
		return err
	}
	return r.setMemory(ctx, entry)
}

func (r *Repository) Delete(ctx context.Context, namespace, key string) error {
	if r.db != nil {
		err := r.deleteDB(ctx, namespace, key)
		return recordConfigFailure(ctx, r.db, namespace, key, 0, err)
	}
	if err := authorization.RequireDurableWrite(ctx, biz.ConfigWriteOperation(namespace, key)); err != nil {
		return err
	}
	ctx, err := authorization.Refresh(ctx)
	if err != nil {
		return err
	}
	if err = requireConfigWrite(ctx, namespace, key); err != nil {
		return err
	}
	return r.deleteMemory(ctx, namespace, key)
}

// DB implementations

func (r *Repository) getDB(ctx context.Context, namespace, key string) (*biz.ConfigEntry, error) {
	var m configModel
	if err := r.db.WithContext(ctx).Where(map[string]any{"namespace": namespace, "key": key, "deleted": 0}).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrConfigNotFound
		}
		return nil, err
	}
	return &biz.ConfigEntry{
		ID:        m.ID,
		Revision:  m.Revision,
		Namespace: m.Namespace,
		Key:       m.Key,
		Value:     m.Value,
		Comment:   m.Comment,
		UpdatedAt: time.Unix(m.UpdatedAt, 0),
	}, nil
}

func (r *Repository) listDB(ctx context.Context, namespace string, page, pageSize int32) ([]*biz.ConfigEntry, int64, error) {
	query := r.db.WithContext(ctx).Model(&configModel{}).Where("deleted = 0")
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
			Revision:  m.Revision,
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
	var storedID, storedRevision int64
	err := authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		if err := lockConfigKey(tx, entry.Namespace, entry.Key); err != nil {
			return err
		}
		if err := requireConfigWrite(ctx, entry.Namespace, entry.Key); err != nil {
			return err
		}
		var existing configModel
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(map[string]any{"namespace": entry.Namespace, "key": entry.Key}).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if _, iam := authorization.QueryScopeFromContext(ctx, biz.ConfigWriteOperation(entry.Namespace, entry.Key)); iam {
				expected, ok := authorization.ExpectedResourceRevision(ctx)
				if !ok || expected != 0 {
					return biz.ErrConfigRevisionConflict
				}
			}
			m := configModel{
				Namespace: entry.Namespace,
				Key:       entry.Key,
				Value:     entry.Value,
				Comment:   entry.Comment,
				UpdatedAt: entry.UpdatedAt.Unix(),
				Revision:  1,
			}
			if err := tx.Create(&m).Error; err != nil {
				return err
			}
			storedID, storedRevision = int64(m.ID), m.Revision
			return appendConfigAudit(ctx, tx, entry.Namespace, entry.Key, storedID)
		}
		if err != nil {
			return err
		}
		if _, iam := authorization.QueryScopeFromContext(ctx, biz.ConfigWriteOperation(entry.Namespace, entry.Key)); iam {
			expected, ok := authorization.ExpectedResourceRevision(ctx)
			logicalRevision := existing.Revision
			if existing.Deleted != 0 {
				logicalRevision = 0
			}
			if !ok || expected != uint64(logicalRevision) {
				return biz.ErrConfigRevisionConflict
			}
		}
		storedID = int64(existing.ID)
		storedRevision = existing.Revision + 1
		if err := tx.Model(&existing).Updates(map[string]any{
			"deleted":    0,
			"value":      entry.Value,
			"comment":    entry.Comment,
			"updated_at": entry.UpdatedAt.Unix(),
			"revision":   storedRevision,
		}).Error; err != nil {
			return err
		}
		return appendConfigAudit(ctx, tx, entry.Namespace, entry.Key, storedID)
	})
	if err == nil {
		entry.ID, entry.Revision = storedID, storedRevision
	}
	return err
}

func (r *Repository) deleteDB(ctx context.Context, namespace, key string) error {
	return authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		if err := lockConfigKey(tx, namespace, key); err != nil {
			return err
		}
		if err := requireConfigWrite(ctx, namespace, key); err != nil {
			return err
		}
		var current configModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(map[string]any{"namespace": namespace, "key": key, "deleted": 0}).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return biz.ErrConfigNotFound
			}
			return err
		}
		if _, iam := authorization.QueryScopeFromContext(ctx, biz.ConfigWriteOperation(namespace, key)); iam {
			expected, ok := authorization.ExpectedResourceRevision(ctx)
			if !ok || current.Revision <= 0 || expected != uint64(current.Revision) {
				return biz.ErrConfigRevisionConflict
			}
		}
		if err := tx.Model(&current).Where("revision = ? AND deleted = 0", current.Revision).Updates(map[string]any{"deleted": 1, "value": "", "comment": "", "revision": current.Revision + 1, "updated_at": time.Now().Unix()}).Error; err != nil {
			return err
		}
		return appendConfigAudit(ctx, tx, namespace, key, int64(current.ID))
	})
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
	end := min(start+int(pageSize), len(all))
	return all[start:end], total, nil
}

func (r *Repository) setMemory(ctx context.Context, entry *biz.ConfigEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := entry.Namespace + "/" + entry.Key
	if _, iam := authorization.QueryScopeFromContext(ctx, biz.ConfigWriteOperation(entry.Namespace, entry.Key)); iam {
		expected, ok := authorization.ExpectedResourceRevision(ctx)
		revision := int64(0)
		if old := r.mem[k]; old != nil {
			revision = old.Revision
		}
		if !ok || expected != uint64(revision) {
			return biz.ErrConfigRevisionConflict
		}
	}
	if existing, ok := r.mem[k]; ok {
		entry.ID = existing.ID
		entry.Revision = existing.Revision
	} else {
		entry.ID = int64(len(r.mem) + 1)
	}
	if r.memVersions == nil {
		r.memVersions = map[string]int64{}
	}
	entry.Revision = max(entry.Revision, r.memVersions[k]) + 1
	r.memVersions[k] = entry.Revision
	copy := *entry
	r.mem[k] = &copy
	return nil
}

func (r *Repository) deleteMemory(ctx context.Context, namespace, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := namespace + "/" + key
	if _, ok := r.mem[k]; !ok {
		return biz.ErrConfigNotFound
	}
	if _, iam := authorization.QueryScopeFromContext(ctx, biz.ConfigWriteOperation(namespace, key)); iam {
		expected, ok := authorization.ExpectedResourceRevision(ctx)
		revision := r.mem[k].Revision
		if !ok || revision <= 0 || expected != uint64(revision) {
			return biz.ErrConfigRevisionConflict
		}
	}
	if r.memVersions == nil {
		r.memVersions = map[string]int64{}
	}
	r.memVersions[k] = r.mem[k].Revision + 1
	delete(r.mem, k)
	return nil
}

func requireConfigWrite(ctx context.Context, namespace, key string) error {
	facts := authorization.ObjectFacts{Context: authorization.Platform()}
	for _, op := range []string{"system.option.update", biz.ConfigWriteOperation(namespace, key)} {
		if err := authorization.Require(ctx, op, facts); err != nil {
			return err
		}
	}
	return nil
}

func appendConfigAudit(ctx context.Context, tx *gorm.DB, namespace, key string, id int64) error {
	for _, op := range []string{"system.option.update", biz.ConfigWriteOperation(namespace, key)} {
		if err := authzquery.AppendWriteAudit(ctx, tx, op, id); err != nil {
			return err
		}
		if op == biz.ConfigWriteOperation(namespace, key) {
			break
		}
	}
	return nil
}

func recordConfigFailure(ctx context.Context, db *gorm.DB, namespace, key string, id int64, err error) error {
	if err == nil {
		return nil
	}
	err = authzquery.RecordWriteFailure(ctx, db, "system.option.update", id, err)
	op := biz.ConfigWriteOperation(namespace, key)
	if op != "system.option.update" {
		err = authzquery.RecordWriteFailure(ctx, db, op, id, err)
	}
	return err
}

// NewRepositoryWithDB uses a shared caller-owned storage client.
func NewRepositoryWithDB(db *gorm.DB) biz.ConfigRepo { return &Repository{db: db} }
