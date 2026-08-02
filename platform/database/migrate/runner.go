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

	"micro-one-api/pkg/safefile"

	"gopkg.in/yaml.v3"
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

	// ownershipFilter, when non-empty, restricts Apply/Status to the
	// migrations explicitly owned by the given service key (Phase 2.4
	// schema isolation). The ownership map is loaded from a YAML manifest
	// next to the migrations dir (migrations/ownership.yaml). When the
	// manifest is absent or the service key has no entry, the runner
	// applies every file as before — preserving the legacy "single shared
	// schema" behaviour.
	ownershipFilter string
	ownership       *ownershipManifest
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

	if owned := r.ownedVersions(); owned != nil {
		filtered := files[:0]
		for _, f := range files {
			if _, ok := owned[versionOf(f)]; ok {
				filtered = append(filtered, f)
			}
		}
		files = filtered
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
	if owned := r.ownedVersions(); owned != nil {
		filtered := files[:0]
		for _, f := range files {
			if _, ok := owned[versionOf(f)]; ok {
				filtered = append(filtered, f)
			}
		}
		files = filtered
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
		// Partitioning is an optional, online-maintenance operation. It has
		// prerequisites that are not valid for every fresh schema.
		if name == "phase3_partitioning.sql" {
			continue
		}
		// schema_split.sql is a reference DDL for the Phase 2.4 cutover: it
		// hardcodes the legacy `oneapi` source schema and is executed manually
		// during the split (docs/deployment.md §10.4), never as a migration.
		if name == "schema_split.sql" {
			continue
		}
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
			// SQLite has no ALTER TABLE ... ADD COLUMN IF NOT EXISTS. The
			// consolidated baseline (000_create_full_schema.sql) and the
			// numbered mirror files may both carry the same column, and the
			// sqlite migrations are written on the documented contract that
			// the runner treats "duplicate column name" as an idempotent
			// no-op (the column is already present). Scope the tolerance
			// strictly to SQLite so a genuine MySQL/Postgres error is never
			// swallowed.
			if (r.driver == "sqlite" || r.driver == "sqlite3") &&
				strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
				continue
			}
			return fmt.Errorf("exec stmt: %w\nstatement: %s", err, stmt)
		}
	}
	insertApplied := rebind("INSERT INTO schema_migrations (version) VALUES (?)", r.usePgPlaceholder())
	if _, err := tx.ExecContext(ctx, insertApplied, version); err != nil {
		return fmt.Errorf("record applied: %w", err)
	}
	return tx.Commit()
}

// splitStatements splits a SQL script on semicolons outside quoted values and
// comments. Migration DDL uses semicolons inside COMMENT string literals.
func splitStatements(sql string) []string {
	var (
		out          []string
		stmt         strings.Builder
		quote        byte
		lineComment  bool
		blockComment bool
	)
	flush := func() {
		if value := strings.TrimSpace(stmt.String()); value != "" {
			out = append(out, value)
		}
		stmt.Reset()
	}

	for i := 0; i < len(sql); i++ {
		current := sql[i]
		next := byte(0)
		if i+1 < len(sql) {
			next = sql[i+1]
		}

		if lineComment {
			if current == '\n' {
				lineComment = false
				stmt.WriteByte(current)
			}
			continue
		}
		if blockComment {
			if current == '*' && next == '/' {
				blockComment = false
				i++
			}
			continue
		}

		if quote != 0 {
			stmt.WriteByte(current)
			if current == '\\' && quote != '`' && next != 0 {
				stmt.WriteByte(next)
				i++
				continue
			}
			if current == quote {
				if next == quote {
					stmt.WriteByte(next)
					i++
					continue
				}
				quote = 0
			}
			continue
		}

		switch {
		case current == '-' && next == '-':
			lineComment = true
			i++
		case current == '#':
			lineComment = true
		case current == '/' && next == '*':
			blockComment = true
			i++
		case current == '\'' || current == '"' || current == '`':
			quote = current
			stmt.WriteByte(current)
		case current == ';':
			flush()
		default:
			stmt.WriteByte(current)
		}
	}
	flush()
	return out
}

// versionOf returns the basename without the .sql suffix.
func versionOf(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// WithOwnershipFilter restricts the runner to the migration files owned by
// the given service key (e.g. "identity", "billing"). The key is resolved
// against a YAML manifest located at <dir>/ownership.yaml; if the manifest
// is missing or the key is unknown, Apply/Status fall back to the unfiltered
// list so a partial deployment (no manifest yet) does not block startup.
//
// Phase 2.4 schema isolation: when each backend service runs migrations
// against its own schema, the manifest maps a service to the exact files
// that own the tables it cares about (plus the shared schema_migrations
// bootstrap). Files not in the manifest remain the responsibility of the
// centralised migrate job.
func (r *Runner) WithOwnershipFilter(service string) *Runner {
	r.ownershipFilter = service
	if r.ownership == nil {
		m, _ := loadOwnershipManifest(r.dir)
		r.ownership = m
	}
	return r
}

// loadOwnershipManifest reads <dir>/ownership.yaml. A missing file returns
// (nil, nil) so callers can treat the no-manifest case as "no filtering".
func loadOwnershipManifest(dir string) (*ownershipManifest, error) {
	path := filepath.Join(dir, ownershipManifestFile)
	data, err := safefile.ReadFile(path)
	if err != nil {
		// Missing manifest is the historical default — not an error.
		return nil, nil
	}
	var m ownershipManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &m, nil
}

// ownedVersions returns the set of migration versions the active service
// filter is allowed to apply. Empty set (or nil filter) means "no filter".
func (r *Runner) ownedVersions() map[string]struct{} {
	if r.ownershipFilter == "" || r.ownership == nil {
		return nil
	}
	// Shared bootstrap files are always included so a fresh per-service
	// schema still gets schema_migrations before its first owned file.
	out := make(map[string]struct{})
	for _, v := range r.ownership.Shared {
		out[v] = struct{}{}
	}
	if svc, ok := r.ownership.Services[r.ownershipFilter]; ok {
		for _, v := range svc {
			out[v] = struct{}{}
		}
	}
	return out
}

// ownershipManifest is the parsed form of migrations/ownership.yaml.
type ownershipManifest struct {
	// Shared migrations apply to every per-service schema (e.g. the
	// schema_migrations bootstrap). Listed by version (basename without
	// .sql).
	Shared []string `yaml:"shared"`
	// Services maps a service key (identity, billing, …) to the migration
	// versions that service owns.
	Services map[string][]string `yaml:"services"`
}

// ownershipManifestFile is the canonical name of the ownership manifest
// placed beside the per-dialect migrations directory.
const ownershipManifestFile = "ownership.yaml"
