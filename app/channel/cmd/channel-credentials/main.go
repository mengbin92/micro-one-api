// Command channel-credentials audits and migrates persisted channel
// credentials. It is dry-run by default; pass -apply only after reviewing the
// record IDs and taking the database backup required by the deployment runbook.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"micro-one-api/app/channel/internal/data"
)

func main() {
	apply := flag.Bool("apply", false, "encrypt suspected plaintext credentials (default is dry-run)")
	flag.Parse()

	repo, err := data.NewRepositoryFromEnv(os.Getenv("DATABASE_DRIVER"), os.Getenv("CHANNEL_SQL_DSN"), os.Getenv("CHANNEL_SCHEMA"))
	if err != nil {
		fatal(err)
	}

	report, err := repo.MigrateCredentials(context.Background(), !*apply)
	if err != nil {
		fatal(err)
	}

	mode := "dry-run"
	if *apply {
		mode = "apply"
	}
	fmt.Printf("mode=%s\n", mode)
	printSummary("channels", report.Channels)
	printSummary("subscription_accounts", report.SubscriptionAccounts)
	for _, record := range report.SuspectedPlaintext {
		fmt.Printf("suspected_plaintext table=%s field=%s id=%d\n", record.Table, record.Field, record.ID)
	}
	for _, record := range report.Indeterminate {
		fmt.Printf("indeterminate table=%s field=%s id=%d\n", record.Table, record.Field, record.ID)
	}
}

func printSummary(name string, summary data.CredentialMigrationSummary) {
	fmt.Printf("summary=%s scanned=%d encrypted=%d suspected_plaintext=%d indeterminate=%d rewritten=%d\n",
		name, summary.Scanned, summary.Encrypted, summary.SuspectedPlaintext, summary.Indeterminate, summary.Rewritten)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "channel-credentials: %v\n", err)
	os.Exit(1)
}
