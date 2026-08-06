package data

import (
	"context"
	"errors"
	"strings"
	"time"

	"micro-one-api/app/billing/internal/biz"

	subscriptionbiz "micro-one-api/domain/subscription/biz"
)

type receivableRepo struct {
	data *Data
}

func NewReceivableRepo(data *Data) biz.ReceivableRepo {
	return &receivableRepo{data: data}
}

func (r *receivableRepo) CreateInTx(ctx context.Context, tx subscriptionbiz.Tx, recv *biz.AccountReceivable) error {
	db := txDB(tx)
	if recv == nil {
		return errors.New("nil receivable")
	}
	if recv.Status == "" {
		recv.Status = biz.ReceivableStatusPending
	}
	if recv.CreatedAt.IsZero() {
		recv.CreatedAt = time.Now()
	}
	recv.UpdatedAt = time.Now()
	model := &accountReceivableModel{
		UserID:        recv.UserID,
		ReservationID: recv.ReservationID,
		OverdueQuota:  recv.OverdueQuota,
		OverdueUSD:    recv.OverdueUSD,
		Status:        recv.Status,
		CreatedAt:     recv.CreatedAt,
		UpdatedAt:     recv.UpdatedAt,
		SettledAt:     recv.SettledAt,
		SettledQuota:  recv.SettledQuota,
		Remark:        stringPtr(recv.Remark),
	}
	if err := db.Create(model).Error; err != nil {
		if isUniqueViolation(err) {
			return biz.ErrReceivableDuplicate
		}
		return err
	}
	recv.ID = model.ID
	return nil
}

func (r *receivableRepo) ListPendingByUser(ctx context.Context, userID string) ([]*biz.AccountReceivable, error) {
	var models []accountReceivableModel
	if err := r.data.db.WithContext(ctx).
		Where("user_id = ? AND status = ?", userID, biz.ReceivableStatusPending).
		Order("created_at ASC, id ASC").
		Find(&models).Error; err != nil {
		return nil, err
	}
	out := make([]*biz.AccountReceivable, len(models))
	for i := range models {
		out[i] = receivableFromModel(&models[i])
	}
	return out, nil
}

func (r *receivableRepo) SettleOldestForUserInTx(ctx context.Context, tx subscriptionbiz.Tx, userID string, amount int64) (int64, error) {
	db := txDB(tx)
	if amount <= 0 {
		return 0, nil
	}
	// Pull the pending receivables oldest first and settle them in order
	// until the recharge amount is exhausted. We deliberately avoid a
	// single UPDATE with LIMIT because not all dialects support it.
	var models []accountReceivableModel
	if err := db.WithContext(ctx).
		Where("user_id = ? AND status = ?", userID, biz.ReceivableStatusPending).
		Order("created_at ASC, id ASC").
		Find(&models).Error; err != nil {
		return 0, err
	}
	remaining := amount
	settled := int64(0)
	now := time.Now()
	for i := range models {
		if remaining <= 0 {
			break
		}
		row := &models[i]
		outstanding := row.OverdueQuota - row.SettledQuota
		if outstanding <= 0 {
			continue
		}
		settleThis := outstanding
		if settleThis > remaining {
			settleThis = remaining
		}
		newSettled := row.SettledQuota + settleThis
		status := biz.ReceivableStatusPending
		var settledAt *time.Time
		if newSettled >= row.OverdueQuota {
			status = biz.ReceivableStatusSettled
			settledAt = &now
		}
		res := db.WithContext(ctx).Model(&accountReceivableModel{}).
			Where("id = ? AND status = ?", row.ID, biz.ReceivableStatusPending).
			Where("settled_quota = ?", row.SettledQuota).
			Updates(map[string]interface{}{
				"settled_quota": newSettled,
				"status":        status,
				"settled_at":    settledAt,
				"updated_at":    now,
			})
		if res.Error != nil {
			return settled, res.Error
		}
		if res.RowsAffected == 0 {
			// Row changed under us (concurrent settle). Skip it; the
			// next loop iteration will re-read whatever is still
			// pending.
			continue
		}
		settled += settleThis
		remaining -= settleThis
	}
	return settled, nil
}

func (r *receivableRepo) SumOverduePendingByUser(ctx context.Context, userID string) (int64, error) {
	var total int64
	if err := r.data.db.WithContext(ctx).Model(&accountReceivableModel{}).
		Where("user_id = ? AND status = ?", userID, biz.ReceivableStatusPending).
		Select("COALESCE(SUM(overdue_quota - settled_quota), 0)").
		Scan(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func receivableFromModel(model *accountReceivableModel) *biz.AccountReceivable {
	if model == nil {
		return nil
	}
	return &biz.AccountReceivable{
		ID:            model.ID,
		UserID:        model.UserID,
		ReservationID: model.ReservationID,
		OverdueQuota:  model.OverdueQuota,
		OverdueUSD:    model.OverdueUSD,
		Status:        model.Status,
		CreatedAt:     model.CreatedAt,
		UpdatedAt:     model.UpdatedAt,
		SettledAt:     model.SettledAt,
		SettledQuota:  model.SettledQuota,
		Remark:        stringFromPtr(model.Remark),
	}
}

// isUniqueViolation reports whether the database error is a unique-key
// collision. The detection is dialect-specific because GORM does not
// abstract this; it covers MySQL, Postgres, and SQLite.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "Duplicate entry"):
		// MySQL
		return true
	case strings.Contains(msg, "duplicate key value"):
		// Postgres
		return true
	case strings.Contains(msg, "UNIQUE constraint failed"):
		// SQLite
		return true
	}
	return false
}
