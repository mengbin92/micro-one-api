package biz

import (
	"context"
	"errors"
	"time"
)

var (
	ErrLogNotFound = errors.New("log entry not found")
)

// LogEntry represents a centralized log record.
type LogEntry struct {
	ID        int64
	Level     string // info, warn, error, debug
	Message   string
	Source    string // service name or component
	RequestID string
	UserID    int64
	CreatedAt time.Time

	Username         string
	TokenName        string
	ModelName        string
	Quota            int64
	PromptTokens     int64
	CompletionTokens int64
	CacheReadTokens  int64
	ChannelID        int64
	ElapsedTime      int64
	IsStream         bool
}

// UsageStat is a One API-style usage aggregate grouped by day and model.
type UsageStat struct {
	Day              string `json:"day"`
	ModelName        string `json:"model_name"`
	RequestCount     int64  `json:"request_count"`
	Quota            int64  `json:"quota"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
}

type DeleteLogsFilter struct {
	Level     string
	Source    string
	UserID    int64
	StartTime time.Time
	EndTime   time.Time
}

// LogRepo is the repository interface for log persistence.
type LogRepo interface {
	Get(ctx context.Context, id int64) (*LogEntry, error)
	List(ctx context.Context, page, pageSize int32, level, source, keyword string) ([]*LogEntry, int64, error)
	ListByUser(ctx context.Context, userID int64, page, pageSize int32, level, keyword string) ([]*LogEntry, int64, error)
	UsageByUser(ctx context.Context, userID int64, startTime, endTime time.Time) ([]*UsageStat, error)
	Create(ctx context.Context, entry *LogEntry) error
	Delete(ctx context.Context, filter DeleteLogsFilter) (int64, error)
	DeleteBefore(ctx context.Context, before time.Time) (int64, error)
}

// LogUsecase implements business logic for log-service.
type LogUsecase struct {
	repo LogRepo
}

func NewLogUsecase(repo LogRepo) *LogUsecase {
	return &LogUsecase{repo: repo}
}

func (uc *LogUsecase) GetLog(ctx context.Context, id int64) (*LogEntry, error) {
	return uc.repo.Get(ctx, id)
}

func (uc *LogUsecase) ListLogs(ctx context.Context, page, pageSize int32, level, source, keyword string) ([]*LogEntry, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	return uc.repo.List(ctx, page, pageSize, level, source, keyword)
}

func (uc *LogUsecase) ListUserLogs(ctx context.Context, userID int64, page, pageSize int32, level, keyword string) ([]*LogEntry, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	return uc.repo.ListByUser(ctx, userID, page, pageSize, level, keyword)
}

func (uc *LogUsecase) UserUsageStats(ctx context.Context, userID int64, startTime, endTime time.Time) ([]*UsageStat, error) {
	return uc.repo.UsageByUser(ctx, userID, startTime, endTime)
}

func (uc *LogUsecase) IngestLog(ctx context.Context, entry *LogEntry) error {
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}
	return uc.repo.Create(ctx, entry)
}

func (uc *LogUsecase) CleanupExpiredLogs(ctx context.Context, retentionDays int, now time.Time) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := now.Add(-time.Duration(retentionDays) * 24 * time.Hour)
	return uc.repo.DeleteBefore(ctx, cutoff)
}

func (uc *LogUsecase) DeleteLogs(ctx context.Context, filter DeleteLogsFilter) (int64, error) {
	if filter.EndTime.IsZero() {
		return 0, errors.New("end_time is required")
	}
	if !filter.StartTime.IsZero() && filter.StartTime.After(filter.EndTime) {
		return 0, errors.New("start_time must be before end_time")
	}
	return uc.repo.Delete(ctx, filter)
}
