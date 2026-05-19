package data

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"micro-one-api/internal/billing/biz"

	"gorm.io/gorm"
)

type reconciliationRunModel struct {
	ID                int64  `gorm:"column:id;primaryKey;autoIncrement"`
	RunAt             int64  `gorm:"column:run_at"`
	ExpiredCleaned    int    `gorm:"column:expired_cleaned"`
	TotalAccounts     int    `gorm:"column:total_accounts"`
	TotalReservations int    `gorm:"column:total_reservations"`
	DiscrepancyCount  int    `gorm:"column:discrepancy_count"`
	Discrepancies     string `gorm:"column:discrepancies"`
	CreatedAt         int64  `gorm:"column:created_at"`
}

func (reconciliationRunModel) TableName() string { return "reconciliation_runs" }

type reconciliationRunRepo struct {
	data *Data
}

// NewReconciliationRunRepo persists reconciliation runs to the reconciliation_runs table.
// Returns nil when no database is configured (memory mode), which the usecase treats as "do not persist".
func NewReconciliationRunRepo(data *Data) biz.ReconciliationRunStore {
	if data == nil || data.db == nil {
		return nil
	}
	return &reconciliationRunRepo{data: data}
}

func (r *reconciliationRunRepo) SaveRun(ctx context.Context, result *biz.ReconciliationResult) (int64, error) {
	if result == nil {
		return 0, errors.New("nil reconciliation result")
	}
	discrepancyJSON := "[]"
	if len(result.AccountInconsistencies) > 0 {
		buf, err := json.Marshal(result.AccountInconsistencies)
		if err != nil {
			return 0, err
		}
		discrepancyJSON = string(buf)
	}
	now := time.Now().Unix()
	model := &reconciliationRunModel{
		RunAt:             result.RunAt.Unix(),
		ExpiredCleaned:    result.ExpiredCleaned,
		TotalAccounts:     result.TotalAccounts,
		TotalReservations: result.TotalReservations,
		DiscrepancyCount:  len(result.AccountInconsistencies),
		Discrepancies:     discrepancyJSON,
		CreatedAt:         now,
	}
	if err := r.data.db.WithContext(ctx).Create(model).Error; err != nil {
		return 0, err
	}
	return model.ID, nil
}

func (r *reconciliationRunRepo) ListRuns(ctx context.Context, page, pageSize int32) ([]*biz.ReconciliationResult, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 50
	}
	var total int64
	if err := r.data.db.WithContext(ctx).Model(&reconciliationRunModel{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var models []reconciliationRunModel
	if err := r.data.db.WithContext(ctx).
		Order("run_at DESC").
		Limit(int(pageSize)).
		Offset(int((page - 1) * pageSize)).
		Find(&models).Error; err != nil {
		return nil, 0, err
	}
	out := make([]*biz.ReconciliationResult, len(models))
	for i, m := range models {
		out[i] = modelToReconciliationResult(&m)
	}
	return out, total, nil
}

func (r *reconciliationRunRepo) GetRun(ctx context.Context, runID int64) (*biz.ReconciliationResult, error) {
	var m reconciliationRunModel
	if err := r.data.db.WithContext(ctx).Where("id = ?", runID).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return modelToReconciliationResult(&m), nil
}

func modelToReconciliationResult(m *reconciliationRunModel) *biz.ReconciliationResult {
	result := &biz.ReconciliationResult{
		RunID:             m.ID,
		RunAt:             time.Unix(m.RunAt, 0),
		ExpiredCleaned:    m.ExpiredCleaned,
		TotalAccounts:     m.TotalAccounts,
		TotalReservations: m.TotalReservations,
	}
	if m.Discrepancies != "" && m.Discrepancies != "[]" {
		var rows []biz.AccountInconsistency
		if err := json.Unmarshal([]byte(m.Discrepancies), &rows); err == nil {
			result.AccountInconsistencies = rows
		}
	}
	return result
}
