package data

import (
	"context"
	"testing"

	"micro-one-api/app/billing/internal/biz"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// 创建测试表
	err = db.Exec(`
		CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			username TEXT,
			display_name TEXT,
			"group" TEXT,
			balance INTEGER DEFAULT 0,
			used_amount INTEGER DEFAULT 0,
			request_count INTEGER DEFAULT 0,
			frozen_amount INTEGER DEFAULT 0,
			status INTEGER DEFAULT 0
		)
	`).Error
	require.NoError(t, err)

	err = db.Exec(`
		CREATE TABLE billing_reservations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			reservation_id TEXT UNIQUE,
			user_id TEXT,
			request_id TEXT,
			amount INTEGER,
			status TEXT,
			model TEXT,
			channel_id TEXT,
			subscription_account_id TEXT DEFAULT '0',
			subscription_id INTEGER NOT NULL DEFAULT 0,
			subscription_amount_usd REAL NOT NULL DEFAULT 0,
			subscription_daily_window_start INTEGER NOT NULL DEFAULT 0,
			subscription_weekly_window_start INTEGER NOT NULL DEFAULT 0,
			subscription_monthly_window_start INTEGER NOT NULL DEFAULT 0,
			balance_amount_quota INTEGER NOT NULL DEFAULT 0,
			balance_amount INTEGER NOT NULL DEFAULT 0,
			actual_cost INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME,
			updated_at DATETIME,
			expired_at DATETIME
		)
	`).Error
	require.NoError(t, err)

	err = db.Exec(`
		CREATE TABLE billing_ledgers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT,
			amount INTEGER,
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
			prompt_cost INTEGER DEFAULT 0,
			completion_cost INTEGER DEFAULT 0,
			cache_read_cost INTEGER DEFAULT 0,
			cache_creation_5m_cost INTEGER DEFAULT 0,
			cache_creation_1h_cost INTEGER DEFAULT 0,
			shadow_cost INTEGER DEFAULT 0,
			channel_id INTEGER DEFAULT 0,
			subscription_account_id INTEGER NOT NULL DEFAULT 0,
			source_kind TEXT NOT NULL DEFAULT '',
			upstream_model_id TEXT NOT NULL DEFAULT '',
			cost_audit_status TEXT NOT NULL DEFAULT 'legacy',
			uncached_input_tokens INTEGER NOT NULL DEFAULT 0,
			reported_prompt_tokens INTEGER NOT NULL DEFAULT 0,
			reported_total_tokens INTEGER NOT NULL DEFAULT 0,
			billable_total_tokens INTEGER NOT NULL DEFAULT 0,
			usage_semantics TEXT NOT NULL DEFAULT '',
			usage_protocol TEXT NOT NULL DEFAULT '',
			usage_field_shape TEXT NOT NULL DEFAULT '',
			usage_parse_status TEXT NOT NULL DEFAULT 'legacy',
			usage_contract_version INTEGER NOT NULL DEFAULT 0,
			canonical_present INTEGER NOT NULL DEFAULT 0,
			usage_decision_reason TEXT NOT NULL DEFAULT '',
			subset_candidate_cost INTEGER NOT NULL DEFAULT 0,
			exclusive_candidate_cost INTEGER NOT NULL DEFAULT 0,
			pricing_config_hash TEXT NOT NULL DEFAULT '',
			elapsed_time INTEGER DEFAULT 0,
			is_stream INTEGER DEFAULT 0,
			endpoint TEXT DEFAULT '',
			upstream_cost INTEGER DEFAULT 0,
			cost_source TEXT NOT NULL DEFAULT 'balance',
			subscription_cost INTEGER NOT NULL DEFAULT 0,
			balance_cost INTEGER NOT NULL DEFAULT 0,
			ledger_dedupe_key TEXT NOT NULL DEFAULT '',
			created_at DATETIME
		)
	`).Error
	require.NoError(t, err)

	err = db.Exec(`
		CREATE TABLE billing_ledger_dedupe_claims (
			ledger_dedupe_key TEXT PRIMARY KEY,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`).Error
	require.NoError(t, err)

	err = db.Exec(`
		CREATE TABLE billing_redeem_codes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			code TEXT UNIQUE,
			amount INTEGER,
			count INTEGER,
			status INTEGER,
			created_by TEXT,
			created_at DATETIME,
			updated_at DATETIME
		)
	`).Error
	require.NoError(t, err)

	err = db.Exec(`
		CREATE TABLE billing_redeem_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT,
			code TEXT,
			amount INTEGER,
			balance_before INTEGER,
			balance_after INTEGER,
			created_at DATETIME
		)
	`).Error
	require.NoError(t, err)

	err = db.Exec(`
		CREATE TABLE account_receivables (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT,
			reservation_id TEXT UNIQUE,
			overdue_quota INTEGER NOT NULL DEFAULT 0,
			overdue_usd REAL NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'pending',
			created_at DATETIME,
			updated_at DATETIME,
			settled_at DATETIME,
			settled_quota INTEGER NOT NULL DEFAULT 0,
			remark TEXT
		)
	`).Error
	require.NoError(t, err)

	return db
}

func TestAccountRepo_GetAccountSnapshot(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	// 插入测试数据
	err := db.Exec(`
		INSERT INTO users (id, username, display_name, "group", balance, used_amount, request_count, frozen_amount, status)
		VALUES (1, 'testuser', 'Test User', 'default', 1000, 100, 10, 50, 1)
	`).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewAccountRepo(data)

	ctx := context.Background()
	account, err := repo.GetAccountSnapshot(ctx, "1")

	require.NoError(t, err)
	assert.NotNil(t, account)
	assert.Equal(t, "1", account.UserID)
	assert.Equal(t, "testuser", account.Username)
	assert.Equal(t, "Test User", account.DisplayName)
	assert.Equal(t, "default", account.Group)
	assert.Equal(t, int64(1000), account.Balance)
	assert.Equal(t, int64(100), account.UsedAmount)
	assert.Equal(t, int64(10), account.RequestCount)
	assert.Equal(t, int64(50), account.FrozenAmount)
	assert.Equal(t, int32(1), account.Status)
}

func TestAccountRepo_GetAccountSnapshot_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	data := &Data{db: db}
	repo := NewAccountRepo(data)

	ctx := context.Background()
	_, err := repo.GetAccountSnapshot(ctx, "999")

	assert.Error(t, err)
	assert.ErrorIs(t, err, biz.ErrAccountNotFound)
}

func TestAccountRepo_UpdateBalance_Success(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	// 插入测试数据
	err := db.Exec(`
		INSERT INTO users (id, username, display_name, "group", balance, used_amount, request_count, frozen_amount, status)
		VALUES (1, 'testuser', 'Test User', 'default', 1000, 100, 10, 50, 1)
	`).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewAccountRepo(data)

	ctx := context.Background()
	newBalance, err := repo.UpdateBalance(ctx, "1", 500, "recharge")

	require.NoError(t, err)
	assert.Equal(t, int64(1500), newBalance)

	// 验证数据库中的值
	var account struct {
		Balance int64 `gorm:"column:balance"`
	}
	err = db.Raw("SELECT balance FROM users WHERE id = 1").Scan(&account).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1500), account.Balance)
}

func TestAccountRepo_UpdateBalance_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	data := &Data{db: db}
	repo := NewAccountRepo(data)

	ctx := context.Background()
	_, err := repo.UpdateBalance(ctx, "999", 500, "recharge")

	assert.Error(t, err)
	assert.ErrorIs(t, err, biz.ErrAccountNotFound)
}

func TestAccountRepo_UpdateBalance_InsufficientQuota(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	// 插入测试数据
	err := db.Exec(`
		INSERT INTO users (id, username, display_name, "group", balance, used_amount, request_count, frozen_amount, status)
		VALUES (1, 'testuser', 'Test User', 'default', 100, 100, 10, 50, 1)
	`).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewAccountRepo(data)

	ctx := context.Background()
	_, err = repo.UpdateBalance(ctx, "1", -200, "consume")

	assert.Error(t, err)
	assert.ErrorIs(t, err, biz.ErrInsufficientQuota)
}

func TestAccountRepo_UpdateUsage_Success(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	err := db.Exec(`
		INSERT INTO users (id, username, display_name, "group", balance, used_amount, request_count, frozen_amount, status)
		VALUES (1, 'testuser', 'Test User', 'default', 1000, 100, 10, 50, 1)
	`).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewAccountRepo(data)

	err = repo.UpdateUsage(context.Background(), "1", 25, 1)
	require.NoError(t, err)

	var account struct {
		UsedAmount   int64 `gorm:"column:used_amount"`
		RequestCount int64 `gorm:"column:request_count"`
	}
	err = db.Raw("SELECT used_amount, request_count FROM users WHERE id = 1").Scan(&account).Error
	require.NoError(t, err)
	assert.Equal(t, int64(125), account.UsedAmount)
	assert.Equal(t, int64(11), account.RequestCount)
}

func TestAccountRepo_UpdateFrozenAmount_Success(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	// 插入测试数据
	err := db.Exec(`
		INSERT INTO users (id, username, display_name, "group", balance, used_amount, request_count, frozen_amount, status)
		VALUES (1, 'testuser', 'Test User', 'default', 1000, 100, 10, 50, 1)
	`).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewAccountRepo(data)

	ctx := context.Background()
	err = repo.UpdateFrozenAmount(ctx, "1", 100)

	require.NoError(t, err)

	// 验证数据库中的值
	var account struct {
		FrozenAmount int64 `gorm:"column:frozen_amount"`
	}
	err = db.Raw("SELECT frozen_amount FROM users WHERE id = 1").Scan(&account).Error
	require.NoError(t, err)
	assert.Equal(t, int64(150), account.FrozenAmount)
}

func TestAccountRepo_Transaction_Rollback(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	// 插入测试数据
	err := db.Exec(`
		INSERT INTO users (id, username, display_name, "group", balance, used_amount, request_count, frozen_amount, status)
		VALUES (1, 'testuser', 'Test User', 'default', 1000, 100, 10, 50, 1)
	`).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewAccountRepo(data)

	ctx := context.Background()
	// 尝试扣减超过配额的金额，应该回滚
	_, err = repo.UpdateBalance(ctx, "1", -2000, "consume")

	assert.Error(t, err)
	assert.ErrorIs(t, err, biz.ErrInsufficientQuota)

	// 验证配额没有被修改
	var account struct {
		Balance int64 `gorm:"column:balance"`
	}
	err = db.Raw("SELECT balance FROM users WHERE id = 1").Scan(&account).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1000), account.Balance) // 配额应该保持不变
}

func TestAccountRepo_CommitBalanceInTx_NetReservedMinusActual(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	err := db.Exec(`
		INSERT INTO users (id, username, display_name, "group", balance, used_amount, request_count, frozen_amount, status)
		VALUES (1, 'testuser', 'Test User', 'default', 800, 100, 10, 200, 1)
	`).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewAccountRepo(data)
	tx := db.Begin()
	oldBalance, newBalance, err := repo.CommitBalanceInTx(context.Background(), &gormTx{db: tx}, "1", 200, 150, true)
	require.NoError(t, err)
	require.NoError(t, tx.Commit().Error)

	assert.Equal(t, int64(800), oldBalance)
	assert.Equal(t, int64(850), newBalance)

	var account struct {
		Balance      int64 `gorm:"column:balance"`
		FrozenAmount int64 `gorm:"column:frozen_amount"`
	}
	err = db.Raw("SELECT balance, frozen_amount FROM users WHERE id = 1").Scan(&account).Error
	require.NoError(t, err)
	assert.Equal(t, int64(850), account.Balance)
	assert.Equal(t, int64(0), account.FrozenAmount)
}

func TestReceivableRepo_SettleOldestForUserInTx_PartialKeepsPending(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	err := db.Exec(`
		INSERT INTO account_receivables (user_id, reservation_id, overdue_quota, overdue_usd, status, settled_quota)
		VALUES ('1', 'res-1', 100, 0.0002, 'pending', 0)
	`).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewReceivableRepo(data)
	tx := db.Begin()
	settled, err := repo.SettleOldestForUserInTx(context.Background(), &gormTx{db: tx}, "1", 80)
	require.NoError(t, err)
	require.NoError(t, tx.Commit().Error)
	assert.Equal(t, int64(80), settled)

	var row struct {
		OverdueQuota int64  `gorm:"column:overdue_quota"`
		SettledQuota int64  `gorm:"column:settled_quota"`
		Status       string `gorm:"column:status"`
	}
	err = db.Raw("SELECT overdue_quota, settled_quota, status FROM account_receivables WHERE reservation_id = 'res-1'").Scan(&row).Error
	require.NoError(t, err)
	assert.Equal(t, int64(100), row.OverdueQuota)
	assert.Equal(t, int64(80), row.SettledQuota)
	assert.Equal(t, biz.ReceivableStatusPending, row.Status)

	pending, err := repo.SumOverduePendingByUser(context.Background(), "1")
	require.NoError(t, err)
	assert.Equal(t, int64(20), pending)
}
