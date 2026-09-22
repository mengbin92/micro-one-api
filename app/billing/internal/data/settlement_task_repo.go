package data

import (
	"context"
	"errors"
	"time"

	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/pkg/jsonx"

	"gorm.io/gorm"
)

type settlementTaskModel struct {
	ID            int64  `gorm:"column:id;primaryKey;autoIncrement"`
	ReservationID string `gorm:"column:reservation_id;uniqueIndex"`
	Payload       string `gorm:"column:payload;type:text"`
	Status        string `gorm:"column:status;index"`
	Attempts      int    `gorm:"column:attempts"`
	LastError     string `gorm:"column:last_error;type:text"`
	NextRetryAt   int64  `gorm:"column:next_retry_at;index"`
	CreatedAt     int64  `gorm:"column:created_at"`
	UpdatedAt     int64  `gorm:"column:updated_at"`
}

func (settlementTaskModel) TableName() string { return "billing_settlement_tasks" }

type settlementTaskRepo struct{ data *Data }

func NewSettlementTaskRepo(data *Data) biz.SettlementTaskStore {
	if data == nil || data.db == nil {
		return nil
	}
	return &settlementTaskRepo{data: data}
}

func (r *settlementTaskRepo) SaveTask(ctx context.Context, task *biz.SettleTask) error {
	if task == nil || task.ReservationID == "" {
		return errors.New("settlement task reservation_id is required")
	}
	payload, err := jsonx.Marshal(task)
	if err != nil {
		return err
	}
	var existing settlementTaskModel
	err = r.data.db.WithContext(ctx).Where("reservation_id = ?", task.ReservationID).First(&existing).Error
	now := time.Now().Unix()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r.data.db.WithContext(ctx).Create(&settlementTaskModel{ReservationID: task.ReservationID, Payload: string(payload), Status: "pending", NextRetryAt: now, CreatedAt: now, UpdatedAt: now}).Error
	}
	if err != nil {
		return err
	}
	if existing.Status == "completed" {
		return nil
	}
	return r.data.db.WithContext(ctx).Model(&existing).Updates(map[string]any{"payload": string(payload), "status": "pending", "next_retry_at": now, "updated_at": now}).Error
}

func (r *settlementTaskRepo) ListPending(ctx context.Context, limit int) ([]*biz.SettleTask, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	var rows []settlementTaskModel
	now := time.Now().Unix()
	err := r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("(status IN ? AND next_retry_at <= ?) OR (status = ? AND updated_at < ?)", []string{"pending", "failed"}, now, "processing", now-300).Order("id ASC").Limit(limit).Find(&rows).Error; err != nil {
			return err
		}
		for i := range rows {
			result := tx.Model(&settlementTaskModel{}).Where("id = ? AND (status IN ? OR (status = ? AND updated_at < ?))", rows[i].ID, []string{"pending", "failed"}, "processing", now-300).Updates(map[string]any{"status": "processing", "updated_at": now})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				rows[i].Payload = ""
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result := make([]*biz.SettleTask, 0, len(rows))
	for _, row := range rows {
		if row.Payload == "" {
			continue
		}
		var task biz.SettleTask
		if err := jsonx.Unmarshal([]byte(row.Payload), &task); err != nil {
			continue
		}
		result = append(result, &task)
	}
	return result, nil
}

func (r *settlementTaskRepo) MarkCompleted(ctx context.Context, reservationID string) error {
	return r.data.db.WithContext(ctx).Model(&settlementTaskModel{}).Where("reservation_id = ? AND status = ?", reservationID, "processing").Updates(map[string]any{"status": "completed", "last_error": "", "updated_at": time.Now().Unix()}).Error
}

func (r *settlementTaskRepo) MarkFailed(ctx context.Context, reservationID, message string, nextRetry time.Time) error {
	return r.data.db.WithContext(ctx).Model(&settlementTaskModel{}).Where("reservation_id = ? AND status = ?", reservationID, "processing").Updates(map[string]any{"status": "failed", "last_error": message, "next_retry_at": nextRetry.Unix(), "attempts": gorm.Expr("attempts + 1"), "updated_at": time.Now().Unix()}).Error
}
