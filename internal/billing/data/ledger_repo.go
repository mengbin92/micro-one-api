package data

import (
	"context"
	"time"

	"micro-one-api/internal/billing/biz"
)

type ledgerRepo struct {
	data *Data
}

func NewLedgerRepo(data *Data) biz.LedgerRepo {
	return &ledgerRepo{data: data}
}

func (r *ledgerRepo) CreateLedger(ctx context.Context, ledger *biz.Ledger) error {
	model := &ledgerModel{
		UserID:           ledger.UserID,
		Amount:           ledger.Amount,
		BalanceAfter:     ledger.BalanceAfter,
		Type:             ledger.Type,
		ReferenceID:      stringPtr(ledger.ReferenceID),
		Remark:           stringPtr(ledger.Remark),
		TokenName:        ledger.TokenName,
		ModelName:        ledger.ModelName,
		Quota:            ledger.Quota,
		PromptTokens:     ledger.PromptTokens,
		CompletionTokens: ledger.CompletionTokens,
		ChannelID:        ledger.ChannelID,
		ElapsedTime:      ledger.ElapsedTime,
		IsStream:         ledger.IsStream,
		Endpoint:         ledger.Endpoint,
	}

	return r.data.db.WithContext(ctx).Create(model).Error
}

func (r *ledgerRepo) ListLedgers(ctx context.Context, userID string, page, pageSize int32) ([]*biz.Ledger, int64, error) {
	var models []ledgerModel
	var total int64

	offset := (page - 1) * pageSize

	query := r.data.db.WithContext(ctx).Model(&ledgerModel{})
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	fetchQuery := r.data.db.WithContext(ctx)
	if userID != "" {
		fetchQuery = fetchQuery.Where("user_id = ?", userID)
	}

	if err := fetchQuery.
		Order("created_at DESC").
		Limit(int(pageSize)).
		Offset(int(offset)).
		Find(&models).Error; err != nil {
		return nil, 0, err
	}

	ledgers := make([]*biz.Ledger, len(models))
	for i, model := range models {
		ledgers[i] = &biz.Ledger{
			ID:               model.ID,
			UserID:           model.UserID,
			Amount:           model.Amount,
			BalanceAfter:     model.BalanceAfter,
			Type:             model.Type,
			ReferenceID:      stringFromPtr(model.ReferenceID),
			Remark:           stringFromPtr(model.Remark),
			TokenName:        model.TokenName,
			ModelName:        model.ModelName,
			Quota:            model.Quota,
			PromptTokens:     model.PromptTokens,
			CompletionTokens: model.CompletionTokens,
			ChannelID:        model.ChannelID,
			ElapsedTime:      model.ElapsedTime,
			IsStream:         model.IsStream,
			Endpoint:         model.Endpoint,
			CreatedAt:        model.CreatedAt,
		}
	}

	return ledgers, total, nil
}

func (r *ledgerRepo) ListLedgersWithTimeRange(ctx context.Context, userID string, page, pageSize int32, startTime, endTime time.Time) ([]*biz.Ledger, int64, error) {
	var models []ledgerModel
	var total int64

	offset := (page - 1) * pageSize

	query := r.data.db.WithContext(ctx).Model(&ledgerModel{})
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}
	if !startTime.IsZero() {
		query = query.Where("created_at >= ?", startTime)
	}
	if !endTime.IsZero() {
		query = query.Where("created_at <= ?", endTime)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	fetchQuery := r.data.db.WithContext(ctx)
	if userID != "" {
		fetchQuery = fetchQuery.Where("user_id = ?", userID)
	}
	if !startTime.IsZero() {
		fetchQuery = fetchQuery.Where("created_at >= ?", startTime)
	}
	if !endTime.IsZero() {
		fetchQuery = fetchQuery.Where("created_at <= ?", endTime)
	}

	if err := fetchQuery.
		Order("created_at DESC").
		Limit(int(pageSize)).
		Offset(int(offset)).
		Find(&models).Error; err != nil {
		return nil, 0, err
	}

	ledgers := make([]*biz.Ledger, len(models))
	for i, model := range models {
		ledgers[i] = &biz.Ledger{
			ID:               model.ID,
			UserID:           model.UserID,
			Amount:           model.Amount,
			BalanceAfter:     model.BalanceAfter,
			Type:             model.Type,
			ReferenceID:      stringFromPtr(model.ReferenceID),
			Remark:           stringFromPtr(model.Remark),
			TokenName:        model.TokenName,
			ModelName:        model.ModelName,
			Quota:            model.Quota,
			PromptTokens:     model.PromptTokens,
			CompletionTokens: model.CompletionTokens,
			ChannelID:        model.ChannelID,
			ElapsedTime:      model.ElapsedTime,
			IsStream:         model.IsStream,
			Endpoint:         model.Endpoint,
			CreatedAt:        model.CreatedAt,
		}
	}

	return ledgers, total, nil
}

func (r *ledgerRepo) ListLedgersWithFilters(ctx context.Context, userID string, page, pageSize int32, ledgerType string, startTime, endTime time.Time) ([]*biz.Ledger, int64, error) {
	var models []ledgerModel
	var total int64

	offset := (page - 1) * pageSize

	// Build count query
	countQuery := r.data.db.WithContext(ctx).Model(&ledgerModel{})
	if userID != "" {
		countQuery = countQuery.Where("user_id = ?", userID)
	}
	if ledgerType != "" {
		countQuery = countQuery.Where("type = ?", ledgerType)
	}
	if !startTime.IsZero() {
		countQuery = countQuery.Where("created_at >= ?", startTime)
	}
	if !endTime.IsZero() {
		countQuery = countQuery.Where("created_at <= ?", endTime)
	}

	if err := countQuery.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Build fetch query
	fetchQuery := r.data.db.WithContext(ctx)
	if userID != "" {
		fetchQuery = fetchQuery.Where("user_id = ?", userID)
	}
	if ledgerType != "" {
		fetchQuery = fetchQuery.Where("type = ?", ledgerType)
	}
	if !startTime.IsZero() {
		fetchQuery = fetchQuery.Where("created_at >= ?", startTime)
	}
	if !endTime.IsZero() {
		fetchQuery = fetchQuery.Where("created_at <= ?", endTime)
	}

	if err := fetchQuery.
		Order("created_at DESC").
		Limit(int(pageSize)).
		Offset(int(offset)).
		Find(&models).Error; err != nil {
		return nil, 0, err
	}

	ledgers := make([]*biz.Ledger, len(models))
	for i, model := range models {
		ledgers[i] = &biz.Ledger{
			ID:               model.ID,
			UserID:           model.UserID,
			Amount:           model.Amount,
			BalanceAfter:     model.BalanceAfter,
			Type:             model.Type,
			ReferenceID:      stringFromPtr(model.ReferenceID),
			Remark:           stringFromPtr(model.Remark),
			TokenName:        model.TokenName,
			ModelName:        model.ModelName,
			Quota:            model.Quota,
			PromptTokens:     model.PromptTokens,
			CompletionTokens: model.CompletionTokens,
			ChannelID:        model.ChannelID,
			ElapsedTime:      model.ElapsedTime,
			IsStream:         model.IsStream,
			Endpoint:         model.Endpoint,
			CreatedAt:        model.CreatedAt,
		}
	}

	return ledgers, total, nil
}
