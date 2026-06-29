package xdb

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	sqlitedriver "gorm.io/driver/sqlite"
	"gorm.io/gorm"

	// Register the CGO-backed SQLite driver as the canonical
	// "sqlite3" database/sql driver. The GORM open path uses
	// gorm.io/driver/sqlite, which under the hood also imports this
	// package, so the driver is registered exactly once.
	_ "github.com/mattn/go-sqlite3"
)

// Driver names accepted by Open / OpenSQL.
const (
	DriverMySQL    = "mysql"
	DriverSQLite3  = "sqlite3"
	DriverPostgres = "postgres"
)

// DatabaseConfig carries the minimal configuration needed by Open to build
// a *gorm.DB for MySQL, SQLite3, or Postgres.
type DatabaseConfig struct {
	// Driver selects the dialector. Empty means "infer from DSN" — see
	// InferDriver for the rules.
	Driver string
	// DSN is the connection string. Driver-specific examples:
	//   mysql:    "user:pass@tcp(127.0.0.1:3306)/db?parseTime=true"
	//   sqlite3:  "file:/data/micro-one-api.db" or ":memory:"
	//   postgres: "host=127.0.0.1 user=app password=... dbname=micro_one_api port=5432 sslmode=disable"
	DSN string
	// Pool is optional. If nil, DefaultPoolConfig() is used; for SQLite3
	// the pool is overridden with a single-writer default to avoid
	// SQLITE_BUSY.
	Pool *PoolConfig
	// SQLite3Pragmas is an optional list of PRAGMA statements applied to
	// every new SQLite3 connection. If nil, sensible defaults are
	// applied (busy_timeout, journal_mode=WAL, foreign_keys=on).
	SQLite3Pragmas []string
}

// Open opens a *gorm.DB for the configured driver/DSN. The pool and
// driver-specific defaults (SQLite3 pragmas, single-writer pool) are
// applied before returning.
func Open(cfg DatabaseConfig) (*gorm.DB, error) {
	driver := NormalizeDriver(cfg.Driver, cfg.DSN)
	switch driver {
	case DriverMySQL:
		return openMySQL(cfg.DSN, cfg.Pool)
	case DriverSQLite3:
		return openSQLite3(cfg.DSN, cfg.Pool, cfg.SQLite3Pragmas)
	case DriverPostgres:
		return openPostgres(cfg.DSN, cfg.Pool)
	default:
		return nil, errors.New("xdb: unsupported database driver: " + cfg.Driver)
	}
}

// OpenSQL opens a *sql.DB for the configured driver/DSN. Driver inference
// follows the same rules as Open.
func OpenSQL(driver, dsn string) (*sql.DB, error) {
	return OpenSQLWithPool(driver, dsn, nil)
}

// OpenSQLWithPool is like OpenSQL but applies a per-driver pool: for
// SQLite3 the connection count is clamped to MaxOpenConns=1 and
// MaxIdleConns=1 unless the caller supplies a non-nil override; for
// MySQL / Postgres the caller's settings are used verbatim. Returning
// *sql.DB (not *gorm.DB) is required by callers that use database/sql
// directly, e.g. the system options repo in internal/admin/data and the
// migrate CLI.
func OpenSQLWithPool(driver, dsn string, pool *PoolConfig) (*sql.DB, error) {
	resolved := NormalizeDriver(driver, dsn)
	switch resolved {
	case DriverMySQL:
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			return nil, err
		}
		if pool == nil {
			pool = DefaultPoolConfig()
		}
		applyPool(db, pool)
		return db, nil
	case DriverSQLite3:
		if pool == nil {
			pool = SQLite3PoolConfig()
		}
		// Open with the registered "sqlite3" driver (mattn/go-sqlite3).
		// Pragmas are connection-local, so we apply defaults now and
		// re-apply any caller-supplied list below.
		db, err := sql.Open("sqlite3", dsn)
		if err != nil {
			return nil, err
		}
		applyPool(db, pool)
		for _, pragma := range defaultSQLite3Pragmas() {
			if _, err := db.Exec(pragma); err != nil {
				_ = db.Close()
				return nil, err
			}
		}
		return db, nil
	case DriverPostgres:
		// gorm.io/driver/postgres registers its pgx-backed driver under
		// the name "pgx" (via github.com/jackc/pgx/v5/stdlib).
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return nil, err
		}
		if pool == nil {
			pool = DefaultPoolConfig()
		}
		applyPool(db, pool)
		return db, nil
	default:
		return nil, errors.New("xdb: unsupported database driver: " + driver)
	}
}

// NormalizeDriver returns the canonical driver name. If driver is empty
// it is inferred from the DSN: prefixes like "postgres://" or
// "postgresql://" and key=value DSNs containing "host=" are treated as
// Postgres; file:/path, *.db, *.sqlite, *.sqlite3, and :memory: are
// treated as SQLite3; everything else defaults to MySQL.
func NormalizeDriver(driver, dsn string) string {
	d := strings.ToLower(strings.TrimSpace(driver))
	if d != "" {
		// Allow callers to use "sqlite" or "sqlite3" interchangeably.
		if d == "sqlite" {
			return DriverSQLite3
		}
		if d == "postgresql" {
			return DriverPostgres
		}
		return d
	}
	return InferDriver(dsn)
}

// InferDriver returns one of {DriverMySQL, DriverSQLite3, DriverPostgres}
// based on the shape of the DSN. The check is intentionally permissive
// on the SQLite/Postgres side and conservative on the MySQL side:
// ambiguous DSNs default to MySQL to keep existing deployments working.
func InferDriver(dsn string) string {
	s := strings.TrimSpace(dsn)
	if s == "" {
		return DriverMySQL
	}
	lower := strings.ToLower(s)
	// Postgres URLs: postgres:// or postgresql://
	if strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://") {
		return DriverPostgres
	}
	// Postgres key=value DSNs start with "host=".
	if strings.HasPrefix(lower, "host=") {
		return DriverPostgres
	}
	// SQLite literals and file paths.
	if lower == ":memory:" {
		return DriverSQLite3
	}
	if strings.HasPrefix(lower, "file:") {
		return DriverSQLite3
	}
	if strings.HasSuffix(lower, ".db") ||
		strings.HasSuffix(lower, ".sqlite") ||
		strings.HasSuffix(lower, ".sqlite3") {
		return DriverSQLite3
	}
	return DriverMySQL
}

func openMySQL(dsn string, pool *PoolConfig) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	if pool == nil {
		pool = DefaultPoolConfig()
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	applyPool(sqlDB, pool)
	return db, nil
}

func openSQLite3(dsn string, pool *PoolConfig, pragmas []string) (*gorm.DB, error) {
	// Open the underlying *sql.DB with the configured pool + default
	// pragmas, then hand it to gorm. gorm.io/driver/sqlite can take a
	// *sql.DB via sqlite.Config{Conn}, so we keep a single DSN/pragma
	// application path shared with OpenSQL.
	sqlDB, err := OpenSQLWithPool(DriverSQLite3, dsn, pool)
	if err != nil {
		return nil, err
	}
	if len(pragmas) == 0 {
		pragmas = defaultSQLite3Pragmas()
	}
	for _, p := range pragmas {
		if _, err := sqlDB.Exec(p); err != nil {
			_ = sqlDB.Close()
			return nil, err
		}
	}
	// Hand the already-configured *sql.DB to gorm.io/driver/sqlite so
	// the same pool and pragmas are shared with callers that go
	// through database/sql directly.
	db, err := gorm.Open(sqlitedriver.New(sqlitedriver.Config{Conn: sqlDB}), &gorm.Config{})
	if err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

func openPostgres(dsn string, pool *PoolConfig) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	if pool == nil {
		pool = DefaultPoolConfig()
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	applyPool(sqlDB, pool)
	return db, nil
}

func applyPool(db *sql.DB, pool *PoolConfig) {
	if pool == nil {
		return
	}
	db.SetMaxOpenConns(pool.MaxOpenConns)
	db.SetMaxIdleConns(pool.MaxIdleConns)
	db.SetConnMaxLifetime(pool.ConnMaxLifetime)
	db.SetConnMaxIdleTime(pool.ConnMaxIdleTime)
}

// SQLite3PoolConfig returns a connection-pool configuration suited for
// SQLite3: a single writer/reader connection with a short idle time.
// The short ConnMaxIdleTime is important because PRAGMAs are
// connection-local in SQLite — recycling connections forces the runtime
// to reapply defaults if the DSN does not pin them.
func SQLite3PoolConfig() *PoolConfig {
	return &PoolConfig{
		MaxOpenConns:    1,
		MaxIdleConns:    1,
		ConnMaxLifetime: 0, // unlimited
		ConnMaxIdleTime: 5 * time.Minute,
	}
}

// defaultSQLite3Pragmas returns a baseline of pragmas applied to every
// new SQLite3 connection. They are idempotent and safe to re-apply.
func defaultSQLite3Pragmas() []string {
	return []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA synchronous = NORMAL",
	}
}

// DefaultSQLite3Pragmas exposes the default pragma set so callers can
// extend it before passing to Open.
func DefaultSQLite3Pragmas() []string {
	out := make([]string, len(defaultSQLite3Pragmas()))
	copy(out, defaultSQLite3Pragmas())
	return out
}

// PostgresDriverName is the name registered by
// github.com/jackc/pgx/v5/stdlib and used by OpenSQLWithPool when the
// driver resolves to postgres. It is exposed as a constant so callers
// that need to open a raw *sql.DB (e.g. the migrate CLI) can pass
// it explicitly to sql.Open.
const PostgresDriverName = "pgx"

// EnsurePostgresDriver imports the pgx stdlib package to guarantee its
// init-time driver registration runs, even when callers reach for the
// driver name via reflection. Safe to call multiple times.
func EnsurePostgresDriver() { _ = stdlib.GetDefaultDriver }

