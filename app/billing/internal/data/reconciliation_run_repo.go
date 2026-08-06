package data

import (
	"context"
	"errors"
	"time"

	"micro-one-api/pkg/jsonx"

	"micro-one-api/app/billing/internal/biz"

	"gorm.io/gorm"
)

type reconciliationRunModel struct {
	ID                int64  `gorm:"column:id;primaryKey;autoIncrement"`
	RunAt             int64  `gorm:"column:run_at"`
	ExpiredCleaned    int    `gorm:"column:expired_cleaned"`
	TotalAccounts     int    `gorm:"column:total_accounts"`
	TotalChannels     int    `gorm:"column:total_channels"`
	TotalReservations int    `gorm:"column:total_reservations"`
	DiscrepancyCount  int    `gorm:"column:discrepancy_count"`
	Discrepancies     string `gorm:"column:discrepancies"`
	CreatedAt         int64  `gorm:"column:created_at"`
}

func (reconciliationRunModel) TableName() string { return "reconciliation_runs" }

type reconciliationDiscrepancyRecord struct {
	Type              string `json:"type,omitempty"`
	UserID            string `json:"user_id,omitempty"`
	ExpectedQuota     int64  `json:"expected_quota,omitempty"`
	ActualQuota       int64  `json:"actual_quota,omitempty"`
	LedgerNetAmount   int64  `json:"ledger_net_amount,omitempty"`
	FrozenQuota       int64  `json:"frozen_quota,omitempty"`
	ChannelID         int64  `json:"channel_id,omitempty"`
	ExpectedUsedQuota int64  `json:"expected_used_quota,omitempty"`
	ActualUsedQuota   int64  `json:"actual_used_quota,omitempty"`
	LedgerQuota       int64  `json:"ledger_quota,omitempty"`
	UpstreamCost      int64  `json:"upstream_cost,omitempty"`
	Difference        int64  `json:"difference,omitempty"`
	LedgerCount       int64  `json:"ledger_count,omitempty"`
	LogCount          int64  `json:"log_count,omitempty"`
	LogQuota          int64  `json:"log_quota,omitempty"`
	CountDiff         int64  `json:"count_diff,omitempty"`
	QuotaDiff         int64  `json:"quota_diff,omitempty"`
}

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
	records := reconciliationRecordsFromResult(result)
	if len(records) > 0 {
		buf, err := jsonx.Marshal(records)
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
		TotalChannels:     result.TotalChannels,
		TotalReservations: result.TotalReservations,
		DiscrepancyCount:  result.DiscrepancyCount(),
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
		TotalChannels:     m.TotalChannels,
		TotalReservations: m.TotalReservations,
	}
	if m.Discrepancies != "" && m.Discrepancies != "[]" {
		var records []reconciliationDiscrepancyRecord
		if err := jsonx.Unmarshal([]byte(m.Discrepancies), &records); err == nil {
			applyReconciliationRecords(result, records)
		}
	}
	return result
}

func reconciliationRecordsFromResult(result *biz.ReconciliationResult) []reconciliationDiscrepancyRecord {
	if result == nil {
		return nil
	}
	records := make([]reconciliationDiscrepancyRecord, 0, result.DiscrepancyCount())
	for _, d := range result.AccountInconsistencies {
		records = append(records, reconciliationDiscrepancyRecord{
			Type:            biz.ReconciliationDiscrepancyTypeAccount,
			UserID:          d.UserID,
			ExpectedQuota:   d.ExpectedQuota,
			ActualQuota:     d.ActualQuota,
			LedgerNetAmount: d.LedgerNetAmount,
			FrozenQuota:     d.FrozenQuota,
		})
	}
	for _, d := range result.ChannelInconsistencies {
		records = append(records, reconciliationDiscrepancyRecord{
			Type:              biz.ReconciliationDiscrepancyTypeChannel,
			ChannelID:         d.ChannelID,
			ExpectedUsedQuota: d.ExpectedUsedQuota,
			ActualUsedQuota:   d.ActualUsedQuota,
			LedgerQuota:       d.LedgerQuota,
			UpstreamCost:      d.UpstreamCost,
			Difference:        d.Difference,
		})
	}
	for _, d := range result.LogInconsistencies {
		records = append(records, reconciliationDiscrepancyRecord{
			Type:        biz.ReconciliationDiscrepancyTypeLog,
			LedgerCount: d.LedgerCount,
			LogCount:    d.LogCount,
			LedgerQuota: d.LedgerQuota,
			LogQuota:    d.LogQuota,
			CountDiff:   d.CountDiff,
			QuotaDiff:   d.QuotaDiff,
		})
	}
	return records
}

func applyReconciliationRecords(result *biz.ReconciliationResult, records []reconciliationDiscrepancyRecord) {
	for _, record := range records {
		switch record.Type {
		case "", biz.ReconciliationDiscrepancyTypeAccount:
			result.AccountInconsistencies = append(result.AccountInconsistencies, biz.AccountInconsistency{
				UserID:          record.UserID,
				ExpectedQuota:   record.ExpectedQuota,
				ActualQuota:     record.ActualQuota,
				LedgerNetAmount: record.LedgerNetAmount,
				FrozenQuota:     record.FrozenQuota,
			})
		case biz.ReconciliationDiscrepancyTypeChannel:
			result.ChannelInconsistencies = append(result.ChannelInconsistencies, biz.ChannelInconsistency{
				ChannelID:         record.ChannelID,
				ExpectedUsedQuota: record.ExpectedUsedQuota,
				ActualUsedQuota:   record.ActualUsedQuota,
				LedgerQuota:       record.LedgerQuota,
				UpstreamCost:      record.UpstreamCost,
				Difference:        record.Difference,
			})
		case biz.ReconciliationDiscrepancyTypeLog:
			result.LogInconsistencies = append(result.LogInconsistencies, biz.LogInconsistency{
				LedgerCount: record.LedgerCount,
				LogCount:    record.LogCount,
				LedgerQuota: record.LedgerQuota,
				LogQuota:    record.LogQuota,
				CountDiff:   record.CountDiff,
				QuotaDiff:   record.QuotaDiff,
			})
		}
	}
}
