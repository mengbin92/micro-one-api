package data

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
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
	ID                    int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Level                 string `gorm:"column:level;index"`
	Message               string `gorm:"column:message"`
	Source                string `gorm:"column:source;index"`
	RequestID             string `gorm:"column:request_id"`
	UserID                int64  `gorm:"column:user_id"`
	CreatedAt             int64  `gorm:"column:created_at;index"`
	Username              string `gorm:"column:username"`
	TokenName             string `gorm:"column:token_name"`
	ModelName             string `gorm:"column:model_name;index"`
	Quota                 int64  `gorm:"column:quota"`
	PromptTokens          int64  `gorm:"column:prompt_tokens"`
	CompletionTokens      int64  `gorm:"column:completion_tokens"`
	CacheReadTokens       int64  `gorm:"column:cache_read_tokens"`
	ChannelID             int64  `gorm:"column:channel_id"`
	SubscriptionAccountID int64  `gorm:"column:subscription_account_id"`
	ElapsedTime           int64  `gorm:"column:elapsed_time"`
	IsStream              bool   `gorm:"column:is_stream"`
}

func (logModel) TableName() string { return "logs" }

func NewRepositoryFromEnv(driver string, dsn ...string) (*Repository, error) {
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
		if allowMemoryRepository() {
			return newMemoryRepository(), nil
		}
		return nil, fmt.Errorf("log database DSN is required; set LOG_SQL_DSN/SQL_DSN or LOG_MEMORY_MODE=true for development")
	}
	db, err := xdb.Open(xdb.DatabaseConfig{Driver: xdb.NormalizeDriver(driver, dbDSN), DSN: dbDSN})
	if err != nil {
		return nil, err
	}
	return &Repository{db: db}, nil
}

func allowMemoryRepository() bool {
	allowed, _ := strconv.ParseBool(os.Getenv("LOG_MEMORY_MODE"))
	return allowed
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

// DB returns the underlying *gorm.DB used by the repository, or nil when the
// repository is backed by the in-memory store. It is exposed so the
// log-service can run periodic partition maintenance (REVIEW_v4 §六) without
// the data package depending on the db utilities.
func (r *Repository) DB() *gorm.DB {
	if r == nil {
		return nil
	}
	return r.db
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

func (r *Repository) Delete(ctx context.Context, filter biz.DeleteLogsFilter) (int64, error) {
	if r.db != nil {
		return r.deleteDB(ctx, filter)
	}
	return r.deleteMemory(filter), nil
}

func (r *Repository) DeleteBefore(ctx context.Context, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, nil
	}
	if r.db != nil {
		result := r.db.WithContext(ctx).Where("created_at < ?", before.Unix()).Delete(&logModel{})
		return result.RowsAffected, result.Error
	}
	return r.deleteBeforeMemory(before), nil
}

func (r *Repository) UsageByUser(ctx context.Context, userID int64, startTime, endTime time.Time) ([]*biz.UsageStat, error) {
	if r.db != nil {
		return r.usageByUserDB(ctx, userID, startTime, endTime)
	}
	return r.usageByUserMemory(userID, startTime, endTime), nil
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
		ID:                    m.ID,
		Level:                 m.Level,
		Message:               m.Message,
		Source:                m.Source,
		RequestID:             m.RequestID,
		UserID:                m.UserID,
		CreatedAt:             time.Unix(m.CreatedAt, 0),
		Username:              m.Username,
		TokenName:             m.TokenName,
		ModelName:             m.ModelName,
		Quota:                 m.Quota,
		PromptTokens:          m.PromptTokens,
		CompletionTokens:      m.CompletionTokens,
		CacheReadTokens:       m.CacheReadTokens,
		ChannelID:             m.ChannelID,
		SubscriptionAccountID: m.SubscriptionAccountID,
		ElapsedTime:           m.ElapsedTime,
		IsStream:              m.IsStream,
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
		entries[i] = logModelToEntry(m)
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
		entries[i] = logModelToEntry(m)
	}
	return entries, total, nil
}

func (r *Repository) createDB(ctx context.Context, entry *biz.LogEntry) error {
	m := logModel{
		Level:                 entry.Level,
		Message:               entry.Message,
		Source:                entry.Source,
		RequestID:             entry.RequestID,
		UserID:                entry.UserID,
		CreatedAt:             entry.CreatedAt.Unix(),
		Username:              entry.Username,
		TokenName:             entry.TokenName,
		ModelName:             entry.ModelName,
		Quota:                 entry.Quota,
		PromptTokens:          entry.PromptTokens,
		CompletionTokens:      entry.CompletionTokens,
		CacheReadTokens:       entry.CacheReadTokens,
		ChannelID:             entry.ChannelID,
		SubscriptionAccountID: entry.SubscriptionAccountID,
		ElapsedTime:           entry.ElapsedTime,
		IsStream:              entry.IsStream,
	}
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return err
	}
	entry.ID = m.ID
	return nil
}

func (r *Repository) deleteDB(ctx context.Context, filter biz.DeleteLogsFilter) (int64, error) {
	query := r.db.WithContext(ctx).Where("created_at <= ?", filter.EndTime.Unix())
	if !filter.StartTime.IsZero() {
		query = query.Where("created_at >= ?", filter.StartTime.Unix())
	}
	if filter.Level != "" {
		query = query.Where("level = ?", filter.Level)
	}
	if filter.Source != "" {
		query = query.Where("source = ?", filter.Source)
	}
	if filter.UserID != 0 {
		query = query.Where("user_id = ?", filter.UserID)
	}
	result := query.Delete(&logModel{})
	return result.RowsAffected, result.Error
}

func (r *Repository) usageByUserDB(ctx context.Context, userID int64, startTime, endTime time.Time) ([]*biz.UsageStat, error) {
	// logs.created_at stores Unix epoch seconds on every supported dialect, so
	// we always cast from an integer epoch and only the formatting function
	// is dialect-specific.
	dayExpr := "FROM_UNIXTIME(created_at, '%Y-%m-%d')"
	if r.db != nil && r.db.Dialector != nil {
		switch r.db.Dialector.Name() {
		case "sqlite":
			dayExpr = "strftime('%Y-%m-%d', created_at, 'unixepoch')"
		case "postgres":
			dayExpr = "to_char(to_timestamp(created_at), 'YYYY-MM-DD')"
		}
	}
	query := r.db.WithContext(ctx).Table("logs").
		Select(dayExpr+" AS day, model_name, COUNT(1) AS request_count, COALESCE(SUM(quota), 0) AS quota, COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, COALESCE(SUM(completion_tokens), 0) AS completion_tokens, COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens").
		Where("user_id = ? AND model_name <> ''", userID)
	if !startTime.IsZero() {
		query = query.Where("created_at >= ?", startTime.Unix())
	}
	if !endTime.IsZero() {
		query = query.Where("created_at <= ?", endTime.Unix())
	}
	var stats []*biz.UsageStat
	if err := query.Group("day, model_name").Order("day ASC, model_name ASC").Scan(&stats).Error; err != nil {
		return nil, err
	}
	return stats, nil
}

func logModelToEntry(m logModel) *biz.LogEntry {
	return &biz.LogEntry{
		ID:                    m.ID,
		Level:                 m.Level,
		Message:               m.Message,
		Source:                m.Source,
		RequestID:             m.RequestID,
		UserID:                m.UserID,
		CreatedAt:             time.Unix(m.CreatedAt, 0),
		Username:              m.Username,
		TokenName:             m.TokenName,
		ModelName:             m.ModelName,
		Quota:                 m.Quota,
		PromptTokens:          m.PromptTokens,
		CompletionTokens:      m.CompletionTokens,
		CacheReadTokens:       m.CacheReadTokens,
		ChannelID:             m.ChannelID,
		SubscriptionAccountID: m.SubscriptionAccountID,
		ElapsedTime:           m.ElapsedTime,
		IsStream:              m.IsStream,
	}
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

func (r *Repository) deleteMemory(filter biz.DeleteLogsFilter) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	var deleted int64
	for id, entry := range r.mem {
		if filter.Level != "" && entry.Level != filter.Level {
			continue
		}
		if filter.Source != "" && entry.Source != filter.Source {
			continue
		}
		if filter.UserID != 0 && entry.UserID != filter.UserID {
			continue
		}
		if !filter.StartTime.IsZero() && entry.CreatedAt.Before(filter.StartTime) {
			continue
		}
		if entry.CreatedAt.After(filter.EndTime) {
			continue
		}
		delete(r.mem, id)
		deleted++
	}
	return deleted
}

func (r *Repository) deleteBeforeMemory(before time.Time) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	var deleted int64
	for id, entry := range r.mem {
		if entry.CreatedAt.Before(before) {
			delete(r.mem, id)
			deleted++
		}
	}
	return deleted
}

func (r *Repository) usageByUserMemory(userID int64, startTime, endTime time.Time) []*biz.UsageStat {
	r.mu.RLock()
	defer r.mu.RUnlock()
	statsByKey := map[string]*biz.UsageStat{}
	for _, entry := range r.mem {
		if entry.UserID != userID || entry.ModelName == "" {
			continue
		}
		if !startTime.IsZero() && entry.CreatedAt.Before(startTime) {
			continue
		}
		if !endTime.IsZero() && entry.CreatedAt.After(endTime) {
			continue
		}
		day := entry.CreatedAt.UTC().Format("2006-01-02")
		key := day + "\x00" + entry.ModelName
		stat := statsByKey[key]
		if stat == nil {
			stat = &biz.UsageStat{Day: day, ModelName: entry.ModelName}
			statsByKey[key] = stat
		}
		stat.RequestCount++
		stat.Quota += entry.Quota
		stat.PromptTokens += entry.PromptTokens
		stat.CompletionTokens += entry.CompletionTokens
		stat.CacheReadTokens += entry.CacheReadTokens
	}
	stats := make([]*biz.UsageStat, 0, len(statsByKey))
	for _, stat := range statsByKey {
		stats = append(stats, stat)
	}
	return stats
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
