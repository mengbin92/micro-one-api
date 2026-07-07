package data

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupOperationReportDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`
		CREATE TABLE payment_orders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT,
			trade_no TEXT,
			asset_type TEXT,
			status TEXT,
			plan_id INTEGER,
			group_id INTEGER,
			money_cents INTEGER,
			created_at DATETIME
		)
	`).Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE user_subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			status TEXT,
			group_id INTEGER,
			metadata TEXT
		)
	`).Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE billing_reservations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			reservation_id TEXT,
			subscription_id INTEGER
		)
	`).Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE billing_ledgers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT,
			type TEXT,
			reference_id TEXT,
			cost_source TEXT,
			subscription_cost INTEGER,
			balance_cost INTEGER,
			created_at DATETIME
		)
	`).Error)
	return db
}

func TestOperationReportRepo_AggregatePaymentOrdersByPlan(t *testing.T) {
	db := setupOperationReportDB(t)
	repo := NewOperationReportRepo(&Data{db: db})
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	require.NoError(t, db.Exec(`
		INSERT INTO payment_orders (user_id, trade_no, asset_type, status, plan_id, group_id, money_cents, created_at) VALUES
		('1', 'prior', 'subscription', 'paid', 7, 3, 10000, ?),
		('1', 'renew', 'subscription', 'paid', 7, 3, 20000, ?),
		('2', 'new', 'subscription', 'paid', 7, 3, 30000, ?),
		('2', 'renew2', 'subscription', 'paid', 7, 3, 40000, ?),
		('3', 'refund', 'subscription', 'refunded', 7, 3, 50000, ?)
	`, start.Add(-time.Hour), start.Add(time.Hour), start.Add(2*time.Hour), start.Add(3*time.Hour), start.Add(4*time.Hour)).Error)

	rows, err := repo.AggregatePaymentOrdersByPlan(context.Background(), start, end, 0, 0, "")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int64(1), rows[0].NewPurchaseCount)
	require.Equal(t, int64(2), rows[0].RenewalCount)
	require.Equal(t, int64(1), rows[0].RefundCount)
	require.Equal(t, int64(900), rows[0].RevenueQuota)
	require.Equal(t, int64(500), rows[0].RefundedQuota)
}

func TestOperationReportRepo_AttributesSubscriptionsAndUsageByPaymentTradeNo(t *testing.T) {
	db := setupOperationReportDB(t)
	repo := NewOperationReportRepo(&Data{db: db})
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	require.NoError(t, db.Exec(`
		INSERT INTO payment_orders (user_id, trade_no, asset_type, status, plan_id, group_id, money_cents, created_at) VALUES
		('42', 'trade-a', 'subscription', 'paid', 7, 3, 10000, ?),
		('42', 'trade-b', 'subscription', 'paid', 8, 3, 10000, ?)
	`, now, now).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO user_subscriptions (id, status, group_id, metadata) VALUES
		(101, 'active', 3, '{"payment_trade_no":"trade-a"}'),
		(102, 'expired', 3, '{"payment_trade_no":"trade-b"}')
	`).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO billing_reservations (reservation_id, subscription_id) VALUES
		('res-a', 101),
		('res-b', 102)
	`).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO billing_ledgers (user_id, type, reference_id, cost_source, subscription_cost, balance_cost, created_at) VALUES
		('42', 'consume', 'res-a', 'subscription', 100, 0, ?),
		('42', 'consume', 'res-a', 'balance', 0, 20, ?),
		('42', 'consume', 'res-b', 'subscription', 200, 0, ?),
		('42', 'consume', 'res-b', 'balance', 0, 40, ?)
	`, now, now, now, now).Error)

	active, expired, revoked, err := repo.CountSubscriptionsByStatus(context.Background(), 0, 3)
	require.NoError(t, err)
	require.Equal(t, int64(1), active[7])
	require.Equal(t, int64(1), expired[8])
	require.Zero(t, revoked[7])

	subUsage, balanceFallback, err := repo.AggregateUsageFallbackByPlan(context.Background(), now.Add(-time.Hour), now.Add(time.Hour), 0, 3, "42")
	require.NoError(t, err)
	require.Equal(t, int64(100), subUsage[7])
	require.Equal(t, int64(20), balanceFallback[7])
	require.Equal(t, int64(200), subUsage[8])
	require.Equal(t, int64(40), balanceFallback[8])
}
