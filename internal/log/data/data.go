package data

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"micro-one-api/internal/log/biz"
	"micro-one-api/internal/pkg/xdb"

	"gorm.io/gorm"
)

type Repository struct {
	db  *gorm.DB
	mu  sync.RWMutex
	mem map[int64]*biz.LogEntry
	seq int64
}

type logModel struct {
	ID        int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Level     string `gorm:"column:level;index"`
	Message   string `gorm:"column:message"`
	Source    string `gorm:"column:source;index"`
	RequestID string `gorm:"column:request_id"`
	UserID    int64  `gorm:"column:user_id"`
	CreatedAt int64  `gorm:"column:created_at;index"`
}

func (logModel) TableName() string { return "logs" }

func NewRepositoryFromEnv(dsn ...string) (*Repository, error) {
	var dbDSN string
	if len(dsn) > 0 && dsn[0] != "" {
		dbDSN = dsn[0]
	} else {
		dbDSN = os.Getenv("LOG_SQL_DSN")
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
	return &Repository{db: db}, nil
}

func newMemoryRepository() *Repository {
	return &Repository{
		mem: map[int64]*biz.LogEntry{
			1: {
				ID:        1,
				Level:     "info",
				Message:   "service started",
				Source:    "log-service",
				RequestID: "init-001",
				CreatedAt: time.Now(),
			},
		},
		seq: 1,
	}
}

func NewMemoryRepositoryForTest() *Repository {
	return &Repository{mem: map[int64]*biz.LogEntry{}}
}

func (r *Repository) Get(ctx context.Context, id int64) (*biz.LogEntry, error) {
	if r.db != nil {
		return r.getDB(ctx, id)
	}
	return r.getMemory(id)
}

func (r *Repository) List(ctx context.Context, page, pageSize int32, level, source, keyword string) ([]*biz.LogEntry, int64, error) {
	if r.db != nil {
		return r.listDB(ctx, page, pageSize, level, source, keyword)
	}
	return r.listMemory(page, pageSize, level, source, keyword)
}

func (r *Repository) ListByUser(ctx context.Context, userID int64, page, pageSize int32, level, keyword string) ([]*biz.LogEntry, int64, error) {
	if r.db != nil {
		return r.listByUserDB(ctx, userID, page, pageSize, level, keyword)
	}
	return r.listByUserMemory(userID, page, pageSize, level, keyword)
}

func (r *Repository) Create(ctx context.Context, entry *biz.LogEntry) error {
	if r.db != nil {
		return r.createDB(ctx, entry)
	}
	return r.createMemory(entry)
}

// DB implementations

func (r *Repository) getDB(ctx context.Context, id int64) (*biz.LogEntry, error) {
	var m logModel
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrLogNotFound
		}
		return nil, err
	}
	return &biz.LogEntry{
		ID:        m.ID,
		Level:     m.Level,
		Message:   m.Message,
		Source:    m.Source,
		RequestID: m.RequestID,
		UserID:    m.UserID,
		CreatedAt: time.Unix(m.CreatedAt, 0),
	}, nil
}

func (r *Repository) listDB(ctx context.Context, page, pageSize int32, level, source, keyword string) ([]*biz.LogEntry, int64, error) {
	query := r.db.WithContext(ctx).Model(&logModel{})
	if level != "" {
		query = query.Where("level = ?", level)
	}
	if source != "" {
		query = query.Where("source = ?", source)
	}
	if keyword != "" {
		query = query.Where("message LIKE ?", "%"+escapeLike(keyword)+"%")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	var models []logModel
	if err := query.Offset(int(offset)).Limit(int(pageSize)).Order("id DESC").Find(&models).Error; err != nil {
		return nil, 0, err
	}
	entries := make([]*biz.LogEntry, len(models))
	for i, m := range models {
		entries[i] = &biz.LogEntry{
			ID:        m.ID,
			Level:     m.Level,
			Message:   m.Message,
			Source:    m.Source,
			RequestID: m.RequestID,
			UserID:    m.UserID,
			CreatedAt: time.Unix(m.CreatedAt, 0),
		}
	}
	return entries, total, nil
}

func (r *Repository) listByUserDB(ctx context.Context, userID int64, page, pageSize int32, level, keyword string) ([]*biz.LogEntry, int64, error) {
	query := r.db.WithContext(ctx).Model(&logModel{}).Where("user_id = ?", userID)
	if level != "" {
		query = query.Where("level = ?", level)
	}
	if keyword != "" {
		query = query.Where("message LIKE ?", "%"+escapeLike(keyword)+"%")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	var models []logModel
	if err := query.Offset(int(offset)).Limit(int(pageSize)).Order("id DESC").Find(&models).Error; err != nil {
		return nil, 0, err
	}
	entries := make([]*biz.LogEntry, len(models))
	for i, m := range models {
		entries[i] = &biz.LogEntry{
			ID:        m.ID,
			Level:     m.Level,
			Message:   m.Message,
			Source:    m.Source,
			RequestID: m.RequestID,
			UserID:    m.UserID,
			CreatedAt: time.Unix(m.CreatedAt, 0),
		}
	}
	return entries, total, nil
}

func (r *Repository) createDB(ctx context.Context, entry *biz.LogEntry) error {
	m := logModel{
		Level:     entry.Level,
		Message:   entry.Message,
		Source:    entry.Source,
		RequestID: entry.RequestID,
		UserID:    entry.UserID,
		CreatedAt: entry.CreatedAt.Unix(),
	}
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return err
	}
	entry.ID = m.ID
	return nil
}

// Memory implementations

func (r *Repository) getMemory(id int64) (*biz.LogEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.mem[id]
	if !ok {
		return nil, biz.ErrLogNotFound
	}
	cloned := *entry
	return &cloned, nil
}

func (r *Repository) listMemory(page, pageSize int32, level, source, keyword string) ([]*biz.LogEntry, int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var all []*biz.LogEntry
	for _, entry := range r.mem {
		if level != "" && entry.Level != level {
			continue
		}
		if source != "" && entry.Source != source {
			continue
		}
		if keyword != "" && !contains(entry.Message, keyword) {
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

func (r *Repository) listByUserMemory(userID int64, page, pageSize int32, level, keyword string) ([]*biz.LogEntry, int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var all []*biz.LogEntry
	for _, entry := range r.mem {
		if entry.UserID != userID {
			continue
		}
		if level != "" && entry.Level != level {
			continue
		}
		if keyword != "" && !contains(entry.Message, keyword) {
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

func (r *Repository) createMemory(entry *biz.LogEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	entry.ID = r.seq
	r.mem[entry.ID] = entry
	return nil
}

func contains(s, substr string) bool {
	return len(substr) == 0 || (len(s) >= len(substr) && searchString(s, substr))
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}
