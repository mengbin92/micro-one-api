package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/pkg/jsonx"
)

func TestCostCLIReplaysArchiveWithoutCredentialsAndKeepsReferenceFixed(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	scope := biz.VendorScope{Provider: "openai", Account: "isolated-account", Currency: "USD", Unit: "currency", Start: start, End: end, Timezone: "UTC", Granularity: "account_period"}
	m := biz.VendorManifest{Scope: scope, Version: "CSV-1", EvidenceID: "CSV-independent", CostBasis: "provider_reported", Mode: "tokens", Complete: true, PagesComplete: true, VendorWatermark: end, LocalWatermark: end, CoverageEvidenceID: "fixed-account", AbsoluteTolerance: "0", RelativeTolerance: "0", Actor: "fixture", Reason: "offline test", Mappings: []biz.VendorSourceMapping{{SourceKind: "channel", SourceID: 1, Provider: scope.Provider, Account: scope.Account, Start: start, End: end, Revision: "mapping-1", EvidenceID: "account-binding"}}}
	zero := "0"
	rows := []biz.VendorBillRow{{Amount: &zero}}
	q := biz.VendorCostQuery{Scope: scope, CredentialBindingEvidence: "credential-binding", CSVReportID: biz.VendorReportID(m, rows), CSVTotal: "0"}
	page, _ := jsonx.Marshal(map[string]any{"data": []any{map[string]any{"start_time": start.Unix(), "end_time": end.Unix(), "results": []any{}}}, "has_more": false, "next_page": nil})
	// Hash the supplier page exactly as the fetcher does, without a network call.
	archive := biz.VendorCostEvidence{Query: q, FetchedAt: end, Pages: []biz.VendorCostPage{{Body: page}}}
	archive.Pages[0].Digest = fmt.Sprintf("%x", sha256.Sum256(page))
	for name, value := range map[string]any{"manifest.json": m, "query.json": q, "archive.json": archive} {
		raw, err := jsonx.Marshal(value)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), raw, 0600))
	}
	var csv bytes.Buffer
	require.NoError(t, biz.WriteVendorCSV(&csv, scope, rows))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "report.csv"), csv.Bytes(), 0600))
	out := filepath.Join(dir, "out.json")
	args := []string{"-query", filepath.Join(dir, "query.json"), "-csv-manifest", filepath.Join(dir, "manifest.json"), "-vendor-bill", filepath.Join(dir, "report.csv"), "-replay", filepath.Join(dir, "archive.json"), "-out", out}
	t.Setenv("VENDOR_COST_API_KEY", "")
	require.Equal(t, 0, execute(args))
	var got biz.VendorCostEvidence
	require.NoError(t, readJSON(out, &got))
	require.True(t, got.CSVMatched)
	require.True(t, got.PagesComplete)
	require.Nil(t, got.Rows[0].Buckets)
	args[len(args)-1] = filepath.Join(dir, "replayed.json")
	args[len(args)-3] = out
	require.Equal(t, 0, execute(args), "pretty archive must preserve original supplier page bytes")
	args[len(args)-1] = out
	require.Equal(t, 2, execute(args), "archive output cannot be overwritten")
}
