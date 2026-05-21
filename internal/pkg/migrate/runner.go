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
)

// Runner applies SQL migration files against a database.
type Runner struct {
	db  *sql.DB
	dir string
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

// New constructs a Runner.
func New(db *sql.DB, dir string) *Runner {
	return &Runner{db: db, dir: dir}
}

// WithBaseline sets the brownfield baseline cutoff version (file basename
// without the .sql suffix) and returns the runner for fluent configuration.
func (r *Runner) WithBaseline(version string) *Runner {
	r.BaselineVersion = version
	return r
}

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

// tableExists works for both MySQL (information_schema.tables filtered by
// current database) and sqlite (sqlite_master). Try MySQL form first; on
// error fall back to sqlite_master.
func (r *Runner) tableExists(ctx context.Context, name string) (bool, error) {
	var n int
	mysqlRow := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?`,
		name)
	if err := mysqlRow.Scan(&n); err == nil {
		return n > 0, nil
	}
	// Fallback for engines without information_schema (e.g. sqlite in tests).
	sqliteRow := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
		name)
	if err := sqliteRow.Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func (r *Runner) markApplied(ctx context.Context, version string) error {
	_, err := r.db.ExecContext(ctx,
		"INSERT INTO schema_migrations (version) VALUES (?)", version)
	return err
}

// applyOne executes one migration file in a transaction. The statements in
// the file may be ;-separated; we split conservatively and execute each
// non-empty statement.
func (r *Runner) applyOne(ctx context.Context, path, version string) error {
	body, err := os.ReadFile(path)
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
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec stmt: %w\nstatement: %s", err, stmt)
		}
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations (version) VALUES (?)", version); err != nil {
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
