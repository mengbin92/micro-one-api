// group-backfill rehearses or applies the channel-owned Phase B projection.
// Unlike group-audit, rehearsal uses writes and locks, then rolls back.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/app/channel/internal/data"
	"micro-one-api/pkg/jsonx"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, stdout, stderr io.Writer) int {
	f := flag.NewFlagSet("group-backfill", flag.ContinueOnError)
	f.SetOutput(stderr)
	driver := f.String("driver", "mysql", "mysql, postgres, sqlite3")
	dsnEnv := f.String("dsn-env", "GROUP_BACKFILL_DSN", "DSN environment variable; never printed")
	baseline := f.String("report", "", "completed v2 group-audit JSON file")
	identity := f.String("identity-schema", "", "schema owning users/tokens")
	options := f.String("options-schema", "", "schema providing billing price options")
	billing := f.String("billing-schema", "", "schema owning subscriptions/orders")
	apply := f.Bool("apply", false, "commit channel projection; default rehearses writes then rolls back")
	timeout := f.Duration("timeout", 2*time.Minute, "whole transaction deadline")
	if err := f.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if f.NArg() != 0 || *baseline == "" || *timeout <= 0 || os.Getenv(*dsnEnv) == "" {
		fmt.Fprintln(stderr, "report, DSN environment and positive timeout are required")
		return 2
	}
	sqlDriver := *driver
	switch sqlDriver {
	case "postgres":
		sqlDriver = "pgx"
	case "mysql", "sqlite3":
	default:
		fmt.Fprintln(stderr, "unsupported driver")
		return 2
	}
	file, err := os.Open(*baseline)
	if err != nil {
		fmt.Fprintln(stderr, "cannot open baseline")
		return 2
	}
	defer file.Close()
	var report biz.GroupAuditReport
	raw, err := io.ReadAll(io.LimitReader(file, (64<<20)+1))
	if err != nil || len(raw) > 64<<20 || jsonx.Unmarshal(raw, &report) != nil {
		fmt.Fprintln(stderr, "invalid baseline JSON")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	db, err := sql.Open(sqlDriver, os.Getenv(*dsnEnv))
	if err != nil {
		fmt.Fprintln(stderr, "cannot open database")
		return 2
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		fmt.Fprintln(stderr, "cannot begin backfill transaction")
		return 2
	}
	defer tx.Rollback()
	repo, err := data.NewRoutingGroupBackfillRepo(tx, *driver, data.GroupAuditSchemas{Identity: *identity, Options: *options, Billing: *billing})
	if err != nil {
		fmt.Fprintln(stderr, "cannot initialize backfill repositories")
		return 2
	}
	result, err := biz.NewRoutingGroupBackfillUsecase(repo).Apply(ctx, &report)
	if err != nil {
		fmt.Fprintln(stderr, "backfill rejected: verify schema, baseline freshness and candidate parity; transaction rolled back")
		return 2
	}
	if *apply {
		err = tx.Commit()
	} else {
		err = tx.Rollback()
	}
	if err != nil {
		fmt.Fprintln(stderr, "transaction completion failed; inspect database progress before retry")
		return 2
	}
	output := struct {
		Applied bool                            `json:"applied"`
		Result  *biz.RoutingGroupBackfillResult `json:"result"`
	}{*apply, result}
	if err := jsonx.NewEncoder(stdout).Encode(output); err != nil {
		fmt.Fprintln(stderr, "cannot output result; inspect database progress before retry")
		return 2
	}
	return 0
}
