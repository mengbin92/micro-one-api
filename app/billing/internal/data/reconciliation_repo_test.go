package data

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/pkg/jsonx"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestGetLedgerConsumeSummaryCollapsesDualTrackRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&ledgerModel{}))
	referenceID := "res-mixed"
	require.NoError(t, db.Create([]*ledgerModel{
		{Type: biz.LedgerTypeConsume, ReferenceID: &referenceID, Quota: 123, ChannelID: 7, SourceKind: biz.CostSourceChannel, UpstreamCost: 45, CostSource: biz.CostSourceSubscription},
		{Type: biz.LedgerTypeConsume, ReferenceID: &referenceID, Quota: 123, ChannelID: 7, SourceKind: biz.CostSourceChannel, CostSource: biz.CostSourceBalance},
	}).Error)

	repo := NewReconciliationRepo(&Data{db: db})
	summary, err := repo.GetLedgerConsumeSummary(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(1), summary.Count)
	require.Equal(t, int64(123), summary.Quota)
	channels, err := repo.SumConsumeLedgerUsageByChannel(context.Background())
	require.NoError(t, err)
	require.Len(t, channels, 1)
	require.Equal(t, int64(123), channels[0].Quota)
	require.Equal(t, int64(45), channels[0].UpstreamCost)
}

func TestSumSubscriptionWindowCostsJoinsFrozenCharges(t *testing.T) {
	for _, tc := range []struct{ name, driver, frozenCollation string }{
		{name: "sqlite", driver: "sqlite"},
		{name: "mysql_fresh", driver: "mysql", frozenCollation: "utf8mb4_0900_ai_ci"},
		{name: "mysql_upgraded", driver: "mysql", frozenCollation: "utf8mb4_unicode_ci"},
		{name: "postgres", driver: "postgres"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, tc.driver)
			var query string
			var vars []any
			var metadataReads int
			if tc.driver == "mysql" {
				require.NoError(t, db.Exec("ALTER TABLE billing_ledgers MODIFY reference_id VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci").Error)
				require.NoError(t, db.Exec("ALTER TABLE billing_reservations MODIFY reservation_id VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL").Error)
				require.NoError(t, db.Exec("ALTER TABLE subscription_window_charges MODIFY reservation_id VARCHAR(64) CHARACTER SET utf8mb4 COLLATE "+tc.frozenCollation+" NOT NULL").Error)
				require.NoError(t, db.Callback().Row().After("gorm:row").Register("test:capture_window_costs", func(tx *gorm.DB) {
					if strings.Contains(tx.Statement.SQL.String(), "information_schema.COLUMNS") {
						metadataReads++
					}
					if sql := tx.Statement.SQL.String(); strings.Contains(sql, "LEFT JOIN subscription_window_charges w") {
						query, vars = sql, append([]any(nil), tx.Statement.Vars...)
					}
				}))
			}

			const subscriptionID, quotaPolicyID, start = 42, 7, 1_700_000_000
			frozenID, legacyID := "frozen", "legacy"
			require.NoError(t, db.Create([]reservationModel{
				{ReservationID: frozenID, RequestID: frozenID, UserID: "1", SubscriptionID: subscriptionID, Status: biz.ReservationStatusCommitted},
				{ReservationID: legacyID, RequestID: legacyID, UserID: "1", SubscriptionID: subscriptionID, Status: biz.ReservationStatusCommitted},
			}).Error)
			createdAt := time.Unix(start+1, 0).UTC()
			require.NoError(t, db.Create([]ledgerModel{
				{UserID: "1", ReferenceID: &frozenID, Type: biz.LedgerTypeConsume, CostSource: biz.CostSourceSubscription, SubscriptionCost: 200, LedgerDedupeKey: frozenID, CreatedAt: createdAt},
				{UserID: "1", ReferenceID: &legacyID, Type: biz.LedgerTypeConsume, CostSource: biz.CostSourceSubscription, SubscriptionCost: 30, LedgerDedupeKey: legacyID, CreatedAt: createdAt},
			}).Error)
			require.NoError(t, db.Table("subscription_window_charges").Create(map[string]any{
				"reservation_id": frozenID, "subscription_id": subscriptionID, "quota_policy_id": quotaPolicyID,
				"daily_window_start": start, "weekly_window_start": start, "monthly_window_start": start,
				"accounting_usd": 0.125, "created_at": createdAt.Unix(),
			}).Error)
			if tc.driver == "mysql" {
				otherCharges := make([]map[string]any, 512)
				for i := range otherCharges {
					otherCharges[i] = map[string]any{
						"reservation_id": fmt.Sprintf("other-%d", i), "subscription_id": subscriptionID + 1,
						"quota_policy_id": quotaPolicyID, "daily_window_start": start,
						"weekly_window_start": start, "monthly_window_start": start,
						"accounting_usd": 1, "created_at": createdAt.Unix(),
					}
				}
				require.NoError(t, db.Table("subscription_window_charges").Create(&otherCharges).Error)
			}

			repo := NewReconciliationRepo(&Data{db: db})
			if tc.driver == "mysql" {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				_, err := repo.SumSubscriptionWindowCosts(ctx, subscriptionID, quotaPolicyID, "daily", start)
				require.ErrorIs(t, err, context.Canceled)
				metadataReads = 0
			}
			costs, err := repo.SumSubscriptionWindowCosts(context.Background(), subscriptionID, quotaPolicyID, "daily", start)
			require.NoError(t, err)
			require.Equal(t, biz.SubscriptionWindowCosts{LedgerCost: 230, LegacyCost: 30, FrozenAccountingUSD: 0.125}, costs)
			if tc.driver == "mysql" {
				requireFrozenChargePrimaryKey(t, db, query, vars)
				weekCosts, err := repo.SumSubscriptionWindowCosts(context.Background(), subscriptionID, quotaPolicyID, "weekly", start)
				require.NoError(t, err)
				require.Equal(t, costs, weekCosts)
				require.Equal(t, 1, metadataReads, "reuse the successful MySQL metadata lookup across windows")
			}
		})
	}
}

func requireFrozenChargePrimaryKey(t *testing.T, db *gorm.DB, query string, vars []any) {
	t.Helper()
	require.NotEmpty(t, query)
	conn, err := db.DB()
	require.NoError(t, err)
	var explanation []byte
	require.NoError(t, conn.QueryRow("EXPLAIN FORMAT=JSON "+query, vars...).Scan(&explanation))
	var plan struct {
		QueryBlock struct {
			NestedLoop []struct {
				Table struct {
					Name       string `json:"table_name"`
					Key        string `json:"key"`
					AccessType string `json:"access_type"`
				} `json:"table"`
			} `json:"nested_loop"`
		} `json:"query_block"`
	}
	require.NoError(t, jsonx.Unmarshal(explanation, &plan))
	for _, step := range plan.QueryBlock.NestedLoop {
		if step.Table.Name == "w" {
			require.Equal(t, "eq_ref", step.Table.AccessType, "frozen-charge table access type")
			require.Equal(t, "PRIMARY", step.Table.Key)
			return
		}
	}
	t.Fatal("missing frozen-charge table in MySQL execution plan")
}

func TestListRefundReconciliationsPairsTradeAndUserAndFindsOrphans(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			if driver == "mysql" {
				// Reproduce the collations on upgraded production tables.
				require.NoError(t, db.Exec("ALTER TABLE payment_orders MODIFY trade_no VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL, MODIFY user_id VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL").Error)
				require.NoError(t, db.Exec("ALTER TABLE billing_ledgers MODIFY reference_id VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci, MODIFY user_id VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL").Error)
			}
			testRefundReconciliations(t, db)
		})
	}
}

func testRefundReconciliations(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Create([]*PaymentOrder{
		{UserID: "1", TradeNo: "matched", Status: biz.PaymentOrderStatusRefunded, AssetType: biz.PaymentAssetTypeSubscription, MoneyCents: 10000, PlanSnapshot: "frozen-snapshot"},
		{UserID: "2", TradeNo: "wrong-user", Status: biz.PaymentOrderStatusRefunded, AssetType: biz.PaymentAssetTypeSubscription, MoneyCents: 20000},
		{UserID: "3", TradeNo: "duplicate", Status: biz.PaymentOrderStatusRefunded, AssetType: biz.PaymentAssetTypeSubscription, MoneyCents: 30000},
		{UserID: "4", TradeNo: "paid", Status: biz.PaymentOrderStatusPaid, AssetType: biz.PaymentAssetTypeSubscription, MoneyCents: 40000},
	}).Error)
	ledger := func(userID, referenceID string, amount int64) *ledgerModel {
		return &ledgerModel{UserID: userID, ReferenceID: &referenceID, Type: biz.LedgerTypeRefund, CostSource: biz.CostSourceReversal, Amount: amount, LedgerDedupeKey: fmt.Sprintf("%s:%s:%d", userID, referenceID, amount)}
	}
	require.NoError(t, db.Create([]*ledgerModel{
		ledger("1", "matched", 100), ledger("99", "wrong-user", 200),
		ledger("3", "duplicate", 100), ledger("3", "duplicate", 200),
		ledger("4", "paid", 400), ledger("5", "missing-order", 500),
		{UserID: "1", Type: biz.LedgerTypeRefund, CostSource: biz.CostSourceBalance, Amount: 999, LedgerDedupeKey: "unrelated-refund"},
	}).Error)
	rows, err := NewReconciliationRepo(&Data{db: db}).ListRefundReconciliations(context.Background())
	require.NoError(t, err)
	require.Len(t, rows, 6)
	byKey := map[string]*biz.RefundReconciliation{}
	for _, row := range rows {
		byKey[row.TradeNo+":"+row.UserID] = row
	}
	require.True(t, byKey["matched:1"].RefundedOrder)
	require.Equal(t, "frozen-snapshot", byKey["matched:1"].PlanSnapshot)
	require.Equal(t, "CNY", byKey["matched:1"].Currency)
	require.Equal(t, int64(100), byKey["matched:1"].ReversalLedgerAmount)
	require.Zero(t, byKey["wrong-user:2"].ReversalLedgerCount)
	require.False(t, byKey["wrong-user:99"].RefundedOrder)
	require.Equal(t, int64(200), byKey["wrong-user:99"].ReversalLedgerAmount)
	require.Equal(t, int64(2), byKey["duplicate:3"].ReversalLedgerCount)
	require.Equal(t, int64(300), byKey["duplicate:3"].ReversalLedgerAmount)
	require.False(t, byKey["paid:4"].RefundedOrder)
	require.False(t, byKey["missing-order:5"].RefundedOrder)
}
