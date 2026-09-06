// group-audit inventories routing groups and effective model/source grants.
// It never runs migrations or writes to the database.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/app/channel/internal/data"
	"micro-one-api/pkg/jsonx"
)

type modelFlags []string

func (f *modelFlags) String() string         { return strings.Join(*f, ",") }
func (f *modelFlags) Set(value string) error { *f = append(*f, value); return nil }

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("group-audit", flag.ContinueOnError)
	flags.SetOutput(stderr)
	driver := flags.String("driver", "mysql", "mysql or sqlite3 (existing database snapshot)")
	dsnEnv := flags.String("dsn-env", "GROUP_AUDIT_DSN", "environment variable containing the DSN; never printed")
	baseEnv := flags.String("base-ratios-env", "", "optional environment variable containing billing base GroupRatios JSON")
	identitySchema := flags.String("identity-schema", "", "MySQL schema owning users; default DSN database")
	optionsSchema := flags.String("options-schema", "", "MySQL schema owning system_options; default DSN database")
	output := flags.String("output", "", "new report file (0600); default stdout")
	limit := flags.Int("max-probes", 100000, "maximum group/model/source authorization checks")
	timeout := flags.Duration("timeout", 2*time.Minute, "snapshot timeout")
	var models modelFlags
	flags.Var(&models, "model", "additional concrete model to probe wildcard grants (repeatable)")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *limit <= 0 || *timeout <= 0 {
		fmt.Fprintln(stderr, "invalid arguments")
		return 2
	}
	dsn := os.Getenv(*dsnEnv)
	if dsn == "" {
		fmt.Fprintln(stderr, "DSN environment variable is empty")
		return 2
	}
	if *driver != "mysql" && *driver != "sqlite3" {
		fmt.Fprintln(stderr, "supported drivers: mysql, sqlite3")
		return 2
	}
	if *driver == "sqlite3" {
		var err error
		dsn, err = readOnlySQLiteDSN(dsn)
		if err != nil {
			fmt.Fprintln(stderr, "SQLite audit requires an existing file without DSN options")
			return 2
		}
	}
	var base map[string]float64
	if *baseEnv != "" {
		if err := jsonx.Unmarshal([]byte(os.Getenv(*baseEnv)), &base); err != nil {
			fmt.Fprintln(stderr, "invalid base ratios JSON")
			return 2
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	db, err := sql.Open(*driver, dsn)
	if err != nil {
		fmt.Fprintln(stderr, "cannot open audit database")
		return 2
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	options := &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}
	if *driver == "sqlite3" {
		options.Isolation = sql.LevelSerializable
	}
	tx, err := db.BeginTx(ctx, options)
	if err != nil {
		fmt.Fprintln(stderr, "cannot begin read-only snapshot")
		return 2
	}
	defer tx.Rollback()
	channelRepo, routingRepo, inventoryRepo, err := data.NewGroupAuditRepositories(tx, *driver, data.GroupAuditSchemas{Identity: *identitySchema, Options: *optionsSchema})
	if err != nil {
		fmt.Fprintln(stderr, "cannot initialize audit repositories")
		return 2
	}
	channels := biz.NewChannelUsecase(channelRepo, nil)
	channels.SetModelRoutingRepo(routingRepo)
	report, err := biz.NewGroupAuditUsecase(inventoryRepo, channels).Run(ctx, models, base, *limit)
	if err != nil {
		// Driver errors may contain DSNs or raw data. Diagnostics from the
		// completed report are safe; connection/query failures stay generic.
		fmt.Fprintln(stderr, "audit incomplete: verify snapshot schema, query access, timeout and probe limit")
		return 2
	}
	encoded, err := jsonx.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "cannot encode audit report")
		return 2
	}
	encoded = append(encoded, '\n')
	if *output != "" {
		// Never truncate an existing baseline or follow a symlink.
		file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			fmt.Fprintln(stderr, "cannot create new audit report file")
			return 2
		}
		_, writeErr := file.Write(encoded)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			_ = os.Remove(*output)
			fmt.Fprintln(stderr, "cannot write audit report")
			return 2
		}
	} else if _, err := stdout.Write(encoded); err != nil {
		fmt.Fprintln(stderr, "cannot write audit report")
		return 2
	}
	if len(report.Issues) > 0 {
		return 1
	}
	return 0
}

// Only a path (optionally file:/path) is accepted. Rebuilding the URI prevents
// caller options such as mode=rw, _journal_mode or _txlock from writing to the
// snapshot before BeginTx. No connection pragmas or migrations are executed.
func readOnlySQLiteDSN(dsn string) (string, error) {
	path := strings.TrimPrefix(dsn, "file:")
	if strings.ContainsAny(path, "?#") || path == "" || path == ":memory:" {
		return "", fmt.Errorf("invalid snapshot path")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("snapshot is not a regular file")
	}
	return (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}).String(), nil
}
