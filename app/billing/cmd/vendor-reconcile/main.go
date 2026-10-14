// vendor-reconcile imports and replays external supplier evidence. It never
// changes a customer balance, price, financial ledger or charging allowlist.
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
	manifestPath := flag.String("manifest", "", "fixed supplier scope, watermarks, mappings and audit metadata JSON")
	vendorPath := flag.String("vendor-bill", "", "normalized supplier CSV")
	costPath := flag.String("cost-evidence", "", "complete same-scope API archive instead of supplier CSV")
	localPath := flag.String("local-attempts", "", "offline attempt evidence JSON (omitting reads billing owner DB)")
	attributionPath := flag.String("attributions", "", "reviewed signed differences keyed by report comparison key JSON")
	outPath := flag.String("out", "", "absolute new JSON report file")
	driver := flag.String("driver", "mysql", "billing database dialect: mysql, postgres, sqlite3")
	importReport := flag.Bool("import", false, "append immutable supplier import and reconciliation run to billing-owned tables")
	flag.Parse()
	fail := func(err error) int { fmt.Fprintln(os.Stderr, err); return 2 }
	if !filepath.IsAbs(*outPath) {
		return fail(fmt.Errorf("-out must be an absolute new file"))
	}
	if *importReport && *localPath != "" {
		return fail(fmt.Errorf("offline fixture replays cannot be imported as production reports"))
	}
	var manifest biz.VendorManifest
	if err := readJSON(*manifestPath, &manifest); err != nil {
		return fail(err)
	}
	if err := biz.ValidateVendorManifest(manifest); err != nil {
		return fail(err)
	}
	var rows []biz.VendorBillRow
	var costEvidence *biz.VendorCostEvidence
	var err error
	if *costPath != "" {
		if *vendorPath != "" {
			return fail(fmt.Errorf("choose CSV or cost evidence"))
		}
		var archive biz.VendorCostEvidence
		if err := readJSON(*costPath, &archive); err != nil {
			return fail(err)
		}
		replayed, err := data.ReplayVendorCostPages(archive.Query, archive.Pages, archive.FetchedAt)
		if err != nil {
			return fail(err)
		}
		if !replayed.CSVMatched || biz.CanonicalVendorScope(manifest.Scope) != biz.CanonicalVendorScope(archive.Query.Scope) || manifest.EvidenceID != replayed.ID() || manifest.CostBasis != "provider_reported" || !manifest.PagesComplete {
			return fail(fmt.Errorf("API import needs same-scope CSV agreement, original page identity and provider_reported basis"))
		}
		rows = replayed.Rows
		costEvidence = &replayed
	} else {
		raw, readErr := readBounded(*vendorPath, 32<<20)
		if readErr != nil {
			return fail(readErr)
		}
		rows, err = biz.ReadVendorCSV(bytes.NewReader(raw), manifest.Scope)
		if err != nil {
			return fail(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var attempts []biz.VendorLocalAttempt
	var repo biz.VendorReconciliationRepo
	if *localPath != "" {
		if err := readJSON(*localPath, &attempts); err != nil {
			return fail(err)
		}
	} else {
		d, err := data.NewVendorReconciliationData(*driver, os.Getenv("VENDOR_RECONCILE_DSN"), os.Getenv("VENDOR_RECONCILE_SCHEMA"))
		if err != nil {
			return fail(fmt.Errorf("billing database unavailable (set dedicated VENDOR_RECONCILE_DSN and optional VENDOR_RECONCILE_SCHEMA)"))
		}
		defer d.Close()
		repo = data.NewVendorReconciliationRepo(d)
		attempts, err = repo.LoadVendorAttempts(ctx, manifest)
		if err != nil {
			return fail(err)
		}
	}
	report, err := biz.ReconcileVendor(manifest, rows, attempts)
	if err != nil {
		return fail(err)
	}
	report.CostAPIEvidence = costEvidence
	if *attributionPath != "" {
		var attributions map[string][]biz.VendorAttribution
		if err := readJSON(*attributionPath, &attributions); err != nil {
			return fail(err)
		}
		seen := map[string]bool{}
		for i := range report.Comparisons {
			c := &report.Comparisons[i]
			items, ok := attributions[c.Key]
			if !ok {
				continue
			}
			for _, a := range items {
				if seen[a.EvidenceID] || a.Scope != manifest.Scope.Granularity {
					return fail(fmt.Errorf("reused attribution evidence or incomparable verification scope"))
				}
				seen[a.EvidenceID] = true
			}
			if err := biz.AttributeVendorDifference(c, items); err != nil {
				return fail(err)
			}
			delete(attributions, c.Key)
		}
		if len(attributions) != 0 {
			return fail(fmt.Errorf("attribution refers to an absent comparison key"))
		}
	}
	file, err := os.OpenFile(*outPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fail(err)
	}
	written := false
	defer func() {
		_ = file.Close()
		if !written {
			_ = os.Remove(*outPath)
		}
	}()
	// Reserve the output path before importing, so a typo cannot persist an
	// import while overwriting or failing to produce its reviewable report.
	if *importReport {
		if err := repo.StoreVendorReconciliation(ctx, &report); err != nil {
			return fail(err)
		}
	}
	encoded, err := jsonx.MarshalIndent(report, "", "  ")
	if err != nil {
		return fail(err)
	}
	if _, err = file.Write(encoded); err == nil {
		err = file.Sync()
	}
	if err != nil {
		return fail(err)
	}
	if err := file.Close(); err != nil {
		return fail(err)
	}
	written = true
	fmt.Printf("vendor reconciliation: %s; supplier scope: %s; report: %s\n", report.Status, report.VerificationScope, *outPath)
	if report.Status != "matched" && report.Status != "not_applicable" {
		return 1
	}
	return 0
}

func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("input exceeds limit")
	}
	return b, nil
}
func readJSON(path string, target any) error {
	b, err := readBounded(path, 32<<20)
	if err != nil {
		return err
	}
	if err := jsonx.ValidateUnique(b); err != nil {
		return err
	}
	d := jsonx.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}
