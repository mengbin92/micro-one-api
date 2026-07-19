// migrate is a tiny CLI for applying SQL migration files in this project.
//
// Usage:
//
//	migrate              apply all pending migrations
//	migrate -status      print status table without applying
//	migrate -baseline V  override the brownfield baseline cutoff (default:
//	                     022_create_schema_migrations)
//	migrate -dir PATH    override the migrations directory (default: ./migrations)
//	migrate -driver D    database driver: mysql (default) or sqlite
//
// DSN is read from MIGRATIONS_DSN, falling back to SQL_DSN. The driver is
// read from MIGRATIONS_DRIVER, falling back to SQL_DRIVER. If the driver
// is empty, it is inferred from the DSN. For MySQL the runner appends
// `multiStatements=true` automatically if missing.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/mattn/go-sqlite3"

	"micro-one-api/platform/database/migrate"
	"micro-one-api/platform/database/xdb"
)

const defaultBaseline = "022_create_schema_migrations"

func main() {
	var (
		dir       = flag.String("dir", "./migrations", "directory containing .sql migration files")
		baseline  = flag.String("baseline", defaultBaseline, "brownfield baseline cutoff version (file basename without .sql)")
		status    = flag.Bool("status", false, "print status table and exit without applying")
		driver    = flag.String("driver", "", "database driver: mysql (default) or sqlite; inferred from DSN when empty")
		ownership = flag.String("ownership", "", "restrict to migrations owned by this service key (Phase 2.4 schema isolation; see migrations/ownership.yaml)")
	)
	flag.Parse()

	dsn := pickDSN()
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "error: MIGRATIONS_DSN or SQL_DSN must be set")
		os.Exit(2)
	}
	drv := xdb.NormalizeDriver(pickDriver(*driver), dsn)

	var (
		db  *sql.DB
		err error
	)
	switch drv {
	case xdb.DriverMySQL:
		dsn = ensureMultiStatements(dsn)
		db, err = sql.Open("mysql", dsn)
	case xdb.DriverSQLite3:
		db, err = sql.Open("sqlite3", dsn)
	case xdb.DriverPostgres:
		db, err = sql.Open(xdb.PostgresDriverName, dsn)
	default:
		fmt.Fprintf(os.Stderr, "error: unsupported driver %q\n", drv)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open DB: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "error: ping DB: %v\n", err)
		os.Exit(1)
	}

	runner := migrate.New(db, *dir).WithBaseline(*baseline)
	// Pin the driver explicitly so the runner uses the right
	// tableExists query and the right bind-parameter placeholder for
	// the active dialect. Without this, the runner defaults to MySQL
	// semantics, which break on Postgres (DATABASE() missing, ? placeholders
	// rejected by pgx) and on SQLite (no information_schema, so tableExists
	// has to fall back to sqlite_master).
	runner = migrate.NewWithDriver(db, *dir, drv).WithBaseline(*baseline)
	if *ownership != "" {
		runner = runner.WithOwnershipFilter(*ownership)
	}
	ctx := context.Background()

	if *status {
		statuses, err := runner.Status(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%-50s  %s\n", "VERSION", "APPLIED")
		for _, s := range statuses {
			mark := "no"
			if s.Applied {
				mark = "yes"
			}
			fmt.Printf("%-50s  %s\n", s.Version, mark)
		}
		return
	}

	applied, err := runner.Apply(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if len(applied) > 0 {
			fmt.Fprintf(os.Stderr, "partial: applied %d migration(s) before failure: %v\n", len(applied), applied)
		}
		os.Exit(1)
	}
	if len(applied) == 0 {
		fmt.Println("nothing to apply")
		return
	}
	fmt.Printf("applied %d migration(s):\n", len(applied))
	for _, v := range applied {
		fmt.Printf("  - %s\n", v)
	}
}

func pickDSN() string {
	if v := os.Getenv("MIGRATIONS_DSN"); v != "" {
		return v
	}
	return os.Getenv("SQL_DSN")
}

func pickDriver(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv("MIGRATIONS_DRIVER"); v != "" {
		return v
	}
	return os.Getenv("SQL_DRIVER")
}

// ensureMultiStatements appends multiStatements=true to a MySQL DSN if the
// flag is missing. The runner splits SQL on top-level semicolons itself, so
// this is belt-and-braces — but real-world migrations sometimes include
// engine-specific multi-statement DDL that the driver must allow.
func ensureMultiStatements(dsn string) string {
	if strings.Contains(dsn, "multiStatements=true") {
		return dsn
	}
	if strings.Contains(dsn, "?") {
		return dsn + "&multiStatements=true"
	}
	// Some DSNs aren't URL-style at all (key=value separated by &). The
	// go-sql-driver/mysql DSN grammar uses ? then key=value. We assume the
	// standard form: user:pass@tcp(host:port)/db?params
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		q := u.Query()
		q.Set("multiStatements", "true")
		u.RawQuery = q.Encode()
		return u.String()
	}
	return dsn + "?multiStatements=true"
}
