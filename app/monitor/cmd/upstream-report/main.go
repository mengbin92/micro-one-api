// upstream-report exports redacted P1 drift and coverage evidence using the monitor owner's DSN.
package main

import (
	"context"
	"flag"
	"fmt"
	"micro-one-api/app/monitor/internal/data"
	"micro-one-api/pkg/jsonx"
	"os"
	"path/filepath"
	"time"
)

func main() {
	driver := flag.String("driver", "mysql", "monitor database dialect")
	output := flag.String("out", "", "absolute new output file")
	flag.Parse()
	if !filepath.IsAbs(*output) {
		fmt.Fprintln(os.Stderr, "-out must be an absolute new file")
		os.Exit(2)
	}
	repo, err := data.NewRepositoryFromEnv(*driver)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := repo.ExportUpstreamReport(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	raw, err := jsonx.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Exported redacted P1 evidence: %s\n", *output)
}
