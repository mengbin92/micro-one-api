package xdb_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"micro-one-api/platform/database/xdb"
)

// openSharedSQLite opens two independent xdb handles to the same SQLite file,
// mirroring the shared-file lite/e2e topology where every service holds its
// own single-connection pool on one database. Handle A is the service under
// test; handle B is any other service writing concurrently.
func openSharedSQLite(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	dbFile := filepath.Join(t.TempDir(), "oneapi.db")
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
	return dbA, dbB
}

// TestRetryTxOnBusyRecoversFromStaleSnapshot is the regression test for the
// v0.30.0 routing acceptance (sqlite3) failure "failed to mark payment order
// paid: assign subscription after payment: database is locked".
//
// Shape of the production bug: gorm Transaction() begins DEFERRED, so a
// read-then-write transaction (billing MarkOrderPaid: SELECT payment_orders
// -> grant subscription -> UPDATE payment_orders) takes a WAL read snapshot
// and upgrades it at the first write. When another connection commits in that
// window, SQLite fails the upgrade immediately with SQLITE_BUSY_SNAPSHOT
// ("database is locked"); busy_timeout cannot resolve a stale snapshot. The
// fix retries the whole transaction: attempt 1 is poisoned and rolls back,
// attempt 2 re-reads a fresh snapshot and succeeds.
//
// The poison is deterministic — B commits exactly once, between A's read and
// A's write on the first attempt — so this test is red without RetryTxOnBusy
// and green with it.
func TestRetryTxOnBusyRecoversFromStaleSnapshot(t *testing.T) {
	dbA, dbB := openSharedSQLite(t)
	poisoned := false

	err := xdb.RetryTxOnBusy(context.Background(), dbA, 3, func(tx *gorm.DB) error {
		var n int64
		if err := tx.Raw("SELECT COUNT(*) FROM t").Scan(&n).Error; err != nil {
			t.Fatalf("A read: %v", err)
		}
		if !poisoned {
			poisoned = true
			// Concurrent cross-service commit inside A's read-to-write
			// window: stales A's WAL snapshot.
			if err := dbB.Exec("INSERT INTO t (v) VALUES ('concurrent-writer')").Error; err != nil {
				t.Fatalf("poison write: %v", err)
			}
		}
		return tx.Exec("INSERT INTO t (v) VALUES ('payment-tx')").Error
	})
	if err != nil {
		t.Fatalf("read-then-write transaction must recover via retry (CI failure): %v", err)
	}

	var rows int64
	if err := dbA.Raw("SELECT COUNT(*) FROM t").Scan(&rows).Error; err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rows != 2 {
		t.Fatalf("expected the concurrent writer's row and A's row, got %d rows", rows)
	}
}

// TestRetryTxOnBusyExhaustsAttempts verifies the retry gives up after the
// configured attempts when contention persists, surfacing the underlying
// error rather than looping forever.
func TestRetryTxOnBusyExhaustsAttempts(t *testing.T) {
	dbA, dbB := openSharedSQLite(t)
	attempts := 0

	err := xdb.RetryTxOnBusy(context.Background(), dbA, 3, func(tx *gorm.DB) error {
		attempts++
		var n int64
		if err := tx.Raw("SELECT COUNT(*) FROM t").Scan(&n).Error; err != nil {
			t.Fatalf("A read: %v", err)
		}
		// Poison every attempt: contention never clears.
		if err := dbB.Exec("INSERT INTO t (v) VALUES ('writer')").Error; err != nil {
			t.Fatalf("poison write: %v", err)
		}
		return tx.Exec("INSERT INTO t (v) VALUES ('payment-tx')").Error
	})
	if err == nil {
		t.Fatal("expected the busy error to surface after exhausting attempts")
	}
	if !xdb.IsSQLiteBusy(err) {
		t.Fatalf("expected a busy error, got: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected exactly 3 attempts, got %d", attempts)
	}
}

// TestIsSQLiteBusy pins the busy-detection the retry relies on: the driver
// error and wrapped string forms, plus non-busy control errors.
func TestIsSQLiteBusy(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("something else"), false},
		{"wrapped locked string", errors.New("assign subscription after payment: database is locked"), true},
		{"wrapped busy string", errors.New("failed to mark payment order paid: database is locked"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := xdb.IsSQLiteBusy(tc.err); got != tc.want {
				t.Fatalf("IsSQLiteBusy(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}

	// Real driver error: drive an actual SQLITE_BUSY_SNAPSHOT through two
	// handles on one file.
	dbA, dbB := openSharedSQLite(t)
	var real error
	_ = dbA.Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Raw("SELECT COUNT(*) FROM t").Scan(&n).Error; err != nil {
			return err
		}
		if err := dbB.Exec("INSERT INTO t (v) VALUES ('b')").Error; err != nil {
			return err
		}
		real = tx.Exec("INSERT INTO t (v) VALUES ('a')").Error
		return real
	})
	if real == nil {
		t.Fatal("expected the un-retried transaction to fail with a stale snapshot")
	}
	if !xdb.IsSQLiteBusy(real) {
		t.Fatalf("IsSQLiteBusy must detect the real driver error, got: %v", real)
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
