// vendor-cost-fetch retrieves read-only supplier cost reports. It has no
// database, billing RPC, deployment or customer-finance write capability.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/app/billing/internal/data"
	"micro-one-api/pkg/jsonx"
)

func main() { os.Exit(run()) }
func run() int {
	return execute(os.Args[1:])
}
func execute(args []string) int {
	fs := flag.NewFlagSet("vendor-cost-fetch", flag.ContinueOnError)
	queryPath := fs.String("query", "", "fixed VendorCostQuery JSON")
	manifestPath := fs.String("csv-manifest", "", "complete independently reviewed CSV manifest")
	csvPath := fs.String("vendor-bill", "", "same-scope reference CSV")
	replayPath := fs.String("replay", "", "archived cost evidence; no network or credential required")
	out := fs.String("out", "", "absolute new archive JSON path")
	if fs.Parse(args) != nil {
		return 2
	}
	fail := func(err error) int { fmt.Fprintln(os.Stderr, err); return 2 }
	var q biz.VendorCostQuery
	var m biz.VendorManifest
	if err := readJSON(*queryPath, &q); err != nil {
		return fail(err)
	}
	if err := readJSON(*manifestPath, &m); err != nil {
		return fail(err)
	}
	raw, err := readFile(*csvPath)
	if err != nil {
		return fail(err)
	}
	rows, err := biz.ReadVendorCSV(bytes.NewReader(raw), m.Scope)
	if err != nil {
		return fail(err)
	}
	if err := q.ValidateCSV(m, rows); err != nil {
		return fail(err)
	}
	if err := biz.ValidateVendorCostQuery(q, time.Now()); err != nil {
		return fail(err)
	}
	if !filepath.IsAbs(*out) {
		return fail(fmt.Errorf("absolute new output path required"))
	}
	file, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fail(err)
	}
	written := false
	defer func() {
		_ = file.Close()
		if !written {
			_ = os.Remove(*out)
		}
	}()
	var evidence biz.VendorCostEvidence
	if *replayPath != "" {
		var archive biz.VendorCostEvidence
		if err := readJSON(*replayPath, &archive); err != nil {
			return fail(err)
		}
		if archive.Query != q {
			return fail(fmt.Errorf("archive scope/reference changed"))
		}
		evidence, err = data.ReplayVendorCostPages(q, archive.Pages, archive.FetchedAt)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		key := os.Getenv("VENDOR_COST_API_KEY")
		evidence, err = data.NewVendorCostReader(data.NewVendorCostAPIData(), key).FetchCosts(ctx, q)
	}
	if err != nil {
		return fail(err)
	}
	encoded, err := jsonx.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return fail(err)
	}
	if _, err := file.Write(encoded); err != nil {
		return fail(err)
	}
	if err := file.Sync(); err != nil {
		return fail(err)
	}
	if err := file.Close(); err != nil {
		return fail(err)
	}
	written = true
	fmt.Printf("cost evidence %s; pages=%d; CSV matched=%t; account_period only\n", evidence.ID(), len(evidence.Pages), evidence.CSVMatched)
	if !evidence.CSVMatched {
		return 1
	}
	return 0
}
func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil || len(b) > 4<<20 {
		return nil, fmt.Errorf("input unavailable or too large")
	}
	return b, nil
}
func readJSON(path string, v any) error {
	b, err := readFile(path)
	if err != nil {
		return err
	}
	if err := jsonx.ValidateUnique(b); err != nil {
		return err
	}
	d := jsonx.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
