package data

import (
	"context"
	"math"
	"strconv"
	"testing"
	"time"

	"micro-one-api/app/billing/internal/biz"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRedeemCodeToBizRejectsOverflowID(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("uint IDs cannot exceed int64 on this architecture")
	}
	overflow := uint64(math.MaxUint64)
	_, err := redeemCodeToBiz(&redeemCodeModel{ID: uint(overflow)})
	require.Error(t, err)
}

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
				updated_at DATETIME,
                revision INTEGER NOT NULL DEFAULT 1
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

	err := repo.CreateRedeemRecordInTx(ctx, &gormTx{db: db}, record)
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

func TestRedeemCodeListFilterSortAndPage(t *testing.T) {
	db := setupRedeemTestDB(t)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	repo := NewRedeemRepo(&Data{db: db})
	require.NoError(t, db.Exec(`INSERT INTO billing_redeem_codes(code, name, amount, status, count) VALUES
		('exact', 'promo%a', 100, 1, 0), ('other', 'promo%b', 20, 2, 1),
		('a', 'promo%c', 300, 1, 1), ('b', 'promoXd', 500, 2, 0), ('c', 'promo%e', 90, 0, 1)`).Error)
	used := biz.RedeemCodeStatusUsed
	opts := biz.RedeemCodeListOptions{Page: 1, PageSize: 1, Keyword: "promo%", Status: &used, Sort: "amount", Order: "asc"}
	for i, want := range []string{"other", "exact", ""} {
		opts.Page = int32(i + 1)
		rows, total, err := repo.ListRedeemCodes(context.Background(), opts)
		require.NoError(t, err)
		require.EqualValues(t, 2, total)
		if want == "" {
			require.Empty(t, rows)
		} else {
			require.Len(t, rows, 1)
			require.Equal(t, want, rows[0].Code)
			require.Equal(t, used, rows[0].Status)
		}
	}
	rows, total, err := repo.ListRedeemCodes(context.Background(), biz.RedeemCodeListOptions{Page: 1, PageSize: 10, Keyword: "exact"})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, "exact", rows[0].Code)
}
