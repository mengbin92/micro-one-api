package data

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"micro-one-api/app/notify/internal/biz"
	"micro-one-api/platform/database/xdb"

	"gorm.io/gorm"
)

type Repository struct {
	db  *gorm.DB
	mu  sync.RWMutex
	mem map[int64]*biz.Notification
	seq int64
}

type notificationModel struct {
	ID           int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Type         string `gorm:"column:type;index"`
	Recipient    string `gorm:"column:recipient"`
	Subject      string `gorm:"column:subject"`
	Content      string `gorm:"column:content"`
	Status       string `gorm:"column:status;index"`
	RetryCount   int    `gorm:"column:retry_count"`
	LastError    string `gorm:"column:last_error"`
	ProcessingAt int64  `gorm:"column:processing_at"`
	CreatedAt    int64  `gorm:"column:created_at;index"`
	SentAt       int64  `gorm:"column:sent_at"`
}

func (notificationModel) TableName() string { return "notifications" }

const processingLease = 10 * time.Minute

func NewRepositoryFromEnv(driver string, dsn ...string) (*Repository, error) {
	var dbDSN string
	if len(dsn) > 0 && dsn[0] != "" {
		dbDSN = dsn[0]
	} else {
		dbDSN = os.Getenv("NOTIFY_SQL_DSN")
		if dbDSN == "" {
			dbDSN = os.Getenv("SQL_DSN")
		}
	}
	if dbDSN == "" {
		return newMemoryRepository(), nil
	}
	// Schema isolation (Phase 2.4): effective schema comes from the wire
	// argument, the per-service env var, or the global DATABASE_SCHEMA fallback.
	schema := xdb.ResolveSchema("", "NOTIFY_SCHEMA", "DATABASE_SCHEMA")
	if len(dsn) > 1 && dsn[1] != "" {
		schema = dsn[1]
	}
	db, err := xdb.Open(xdb.DatabaseConfig{Driver: xdb.NormalizeDriver(driver, dbDSN), DSN: dbDSN, Schema: schema})
	if err != nil {
		return nil, err
	}
	return &Repository{db: db}, nil
}

func newMemoryRepository() *Repository {
	return &Repository{
		mem: map[int64]*biz.Notification{
			1: {
				ID:         1,
				Type:       biz.NotifyTypeWebhook,
				Recipient:  "https://example.com/webhook",
				Subject:    "test",
				Content:    "notification system ready",
				Status:     biz.NotifyStatusSent,
				RetryCount: 0,
				CreatedAt:  time.Now(),
				SentAt:     time.Now(),
			},
		},
		seq: 1,
	}
}

func (r *Repository) Create(ctx context.Context, n *biz.Notification) error {
	if r.db != nil {
		return r.createDB(ctx, n)
	}
	return r.createMemory(n)
}

func (r *Repository) Get(ctx context.Context, id int64) (*biz.Notification, error) {
	if r.db != nil {
		return r.getDB(ctx, id)
	}
	return r.getMemory(id)
}

func (r *Repository) List(ctx context.Context, page, pageSize int32, notifyType, status string) ([]*biz.Notification, int64, error) {
	if r.db != nil {
		return r.listDB(ctx, page, pageSize, notifyType, status)
	}
	return r.listMemory(page, pageSize, notifyType, status)
}

func (r *Repository) ListPending(ctx context.Context, limit int32, maxRetry int) ([]*biz.Notification, error) {
	if r.db != nil {
		return r.listPendingDB(ctx, limit, maxRetry)
	}
	return r.listPendingMemory(limit, maxRetry), nil
}

// ClaimPending atomically changes pending rows to processing before returning
// them. A second worker observing the same row gets zero affected rows and
// therefore cannot send it concurrently.
func (r *Repository) ClaimPending(ctx context.Context, limit int32, maxRetry int) ([]*biz.Notification, error) {
	if limit < 1 {
		limit = 20
	}
	if maxRetry < 1 {
		maxRetry = 3
	}
	if r.db == nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		items := make([]*biz.Notification, 0, limit)
		for _, n := range r.mem {
			if n.Status != biz.NotifyStatusPending || n.RetryCount >= maxRetry || len(items) >= int(limit) {
				continue
			}
			n.Status = biz.NotifyStatusProcessing
			n.ProcessingAt = time.Now()
			cloned := *n
			items = append(items, &cloned)
		}
		return items, nil
	}
	var candidates []notificationModel
	if err := r.db.WithContext(ctx).Where("status = ? AND retry_count < ?", biz.NotifyStatusPending, maxRetry).Order("id ASC").Limit(int(limit)).Find(&candidates).Error; err != nil {
		return nil, err
	}
	items := make([]*biz.Notification, 0, len(candidates))
	for _, m := range candidates {
		res := r.db.WithContext(ctx).Model(&notificationModel{}).Where("id = ? AND status = ?", m.ID, biz.NotifyStatusPending).Updates(map[string]any{"status": biz.NotifyStatusProcessing, "processing_at": time.Now().Unix()})
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected != 1 {
			continue
		}
		m.Status = biz.NotifyStatusProcessing
		m.ProcessingAt = time.Now().Unix()
		items = append(items, notificationFromModel(m))
	}
	return items, nil
}

// RecoverProcessing makes rows claimed by a worker that exited before send
// visible again after restart. External delivery may have succeeded before the
// crash, so senders must tolerate a bounded duplicate.
func (r *Repository) RecoverProcessing(ctx context.Context) error {
	if r.db != nil {
		cutoff := time.Now().Add(-processingLease).Unix()
		return r.db.WithContext(ctx).Model(&notificationModel{}).Where("status = ? AND (processing_at = 0 OR processing_at < ?)", biz.NotifyStatusProcessing, cutoff).Updates(map[string]any{"status": biz.NotifyStatusPending, "processing_at": 0}).Error
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range r.mem {
		if n.Status == biz.NotifyStatusProcessing && (n.ProcessingAt.IsZero() || n.ProcessingAt.Before(time.Now().Add(-processingLease))) {
			n.Status = biz.NotifyStatusPending
			n.ProcessingAt = time.Time{}
		}
	}
	return nil
}

func (r *Repository) UpdateStatus(ctx context.Context, id int64, status string) error {
	if r.db != nil {
		return r.updateStatusDB(ctx, id, status)
	}
	return r.updateStatusMemory(id, status)
}

func (r *Repository) MarkFailed(ctx context.Context, id int64) error {
	if r.db != nil {
		return r.markFailedDB(ctx, id)
	}
	return r.markFailedMemory(id)
}

func (r *Repository) RecordFailure(ctx context.Context, id int64, maxRetry int, lastError string) error {
	lastError = normalizeLastError(lastError)
	if r.db != nil {
		return r.recordFailureDB(ctx, id, maxRetry, lastError)
	}
	return r.recordFailureMemory(id, maxRetry, lastError)
}

// DB implementations

func (r *Repository) createDB(ctx context.Context, n *biz.Notification) error {
	m := notificationModel{
		Type:       n.Type,
		Recipient:  n.Recipient,
		Subject:    n.Subject,
		Content:    n.Content,
		Status:     n.Status,
		RetryCount: n.RetryCount,
		LastError:  n.LastError,
		CreatedAt:  n.CreatedAt.Unix(),
	}
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return err
	}
	n.ID = m.ID
	return nil
}

func (r *Repository) getDB(ctx context.Context, id int64) (*biz.Notification, error) {
	var m notificationModel
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrNotificationNotFound
		}
		return nil, err
	}
	return notificationFromModel(m), nil
}

func (r *Repository) listDB(ctx context.Context, page, pageSize int32, notifyType, status string) ([]*biz.Notification, int64, error) {
	query := r.db.WithContext(ctx).Model(&notificationModel{})
	if notifyType != "" {
		query = query.Where("type = ?", notifyType)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	var models []notificationModel
	if err := query.Offset(int(offset)).Limit(int(pageSize)).Order("id DESC").Find(&models).Error; err != nil {
		return nil, 0, err
	}
	entries := make([]*biz.Notification, len(models))
	for i, m := range models {
		entries[i] = notificationFromModel(m)
	}
	return entries, total, nil
}

func (r *Repository) listPendingDB(ctx context.Context, limit int32, maxRetry int) ([]*biz.Notification, error) {
	var models []notificationModel
	if err := r.db.WithContext(ctx).
		Where("status = ? AND retry_count < ?", biz.NotifyStatusPending, maxRetry).
		Order("id ASC").
		Limit(int(limit)).
		Find(&models).Error; err != nil {
		return nil, err
	}
	entries := make([]*biz.Notification, len(models))
	for i, m := range models {
		entries[i] = notificationFromModel(m)
	}
	return entries, nil
}

func (r *Repository) updateStatusDB(ctx context.Context, id int64, status string) error {
	updates := map[string]any{
		"status":        status,
		"processing_at": 0,
	}
	if status == biz.NotifyStatusSent {
		updates["sent_at"] = time.Now().Unix()
		updates["last_error"] = ""
		updates["processing_at"] = 0
	}
	return r.db.WithContext(ctx).Model(&notificationModel{}).Where("id = ?", id).Updates(updates).Error
}

func (r *Repository) markFailedDB(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Model(&notificationModel{}).Where("id = ?", id).Updates(map[string]any{
		"status":        biz.NotifyStatusFailed,
		"processing_at": 0,
	}).Error
}

func (r *Repository) recordFailureDB(ctx context.Context, id int64, maxRetry int, lastError string) error {
	return r.db.WithContext(ctx).Model(&notificationModel{}).Where("id = ?", id).Updates(map[string]any{
		"status":        gorm.Expr("CASE WHEN retry_count + 1 >= ? THEN ? ELSE ? END", maxRetry, biz.NotifyStatusFailed, biz.NotifyStatusPending),
		"retry_count":   gorm.Expr("retry_count + ?", 1),
		"last_error":    lastError,
		"processing_at": 0,
	}).Error
}

func (r *Repository) CompleteProcessing(ctx context.Context, id int64, processingAt time.Time, status string, maxRetry int, lastError string) error {
	if r.db == nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		n, ok := r.mem[id]
		if !ok {
			return biz.ErrNotificationNotFound
		}
		if n.Status != biz.NotifyStatusProcessing || !sameLease(n.ProcessingAt, processingAt) {
			return biz.ErrNotificationLeaseLost
		}
		if status == biz.NotifyStatusSent {
			n.Status = biz.NotifyStatusSent
			n.SentAt = time.Now()
			n.LastError = ""
		} else {
			n.RetryCount++
			if n.RetryCount >= maxRetry {
				n.Status = biz.NotifyStatusFailed
			} else {
				n.Status = biz.NotifyStatusPending
			}
			n.LastError = normalizeLastError(lastError)
		}
		n.ProcessingAt = time.Time{}
		return nil
	}
	lease := processingAt.Unix()
	if status == biz.NotifyStatusSent {
		result := r.db.WithContext(ctx).Model(&notificationModel{}).
			Where("id = ? AND status = ? AND processing_at = ?", id, biz.NotifyStatusProcessing, lease).
			Updates(map[string]any{"status": biz.NotifyStatusSent, "processing_at": 0, "sent_at": time.Now().Unix(), "last_error": ""})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return biz.ErrNotificationLeaseLost
		}
		return nil
	}
	result := r.db.WithContext(ctx).Model(&notificationModel{}).
		Where("id = ? AND status = ? AND processing_at = ?", id, biz.NotifyStatusProcessing, lease).
		Updates(map[string]any{
			"status":        gorm.Expr("CASE WHEN retry_count + 1 >= ? THEN ? ELSE ? END", maxRetry, biz.NotifyStatusFailed, biz.NotifyStatusPending),
			"retry_count":   gorm.Expr("retry_count + ?", 1),
			"last_error":    normalizeLastError(lastError),
			"processing_at": 0,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return biz.ErrNotificationLeaseLost
	}
	return nil
}

// Memory implementations

func (r *Repository) createMemory(n *biz.Notification) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	n.ID = r.seq
	r.mem[n.ID] = n
	return nil
}

func (r *Repository) getMemory(id int64) (*biz.Notification, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.mem[id]
	if !ok {
		return nil, biz.ErrNotificationNotFound
	}
	cloned := *n
	return &cloned, nil
}

func (r *Repository) listMemory(page, pageSize int32, notifyType, status string) ([]*biz.Notification, int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var all []*biz.Notification
	for _, n := range r.mem {
		if notifyType != "" && n.Type != notifyType {
			continue
		}
		if status != "" && n.Status != status {
			continue
		}
		cloned := *n
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

func (r *Repository) listPendingMemory(limit int32, maxRetry int) []*biz.Notification {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]*biz.Notification, 0)
	for _, n := range r.mem {
		if n.Status != biz.NotifyStatusPending || n.RetryCount >= maxRetry {
			continue
		}
		cloned := *n
		items = append(items, &cloned)
		if len(items) >= int(limit) {
			break
		}
	}
	return items
}

func (r *Repository) updateStatusMemory(id int64, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.mem[id]
	if !ok {
		return biz.ErrNotificationNotFound
	}
	n.Status = status
	if status != biz.NotifyStatusProcessing {
		n.ProcessingAt = time.Time{}
	}
	if status == biz.NotifyStatusSent {
		n.SentAt = time.Now()
		n.LastError = ""
	}
	return nil
}

func notificationFromModel(m notificationModel) *biz.Notification {
	processingAt := time.Time{}
	if m.ProcessingAt > 0 {
		processingAt = time.Unix(m.ProcessingAt, 0)
	}
	sentAt := time.Time{}
	if m.SentAt > 0 {
		sentAt = time.Unix(m.SentAt, 0)
	}
	return &biz.Notification{ID: m.ID, Type: m.Type, Recipient: m.Recipient, Subject: m.Subject, Content: m.Content, Status: m.Status, RetryCount: m.RetryCount, LastError: m.LastError, ProcessingAt: processingAt, CreatedAt: time.Unix(m.CreatedAt, 0), SentAt: sentAt}
}

func sameLease(a, b time.Time) bool {
	return !a.IsZero() && !b.IsZero() && a.Unix() == b.Unix()
}

func (r *Repository) markFailedMemory(id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.mem[id]
	if !ok {
		return biz.ErrNotificationNotFound
	}
	n.Status = biz.NotifyStatusFailed
	n.ProcessingAt = time.Time{}
	return nil
}

func (r *Repository) recordFailureMemory(id int64, maxRetry int, lastError string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.mem[id]
	if !ok {
		return biz.ErrNotificationNotFound
	}
	n.RetryCount++
	n.ProcessingAt = time.Time{}
	if n.RetryCount >= maxRetry {
		n.Status = biz.NotifyStatusFailed
	} else {
		n.Status = biz.NotifyStatusPending
	}
	n.LastError = lastError
	return nil
}

func normalizeLastError(lastError string) string {
	runes := []rune(lastError)
	if len(runes) > 2048 {
		return string(runes[:2048])
	}
	return lastError
}
