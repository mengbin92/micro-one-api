// routing-backfill explicitly maps legacy identity preferences to channel IDs.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"micro-one-api/app/identity/internal/data"
	"micro-one-api/platform/database/xdb"
	"micro-one-api/platform/routingclient"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	driver := flag.String("driver", "mysql", "identity database driver")
	dsnEnv := flag.String("dsn-env", "IDENTITY_SQL_DSN", "environment variable containing identity DSN")
	schema := flag.String("schema", "", "identity-owned schema")
	endpoint := flag.String("channel-endpoint", os.Getenv("CHANNEL_GRPC_ENDPOINT"), "channel gRPC endpoint")
	apply := flag.Bool("apply", false, "commit the transaction (default writes then rolls back)")
	flag.Parse()
	dsn := os.Getenv(*dsnEnv)
	if dsn == "" {
		return fmt.Errorf("identity DSN environment variable is empty")
	}
	client, closeClient, err := routingclient.Dial(*endpoint)
	if err != nil {
		return err
	}
	defer closeClient()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	groups, err := client.ListRoutingGroups(ctx)
	if err != nil {
		return fmt.Errorf("read channel group facts failed")
	}
	db, err := xdb.Open(xdb.DatabaseConfig{Driver: *driver, DSN: dsn, Schema: *schema})
	if err != nil {
		return fmt.Errorf("open identity database failed")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	count, err := data.NewRoutingBackfillRepository(db).BackfillRoutingGroups(ctx, groups, *apply)
	if err != nil {
		return err
	}
	fmt.Printf("users_mapped=%d applied=%t\n", count, *apply)
	return nil
}
