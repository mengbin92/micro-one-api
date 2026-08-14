package data

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"micro-one-api/app/billing/internal/biz"

	subscriptionbiz "micro-one-api/domain/subscription/biz"

	"gorm.io/gorm"
)

type ledgerRepo struct {
	data *Data
}

func NewLedgerRepo(data *Data) biz.LedgerRepo {
	return &ledgerRepo{data: data}
}

func (r *ledgerRepo) CreateLedger(ctx context.Context, ledger *biz.Ledger) error {
	return r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.CreateLedgerInTx(ctx, &gormTx{db: tx}, ledger)
	})
}

// CreateLedgerInTx inserts a ledger entry inside the caller's transaction.
//
// Partition-safe idempotency is enforced by billing_ledger_dedupe_claims. Its
// primary key is claimed before the ledger insert in the same transaction, so
// concurrent writers are atomically arbitrated even after billing_ledgers loses
// its global unique index for MySQL RANGE partitioning. A duplicate claim is
// returned as biz.ErrLedgerDedupeExists; rolling back the caller transaction
// also rolls back the claim.
func (r *ledgerRepo) CreateLedgerInTx(ctx context.Context, tx subscriptionbiz.Tx, ledger *biz.Ledger) error {
	db := txDB(tx)
	costSource := ledger.CostSource
	if costSource == "" {
		costSource = biz.CostSourceBalance
	}
	balanceCost := ledger.BalanceCost
	if costSource == biz.CostSourceBalance && ledger.Type == biz.LedgerTypeConsume && ledger.SubscriptionCost == 0 && balanceCost == 0 {
		balanceCost = absLedgerAmount(ledger.Amount)
	}
	dedupeKey := ledger.LedgerDedupeKey
	if dedupeKey == "" {
		dedupeKey = legacyLedgerDedupeKey(ledger)
	}
	if dedupeKey != "" {
		claim := &ledgerDedupeClaimModel{LedgerDedupeKey: dedupeKey}
		if err := db.WithContext(ctx).Create(claim).Error; err != nil {
			if isUniqueConstraintError(err) {
				return biz.ErrLedgerDedupeExists
			}
			return err
		}
	}
	model := &ledgerModel{
		UserID:                ledger.UserID,
		Amount:                ledger.Amount,
		UpstreamCost:          ledger.UpstreamCost,
		BalanceAfter:          ledger.BalanceAfter,
		Type:                  ledger.Type,
		ReferenceID:           stringPtr(ledger.ReferenceID),
		Remark:                stringPtr(ledger.Remark),
		TokenName:             ledger.TokenName,
		ModelName:             ledger.ModelName,
		Quota:                 ledger.Quota,
		PromptTokens:          ledger.PromptTokens,
		CompletionTokens:      ledger.CompletionTokens,
		CacheReadTokens:       ledger.CacheReadTokens,
		CacheCreation5mTokens: ledger.CacheCreation5mTokens,
		CacheCreation1hTokens: ledger.CacheCreation1hTokens,
		PromptCost:            ledger.PromptCost,
		CompletionCost:        ledger.CompletionCost,
		CacheReadCost:         ledger.CacheReadCost,
		CacheCreation5mCost:   ledger.CacheCreation5mCost,
		CacheCreation1hCost:   ledger.CacheCreation1hCost,
		ShadowCost:            ledger.ShadowCost,
		ChannelID:             ledger.ChannelID,
		SubscriptionAccountID: ledger.SubscriptionAccountID,
		ElapsedTime:           ledger.ElapsedTime,
		IsStream:              ledger.IsStream,
		Endpoint:              ledger.Endpoint,
		CostSource:            costSource,
		SubscriptionCost:      ledger.SubscriptionCost,
		BalanceCost:           balanceCost,
		LedgerDedupeKey:       dedupeKey,
		CreatedAt:             ledger.CreatedAt,
	}
	return db.Create(model).Error
}

func legacyLedgerDedupeKey(ledger *biz.Ledger) string {
	if ledger == nil {
		return fmt.Sprintf("legacy:unknown:%d", time.Now().UnixNano())
	}
	if ledger.ReferenceID != "" {
		return fmt.Sprintf("%s:%s:legacy", ledger.ReferenceID, ledger.Type)
	}
	return fmt.Sprintf("legacy:%s:%s:%d", ledger.UserID, ledger.Type, time.Now().UnixNano())
}

func absLedgerAmount(amount int64) int64 {
	if amount < 0 {
		return -amount
	}
	return amount
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate entry") ||
		strings.Contains(msg, "unique constraint failed") ||
		strings.Contains(msg, "duplicate key value violates unique constraint")
}

// GetLedgerByID returns a single ledger entry by its primary key, enriched
// with the username from the users table.
func (r *ledgerRepo) GetLedgerByID(ctx context.Context, id int64) (*biz.Ledger, error) {
	var m ledgerModel
	if err := r.data.db.WithContext(ctx).Model(&ledgerModel{}).
		Select("billing_ledgers.*, users.username AS username").
		Joins("LEFT JOIN users ON users.id = billing_ledgers.user_id").
		Where("billing_ledgers.id = ?", id).
		First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrLedgerNotFound
		}
		return nil, err
	}
	return ledgerFromModel(&m), nil
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

	fetchQuery := r.data.db.WithContext(ctx).Model(&ledgerModel{}).
		Select("billing_ledgers.*, users.username AS username").
		Joins("LEFT JOIN users ON users.id = billing_ledgers.user_id")
	if userID != "" {
		fetchQuery = fetchQuery.Where("billing_ledgers.user_id = ?", userID)
	}

	if err := fetchQuery.
		Order("billing_ledgers.created_at DESC").
		Limit(int(pageSize)).
		Offset(int(offset)).
		Find(&models).Error; err != nil {
		return nil, 0, err
	}

	ledgers := make([]*biz.Ledger, len(models))
	for i, model := range models {
		ledgers[i] = ledgerFromModel(&model)
	}
	return ledgers, total, nil
}

func (r *ledgerRepo) ListLedgersWithTimeRange(ctx context.Context, userID string, page, pageSize int32, startTime, endTime time.Time) ([]*biz.Ledger, int64, error) {
	return r.listLedgersInternal(ctx, userID, page, pageSize, "", startTime, endTime, false)
}

func (r *ledgerRepo) ListLedgersWithFilters(ctx context.Context, userID string, page, pageSize int32, ledgerType string, startTime, endTime time.Time) ([]*biz.Ledger, int64, error) {
	return r.listLedgersInternal(ctx, userID, page, pageSize, ledgerType, startTime, endTime, false)
}

func (r *ledgerRepo) listLedgersInternal(ctx context.Context, userID string, page, pageSize int32, ledgerType string, startTime, endTime time.Time, _ bool) ([]*biz.Ledger, int64, error) {
	var models []ledgerModel
	var total int64

	offset := int((page - 1) * pageSize)
	if offset < 0 {
		offset = 0
	}

	query := r.data.db.WithContext(ctx).Model(&ledgerModel{})
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}
	if ledgerType != "" {
		query = query.Where("type = ?", ledgerType)
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

	fetchQuery := r.data.db.WithContext(ctx).Model(&ledgerModel{}).
		Select("billing_ledgers.*, users.username AS username").
		Joins("LEFT JOIN users ON users.id = billing_ledgers.user_id")
	if userID != "" {
		fetchQuery = fetchQuery.Where("billing_ledgers.user_id = ?", userID)
	}
	if ledgerType != "" {
		fetchQuery = fetchQuery.Where("billing_ledgers.type = ?", ledgerType)
	}
	if !startTime.IsZero() {
		fetchQuery = fetchQuery.Where("billing_ledgers.created_at >= ?", startTime)
	}
	if !endTime.IsZero() {
		fetchQuery = fetchQuery.Where("billing_ledgers.created_at <= ?", endTime)
	}
	if err := fetchQuery.
		Order("billing_ledgers.created_at DESC").
		Limit(int(pageSize)).
		Offset(offset).
		Find(&models).Error; err != nil {
		return nil, 0, err
	}

	ledgers := make([]*biz.Ledger, len(models))
	for i := range models {
		ledgers[i] = ledgerFromModel(&models[i])
	}
	return ledgers, total, nil
}

func (r *ledgerRepo) ListLedgersBySubscriptionAccount(ctx context.Context, subscriptionAccountID int64, page, pageSize int32) ([]*biz.Ledger, int64, error) {
	var models []ledgerModel
	var total int64

	offset := int((page - 1) * pageSize)
	if offset < 0 {
		offset = 0
	}

	query := r.data.db.WithContext(ctx).Model(&ledgerModel{}).
		Where("subscription_account_id = ?", subscriptionAccountID)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if err := r.data.db.WithContext(ctx).Model(&ledgerModel{}).
		Select("billing_ledgers.*, users.username AS username").
		Joins("LEFT JOIN users ON users.id = billing_ledgers.user_id").
		Where("billing_ledgers.subscription_account_id = ?", subscriptionAccountID).
		Order("billing_ledgers.created_at DESC").
		Limit(int(pageSize)).
		Offset(offset).
		Find(&models).Error; err != nil {
		return nil, 0, err
	}

	ledgers := make([]*biz.Ledger, len(models))
	for i := range models {
		ledgers[i] = ledgerFromModel(&models[i])
	}
	return ledgers, total, nil
}

func (r *ledgerRepo) AggregateLedgerByDate(ctx context.Context, userID string, ledgerType string, startTime, endTime time.Time) ([]*biz.DailyAggregate, []*biz.ModelAggregate, error) {
	if ledgerType == "" {
		ledgerType = biz.LedgerTypeConsume
	}

	// Build dialect-specific date expression
	var dateExpr string
	switch r.data.db.Dialector.Name() {
	case "postgres":
		dateExpr = `TO_CHAR(created_at, 'YYYY-MM-DD')`
	case "mysql":
		dateExpr = `DATE_FORMAT(created_at, '%Y-%m-%d')`
	default: // sqlite
		dateExpr = `strftime('%Y-%m-%d', created_at)`
	}

	dailyQuery := r.data.db.WithContext(ctx).Model(&ledgerModel{}).
		Select(dateExpr+` as date,
			COALESCE(SUM(ABS(amount)), 0) as quota,
			COALESCE(SUM(prompt_tokens), 0) as prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) as completion_tokens,
			COALESCE(SUM(cache_read_tokens), 0) as cache_read_tokens,
			COALESCE(SUM(cache_creation_5m_tokens), 0) as cache_creation_5m_tokens,
			COALESCE(SUM(cache_creation_1h_tokens), 0) as cache_creation_1h_tokens,
			COUNT(*) as count,
			COALESCE(SUM(elapsed_time), 0) as elapsed_time`).
		Where("type = ?", ledgerType)
	if userID != "" {
		dailyQuery = dailyQuery.Where("user_id = ?", userID)
	}
	if !startTime.IsZero() {
		dailyQuery = dailyQuery.Where("created_at >= ?", startTime)
	}
	if !endTime.IsZero() {
		dailyQuery = dailyQuery.Where("created_at <= ?", endTime)
	}
	dailyQuery = dailyQuery.Group("date").Order("date ASC")

	type dailyRow struct {
		Date                  string
		Quota                 int64
		PromptTokens          int64
		CompletionTokens      int64
		CacheReadTokens       int64 `gorm:"column:cache_read_tokens"`
		CacheCreation5mTokens int64 `gorm:"column:cache_creation_5m_tokens"`
		CacheCreation1hTokens int64 `gorm:"column:cache_creation_1h_tokens"`
		Count                 int64 `gorm:"column:count"`
		ElapsedTime           int64 `gorm:"column:elapsed_time"`
	}
	var dailyRows []dailyRow
	if err := dailyQuery.Scan(&dailyRows).Error; err != nil {
		return nil, nil, err
	}
	daily := make([]*biz.DailyAggregate, len(dailyRows))
	for i, row := range dailyRows {
		daily[i] = &biz.DailyAggregate{
			Date:                  row.Date,
			Quota:                 row.Quota,
			PromptTokens:          row.PromptTokens,
			CompletionTokens:      row.CompletionTokens,
			CacheReadTokens:       row.CacheReadTokens,
			CacheCreation5mTokens: row.CacheCreation5mTokens,
			CacheCreation1hTokens: row.CacheCreation1hTokens,
			Count:                 row.Count,
			ElapsedTime:           row.ElapsedTime,
		}
	}

	modelQuery := r.data.db.WithContext(ctx).Model(&ledgerModel{}).
		Select(`model_name as model, COALESCE(SUM(quota), 0) as tokens`).
		Where("type = ?", ledgerType)
	if userID != "" {
		modelQuery = modelQuery.Where("user_id = ?", userID)
	}
	if !startTime.IsZero() {
		modelQuery = modelQuery.Where("created_at >= ?", startTime)
	}
	if !endTime.IsZero() {
		modelQuery = modelQuery.Where("created_at <= ?", endTime)
	}
	modelQuery = modelQuery.Group("model_name").Order("tokens DESC")

	type modelRow struct {
		Model  string
		Tokens int64
	}
	var modelRows []modelRow
	if err := modelQuery.Scan(&modelRows).Error; err != nil {
		return nil, nil, err
	}
	models := make([]*biz.ModelAggregate, len(modelRows))
	for i, row := range modelRows {
		models[i] = &biz.ModelAggregate{Model: row.Model, Tokens: row.Tokens}
	}
	return daily, models, nil
}

func (r *ledgerRepo) AggregateUsage(ctx context.Context, filter biz.UsageFilter) ([]*biz.UsageBucket, *biz.UsageTotals, error) {
	// Empty Type means "all types"; the previous default-to-consume
	// behaviour was a bug in the dual-track port. The caller decides
	// whether to scope the aggregate; we never impose a default.
	groupCols, selectCols, joinSQL := usageQueryParts(filter.GroupBy)
	if groupCols == "" {
		return nil, nil, fmt.Errorf("no group_by dimensions")
	}

	q := r.data.db.WithContext(ctx).Model(&ledgerModel{}).
		Select(selectCols).
		Joins(joinSQL)
	// Empty Type means "all types". The caller decides whether to
	// scope the aggregate; we never impose a default.
	if filter.Type != "" {
		q = q.Where("type = ?", filter.Type)
	}
	if filter.UserID != "" {
		q = q.Where("user_id = ?", filter.UserID)
	}
	if filter.ChannelID > 0 {
		q = q.Where("channel_id = ?", filter.ChannelID)
	}
	if filter.SubscriptionAccountID > 0 {
		q = q.Where("subscription_account_id = ?", filter.SubscriptionAccountID)
	}
	if filter.Model != "" {
		q = q.Where("model_name = ?", filter.Model)
	}
	if !filter.StartTime.IsZero() {
		q = q.Where("created_at >= ?", filter.StartTime)
	}
	if !filter.EndTime.IsZero() {
		q = q.Where("created_at <= ?", filter.EndTime)
	}
	q = q.Group(groupCols)
	type bucketRow struct {
		UserID                string
		ChannelID             int64  `gorm:"column:channel_id"`
		SubscriptionAccountID int64  `gorm:"column:subscription_account_id"`
		Model                 string `gorm:"column:model"`
		TokenName             string `gorm:"column:token_name"`
		Type                  string `gorm:"column:type"`
		Day                   string `gorm:"column:day"`
		Hour                  string `gorm:"column:hour"`
		Quota                 int64
		UpstreamCost          int64
		GrossProfit           int64
		PromptTokens          int64
		CompletionTokens      int64
		CacheReadTokens       int64 `gorm:"column:cache_read_tokens"`
		CacheCreation5mTokens int64 `gorm:"column:cache_creation_5m_tokens"`
		CacheCreation1hTokens int64 `gorm:"column:cache_creation_1h_tokens"`
		Count                 int64 `gorm:"column:count"`
		ElapsedTime           int64 `gorm:"column:elapsed_time"`
	}
	var rows []bucketRow
	if err := q.Scan(&rows).Error; err != nil {
		return nil, nil, err
	}
	buckets := make([]*biz.UsageBucket, len(rows))
	totals := &biz.UsageTotals{}
	for i, row := range rows {
		buckets[i] = &biz.UsageBucket{
			UserID:                row.UserID,
			ChannelID:             row.ChannelID,
			SubscriptionAccountID: row.SubscriptionAccountID,
			Model:                 row.Model,
			TokenName:             row.TokenName,
			Type:                  row.Type,
			Day:                   row.Day,
			Hour:                  row.Hour,
			Quota:                 row.Quota,
			UpstreamCost:          row.UpstreamCost,
			GrossProfit:           row.GrossProfit,
			PromptTokens:          row.PromptTokens,
			CompletionTokens:      row.CompletionTokens,
			CacheReadTokens:       row.CacheReadTokens,
			CacheCreation5mTokens: row.CacheCreation5mTokens,
			CacheCreation1hTokens: row.CacheCreation1hTokens,
			Count:                 row.Count,
			ElapsedTime:           row.ElapsedTime,
		}
		totals.Quota += row.Quota
		totals.UpstreamCost += row.UpstreamCost
		totals.GrossProfit += row.GrossProfit
		totals.PromptTokens += row.PromptTokens
		totals.CompletionTokens += row.CompletionTokens
		totals.CacheReadTokens += row.CacheReadTokens
		totals.CacheCreation5mTokens += row.CacheCreation5mTokens
		totals.CacheCreation1hTokens += row.CacheCreation1hTokens
		totals.Count += row.Count
		totals.ElapsedTime += row.ElapsedTime
	}

	// Sort buckets by quota DESC when limit is set.
	if filter.Limit > 0 && len(buckets) > filter.Limit {
		sort.Slice(buckets, func(i, j int) bool {
			return buckets[i].Quota > buckets[j].Quota
		})
		buckets = buckets[:filter.Limit]
	}
	return buckets, totals, nil
}

func usageQueryParts(groupBy []string) (groupCols, selectCols, joinSQL string) {
	cols := []string{
		"COALESCE(SUM(ABS(amount)), 0) as quota",
		"COALESCE(SUM(upstream_cost), 0) as upstream_cost",
		"COALESCE(SUM(ABS(amount) - upstream_cost), 0) as gross_profit",
		"COALESCE(SUM(prompt_tokens), 0) as prompt_tokens",
		"COALESCE(SUM(completion_tokens), 0) as completion_tokens",
		"COALESCE(SUM(cache_read_tokens), 0) as cache_read_tokens",
		"COALESCE(SUM(cache_creation_5m_tokens), 0) as cache_creation_5m_tokens",
		"COALESCE(SUM(cache_creation_1h_tokens), 0) as cache_creation_1h_tokens",
		"COUNT(*) as count",
		"COALESCE(SUM(elapsed_time), 0) as elapsed_time",
	}
	// We accumulate the SELECT column expression and the GROUP BY
	// column separately: the SELECT uses an alias when the source
	// column is hidden behind a function/case expression; the GROUP
	// BY uses the bare column name.
	for _, dim := range groupBy {
		switch dim {
		case biz.UsageDimUser:
			cols = append(cols, "user_id")
		case biz.UsageDimChannel:
			cols = append(cols, "channel_id")
		case biz.UsageDimModel:
			cols = append(cols, "model_name as model")
		case biz.UsageDimToken:
			cols = append(cols, "token_name")
		case biz.UsageDimType:
			cols = append(cols, "type")
		case biz.UsageDimDay:
			// Use DATE_FORMAT for MySQL, strftime for SQLite
			cols = append(cols, "DATE_FORMAT(created_at, '%Y-%m-%d') as day")
		case biz.UsageDimHour:
			// Use DATE_FORMAT for MySQL, strftime for SQLite
			cols = append(cols, "DATE_FORMAT(created_at, '%Y-%m-%d %H') as hour")
		case biz.UsageDimSubscriptionAccount:
			cols = append(cols, "subscription_account_id")
		}
	}
	// The original code grouped by the user-facing dimension string
	// (e.g. "channel") which is not a real column; map each
	// dimension to its bare column name so SQLite's strict
	// GROUP BY resolution is happy.
	dimColumn := func(dim string) string {
		switch dim {
		case biz.UsageDimUser:
			return "user_id"
		case biz.UsageDimChannel:
			return "channel_id"
		case biz.UsageDimModel:
			return "model_name"
		case biz.UsageDimToken:
			return "token_name"
		case biz.UsageDimType:
			return "type"
		case biz.UsageDimDay, biz.UsageDimHour:
			return "created_at"
		case biz.UsageDimSubscriptionAccount:
			return "subscription_account_id"
		}
		return dim
	}
	groupByCols := make([]string, 0, len(groupBy))
	for _, dim := range groupBy {
		groupByCols = append(groupByCols, dimColumn(dim))
	}
	groupCols = strings.Join(groupByCols, ", ")
	selectCols = strings.Join(cols, ", ")
	return groupCols, selectCols, ""
}

// FindByDedupeKey returns the ledger entry with the given dedupe key or
// nil if none exists. Used by the CAS commit pipeline to detect
// pre-existing entries left by an earlier failed attempt.
func (r *ledgerRepo) FindByDedupeKey(ctx context.Context, tx subscriptionbiz.Tx, key string) (*biz.Ledger, error) {
	if key == "" {
		return nil, nil
	}
	// When no transaction is supplied (e.g. the best-effort split recovery in
	// CommitQuotaWithUsageAndSplit, which runs outside a tx), fall back to the
	// repo's own DB handle. txDB(nil) returns nil and the subsequent
	// db.WithContext call would panic — this was the root cause of the
	// billing-service crash loop that produced intermittent 402s on the relay.
	db := txDB(tx)
	if db == nil {
		if r.data == nil || r.data.db == nil {
			return nil, fmt.Errorf("ledger repo: no database available")
		}
		db = r.data.db
	}
	var model ledgerModel
	if err := db.WithContext(ctx).Where("ledger_dedupe_key = ?", key).First(&model).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return ledgerFromModel(&model), nil
}

// SumSubscriptionCostByReservation returns the total subscription_cost
// recorded against the given reservation IDs. Used by reconciliation to
// verify the subscription-side ledger matches the per-reservation actual
// absorption.
func (r *ledgerRepo) SumSubscriptionCostByReservation(ctx context.Context, reservationIDs []string) (int64, error) {
	if len(reservationIDs) == 0 {
		return 0, nil
	}
	var total int64
	q := r.data.db.WithContext(ctx).Model(&ledgerModel{}).
		Where("reference_id IN ?", reservationIDs).
		Where("type = ?", biz.LedgerTypeConsume).
		Where("cost_source = ?", biz.CostSourceSubscription).
		Select("COALESCE(SUM(subscription_cost), 0)")
	if err := q.Scan(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func ledgerFromModel(model *ledgerModel) *biz.Ledger {
	if model == nil {
		return nil
	}
	return &biz.Ledger{
		ID:                    model.ID,
		UserID:                model.UserID,
		Amount:                model.Amount,
		UpstreamCost:          model.UpstreamCost,
		BalanceAfter:          model.BalanceAfter,
		Type:                  model.Type,
		ReferenceID:           stringFromPtr(model.ReferenceID),
		Remark:                stringFromPtr(model.Remark),
		TokenName:             model.TokenName,
		ModelName:             model.ModelName,
		Quota:                 model.Quota,
		PromptTokens:          model.PromptTokens,
		CompletionTokens:      model.CompletionTokens,
		CacheReadTokens:       model.CacheReadTokens,
		CacheCreation5mTokens: model.CacheCreation5mTokens,
		CacheCreation1hTokens: model.CacheCreation1hTokens,
		PromptCost:            model.PromptCost,
		CompletionCost:        model.CompletionCost,
		CacheReadCost:         model.CacheReadCost,
		CacheCreation5mCost:   model.CacheCreation5mCost,
		CacheCreation1hCost:   model.CacheCreation1hCost,
		ShadowCost:            model.ShadowCost,
		ChannelID:             model.ChannelID,
		SubscriptionAccountID: model.SubscriptionAccountID,
		ElapsedTime:           model.ElapsedTime,
		IsStream:              model.IsStream,
		Endpoint:              model.Endpoint,
		CostSource:            model.CostSource,
		SubscriptionCost:      model.SubscriptionCost,
		BalanceCost:           model.BalanceCost,
		LedgerDedupeKey:       model.LedgerDedupeKey,
		Username:              model.Username,
		CreatedAt:             model.CreatedAt,
	}
}
