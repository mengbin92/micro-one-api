package xdb_test

import (
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"micro-one-api/platform/database/xdb"
)

// TestSQLiteWALConcurrentWriterKeepsRMWTransactionAlive is the regression
// test for the v0.30.0 routing acceptance (sqlite3) failure
// "failed to mark payment order paid: assign subscription after payment:
// database is locked".
//
// Shape of the production bug: on the shared-file SQLite topology (lite
// compose + routing e2e) every service writes the same database file, and
// billing's MarkPaymentOrderPaid runs one gorm Transaction that reads
// payment_orders, grants the subscription, then writes payment_orders. With
// DEFERRED transactions the read takes a WAL snapshot and the first write
// upgrades it; a concurrent writer committing in between makes the upgrade
// fail immediately with SQLITE_BUSY_SNAPSHOT ("database is locked"), which
// busy_timeout cannot resolve. The fix (withSQLite3Pragmas appending
// _txlock=immediate) makes BEGIN acquire the write lock up front, so the
// snapshot can never go stale — the concurrent writer queues on its busy
// timeout instead of poisoning the transaction.
//
// Handle A mirrors the deployment DSN. Handle B is any other service writing
// the same file; its short busy timeout only keeps this test fast — B's
// outcome is deliberately not asserted, only A's transaction must survive.
func TestSQLiteWALConcurrentWriterKeepsRMWTransactionAlive(t *testing.T) {
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "oneapi.db")
	dsnA := "file:" + dbFile + "?_busy_timeout=10000&_journal_mode=WAL"
	dsnB := "file:" + dbFile + "?_busy_timeout=50&_journal_mode=WAL"

	dbA, err := xdb.Open(xdb.DatabaseConfig{Driver: xdb.DriverSQLite3, DSN: dsnA})
	if err != nil {
		t.Fatalf("open A: %v", err)
	}
	sqlA, _ := dbA.DB()
	t.Cleanup(func() { _ = sqlA.Close() })

	dbB, err := xdb.Open(xdb.DatabaseConfig{Driver: xdb.DriverSQLite3, DSN: dsnB})
	if err != nil {
		t.Fatalf("open B: %v", err)
	}
	sqlB, _ := dbB.DB()
	t.Cleanup(func() { _ = sqlB.Close() })

	if err := dbA.Exec("CREATE TABLE IF NOT EXISTS t (id INTEGER PRIMARY KEY, v TEXT)").Error; err != nil {
		t.Fatalf("create table: %v", err)
	}

	// The exact MarkOrderPaid interleave: A reads inside its transaction, a
	// concurrent writer commits, then A performs its first write.
	txErr := dbA.Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Raw("SELECT COUNT(*) FROM t").Scan(&n).Error; err != nil {
			t.Fatalf("A read: %v", err)
		}
		if err := dbB.Exec("INSERT INTO t (v) VALUES ('concurrent-writer')").Error; err != nil {
			t.Logf("concurrent writer B (outcome not asserted): %v", err)
		}
		return tx.Exec("INSERT INTO t (v) VALUES ('payment-tx')").Error
	})
	if txErr != nil {
		t.Fatalf("read-modify-write transaction poisoned by concurrent writer (CI failure): %v", txErr)
	}

	var rows int64
	if err := dbA.Raw("SELECT COUNT(*) FROM t").Scan(&rows).Error; err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rows != 1 {
		t.Fatalf("expected exactly A's row committed (B either queued or failed), got %d rows", rows)
	}
}

// TestOpenSQLite3DoesNotDowngradeDSNBusyTimeout guards the companion fix:
// when the deployment DSN pins a busy timeout (the routing e2e sets 10s),
// xdb must not reset it to the 5s default on the initial pooled connection.
func TestOpenSQLite3DoesNotDowngradeDSNBusyTimeout(t *testing.T) {
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "oneapi.db") + "?_busy_timeout=10000&_journal_mode=WAL"
	db, err := xdb.Open(xdb.DatabaseConfig{Driver: xdb.DriverSQLite3, DSN: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })

	var got int
	if err := sqlDB.QueryRow("PRAGMA busy_timeout").Scan(&got); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if got != 10000 {
		t.Fatalf("busy_timeout=%d, want 10000 (DSN value must not be downgraded)", got)
	}
}
