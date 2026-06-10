package data

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"micro-one-api/internal/billing/biz"

	"gorm.io/gorm"
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
		UpstreamCost:     ledger.UpstreamCost,
		BalanceAfter:     ledger.BalanceAfter,
		Type:             ledger.Type,
		ReferenceID:      stringPtr(ledger.ReferenceID),
		Remark:           stringPtr(ledger.Remark),
		TokenName:        ledger.TokenName,
		ModelName:        ledger.ModelName,
		Quota:            ledger.Quota,
		PromptTokens:     ledger.PromptTokens,
		CompletionTokens: ledger.CompletionTokens,
		CacheReadTokens:  ledger.CacheReadTokens,
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
			UpstreamCost:     model.UpstreamCost,
			BalanceAfter:     model.BalanceAfter,
			Type:             model.Type,
			ReferenceID:      stringFromPtr(model.ReferenceID),
			Remark:           stringFromPtr(model.Remark),
			TokenName:        model.TokenName,
			ModelName:        model.ModelName,
			Quota:            model.Quota,
			PromptTokens:     model.PromptTokens,
			CompletionTokens: model.CompletionTokens,
			CacheReadTokens:  model.CacheReadTokens,
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
			UpstreamCost:     model.UpstreamCost,
			BalanceAfter:     model.BalanceAfter,
			Type:             model.Type,
			ReferenceID:      stringFromPtr(model.ReferenceID),
			Remark:           stringFromPtr(model.Remark),
			TokenName:        model.TokenName,
			ModelName:        model.ModelName,
			Quota:            model.Quota,
			PromptTokens:     model.PromptTokens,
			CompletionTokens: model.CompletionTokens,
			CacheReadTokens:  model.CacheReadTokens,
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
			UpstreamCost:     model.UpstreamCost,
			BalanceAfter:     model.BalanceAfter,
			Type:             model.Type,
			ReferenceID:      stringFromPtr(model.ReferenceID),
			Remark:           stringFromPtr(model.Remark),
			TokenName:        model.TokenName,
			ModelName:        model.ModelName,
			Quota:            model.Quota,
			PromptTokens:     model.PromptTokens,
			CompletionTokens: model.CompletionTokens,
			CacheReadTokens:  model.CacheReadTokens,
			ChannelID:        model.ChannelID,
			ElapsedTime:      model.ElapsedTime,
			IsStream:         model.IsStream,
			Endpoint:         model.Endpoint,
			CreatedAt:        model.CreatedAt,
		}
	}

	return ledgers, total, nil
}

func (r *ledgerRepo) AggregateLedgerByDate(ctx context.Context, userID string, ledgerType string, startTime, endTime time.Time) ([]*biz.DailyAggregate, []*biz.ModelAggregate, error) {
	// Fetch raw rows, aggregate in Go (database-agnostic, avoids MySQL-specific SQL)
	type rawRow struct {
		CreatedAt        time.Time
		Amount           int64
		PromptTokens     int64
		CompletionTokens int64
		CacheReadTokens  int64
		ElapsedTime      int64
		ModelName        string
	}
	var rows []rawRow
	err := r.data.db.WithContext(ctx).Raw(`
		SELECT created_at, amount, prompt_tokens, completion_tokens, cache_read_tokens, elapsed_time, model_name
		FROM billing_ledgers
		WHERE user_id = ? AND type = ? AND created_at >= ? AND created_at <= ?
	`, userID, ledgerType, startTime, endTime).Scan(&rows).Error
	if err != nil {
		return nil, nil, err
	}

	// Aggregate by date
	type dailyAcc struct {
		quota            int64
		promptTokens     int64
		completionTokens int64
		cacheReadTokens  int64
		count            int64
		elapsedTime      int64
	}
	dailyMap := map[string]*dailyAcc{}
	modelMap := map[string]int64{}

	for _, row := range rows {
		date := row.CreatedAt.Format("2006-01-02")
		acc, ok := dailyMap[date]
		if !ok {
			acc = &dailyAcc{}
			dailyMap[date] = acc
		}
		amount := row.Amount
		if amount < 0 {
			amount = -amount
		}
		acc.quota += amount
		acc.promptTokens += row.PromptTokens
		acc.completionTokens += row.CompletionTokens
		acc.cacheReadTokens += row.CacheReadTokens
		acc.count++
		acc.elapsedTime += row.ElapsedTime

		if row.ModelName != "" {
			modelMap[row.ModelName] += row.PromptTokens + row.CompletionTokens
		}
	}

	// Build sorted daily results
	dates := make([]string, 0, len(dailyMap))
	for d := range dailyMap {
		dates = append(dates, d)
	}
	sort.Strings(dates)

	daily := make([]*biz.DailyAggregate, len(dates))
	for i, d := range dates {
		acc := dailyMap[d]
		daily[i] = &biz.DailyAggregate{
			Date:             d,
			Quota:            acc.quota,
			PromptTokens:     acc.promptTokens,
			CompletionTokens: acc.completionTokens,
			CacheReadTokens:  acc.cacheReadTokens,
			Count:            acc.count,
			ElapsedTime:      acc.elapsedTime,
		}
	}

	// Build sorted model results (by tokens desc)
	type modelKV struct {
		Model  string
		Tokens int64
	}
	modelList := make([]modelKV, 0, len(modelMap))
	for m, t := range modelMap {
		modelList = append(modelList, modelKV{Model: m, Tokens: t})
	}
	sort.Slice(modelList, func(i, j int) bool {
		return modelList[i].Tokens > modelList[j].Tokens
	})

	models := make([]*biz.ModelAggregate, len(modelList))
	for i, m := range modelList {
		models[i] = &biz.ModelAggregate{
			Model:  m.Model,
			Tokens: m.Tokens,
		}
	}

	return daily, models, nil
}

// AggregateUsage performs a multi-dimensional aggregation in SQL (GROUP BY),
// across all users by default. Grouping dimensions are resolved against a fixed
// allow-list, so group_by values can never reach the query as raw SQL.
func (r *ledgerRepo) AggregateUsage(ctx context.Context, filter biz.UsageFilter) ([]*biz.UsageBucket, *biz.UsageTotals, error) {
	dayExpr, hourExpr := r.dateExprs()

	// dimension -> (select expression, output column alias)
	type dim struct{ expr, alias string }
	allowed := map[string]dim{
		biz.UsageDimUser:    {"user_id", "g_user"},
		biz.UsageDimChannel: {"channel_id", "g_channel"},
		biz.UsageDimModel:   {"model_name", "g_model"},
		biz.UsageDimToken:   {"token_name", "g_token"},
		biz.UsageDimType:    {"type", "g_type"},
		biz.UsageDimDay:     {dayExpr, "g_day"},
		biz.UsageDimHour:    {hourExpr, "g_hour"},
	}

	selectParts := make([]string, 0, len(filter.GroupBy)+5)
	groupParts := make([]string, 0, len(filter.GroupBy))
	seen := map[string]bool{}
	for _, raw := range filter.GroupBy {
		key := strings.ToLower(strings.TrimSpace(raw))
		d, ok := allowed[key]
		if !ok || seen[key] {
			continue
		}
		seen[key] = true
		selectParts = append(selectParts, fmt.Sprintf("%s AS %s", d.expr, d.alias))
		groupParts = append(groupParts, d.alias)
	}

	selectParts = append(selectParts,
		"COALESCE(SUM(ABS(amount)), 0) AS quota",
		"COALESCE(SUM(upstream_cost), 0) AS upstream_cost",
		"COALESCE(SUM(ABS(amount) - upstream_cost), 0) AS gross_profit",
		"COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens",
		"COALESCE(SUM(completion_tokens), 0) AS completion_tokens",
		"COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens",
		"COUNT(1) AS count",
		"COALESCE(SUM(elapsed_time), 0) AS elapsed_time",
	)

	db := r.data.db.WithContext(ctx).Table("billing_ledgers")
	db = applyUsageFilters(db, filter)

	db = db.Select(strings.Join(selectParts, ", "))
	if len(groupParts) > 0 {
		db = db.Group(strings.Join(groupParts, ", "))
	}
	db = db.Order("quota DESC")
	if filter.Limit > 0 {
		db = db.Limit(filter.Limit)
	}

	type aggRow struct {
		GUser            string
		GChannel         int64
		GModel           string
		GToken           string
		GType            string
		GDay             string
		GHour            string
		Quota            int64
		UpstreamCost     int64
		GrossProfit      int64
		PromptTokens     int64
		CompletionTokens int64
		CacheReadTokens  int64
		Count            int64
		ElapsedTime      int64
	}
	var rows []aggRow
	if err := db.Scan(&rows).Error; err != nil {
		return nil, nil, err
	}

	buckets := make([]*biz.UsageBucket, len(rows))
	totals := &biz.UsageTotals{}
	for i := range rows {
		row := rows[i]
		buckets[i] = &biz.UsageBucket{
			UserID:           row.GUser,
			ChannelID:        row.GChannel,
			Model:            row.GModel,
			TokenName:        row.GToken,
			Type:             row.GType,
			Day:              row.GDay,
			Hour:             row.GHour,
			Quota:            row.Quota,
			UpstreamCost:     row.UpstreamCost,
			GrossProfit:      row.GrossProfit,
			PromptTokens:     row.PromptTokens,
			CompletionTokens: row.CompletionTokens,
			CacheReadTokens:  row.CacheReadTokens,
			Count:            row.Count,
			ElapsedTime:      row.ElapsedTime,
		}
	}

	totals, err := r.aggregateUsageTotals(ctx, filter)
	if err != nil {
		return nil, nil, err
	}
	return buckets, totals, nil
}

func (r *ledgerRepo) aggregateUsageTotals(ctx context.Context, filter biz.UsageFilter) (*biz.UsageTotals, error) {
	type totalRow struct {
		Quota            int64
		UpstreamCost     int64
		GrossProfit      int64
		PromptTokens     int64
		CompletionTokens int64
		CacheReadTokens  int64
		Count            int64
		ElapsedTime      int64
	}
	var row totalRow
	err := applyUsageFilters(r.data.db.WithContext(ctx).Table("billing_ledgers"), filter).
		Select(strings.Join([]string{
			"COALESCE(SUM(ABS(amount)), 0) AS quota",
			"COALESCE(SUM(upstream_cost), 0) AS upstream_cost",
			"COALESCE(SUM(ABS(amount) - upstream_cost), 0) AS gross_profit",
			"COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens",
			"COALESCE(SUM(completion_tokens), 0) AS completion_tokens",
			"COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens",
			"COUNT(1) AS count",
			"COALESCE(SUM(elapsed_time), 0) AS elapsed_time",
		}, ", ")).
		Scan(&row).Error
	if err != nil {
		return nil, err
	}
	return &biz.UsageTotals{
		Quota:            row.Quota,
		UpstreamCost:     row.UpstreamCost,
		GrossProfit:      row.GrossProfit,
		PromptTokens:     row.PromptTokens,
		CompletionTokens: row.CompletionTokens,
		CacheReadTokens:  row.CacheReadTokens,
		Count:            row.Count,
		ElapsedTime:      row.ElapsedTime,
	}, nil
}

func applyUsageFilters(db *gorm.DB, filter biz.UsageFilter) *gorm.DB {
	if filter.Type != "" {
		db = db.Where("type = ?", filter.Type)
	}
	if filter.UserID != "" {
		db = db.Where("user_id = ?", filter.UserID)
	}
	if filter.ChannelID != 0 {
		db = db.Where("channel_id = ?", filter.ChannelID)
	}
	if filter.Model != "" {
		db = db.Where("model_name = ?", filter.Model)
	}
	if !filter.StartTime.IsZero() {
		db = db.Where("created_at >= ?", filter.StartTime)
	}
	if !filter.EndTime.IsZero() {
		db = db.Where("created_at <= ?", filter.EndTime)
	}
	return db
}

// dateExprs returns dialect-specific SQL expressions formatting created_at into
// day (YYYY-MM-DD) and hour (YYYY-MM-DD HH) strings.
func (r *ledgerRepo) dateExprs() (day, hour string) {
	if r.data.db != nil && r.data.db.Dialector != nil && r.data.db.Dialector.Name() == "sqlite" {
		return "strftime('%Y-%m-%d', created_at)", "strftime('%Y-%m-%d %H', created_at)"
	}
	return "DATE_FORMAT(created_at, '%Y-%m-%d')", "DATE_FORMAT(created_at, '%Y-%m-%d %H')"
}
