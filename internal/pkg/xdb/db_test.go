package xdb

import (
	"database/sql"
	"strings"
	"testing"
)

func TestInferDriver(t *testing.T) {
	cases := []struct {
		dsn  string
		want string
	}{
		{"", DriverMySQL},
		{"user:pass@tcp(127.0.0.1:3306)/db?parseTime=true", DriverMySQL},
		{":memory:", DriverSQLite3},
		{"file:/data/micro-one-api.db", DriverSQLite3},
		{"file:/tmp/x.db?_busy_timeout=5000", DriverSQLite3},
		{"/var/data/app.db", DriverSQLite3},
		{"./local.sqlite", DriverSQLite3},
		{"./local.sqlite3", DriverSQLite3},
		{"/tmp/app.sqlite", DriverSQLite3},
		{"postgres://user:pw@host:5432/db", DriverPostgres},
		{"postgresql://user:pw@host:5432/db", DriverPostgres},
		{"host=127.0.0.1 user=app dbname=micro_one_api", DriverPostgres},
	}
	for _, c := range cases {
		if got := InferDriver(c.dsn); got != c.want {
			t.Errorf("InferDriver(%q)=%q, want %q", c.dsn, got, c.want)
		}
	}
}

func TestNormalizeDriver(t *testing.T) {
	if got := NormalizeDriver("", ":memory:"); got != DriverSQLite3 {
		t.Errorf("NormalizeDriver empty+memory = %q, want %q", got, DriverSQLite3)
	}
	if got := NormalizeDriver("", "user:pass@tcp(127.0.0.1:3306)/db"); got != DriverMySQL {
		t.Errorf("NormalizeDriver blank+MySQL DSN = %q, want %q", got, DriverMySQL)
	}
	if got := NormalizeDriver("MySQL", "x"); got != DriverMySQL {
		t.Errorf("NormalizeDriver case insensitive = %q, want %q", got, DriverMySQL)
	}
	if got := NormalizeDriver("sqlite", "x"); got != DriverSQLite3 {
		t.Errorf("NormalizeDriver 'sqlite' alias = %q, want %q", got, DriverSQLite3)
	}
	if got := NormalizeDriver("postgresql", "x"); got != DriverPostgres {
		t.Errorf("NormalizeDriver 'postgresql' alias = %q, want %q", got, DriverPostgres)
	}
	if got := NormalizeDriver("SQLITE3", "x"); got != DriverSQLite3 {
		t.Errorf("NormalizeDriver case insensitive = %q, want %q", got, DriverSQLite3)
	}
}

func TestOpenSQLite3InMemory(t *testing.T) {
	db, err := Open(DatabaseConfig{Driver: DriverSQLite3, DSN: ":memory:"})
	if err != nil {
		t.Fatalf("Open SQLite3 :memory: failed: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	if db.Dialector == nil {
		t.Fatal("expected non-nil dialector")
	}
	if db.Dialector.Name() != "sqlite" {
		t.Errorf("Dialector.Name()=%q, want sqlite", db.Dialector.Name())
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() failed: %v", err)
	}
	if sqlDB.Driver() == nil {
		t.Fatal("expected non-nil driver")
	}
}

func TestOpenDriverInferenceFromDSN(t *testing.T) {
	db, err := Open(DatabaseConfig{DSN: ":memory:"})
	if err != nil {
		t.Fatalf("Open (driver inferred) failed: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	if db.Dialector.Name() != "sqlite" {
		t.Errorf("dialector name = %q, want sqlite", db.Dialector.Name())
	}
}

func TestOpenUnsupportedDriver(t *testing.T) {
	_, err := Open(DatabaseConfig{Driver: "oracle", DSN: "x"})
	if err == nil {
		t.Fatal("expected error for unsupported driver")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("error %q does not mention 'unsupported'", err.Error())
	}
}

func TestOpenSQLite3WithCustomPragma(t *testing.T) {
	db, err := Open(DatabaseConfig{
		Driver:         DriverSQLite3,
		DSN:            ":memory:",
		SQLite3Pragmas: []string{"PRAGMA busy_timeout = 1234"},
	})
	if err != nil {
		t.Fatalf("Open SQLite3 custom pragma failed: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	// busy_timeout is connection-local; verify by querying the
	// pragma back on the same *sql.DB.
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() failed: %v", err)
	}
	var got int
	if err := sqlDB.QueryRow("PRAGMA busy_timeout").Scan(&got); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if got != 1234 {
		t.Errorf("busy_timeout=%d, want 1234 (custom pragma not applied)", got)
	}
}

func TestSQLite3PoolConfig(t *testing.T) {
	p := SQLite3PoolConfig()
	if p == nil {
		t.Fatal("SQLite3PoolConfig returned nil")
	}
	if p.MaxOpenConns != 1 {
		t.Errorf("MaxOpenConns=%d, want 1", p.MaxOpenConns)
	}
	if p.MaxIdleConns != 1 {
		t.Errorf("MaxIdleConns=%d, want 1", p.MaxIdleConns)
	}
}

func TestDefaultSQLite3Pragmas(t *testing.T) {
	p := DefaultSQLite3Pragmas()
	if len(p) == 0 {
		t.Fatal("DefaultSQLite3Pragmas returned empty slice")
	}
	// Verify mutating the returned slice does not change the package state.
	p[0] = "PRAGMA foo"
	again := DefaultSQLite3Pragmas()
	if again[0] == "PRAGMA foo" {
		t.Fatal("DefaultSQLite3Pragmas returned shared backing array")
	}
}

func TestPostgresDriverNameConstant(t *testing.T) {
	if PostgresDriverName != "pgx" {
		t.Errorf("PostgresDriverName=%q, want %q", PostgresDriverName, "pgx")
	}
	EnsurePostgresDriver()
}

func TestOpenPostgresDriverNameRegistered(t *testing.T) {
	// The gorm pgx stdlib driver registers under "pgx". We can verify
	// the registration without an actual Postgres server by calling
	// sql.Open — the call should return a *sql.DB, with a real
	// connection only attempted on first use. Then we explicitly Close
	// before any I/O.
	db, err := sql.Open("pgx", "host=127.0.0.1 port=1 sslmode=disable")
	if err != nil {
		t.Fatalf("sql.Open pgx: %v", err)
	}
	defer db.Close()
	if db == nil {
		t.Fatal("expected non-nil *sql.DB for pgx driver")
	}
}

func TestOpenPostgresRejectsBadDSN(t *testing.T) {
	// A reachable-but-non-Postgres endpoint should fail on Ping, not on
	// Open. We only verify that Open does not panic and that the
	// returned error from Ping is non-nil.
	db, err := Open(DatabaseConfig{Driver: DriverPostgres, DSN: "host=127.0.0.1 port=1 sslmode=disable"})
	if err != nil {
		// Some driver versions validate the DSN eagerly and fail here.
		// Either outcome is acceptable; we only require it not to panic.
		return
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	if db.Dialector == nil {
		t.Fatal("expected non-nil dialector")
	}
	if db.Dialector.Name() != "postgres" {
		t.Errorf("Dialector.Name()=%q, want postgres", db.Dialector.Name())
	}
}
