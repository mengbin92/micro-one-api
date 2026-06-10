package data

import (
	"context"
	"testing"

	"micro-one-api/internal/billing/biz"

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
			quota INTEGER DEFAULT 0,
			used_quota INTEGER DEFAULT 0,
			request_count INTEGER DEFAULT 0,
			frozen_quota INTEGER DEFAULT 0,
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
			channel_id INTEGER DEFAULT 0,
			elapsed_time INTEGER DEFAULT 0,
			is_stream INTEGER DEFAULT 0,
			endpoint TEXT DEFAULT '',
			created_at DATETIME
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
			quota_before INTEGER,
			quota_after INTEGER,
			created_at DATETIME
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
		INSERT INTO users (id, username, display_name, "group", quota, used_quota, request_count, frozen_quota, status)
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
	assert.Equal(t, int64(1000), account.Quota)
	assert.Equal(t, int64(100), account.UsedQuota)
	assert.Equal(t, int64(10), account.RequestCount)
	assert.Equal(t, int64(50), account.FrozenQuota)
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

func TestAccountRepo_UpdateQuota_Success(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	// 插入测试数据
	err := db.Exec(`
		INSERT INTO users (id, username, display_name, "group", quota, used_quota, request_count, frozen_quota, status)
		VALUES (1, 'testuser', 'Test User', 'default', 1000, 100, 10, 50, 1)
	`).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewAccountRepo(data)

	ctx := context.Background()
	newQuota, err := repo.UpdateQuota(ctx, "1", 500, "recharge")

	require.NoError(t, err)
	assert.Equal(t, int64(1500), newQuota)

	// 验证数据库中的值
	var account struct {
		Quota int64 `gorm:"column:quota"`
	}
	err = db.Raw("SELECT quota FROM users WHERE id = 1").Scan(&account).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1500), account.Quota)
}

func TestAccountRepo_UpdateQuota_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	data := &Data{db: db}
	repo := NewAccountRepo(data)

	ctx := context.Background()
	_, err := repo.UpdateQuota(ctx, "999", 500, "recharge")

	assert.Error(t, err)
	assert.ErrorIs(t, err, biz.ErrAccountNotFound)
}

func TestAccountRepo_UpdateQuota_InsufficientQuota(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	// 插入测试数据
	err := db.Exec(`
		INSERT INTO users (id, username, display_name, "group", quota, used_quota, request_count, frozen_quota, status)
		VALUES (1, 'testuser', 'Test User', 'default', 100, 100, 10, 50, 1)
	`).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewAccountRepo(data)

	ctx := context.Background()
	_, err = repo.UpdateQuota(ctx, "1", -200, "consume")

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
		INSERT INTO users (id, username, display_name, "group", quota, used_quota, request_count, frozen_quota, status)
		VALUES (1, 'testuser', 'Test User', 'default', 1000, 100, 10, 50, 1)
	`).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewAccountRepo(data)

	err = repo.UpdateUsage(context.Background(), "1", 25, 1)
	require.NoError(t, err)

	var account struct {
		UsedQuota    int64 `gorm:"column:used_quota"`
		RequestCount int64 `gorm:"column:request_count"`
	}
	err = db.Raw("SELECT used_quota, request_count FROM users WHERE id = 1").Scan(&account).Error
	require.NoError(t, err)
	assert.Equal(t, int64(125), account.UsedQuota)
	assert.Equal(t, int64(11), account.RequestCount)
}

func TestAccountRepo_UpdateFrozenQuota_Success(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	// 插入测试数据
	err := db.Exec(`
		INSERT INTO users (id, username, display_name, "group", quota, used_quota, request_count, frozen_quota, status)
		VALUES (1, 'testuser', 'Test User', 'default', 1000, 100, 10, 50, 1)
	`).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewAccountRepo(data)

	ctx := context.Background()
	err = repo.UpdateFrozenQuota(ctx, "1", 100)

	require.NoError(t, err)

	// 验证数据库中的值
	var account struct {
		FrozenQuota int64 `gorm:"column:frozen_quota"`
	}
	err = db.Raw("SELECT frozen_quota FROM users WHERE id = 1").Scan(&account).Error
	require.NoError(t, err)
	assert.Equal(t, int64(150), account.FrozenQuota)
}

func TestAccountRepo_Transaction_Rollback(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	// 插入测试数据
	err := db.Exec(`
		INSERT INTO users (id, username, display_name, "group", quota, used_quota, request_count, frozen_quota, status)
		VALUES (1, 'testuser', 'Test User', 'default', 1000, 100, 10, 50, 1)
	`).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewAccountRepo(data)

	ctx := context.Background()
	// 尝试扣减超过配额的金额，应该回滚
	_, err = repo.UpdateQuota(ctx, "1", -2000, "consume")

	assert.Error(t, err)
	assert.ErrorIs(t, err, biz.ErrInsufficientQuota)

	// 验证配额没有被修改
	var account struct {
		Quota int64 `gorm:"column:quota"`
	}
	err = db.Raw("SELECT quota FROM users WHERE id = 1").Scan(&account).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1000), account.Quota) // 配额应该保持不变
}
