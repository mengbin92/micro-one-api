// backup-worker is a host-only orchestration role. Never put its Docker or
// root-database privileges inside the public admin-api container.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"micro-one-api/domain/backup/data"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/database/xdb"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	mode := flag.String("mode", "worker", "worker, once, recover, catalog, fetch or restore")
	config := flag.String("config", "", "private worker JSON configuration")
	artifact := flag.String("artifact", "", "encrypted artifact file (fetch output or restore input)")
	jobID := flag.String("job-id", "", "external S3 recovery receipt ID for fetch")
	key := flag.String("object-key", "", "receipt object key for fetch")
	digest := flag.String("sha256", "", "receipt encrypted artifact SHA256")
	identity := flag.String("identity-file", "", "offline age identity file (restore only)")
	confirm := flag.Bool("empty-target", false, "restore into an isolated empty target only")
	flag.Parse()
	c, err := data.LoadConfig(*config)
	if err != nil {
		return err
	}
	runner := &data.Runner{Config: c}
	if *mode != "fetch" && *mode != "catalog" {
		unlock, err := runner.LockHostState()
		if err != nil {
			return err
		}
		defer unlock()
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if *mode != "worker" {
		bounded, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutMinutes)*time.Minute+3*time.Minute)
		defer cancel()
		ctx = bounded
	}
	if *mode == "restore" {
		if !*confirm || *identity == "" || *artifact == "" {
			return fmt.Errorf("isolated empty target, artifact, receipt and offline identity required")
		}
		raw, err := os.ReadFile(*identity)
		if err != nil {
			return err
		}
		keys := strings.Split(strings.TrimSpace(string(raw)), "\n")
		var secret string
		for _, v := range keys {
			if strings.HasPrefix(v, "AGE-SECRET-KEY-") {
				if secret != "" {
					return fmt.Errorf("exactly one recovery identity required")
				}
				secret = v
			}
		}
		start := time.Now()
		m, err := runner.Restore(ctx, *artifact, secret, *digest)
		if err != nil {
			return err
		}
		fmt.Printf("restored job=%s elapsed_seconds=%.3f; application writers remain stopped; owner checks required\n", m.JobID, time.Since(start).Seconds())
		return nil
	}
	store, err := data.NewObjectStore(c.S3)
	if err != nil {
		return err
	}
	if *mode == "catalog" {
		items, err := store.Catalog(ctx)
		if err != nil {
			return err
		}
		return jsonx.NewEncoder(os.Stdout).Encode(items)
	}
	if *mode == "fetch" {
		if *jobID != "" {
			receipt, err := store.Receipt(ctx, *jobID)
			if err != nil {
				return err
			}
			*key = receipt.ObjectKey
			*digest = receipt.SHA256
		}
		if err := store.Get(ctx, *key, *artifact, *digest); err != nil {
			return err
		}
		fmt.Printf("download verified sha256=%s\n", *digest)
		return nil
	}
	if *mode != "worker" && *mode != "once" && *mode != "recover" {
		return fmt.Errorf("unknown backup mode")
	}
	db, err := xdb.Open(xdb.DatabaseConfig{Driver: "mysql", DSN: os.Getenv(c.ControlDSNEnv)})
	if err != nil {
		return fmt.Errorf("backup control DB unavailable")
	}
	pool, err := db.DB()
	if err != nil {
		return err
	}
	defer pool.Close()
	repo := data.NewRepo(&data.Data{DB: db})
	for {
		_, err = repo.Lead(ctx, func(work context.Context) error {
			if *mode == "recover" {
				return runner.Recover(work)
			}
			cycle, cancel := context.WithTimeout(work, time.Duration(c.TimeoutMinutes)*time.Minute+3*time.Minute)
			defer cancel()
			return data.Tick(cycle, repo, runner, store)
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "backup tick requires attention:", err)
		}
		if *mode != "worker" {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(15 * time.Second):
		}
	}
}
