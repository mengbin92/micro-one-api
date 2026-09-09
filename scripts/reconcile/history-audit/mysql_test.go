package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
)

// Opt-in storage-boundary smoke. Only a dedicated, already migrated scratch
// database named history_audit_test is accepted; never use a production DSN.
func TestMySQLSelectOnly(t *testing.T) {
	dsn := os.Getenv("HISTORY_AUDIT_TEST_ADMIN_DSN")
	if dsn == "" {
		t.Skip("set HISTORY_AUDIT_TEST_ADMIN_DSN for isolated MySQL smoke")
	}
	cfg, err := mysql.ParseDSN(dsn)
	require.NoError(t, err)
	require.Equal(t, "history_audit_test", cfg.DBName)
	admin, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { admin.Close() })
	var initial int
	require.NoError(t, admin.QueryRow("SELECT COUNT(*) FROM billing_ledgers").Scan(&initial))
	require.Zero(t, initial, "smoke requires an empty scratch ledger")
	t.Cleanup(func() {
		for _, table := range []string{"billing_ledgers", "billing_reservations", "billing_pricing_snapshots"} {
			_, _ = admin.Exec("DELETE FROM " + table)
		}
	})
	rows := fixture(t)
	s := rows[0].Evidence.Snapshot
	insertTestRow(t, admin, "billing_pricing_snapshots", map[string]any{
		"config_hash": rows[0].Evidence.PricingHash, "model_name": s.Model,
		"input_price": s.Input, "output_price": s.Output, "cache_read_price": s.Read,
		"cache_creation_5m_price": s.Write5m, "cache_creation_1h_price": s.Write1h,
		"group_ratio": s.Ratio, "cache_creation_mode": s.Mode, "snapshot_version": s.Version,
	})
	for _, l := range rows {
		raw, err := jsonx.Marshal(l.Evidence)
		require.NoError(t, err)
		var columns map[string]any
		require.NoError(t, jsonx.Unmarshal(raw, &columns))
		delete(columns, "snapshot")
		columns["id"], columns["type"], columns["user_id"] = l.ID, "consume", l.UserID
		columns["created_at"] = l.CreatedAt.Format("2006-01-02 15:04:05.000")
		columns["reference_id"], columns["ledger_dedupe_key"] = l.ReferenceID, l.DedupeKey
		columns["amount"], columns["cost_source"] = l.Amount, l.CostSource
		columns["balance_after"] = 0
		columns["subscription_cost"], columns["balance_cost"] = l.SubscriptionCost, l.BalanceCost
		columns["upstream_cost"], columns["cost_audit_status"] = l.UpstreamCost, l.CostAuditStatus
		for i, name := range []string{"prompt_cost", "cache_read_cost", "cache_creation_5m_cost", "cache_creation_1h_cost", "completion_cost"} {
			columns[name] = l.BucketCosts[i]
		}
		insertTestRow(t, admin, "billing_ledgers", columns)
	}
	insertTestRow(t, admin, "billing_reservations", map[string]any{
		"reservation_id": "subset-split", "user_id": "fixture-user", "subscription_id": 7,
		"request_id": "fixture-request",
		"amount":     110, "status": "committed", "actual_cost": 110,
		"created_at": "2026-08-01 00:00:00", "updated_at": "2026-08-01 00:00:00",
	})
	// A SELECT-only database identity, separate from the fixture seeding client.
	name := fmt.Sprintf("ha_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE USER '" + name + "'@'%' IDENTIFIED BY 'fixture-only'")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = admin.Exec("DROP USER '" + name + "'@'%'") })
	for _, table := range []string{"billing_ledgers", "billing_pricing_snapshots", "billing_reservations"} {
		_, err = admin.Exec("GRANT SELECT ON history_audit_test." + table + " TO '" + name + "'@'%'")
		require.NoError(t, err)
	}
	cfg.User, cfg.Passwd = name, "fixture-only"
	ro, err := sql.Open("mysql", cfg.FormatDSN())
	require.NoError(t, err)
	t.Cleanup(func() { ro.Close() })
	// An empty-target UPDATE demonstrates the grant boundary without touching
	// any data even if the permission expectation regresses.
	_, err = ro.Exec("UPDATE billing_ledgers SET amount = amount WHERE 1 = 0")
	var mysqlErr *mysql.MySQLError
	require.ErrorAs(t, err, &mysqlErr)
	require.EqualValues(t, 1142, mysqlErr.Number)
	t.Setenv("HISTORY_AUDIT_DSN", cfg.FormatDSN())
	args := []string{"-start", "2026-08-01T00:00:00Z", "-end", "2026-08-02T00:00:00Z"}
	var first, second, expected, stderr bytes.Buffer
	require.NoError(t, run(args, &first, &stderr))
	require.NoError(t, run(args, &second, &stderr))
	require.Equal(t, first.String(), second.String())
	reports, err := audit(rows)
	require.NoError(t, err)
	require.NoError(t, writeReport(&expected, "json", reports))
	require.Equal(t, expected.String(), first.String(), "real SQL projection matches offline evidence")
	// The half-open end excludes the second split ledger. The sibling count
	// leaves the request unknown, with no fabricated per-ledger delta.
	partial, err := readLedgers(context.Background(), ro, rows[0].CreatedAt, rows[1].CreatedAt)
	require.NoError(t, err)
	require.Len(t, partial, 1)
	r, err := audit(partial)
	require.NoError(t, err)
	require.Equal(t, "unknown", r[0].Classification)
	require.Nil(t, r[0].CanonicalDelta)
	var count int
	var charged int64
	require.NoError(t, admin.QueryRow("SELECT COUNT(*), SUM(-amount) FROM billing_ledgers").Scan(&count, &charged))
	require.Equal(t, len(rows), count)
	require.EqualValues(t, 413, charged)
}

func insertTestRow(t *testing.T, db *sql.DB, table string, columns map[string]any) {
	t.Helper()
	keys := make([]string, 0, len(columns))
	for key := range columns {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values, marks := make([]any, 0, len(keys)), make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, columns[key])
		marks = append(marks, "?")
	}
	_, err := db.Exec("INSERT INTO "+table+" (`"+strings.Join(keys, "`,`")+"`) VALUES ("+strings.Join(marks, ",")+")", values...)
	require.NoError(t, err)
}
