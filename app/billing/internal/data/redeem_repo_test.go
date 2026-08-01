package data

import (
	"context"
	"testing"
	"time"

	"micro-one-api/app/billing/internal/biz"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupRedeemTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	err = db.Exec(`
			CREATE TABLE billing_redeem_codes (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				code TEXT UNIQUE,
				name TEXT,
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

	return db
}

func TestRedeemRepo_CreateRedeemCode(t *testing.T) {
	db := setupRedeemTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	data := &Data{db: db}
	repo := NewRedeemRepo(data)

	ctx := context.Background()
	code := &biz.RedeemCode{
		Code:      "CODE123",
		Name:      "测试兑换码",
		Amount:    1000,
		Count:     10,
		Status:    1,
		CreatedBy: "admin",
	}

	err := repo.CreateRedeemCode(ctx, code)
	require.NoError(t, err)

	// 验证数据已插入
	var model redeemCodeModel
	err = db.Where("code = ?", "CODE123").First(&model).Error
	require.NoError(t, err)
	assert.Equal(t, "CODE123", model.Code)
	assert.Equal(t, "测试兑换码", stringFromPtr(model.Name))
	assert.Equal(t, int64(1000), model.Amount)
	assert.Equal(t, 10, model.Count)
	assert.Equal(t, int8(1), model.Status)
}

func TestRedeemRepo_GetRedeemCode(t *testing.T) {
	db := setupRedeemTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	// 插入测试数据
	now := time.Now()
	err := db.Exec(`
			INSERT INTO billing_redeem_codes (code, name, amount, count, status, created_by, created_at, updated_at)
			VALUES ('CODE123', '测试兑换码', 1000, 10, 1, 'admin', ?, ?)
		`, now, now).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewRedeemRepo(data)

	ctx := context.Background()
	code, err := repo.GetRedeemCode(ctx, "CODE123")

	require.NoError(t, err)
	assert.NotNil(t, code)
	assert.Equal(t, "CODE123", code.Code)
	assert.Equal(t, "测试兑换码", code.Name)
	assert.Equal(t, int64(1000), code.Amount)
	assert.Equal(t, int32(10), code.Count)
	assert.Equal(t, int32(1), code.Status)
	assert.Equal(t, "admin", code.CreatedBy)
}

func TestRedeemRepo_GetRedeemCode_NotFound(t *testing.T) {
	db := setupRedeemTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	data := &Data{db: db}
	repo := NewRedeemRepo(data)

	ctx := context.Background()
	_, err := repo.GetRedeemCode(ctx, "NONEXISTENT")

	assert.Error(t, err)
	assert.ErrorIs(t, err, biz.ErrRedeemCodeNotFound)
}

func TestRedeemRepo_UpdateRedeemCodeCount(t *testing.T) {
	db := setupRedeemTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	// 插入测试数据
	now := time.Now()
	err := db.Exec(`
			INSERT INTO billing_redeem_codes (code, name, amount, count, status, created_by, created_at, updated_at)
			VALUES ('CODE123', '测试兑换码', 1000, 10, 1, 'admin', ?, ?)
		`, now, now).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewRedeemRepo(data)

	ctx := context.Background()
	err = repo.UpdateRedeemCodeCount(ctx, "CODE123", 3)
	require.NoError(t, err)

	// 验证计数已更新
	var model redeemCodeModel
	err = db.Where("code = ?", "CODE123").First(&model).Error
	require.NoError(t, err)
	assert.Equal(t, 7, model.Count) // 10 - 3 = 7
}

func TestRedeemRepo_UpdateRedeemCodeCount_Insufficient(t *testing.T) {
	db := setupRedeemTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	// 插入测试数据，只有 2 个可用
	now := time.Now()
	err := db.Exec(`
			INSERT INTO billing_redeem_codes (code, name, amount, count, status, created_by, created_at, updated_at)
			VALUES ('CODE123', '测试兑换码', 1000, 2, 1, 'admin', ?, ?)
		`, now, now).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewRedeemRepo(data)

	ctx := context.Background()
	err = repo.UpdateRedeemCodeCount(ctx, "CODE123", 5) // 尝试减少 5 个
	assert.Error(t, err)                                // 应该失败，因为只有 2 个可用
	assert.ErrorIs(t, err, biz.ErrRedeemCodeUsedUp)

	// 验证计数没有被修改
	var model redeemCodeModel
	err = db.Where("code = ?", "CODE123").First(&model).Error
	require.NoError(t, err)
	assert.Equal(t, 2, model.Count) // 应该保持不变
}

func TestRedeemRepo_CreateRedeemRecord(t *testing.T) {
	db := setupRedeemTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	data := &Data{db: db}
	repo := NewRedeemRepo(data)

	ctx := context.Background()
	record := &biz.RedeemRecord{
		UserID:        "user1",
		Code:          "CODE123",
		Amount:        1000,
		BalanceBefore: 500,
		BalanceAfter:  1500,
	}

	err := repo.CreateRedeemRecord(ctx, record)
	require.NoError(t, err)

	// 验证数据已插入
	var model redeemRecordModel
	err = db.Where("user_id = ? AND code = ?", "user1", "CODE123").First(&model).Error
	require.NoError(t, err)
	assert.Equal(t, "user1", model.UserID)
	assert.Equal(t, "CODE123", model.Code)
	assert.Equal(t, int64(1000), model.Amount)
	assert.Equal(t, int64(500), model.BalanceBefore)
	assert.Equal(t, int64(1500), model.BalanceAfter)
}
