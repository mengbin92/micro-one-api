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

func setupReservationTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
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
			subscription_account_id TEXT,
			created_at DATETIME,
			updated_at DATETIME,
			expired_at DATETIME
		)
	`).Error
	require.NoError(t, err)

	return db
}

func TestReservationRepo_CreateReservation(t *testing.T) {
	db := setupReservationTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	data := &Data{db: db}
	repo := NewReservationRepo(data)

	ctx := context.Background()
	now := time.Now()
	reservation := &biz.Reservation{
		ReservationID: "res_test_001",
		UserID:       "user1",
		RequestID:    "req_test_001",
		Amount:       100,
		Status:       "reserved",
		Model:        "gpt-4o-mini",
		ChannelID:    "channel1",
		CreatedAt:    now,
		UpdatedAt:    now,
		ExpiredAt:    now.Add(5 * time.Minute),
	}

	err := repo.CreateReservation(ctx, reservation)
	require.NoError(t, err)

	// 验证数据已插入
	var model reservationModel
	err = db.Where("reservation_id = ?", "res_test_001").First(&model).Error
	require.NoError(t, err)
	assert.Equal(t, "res_test_001", model.ReservationID)
	assert.Equal(t, "user1", model.UserID)
	assert.Equal(t, "req_test_001", model.RequestID)
	assert.Equal(t, int64(100), model.Amount)
	assert.Equal(t, "reserved", model.Status)
}

func TestReservationRepo_GetReservation(t *testing.T) {
	db := setupReservationTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	// 插入测试数据
	now := time.Now()
	err := db.Exec(`
		INSERT INTO billing_reservations (reservation_id, user_id, request_id, amount, status, model, channel_id, created_at, updated_at, expired_at)
		VALUES ('res_test_001', 'user1', 'req_test_001', 100, 'reserved', 'gpt-4o-mini', 'channel1', ?, ?, ?)
	`, now, now, now.Add(5*time.Minute)).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewReservationRepo(data)

	ctx := context.Background()
	reservation, err := repo.GetReservation(ctx, "res_test_001")

	require.NoError(t, err)
	assert.NotNil(t, reservation)
	assert.Equal(t, "res_test_001", reservation.ReservationID)
	assert.Equal(t, "user1", reservation.UserID)
	assert.Equal(t, "req_test_001", reservation.RequestID)
	assert.Equal(t, int64(100), reservation.Amount)
	assert.Equal(t, "reserved", reservation.Status)
	assert.Equal(t, "gpt-4o-mini", reservation.Model)
	assert.Equal(t, "channel1", reservation.ChannelID)
}

func TestReservationRepo_GetReservation_NotFound(t *testing.T) {
	db := setupReservationTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	data := &Data{db: db}
	repo := NewReservationRepo(data)

	ctx := context.Background()
	_, err := repo.GetReservation(ctx, "nonexistent")

	assert.Error(t, err)
	assert.ErrorIs(t, err, biz.ErrReservationNotFound)
}

func TestReservationRepo_UpdateReservationStatus(t *testing.T) {
	db := setupReservationTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	// 插入测试数据
	now := time.Now()
	err := db.Exec(`
		INSERT INTO billing_reservations (reservation_id, user_id, request_id, amount, status, model, channel_id, created_at, updated_at, expired_at)
		VALUES ('res_test_001', 'user1', 'req_test_001', 100, 'reserved', 'gpt-4o-mini', 'channel1', ?, ?, ?)
	`, now, now, now.Add(5*time.Minute)).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewReservationRepo(data)

	ctx := context.Background()
	err = repo.UpdateReservationStatus(ctx, "res_test_001", "committed")
	require.NoError(t, err)

	// 验证状态已更新
	var model reservationModel
	err = db.Where("reservation_id = ?", "res_test_001").First(&model).Error
	require.NoError(t, err)
	assert.Equal(t, "committed", model.Status)
}

func TestReservationRepo_GetExpiredReservations(t *testing.T) {
	db := setupReservationTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	now := time.Now()
	// 插入测试数据：一个已过期，一个未过期
	err := db.Exec(`
		INSERT INTO billing_reservations (reservation_id, user_id, request_id, amount, status, model, channel_id, created_at, updated_at, expired_at)
		VALUES
			('res_expired', 'user1', 'req_expired', 100, 'reserved', 'gpt-4o-mini', 'channel1', ?, ?, ?),
			('res_active', 'user1', 'req_active', 200, 'reserved', 'gpt-4o-mini', 'channel1', ?, ?, ?)
	`, now.Add(-10*time.Minute), now.Add(-10*time.Minute), now.Add(-5*time.Minute),
		now, now, now.Add(5*time.Minute)).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewReservationRepo(data)

	ctx := context.Background()
	expired, err := repo.GetExpiredReservations(ctx)

	require.NoError(t, err)
	assert.Len(t, expired, 1)
	assert.Equal(t, "res_expired", expired[0].ReservationID)
}

func TestReservationRepo_GetExpiredReservations_Empty(t *testing.T) {
	db := setupReservationTestDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	now := time.Now()
	// 插入测试数据：只有未过期的
	err := db.Exec(`
		INSERT INTO billing_reservations (reservation_id, user_id, request_id, amount, status, model, channel_id, created_at, updated_at, expired_at)
		VALUES ('res_active', 'user1', 'req_active', 200, 'reserved', 'gpt-4o-mini', 'channel1', ?, ?, ?)
	`, now, now, now.Add(5*time.Minute)).Error
	require.NoError(t, err)

	data := &Data{db: db}
	repo := NewReservationRepo(data)

	ctx := context.Background()
	expired, err := repo.GetExpiredReservations(ctx)

	require.NoError(t, err)
	assert.Len(t, expired, 0)
}