package xdb

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
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

	// Schema optionally isolates this connection to a specific database
	// schema. Phase 2.4 schema isolation.
	//
	//   MySQL:    rewritten into the DSN as the DBName (the schema IS the
	//             database in MySQL). Every connection therefore uses the
	//             per-service schema without any per-statement rewrite.
	//   Postgres: applied as "SET search_path TO <schema>" on each pooled
	//             connection (ConnMaxLifetime may recycle connections, so we
	//             hook the connector init).
	//   SQLite3:  ignored — SQLite schema isolation is achieved by pointing
	//             the DSN at a different file.
	Schema string
}

// Open opens a *gorm.DB for the configured driver/DSN. The pool and
// driver-specific defaults (SQLite3 pragmas, single-writer pool) are
// applied before returning.
func Open(cfg DatabaseConfig) (*gorm.DB, error) {
	driver := NormalizeDriver(cfg.Driver, cfg.DSN)
	switch driver {
	case DriverMySQL:
		return openMySQL(cfg.DSN, cfg.Schema, cfg.Pool)
	case DriverSQLite3:
		return openSQLite3(cfg.DSN, cfg.Pool, cfg.SQLite3Pragmas)
	case DriverPostgres:
		return openPostgres(cfg.DSN, cfg.Schema, cfg.Pool)
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

func openMySQL(dsn string, schema string, pool *PoolConfig) (*gorm.DB, error) {
	if schema != "" {
		rewritten, err := withMySQLDBName(dsn, schema)
		if err != nil {
			return nil, fmt.Errorf("xdb: rewrite MySQL DSN schema: %w", err)
		}
		dsn = rewritten
	}
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

func openPostgres(dsn string, schema string, pool *PoolConfig) (*gorm.DB, error) {
	// Prefer embedding the search_path into the DSN so every pooled
	// connection inherits it; ConnMaxLifetime may recycle connections and
	// a per-connection SET would be lost.
	if schema != "" {
		dsn = withPostgresSearchPath(dsn, schema)
	}
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

// ResolveSchema picks the effective schema for a service connection.
// Priority:
//   1. schema argument (passed by wire / explicit callers)
//   2. svcEnv (per-service schema env var, e.g. BILLING_SCHEMA)
//   3. fallbackEnv (global schema env var, e.g. DATABASE_SCHEMA)
// An empty result means "use the DSN as-is" (legacy shared-database
// behaviour). The returned schema is trimmed of surrounding whitespace,
// quotes and backticks so values like `oneapi_billing` work correctly.
func ResolveSchema(schema, svcEnv, fallbackEnv string) string {
	trim := func(v string) string {
		v = strings.TrimSpace(v)
		for _, c := range []string{"\"", "`", "'", " "} {
			v = strings.Trim(v, c)
		}

		return v
	}
	if schema = trim(schema); schema != "" {
		return schema
	}
	if schema = trim(os.Getenv(svcEnv)); schema != "" {
		return schema
	}
	if schema = trim(os.Getenv(fallbackEnv)); schema != "" {
		return schema
	}
	return ""
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


// ---------------------------------------------------------------------------
// Phase 2.4: per-service database schema isolation helpers.
//
// The deployment model has every backend service connecting to the same MySQL
// instance (or Postgres) with the same credentials. Schema isolation lets each
// service point at its own database/schema so a runaway migration, table-lock
// or restore blast radius is bounded to that service.
//
// Because the application uses a mix of gorm AutoMigrate-style model queries
// (`.Table("users")`, `.Model(&reservationModel{})`) and hand-written SQL
// (`.Exec("UPDATE users SET ...")`, raw FROM clauses), the only place that
// can guarantee every statement resolves the right table is the connection
// itself: MySQL via the DSN's DBName, Postgres via search_path. SQLite
// schemas are separate files and do not need a runtime remap.
// ---------------------------------------------------------------------------

// withMySQLDBName returns a copy of dsn with the database name replaced by
// schema. It parses the go-sql-driver DSN, overwrites Config.DBName, and
// reformats — this preserves auth, TLS, parseTime, charset, etc. without
// fragile string surgery. Backticks/quotes are stripped from schema because
// the MySQL DSN grammar takes a bare identifier.
func withMySQLDBName(dsn, schema string) (string, error) {
	schema = strings.Trim(schema, "`\"' ")
	if schema == "" {
		return dsn, nil
	}
	cfg, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		return dsn, err
	}
	cfg.DBName = schema
	return cfg.FormatDSN(), nil
}

// RewriteMySQLDBName is the exported form of withMySQLDBName. Callers that
// open raw *sql.DB connections (e.g. admin-api's system_options repo) use
// it to apply the configured service schema to a DSN they already hold.
func RewriteMySQLDBName(dsn, schema string) (string, error) {
	return withMySQLDBName(dsn, schema)
}

// RewritePostgresSearchPath is the exported form of withPostgresSearchPath.
func RewritePostgresSearchPath(dsn, schema string) string {
	return withPostgresSearchPath(dsn, schema)
}

// withPostgresSearchPath injects (or replaces) options=-c search_path=... into
// a Postgres DSN. Both URL (postgres://...) and key=value forms are accepted.
//
// Behaviour for existing options:
//   - URL form (postgres://...?options=...): the entire options value is
//     replaced with one containing our search_path. Postgres only keeps the
//     last options= parameter, so a caller-supplied search_path is overridden.
//   - key=value form (host=... options='...'): the search_path setting is
//     appended to the existing options string. The last "-c search_path=..."
//     inside options wins, so our appended value takes precedence.
//
// This means callers cannot override the service schema via a raw options=
// value in the DSN; the per-service schema always takes precedence.
func withPostgresSearchPath(dsn, schema string) string {
	schema = strings.Trim(schema, "\"' ")
	if schema == "" {
		return dsn
	}
	value := "-c search_path=" + schema
	// URL form: postgres://user:pw@host:5432/db?sslmode=disable
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		if i := strings.IndexByte(dsn, '?'); i >= 0 {
			base, q := dsn[:i+1], dsn[i+1:]
			vals := parsePostgresQueryString(q)
			vals = setPostgresOption(vals, "options", value)
			return base + encodePostgresValues(vals)
		}
		return dsn + "?options=" + url.QueryEscape(value)
	}
	// key=value form: host=... dbname=...
	if existing := postGresKVValue(dsn, "options"); existing != "" {
		// Replace just the options token; append our search_path.
		newOpts := existing + " " + value
		return replacePostgresKV(dsn, "options", newOpts)
	}
	if strings.TrimSpace(dsn) == "" {
		return ""
	}
	if strings.HasSuffix(strings.TrimSpace(dsn), "=") {
		return dsn + value
	}
	return dsn + " options='" + value + "'"
}

// parsePostgresQueryString splits a URL query string (after '?') into an
// ordered slice of {key, value} pairs. Repeated keys preserve order so we
// never silently drop an existing option.
func parsePostgresQueryString(q string) []pgKV {
	var out []pgKV
	for _, pair := range strings.Split(q, "&") {
		if pair == "" {
			continue
		}
		k, v, _ := strings.Cut(pair, "=")
		key, err := url.QueryUnescape(k)
		if err != nil {
			key = k
		}
		val, err := url.QueryUnescape(v)
		if err != nil {
			val = v
		}
		out = append(out, pgKV{key: key, value: val})
	}
	return out
}

type pgKV struct {
	key   string
	value string
}

// setPostgresOption ensures the first occurrence of key has the given value
// and drops later duplicates, so an existing options=... is replaced rather
// than doubled.
func setPostgresOption(vals []pgKV, key, value string) []pgKV {
	set := false
	out := make([]pgKV, 0, len(vals))
	for _, kv := range vals {
		if kv.key == key {
			if !set {
				out = append(out, pgKV{key: key, value: value})
				set = true
			}
			continue
		}
		out = append(out, kv)
	}
	if !set {
		out = append(out, pgKV{key: key, value: value})
	}
	return out
}

// encodePostgresValues re-encodes the ordered pairs as a URL query string.
func encodePostgresValues(vals []pgKV) string {
	parts := make([]string, 0, len(vals))
	for _, kv := range vals {
		parts = append(parts, url.QueryEscape(kv.key)+"="+url.QueryEscape(kv.value))
	}
	return strings.Join(parts, "&")
}

// postGresKVValue returns the value of key in a whitespace-separated
// key=value DSN. Quoted values ("foo bar" or 'foo bar') are honoured. Returns
// "" when the key is absent.
func postGresKVValue(dsn, key string) string {
	for _, kv := range splitPostgresKV(dsn) {
		if kv.key == key {
			return kv.value
		}
	}
	return ""
}

// replacePostgresKV returns dsn with the first occurrence of key replaced by
// key=value. When key is absent the pair is appended.
func replacePostgresKV(dsn, key, value string) string {
	parts := splitPostgresKV(dsn)
	out := make([]pgKV, 0, len(parts))
	set := false
	for _, kv := range parts {
		if kv.key == key {
			if !set {
				out = append(out, pgKV{key: key, value: value})
				set = true
			}
			continue
		}
		out = append(out, kv)
	}
	if !set {
		out = append(out, pgKV{key: key, value: value})
	}
	var b strings.Builder
	for i, kv := range out {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(kv.key)
		b.WriteByte('=')
		if strings.ContainsAny(kv.value, " \t") {
			b.WriteByte('\'')
			b.WriteString(kv.value)
			b.WriteByte('\'')
		} else {
			b.WriteString(kv.value)
		}
	}
	return b.String()
}

// splitPostgresKV tokenises a Postgres key=value DSN. It understands
// single- and double-quoted values; unquoted values run until the next
// whitespace. Pairs without an '=' (rare, malformed) are dropped.
//
// Note: an unterminated quoted value is consumed until the end of the string
// rather than returning an error. This keeps the DSN rewrite best-effort;
// malformed connection strings will surface later when the driver parses them.
func splitPostgresKV(dsn string) []pgKV {
	var out []pgKV
	i := 0
	for i < len(dsn) {
		// Skip whitespace between pairs.
		for i < len(dsn) && (dsn[i] == ' ' || dsn[i] == '\t') {
			i++
		}
		if i >= len(dsn) {
			break
		}
		// Read key up to '='.
		keyStart := i
		for i < len(dsn) && dsn[i] != '=' && dsn[i] != ' ' && dsn[i] != '\t' {
			i++
		}
		if i >= len(dsn) || dsn[i] != '=' {
			// No '=' for this token — skip it.
			for i < len(dsn) && dsn[i] != ' ' && dsn[i] != '\t' {
				i++
			}
			continue
		}
		key := dsn[keyStart:i]
		i++ // consume '='
		// Read value — quoted or bare.
		var value strings.Builder
		if i < len(dsn) && (dsn[i] == '\'' || dsn[i] == '"') {
			quote := dsn[i]
			i++
			for i < len(dsn) && dsn[i] != quote {
				value.WriteByte(dsn[i])
				i++
			}
			if i < len(dsn) {
				i++ // closing quote
			}
		} else {
			for i < len(dsn) && dsn[i] != ' ' && dsn[i] != '\t' {
				value.WriteByte(dsn[i])
				i++
			}
		}
		out = append(out, pgKV{key: key, value: value.String()})
	}
	return out
}
