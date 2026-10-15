package data

import (
	"context"
	"time"

	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/pkg/jsonx"

	"gorm.io/gorm"
	"micro-one-api/platform/database/authzquery"
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

func (r *settlementTaskRepo) ReceiptsAvailable(ctx context.Context) bool {
	db := r.data.db.WithContext(ctx)
	return db.Migrator().HasTable(&settlementTaskModel{}) && db.Migrator().HasTable(&reservationModel{})
}

func (r *settlementTaskRepo) SaveTask(ctx context.Context, task *biz.SettleTask) error {
	receipt, err := r.AcceptTask(ctx, task)
	if err != nil {
		return err
	}
	if !receipt.SettlementAccepted {
		return biz.ErrSettlementReservationClosed
	}
	return nil
}

func (r *settlementTaskRepo) AcceptTask(ctx context.Context, task *biz.SettleTask) (*biz.SettlementReceipt, error) {
	if err := task.ValidateReceipt(); err != nil {
		return nil, err
	}
	digest, err := task.PayloadDigest()
	if err != nil {
		return nil, biz.ErrSettlementReceiptInvalid
	}
	accepted := *task
	accepted.AcceptedPayloadDigest = digest
	payload, err := jsonx.Marshal(&accepted)
	if err != nil {
		return nil, biz.ErrSettlementReceiptInvalid
	}
	receipt := &biz.SettlementReceipt{Version: biz.SettlementReceiptVersionV1, EventID: task.ReservationID + "/final/0", PayloadDigest: digest}
	err = authzquery.RunInTx(ctx, r.data.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		// Intake and expiry/commit serialize on the same reservation row.
		reservation, err := (&reservationRepo{data: r.data}).GetReservationInTx(ctx, &gormTx{db: tx}, task.ReservationID)
		if err != nil {
			return err
		}
		var existing settlementTaskModel
		q := tx.WithContext(ctx).Where("reservation_id = ?", task.ReservationID)
		if !isSQLite(dialectorName(tx)) {
			q = q.Clauses(forUpdateClause(dialectorName(tx)))
		}
		if err := q.Limit(1).Find(&existing).Error; err != nil {
			return err
		}
		receipt.Duplicate, receipt.Received = existing.ID != 0, existing.ID != 0
		if existing.ID != 0 {
			var saved biz.SettleTask
			if jsonx.Unmarshal([]byte(existing.Payload), &saved) != nil || saved.ReservationID != existing.ReservationID || saved.ValidateReceipt() != nil {
				return biz.ErrSettlementReceiptInvalid
			}
			acceptedDigest, err := saved.PayloadDigest()
			if err != nil {
				return biz.ErrSettlementReceiptInvalid
			}
			if acceptedDigest != digest {
				return biz.ErrSettlementPayloadConflict
			}
		}
		switch reservation.Status {
		case biz.ReservationStatusReleased, biz.ReservationStatusExpired:
			receipt.Status = reservation.Status
			receipt.SettlementAccepted, receipt.Settled = false, false
			return nil
		case biz.ReservationStatusCommitted:
			if existing.ID == 0 {
				return biz.ErrSettlementReservationClosed
			}
			receipt.Status, receipt.SettlementAccepted, receipt.Settled = "settled", true, true
			receipt.CommittedAmount = reservation.ActualCost
			return nil
		case biz.ReservationStatusReserved:
		default:
			return biz.ErrSettlementReservationClosed
		}
		receipt.Status, receipt.SettlementAccepted, receipt.Settled = "accepted", true, false
		if existing.ID != 0 {
			switch existing.Status {
			case "processing":
				receipt.Status = "processing"
			case "pending", "failed":
			default:
				return biz.ErrSettlementReceiptInvalid
			}
			return nil // A replay never rewrites inputs, the first clock, or a worker claim.
		}
		now := time.Now().Unix()
		if err := tx.WithContext(ctx).Create(&settlementTaskModel{ReservationID: task.ReservationID, Payload: string(payload), Status: "pending", NextRetryAt: now, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		receipt.Received = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return receipt, nil
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
			result := tx.Model(&settlementTaskModel{}).Where("id = ? AND ((status IN ? AND next_retry_at <= ?) OR (status = ? AND updated_at < ?))", rows[i].ID, []string{"pending", "failed"}, now, "processing", now-300).Updates(map[string]any{"status": "processing", "updated_at": now})
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
		if jsonx.Unmarshal([]byte(row.Payload), &task) != nil || task.ReservationID != row.ReservationID || task.ValidateReceipt() != nil {
			if err := r.data.db.WithContext(ctx).Model(&settlementTaskModel{}).Where("id = ? AND status = ?", row.ID, "processing").Updates(map[string]any{"status": "invalid", "last_error": "stored settlement payload failed validation", "updated_at": time.Now().Unix()}).Error; err != nil {
				return nil, err
			}
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
