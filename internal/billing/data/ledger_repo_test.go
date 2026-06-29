package data

import (
	"context"
	"testing"
	"time"

	"micro-one-api/internal/billing/biz"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupLedgerTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	err = db.Exec(`
		CREATE TABLE billing_ledgers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT,
			amount INTEGER,
			upstream_cost INTEGER DEFAULT 0,
			balance_after INTEGER,
			type TEXT,
			reference_id TEXT,
			remark TEXT,
			token_name TEXT DEFAULT '',
			model_name TEXT DEFAULT '',
			quota INTEGER DEFAULT 0,
			prompt_tokens INTEGER DEFAULT 0,
			completion_tokens INTEGER DEFAULT 0,
			cache_read_tokens INTEGER DEFAULT 0,
			channel_id INTEGER DEFAULT 0,
			subscription_account_id INTEGER DEFAULT 0,
			elapsed_time INTEGER DEFAULT 0,
			is_stream INTEGER DEFAULT 0,
			endpoint TEXT DEFAULT '',
			created_at DATETIME
		)
	`).Error
	require.NoError(t, err)

	return db
}

func TestLedgerRepo_CreateLedger(t *testing.T) {
	db := setupLedgerTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	data := &Data{db: db}
	repo := NewLedgerRepo(data)

	ctx := context.Background()
	ledger := &biz.Ledger{
		UserID:           "user1",
		Amount:           -100,
		BalanceAfter:     900,
		Type:             "consume",
		ReferenceID:      "res_test_001",
		Remark:           "test consume",
		TokenName:        "token-1",
		ModelName:        "gpt-test",
		Quota:            12,
		PromptTokens:     7,
		CompletionTokens: 5,
		CacheReadTokens:  3,
		ChannelID:        3,
		ElapsedTime:      123,
		Endpoint:         "/v1/chat/completions",
		UpstreamCost:     40,
	}

	err := repo.CreateLedger(ctx, ledger)
	require.NoError(t, err)

	// 验证数据已插入
	var model ledgerModel
	err = db.Where("user_id = ?", "user1").First(&model).Error
	require.NoError(t, err)
	assert.Equal(t, "user1", model.UserID)
	assert.Equal(t, int64(-100), model.Amount)
	assert.Equal(t, int64(40), model.UpstreamCost)
	assert.Equal(t, int64(900), model.BalanceAfter)
	assert.Equal(t, "consume", model.Type)
	assert.Equal(t, "token-1", model.TokenName)
	assert.Equal(t, "gpt-test", model.ModelName)
	assert.Equal(t, int64(12), model.Quota)
	assert.Equal(t, int64(3), model.CacheReadTokens)
}

func TestLedgerRepo_ListLedgers(t *testing.T) {
	db := setupLedgerTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	// 插入测试数据
	now := time.Now()
	err := db.Exec(`
		INSERT INTO billing_ledgers (user_id, amount, balance_after, type, reference_id, remark, created_at)
		VALUES
			('user1', -100, 900, 'consume', 'res_001', 'consume 1', ?),
			('user1', 500, 1400, 'recharge', NULL, 'topup', ?),
			('user1', 50, 1450, 'refund', 'res_001', 'refund', ?),
			('user2', -200, 800, 'consume', 'res_002', 'consume 2', ?)
	`, now, now.Add(-time.Hour), now.Add(-30*time.Minute), now).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewLedgerRepo(data)

	ctx := context.Background()
	ledgers, total, err := repo.ListLedgers(ctx, "user1", 1, 10)

	require.NoError(t, err)
	assert.Len(t, ledgers, 3)
	assert.Equal(t, int64(3), total)
	assert.Equal(t, "user1", ledgers[0].UserID)
}

func TestLedgerRepo_ListLedgers_Pagination(t *testing.T) {
	db := setupLedgerTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	// 插入测试数据
	now := time.Now()
	for i := 0; i < 15; i++ {
		err := db.Exec(`
			INSERT INTO billing_ledgers (user_id, amount, balance_after, type, reference_id, remark, created_at)
			VALUES (?, -100, ?, 'consume', ?, ?, ?)
		`, "user1", 1000-i*100, "res_"+string(rune('0'+i)), "consume "+string(rune('0'+i)), now.Add(time.Duration(-i)*time.Minute)).Error
		require.NoError(t, err)
	}

	data := &Data{db: db}
	repo := NewLedgerRepo(data)

	ctx := context.Background()

	// 第一页
	ledgers1, total, err := repo.ListLedgers(ctx, "user1", 1, 10)
	require.NoError(t, err)
	assert.Len(t, ledgers1, 10)
	assert.Equal(t, int64(15), total)

	// 第二页
	ledgers2, _, err := repo.ListLedgers(ctx, "user1", 2, 10)
	require.NoError(t, err)
	assert.Len(t, ledgers2, 5)
}

func TestLedgerRepo_ListLedgers_Empty(t *testing.T) {
	db := setupLedgerTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	data := &Data{db: db}
	repo := NewLedgerRepo(data)

	ctx := context.Background()
	ledgers, total, err := repo.ListLedgers(ctx, "user1", 1, 10)

	require.NoError(t, err)
	assert.Len(t, ledgers, 0)
	assert.Equal(t, int64(0), total)
}

func TestLedgerRepo_AggregateLedgerByDate(t *testing.T) {
	db := setupLedgerTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
	yesterday := today.AddDate(0, 0, -1)

	// Insert test data: two days, two models, quota != |amount| (simulates ModelPrice billing)
	err := db.Exec(`
		INSERT INTO billing_ledgers (user_id, amount, balance_after, type, model_name, quota, prompt_tokens, completion_tokens, cache_read_tokens, elapsed_time, created_at)
		VALUES
			('u1', -217500, 782500, 'consume', 'mimo-v2.5-pro', 1000000, 600000, 400000, 120000, 500, ?),
			('u1', -100, 782400, 'consume', 'gpt-4o', 200, 100, 100, 20, 200, ?),
			('u1', -300, 782100, 'consume', 'mimo-v2.5-pro', 500, 300, 200, 30, 300, ?),
			('u1', 500000, 1282100, 'recharge', '', 0, 0, 0, 0, 0, ?),
			('u2', -999, 0, 'consume', 'other-model', 999, 500, 499, 50, 100, ?)
	`, today, today, yesterday, yesterday, today).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewLedgerRepo(data)

	ctx := context.Background()
	startTime := yesterday.Add(-time.Hour)
	endTime := today.Add(time.Hour)

	daily, models, err := repo.AggregateLedgerByDate(ctx, "u1", "consume", startTime, endTime)
	require.NoError(t, err)

	// Should have 2 days
	require.Len(t, daily, 2)

	// Yesterday: 1 entry, amount=-300, quota=300
	assert.Equal(t, yesterday.Format("2006-01-02"), daily[0].Date)
	assert.Equal(t, int64(300), daily[0].Quota) // |amount|, NOT quota(500)
	assert.Equal(t, int64(300), daily[0].PromptTokens)
	assert.Equal(t, int64(200), daily[0].CompletionTokens)
	assert.Equal(t, int64(30), daily[0].CacheReadTokens)
	assert.Equal(t, int64(1), daily[0].Count)

	// Today: 2 entries, amount=(-217500)+(-100), quota=217600
	assert.Equal(t, today.Format("2006-01-02"), daily[1].Date)
	assert.Equal(t, int64(217600), daily[1].Quota) // 217500 + 100
	assert.Equal(t, int64(600100), daily[1].PromptTokens)
	assert.Equal(t, int64(400100), daily[1].CompletionTokens)
	assert.Equal(t, int64(120020), daily[1].CacheReadTokens)
	assert.Equal(t, int64(2), daily[1].Count)

	// Models: mimo-v2.5-pro (1M+500 tokens) > gpt-4o (200 tokens)
	require.Len(t, models, 2)
	assert.Equal(t, "mimo-v2.5-pro", models[0].Model)
	assert.Equal(t, int64(1000500), models[0].Tokens)
	assert.Equal(t, "gpt-4o", models[1].Model)
	assert.Equal(t, int64(200), models[1].Tokens)

	// Verify: recharge entry is excluded (type != consume)
	// Verify: u2 entry is excluded (different user)
	for _, d := range daily {
		assert.NotEqual(t, int64(500000), d.Quota, "recharge should be excluded")
	}
}

func TestLedgerRepo_AggregateLedgerByDate_Empty(t *testing.T) {
	db := setupLedgerTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	data := &Data{db: db}
	repo := NewLedgerRepo(data)

	ctx := context.Background()
	daily, models, err := repo.AggregateLedgerByDate(ctx, "nobody", "consume", time.Now().AddDate(0, 0, -7), time.Now())
	require.NoError(t, err)
	assert.Empty(t, daily)
	assert.Empty(t, models)
}

func TestLedgerRepo_AggregateUsage_ByChannelCrossUser(t *testing.T) {
	db := setupLedgerTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	data := &Data{db: db}
	repo := NewLedgerRepo(data)
	ctx := context.Background()

	seed := []*biz.Ledger{
		{UserID: "u1", Amount: -100, UpstreamCost: 30, Type: "consume", ModelName: "gpt-a", ChannelID: 1, PromptTokens: 10, CompletionTokens: 5, CacheReadTokens: 2, ElapsedTime: 100},
		{UserID: "u2", Amount: -50, UpstreamCost: 20, Type: "consume", ModelName: "gpt-a", ChannelID: 1, PromptTokens: 4, CompletionTokens: 2, CacheReadTokens: 1, ElapsedTime: 40},
		{UserID: "u1", Amount: -25, UpstreamCost: 10, Type: "consume", ModelName: "gpt-b", ChannelID: 2, PromptTokens: 3, CompletionTokens: 1, CacheReadTokens: 1, ElapsedTime: 20},
		{UserID: "u1", Amount: 1000, Type: "recharge", ChannelID: 0},
	}
	for _, l := range seed {
		require.NoError(t, repo.CreateLedger(ctx, l))
	}

	// Aggregate consume usage grouped by channel, across all users.
	buckets, totals, err := repo.AggregateUsage(ctx, biz.UsageFilter{
		GroupBy: []string{biz.UsageDimChannel},
		Type:    "consume",
	})
	require.NoError(t, err)
	require.Len(t, buckets, 2)

	// Ordered by quota desc: channel 1 (150) before channel 2 (25).
	assert.Equal(t, int64(1), buckets[0].ChannelID)
	assert.Equal(t, int64(150), buckets[0].Quota)
	assert.Equal(t, int64(50), buckets[0].UpstreamCost)
	assert.Equal(t, int64(100), buckets[0].GrossProfit)
	assert.Equal(t, int64(3), buckets[0].CacheReadTokens)
	assert.Equal(t, int64(2), buckets[0].Count)
	assert.Equal(t, int64(2), buckets[1].ChannelID)
	assert.Equal(t, int64(25), buckets[1].Quota)

	// Totals exclude the recharge row (consume filter) -> 175.
	assert.Equal(t, int64(175), totals.Quota)
	assert.Equal(t, int64(60), totals.UpstreamCost)
	assert.Equal(t, int64(115), totals.GrossProfit)
	assert.Equal(t, int64(4), totals.CacheReadTokens)
	assert.Equal(t, int64(3), totals.Count)
}

func TestLedgerRepo_AggregateUsage_TotalsIgnoreLimit(t *testing.T) {
	db := setupLedgerTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	data := &Data{db: db}
	repo := NewLedgerRepo(data)
	ctx := context.Background()

	for _, l := range []*biz.Ledger{
		{UserID: "u1", Amount: -100, UpstreamCost: 40, Type: "consume", ModelName: "gpt-a"},
		{UserID: "u1", Amount: -50, UpstreamCost: 10, Type: "consume", ModelName: "gpt-b"},
	} {
		require.NoError(t, repo.CreateLedger(ctx, l))
	}

	buckets, totals, err := repo.AggregateUsage(ctx, biz.UsageFilter{
		GroupBy: []string{biz.UsageDimModel},
		Type:    "consume",
		Limit:   1,
	})
	require.NoError(t, err)
	require.Len(t, buckets, 1)
	assert.Equal(t, int64(100), buckets[0].Quota)
	assert.Equal(t, int64(150), totals.Quota)
	assert.Equal(t, int64(50), totals.UpstreamCost)
	assert.Equal(t, int64(100), totals.GrossProfit)
}

func TestLedgerRepo_AggregateUsage_ByType_AllTypes(t *testing.T) {
	db := setupLedgerTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	data := &Data{db: db}
	repo := NewLedgerRepo(data)
	ctx := context.Background()

	for _, l := range []*biz.Ledger{
		{UserID: "u1", Amount: -100, Type: "consume"},
		{UserID: "u1", Amount: -40, Type: "consume"},
		{UserID: "u1", Amount: 1000, Type: "recharge"},
	} {
		require.NoError(t, repo.CreateLedger(ctx, l))
	}

	// Empty Type => no type filter, aggregate every type.
	buckets, totals, err := repo.AggregateUsage(ctx, biz.UsageFilter{GroupBy: []string{biz.UsageDimType}})
	require.NoError(t, err)
	require.Len(t, buckets, 2)

	byType := map[string]int64{}
	for _, b := range buckets {
		byType[b.Type] = b.Quota
	}
	assert.Equal(t, int64(140), byType["consume"])
	assert.Equal(t, int64(1000), byType["recharge"])
	assert.Equal(t, int64(1140), totals.Quota)
}
