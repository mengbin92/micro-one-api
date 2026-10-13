package data

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"micro-one-api/domain/execution"
	"micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
)

type executionOutboxModel struct {
	ID        string `gorm:"primaryKey"`
	Digest    string
	Payload   string
	Status    string `gorm:"index"`
	NextRetry int64  `gorm:"index"`
	CreatedAt int64
}

func (executionOutboxModel) TableName() string { return "execution_outbox" }

type executionOutboxRepo struct {
	d    *Data
	path string
}

func NewExecutionData(path string, client biz.ExecutionSender) (*Data, func(), error) {
	if !filepath.IsAbs(path) {
		return nil, nil, fmt.Errorf("outbox path must be absolute")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, nil, err
	}
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return nil, nil, fmt.Errorf("outbox must be a regular file")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, nil, err
	}
	_ = f.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return nil, nil, err
	}
	dsn := url.URL{Scheme: "file", Path: path}
	q := url.Values{"_journal_mode": {"WAL"}, "_synchronous": {"FULL"}, "_busy_timeout": {"5000"}}
	dsn.RawQuery = q.Encode()
	db, err := gorm.Open(sqlite.Open(dsn.String()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, nil, err
	}
	sql, err := db.DB()
	if err != nil {
		return nil, nil, err
	}
	sql.SetMaxOpenConns(1)
	cleanup := func() { _ = sql.Close() }
	for _, query := range []string{"PRAGMA max_page_count=32768", "PRAGMA journal_size_limit=1048576", "PRAGMA wal_autocheckpoint=256"} {
		if err = db.Exec(query).Error; err != nil {
			cleanup()
			return nil, nil, err
		}
	}
	if err = db.AutoMigrate(&executionOutboxModel{}); err != nil {
		cleanup()
		return nil, nil, err
	}
	return &Data{ExecutionDB: db, ExecutionSender: client}, cleanup, nil
}
func NewExecutionOutboxRepo(d *Data, path string) biz.ExecutionOutboxRepo {
	return &executionOutboxRepo{d: d, path: path}
}
func (r *executionOutboxRepo) Put(ctx context.Context, e execution.Event) error {
	return r.put(ctx, e, false)
}
func (r *executionOutboxRepo) Admit(ctx context.Context, e execution.Event) error {
	return r.put(ctx, e, true)
}
func (r *executionOutboxRepo) put(ctx context.Context, e execution.Event, admit bool) error {
	b, d, err := e.Encode()
	if err != nil {
		return err
	}
	return r.d.ExecutionDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old executionOutboxModel
		if err := tx.Where("id = ?", e.ID).Limit(1).Find(&old).Error; err != nil {
			return err
		}
		if old.ID != "" {
			if admit {
				return biz.ErrExecutionAlreadyAdmitted
			}
			if old.Digest != d {
				return fmt.Errorf("outbox payload conflict")
			}
			return nil
		}
		if err := tx.Where("status = ? AND created_at < ?", "acknowledged", time.Now().Add(-7*24*time.Hour).Unix()).Delete(&executionOutboxModel{}).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&executionOutboxModel{}).Where("status = ?", "pending").Count(&count).Error; err != nil {
			return err
		}
		var bytes int64
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if info, err := os.Stat(r.path + suffix); err == nil {
				bytes += info.Size()
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if count >= 10000 || bytes > 120<<20 {
			return fmt.Errorf("outbox capacity exhausted")
		}
		return tx.Create(&executionOutboxModel{ID: e.ID, Digest: d, Payload: string(b), Status: "pending", CreatedAt: time.Now().Unix()}).Error
	})
}
func (r *executionOutboxRepo) Pending(ctx context.Context, limit int) ([]biz.PendingExecution, error) {
	var rows []executionOutboxModel
	if err := r.d.ExecutionDB.WithContext(ctx).Where("status = ? AND next_retry <= ?", "pending", time.Now().Unix()).Order("created_at ASC, id ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]biz.PendingExecution, 0, len(rows))
	for _, row := range rows {
		var e execution.Event
		if jsonx.Unmarshal([]byte(row.Payload), &e) != nil {
			return nil, biz.ErrExecutionReceipt
		}
		_, d, err := e.Encode()
		if err != nil || d != row.Digest {
			return nil, biz.ErrExecutionReceipt
		}
		out = append(out, biz.PendingExecution{Event: e, Digest: d})
	}
	return out, nil
}
func (r *executionOutboxRepo) Acknowledge(ctx context.Context, id, d string) error {
	return r.d.ExecutionDB.WithContext(ctx).Model(&executionOutboxModel{}).Where("id = ? AND digest = ?", id, d).Updates(map[string]any{"status": "acknowledged", "payload": ""}).Error
}
func (r *executionOutboxRepo) Defer(ctx context.Context, id string, t time.Time) error {
	return r.d.ExecutionDB.WithContext(ctx).Model(&executionOutboxModel{}).Where("id = ? AND status = ?", id, "pending").Update("next_retry", t.Unix()).Error
}
func (r *executionOutboxRepo) Send(ctx context.Context, e execution.Event) (execution.Receipt, error) {
	if r.d.ExecutionSender == nil {
		return execution.Receipt{}, biz.ErrExecutionReceipt
	}
	return r.d.ExecutionSender.Send(ctx, e)
}

func (r *executionOutboxRepo) Stats(ctx context.Context) (int64, int64, error) {
	var row struct {
		Count  int64
		Oldest int64
	}
	err := r.d.ExecutionDB.WithContext(ctx).Model(&executionOutboxModel{}).Where("status = ?", "pending").Select("COUNT(*) AS count, COALESCE(MIN(created_at),0) AS oldest").Scan(&row).Error
	return row.Count, row.Oldest, err
}
