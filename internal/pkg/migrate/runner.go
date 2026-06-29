// Package migrate provides a minimal SQL migration runner with brownfield
// baseline support.
//
// Migrations live as plain .sql files in a directory. Filenames must sort in
// the order migrations should be applied — the recommended convention is a
// zero-padded numeric prefix (e.g. 000_create_core_tables.sql, 001_..., 022_...).
//
// State is tracked in a schema_migrations table created on first run.
// Migrations are applied each within a single transaction; on failure the
// transaction rolls back and that migration is NOT recorded as applied.
//
// Brownfield: if the schema_migrations table is empty but core tables
// already exist (e.g. docker-entrypoint-initdb.d already initialised the DB),
// every existing .sql file is silently registered as applied — without
// executing it — so subsequent .sql files added later can be applied
// incrementally without re-running historical DDL.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"micro-one-api/internal/pkg/safefile"
)

// rebind rewrites "?" placeholders to "$1, $2, ..." when the target
// driver is Postgres. The naive, single-pass scanner below is sufficient
// for the migration files in this project (no quoted strings, no
// dollar-quoted blocks, no ? inside line comments) but is intentionally
// defensive: we count "?" characters, not just occurrences, and we leave
// the SQL untouched for non-Postgres drivers.
func rebind(query string, pgPlaceholder bool) string {
	if !pgPlaceholder {
		return query
	}
	var sb strings.Builder
	sb.Grow(len(query) + 8)
	n := 1
	for i := 0; i < len(query); i++ {
		c := query[i]
		if c == '?' {
			fmt.Fprintf(&sb, "$%d", n)
			n++
			continue
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

// Runner applies SQL migration files against a database.
type Runner struct {
	db  *sql.DB
	dir string
	// driver is the canonical driver name (one of "mysql", "sqlite3",
	// "postgres"). It is set explicitly via NewWithDriver. An empty
	// value (legacy New constructor) means "behave like MySQL when
	// possible, fall back to sqlite_master otherwise". The driver
	// controls which table-existence query and which bind-parameter
	// placeholder the runner uses.
	driver string
	// BaselineVersion is the highest migration version that should be
	// considered already applied when the runner is invoked for the first
	// time against an existing database (brownfield). Files with version <=
	// BaselineVersion (lexicographic, .sql suffix stripped) are recorded in
	// schema_migrations without being executed. Files greater than the
	// baseline are applied normally.
	//
	// If brownfield is detected but BaselineVersion is empty, Apply returns
	// an error rather than risk re-running historical migrations.
	BaselineVersion string
}

// MigrationStatus describes one migration file and whether it has been
// recorded as applied.
type MigrationStatus struct {
	Version string
	Applied bool
}

// brownfieldMarkerTables lists tables whose presence implies the DB is an
// existing deployment that should be baseline-marked rather than re-migrated
// from scratch.
var brownfieldMarkerTables = []string{"users", "channels"}

// New constructs a Runner for the given *sql.DB. The driver is not
// auto-inferred (Go's database/sql does not expose the registered
// name of the driver bound to a *sql.DB), so the runner defaults to
// MySQL semantics with a sqlite_master fallback for tableExists —
// the historical behaviour. Callers that need SQLite3 or Postgres
// semantics should use NewWithDriver.
func New(db *sql.DB, dir string) *Runner {
	return &Runner{db: db, dir: dir, driver: ""}
}

// NewWithDriver constructs a Runner that pins a specific canonical driver
// name ("mysql", "sqlite3", or "postgres"). Useful for tests and for
// callers that want to short-circuit the inference. The driver argument
// is normalised via NormalizeDriverName.
func NewWithDriver(db *sql.DB, dir, driver string) *Runner {
	return &Runner{db: db, dir: dir, driver: NormalizeDriverName(driver)}
}

// NormalizeDriverName normalises a driver name to one of
// {"mysql", "sqlite3", "postgres"}. "postgresql" and "pgx" are aliased to
// "postgres"; the empty string is allowed and means "default to MySQL".
func NormalizeDriverName(name string) string {
	drv := strings.ToLower(strings.TrimSpace(name))
	switch drv {
	case "postgresql", "pgx":
		return "postgres"
	case "sqlite":
		return "sqlite3"
	}
	return drv
}

// WithBaseline sets the brownfield baseline cutoff version (file basename
// without the .sql suffix) and returns the runner for fluent configuration.
func (r *Runner) WithBaseline(version string) *Runner {
	r.BaselineVersion = version
	return r
}

// isPostgres reports whether the runner is bound to a Postgres driver.
// It is used to decide whether bind-parameter placeholders need to be
// rewritten from "?" to "$N".
func (r *Runner) isPostgres() bool {
	switch r.driver {
	case "postgres", "postgresql", "pgx":
		return true
	default:
		return false
	}
}

// usePgPlaceholder is the rebind trigger for migration statements.
func (r *Runner) usePgPlaceholder() bool { return r.isPostgres() }

// Apply applies all pending migrations in order and returns the list of
// migration versions that were actually executed (excludes brownfield-baselined
// entries). If any migration fails, the error is returned and the slice
// contains the versions applied before the failure.
func (r *Runner) Apply(ctx context.Context) ([]string, error) {
	if err := r.ensureSchemaMigrationsTable(ctx); err != nil {
		return nil, fmt.Errorf("ensure schema_migrations: %w", err)
	}

	files, err := r.listMigrationFiles()
	if err != nil {
		return nil, err
	}

	applied, err := r.loadAppliedSet(ctx)
	if err != nil {
		return nil, fmt.Errorf("load applied set: %w", err)
	}

	// Brownfield baseline: schema_migrations is empty but the DB has been
	// initialised some other way (e.g. docker-entrypoint-initdb.d already
	// ran all the .sql files). Mark every file whose version is <=
	// BaselineVersion as applied without executing it, so that subsequent
	// "new" migrations beyond that cutoff still get applied incrementally.
	//
	// If brownfield is detected but no BaselineVersion was configured, we
	// refuse to proceed: silently re-running CREATE TABLE on an existing
	// schema would surface as opaque "table already exists" errors and could
	// mask a real misconfiguration.
	if len(applied) == 0 {
		brownfield, err := r.detectBrownfield(ctx)
		if err != nil {
			return nil, fmt.Errorf("detect brownfield: %w", err)
		}
		if brownfield {
			if r.BaselineVersion == "" {
				return nil, fmt.Errorf("brownfield database detected (one of %v exists) but BaselineVersion is not set; refusing to re-run historical migrations", brownfieldMarkerTables)
			}
			for _, f := range files {
				v := versionOf(f)
				if v > r.BaselineVersion {
					continue
				}
				if err := r.markApplied(ctx, v); err != nil {
					return nil, fmt.Errorf("baseline mark %s: %w", v, err)
				}
				applied[v] = struct{}{}
			}
		}
	}

	executed := make([]string, 0)
	for _, f := range files {
		v := versionOf(f)
		if _, ok := applied[v]; ok {
			continue
		}
		if err := r.applyOne(ctx, f, v); err != nil {
			return executed, fmt.Errorf("apply %s: %w", v, err)
		}
		executed = append(executed, v)
	}
	return executed, nil
}

// Status returns every migration file with its applied flag, in filename order.
func (r *Runner) Status(ctx context.Context) ([]MigrationStatus, error) {
	if err := r.ensureSchemaMigrationsTable(ctx); err != nil {
		return nil, fmt.Errorf("ensure schema_migrations: %w", err)
	}
	files, err := r.listMigrationFiles()
	if err != nil {
		return nil, err
	}
	applied, err := r.loadAppliedSet(ctx)
	if err != nil {
		return nil, fmt.Errorf("load applied set: %w", err)
	}
	out := make([]MigrationStatus, 0, len(files))
	for _, f := range files {
		v := versionOf(f)
		_, ok := applied[v]
		out = append(out, MigrationStatus{Version: v, Applied: ok})
	}
	return out, nil
}

func (r *Runner) ensureSchemaMigrationsTable(ctx context.Context) error {
	const ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version VARCHAR(255) NOT NULL PRIMARY KEY,
		applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`
	_, err := r.db.ExecContext(ctx, ddl)
	return err
}

func (r *Runner) listMigrationFiles() ([]string, error) {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", r.dir, err)
	}
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".sql") {
			continue
		}
		files = append(files, filepath.Join(r.dir, name))
	}
	sort.Strings(files)
	return files, nil
}

func (r *Runner) loadAppliedSet(ctx context.Context) (map[string]struct{}, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := make(map[string]struct{})
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		set[v] = struct{}{}
	}
	return set, rows.Err()
}

func (r *Runner) detectBrownfield(ctx context.Context) (bool, error) {
	for _, name := range brownfieldMarkerTables {
		exists, err := r.tableExists(ctx, name)
		if err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}
	return false, nil
}

// tableExists returns whether a table with the given name exists in the
// current database. The query is dialect-specific when the runner was
// constructed with a known driver (NewWithDriver). When the driver is
// unknown (the legacy New constructor) we probe information_schema
// first and fall back to sqlite_master; this keeps the existing
// MySQL default while still letting the SQLite-only test suite
// (which calls New without a driver) detect the schema.
func (r *Runner) tableExists(ctx context.Context, name string) (bool, error) {
	switch r.driver {
	case "postgres", "postgresql", "pgx":
		var n int
		err := r.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1`,
			name).Scan(&n)
		if err != nil {
			return false, err
		}
		return n > 0, nil
	case "sqlite", "sqlite3":
		var n int
		err := r.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
			name).Scan(&n)
		if err != nil {
			return false, err
		}
		return n > 0, nil
	case "mysql":
		var n int
		err := r.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?`,
			name).Scan(&n)
		if err != nil {
			return false, err
		}
		return n > 0, nil
	default:
		// Unknown driver: try MySQL form, then fall back to sqlite_master.
		// This is the behaviour of the legacy New(db, dir) constructor
		// before the runner learned about Postgres. It keeps the existing
		// MySQL path working while letting SQLite tests run unmodified.
		var n int
		err := r.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?`,
			name).Scan(&n)
		if err == nil {
			return n > 0, nil
		}
		err = r.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
			name).Scan(&n)
		if err != nil {
			return false, err
		}
		return n > 0, nil
	}
}

func (r *Runner) markApplied(ctx context.Context, version string) error {
	stmt := rebind("INSERT INTO schema_migrations (version) VALUES (?)", r.usePgPlaceholder())
	_, err := r.db.ExecContext(ctx, stmt, version)
	return err
}

// applyOne executes one migration file in a transaction. The statements in
// the file may be ;-separated; we split conservatively and execute each
// non-empty statement.
func (r *Runner) applyOne(ctx context.Context, path, version string) error {
	body, err := safefile.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback() // no-op once Commit has run
	}()

	for _, stmt := range splitStatements(string(body)) {
		// Migration files use "?" placeholders; Postgres (pgx) requires
		// "$N" instead. rebind is a no-op for non-Postgres drivers.
		bound := rebind(stmt, r.usePgPlaceholder())
		if _, err := tx.ExecContext(ctx, bound); err != nil {
			return fmt.Errorf("exec stmt: %w\nstatement: %s", err, stmt)
		}
	}
	insertApplied := rebind("INSERT INTO schema_migrations (version) VALUES (?)", r.usePgPlaceholder())
	if _, err := tx.ExecContext(ctx, insertApplied, version); err != nil {
		return fmt.Errorf("record applied: %w", err)
	}
	return tx.Commit()
}

// splitStatements splits a SQL script on top-level semicolons. It strips line
// comments (-- ...) and skips empty statements. Block comments and string
// literals are NOT parsed; migrations in this project don't use them.
func splitStatements(sql string) []string {
	// Strip line comments first.
	var sb strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		idx := strings.Index(line, "--")
		if idx >= 0 {
			line = line[:idx]
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	cleaned := sb.String()

	parts := strings.Split(cleaned, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// versionOf returns the basename without the .sql suffix.
func versionOf(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
