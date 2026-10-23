package data

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/database/testutil"
)

// Replay the public production archive through the same owner reconciliation
// and storage boundary. The archive's omitted private execution joins remain
// unverified; these rows must never become authoritative local attempts.
func TestVendorRetrospectiveArchiveAppendsImmutableRun(t *testing.T) {
	base := filepath.Join("..", "..", "..", "..", "docs", "runbooks", "evidence", "deepseek-channel11-closed-window-2026-10-23")
	manifestRaw, err := os.ReadFile(filepath.Join(base, "retrospective-owner", "manifest.json"))
	require.NoError(t, err)
	var manifest biz.VendorManifest
	require.NoError(t, biz.DecodeVendorJSON(manifestRaw, &manifest))
	csvRaw, err := os.ReadFile(filepath.Join(base, "owner-execution", "vendor-normalized.csv"))
	require.NoError(t, err)
	rows, err := biz.ReadVendorCSV(bytes.NewReader(csvRaw), manifest.Scope)
	require.NoError(t, err)
	finalRaw, err := os.ReadFile(filepath.Join(base, "retrospective-owner", "final-manifest.json"))
	require.NoError(t, err)
	var finalManifest biz.VendorManifest
	require.NoError(t, biz.DecodeVendorJSON(finalRaw, &finalManifest))
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := testutil.RoutingContextDB(t, dialect)
			repo := NewVendorReconciliationRepo(&Data{db: db})
			ctx := context.Background()
			baselineManifest := manifest
			baselineManifest.RetrospectiveEvidence = nil
			local, err := repo.LoadVendorAttempts(ctx, baselineManifest)
			require.NoError(t, err)
			baseline, err := biz.ReconcileVendor(baselineManifest, rows, local)
			require.NoError(t, err)
			require.NoError(t, repo.StoreVendorReconciliation(ctx, &baseline))
			var original vendorReportModel
			require.NoError(t, db.Where("id = ?", baseline.ID).First(&original).Error)
			var firstRun vendorRunModel
			require.NoError(t, db.Where("report_id = ?", baseline.ID).First(&firstRun).Error)

			replayed, err := biz.ReconcileVendor(manifest, rows, local)
			require.NoError(t, err)
			require.Equal(t, "2bc6d3dd046db8ee94ef5f576a1482fca3815cb9bc9e4d198f2e1a0ea5f419db", replayed.ID)
			require.Equal(t, baseline.ID, replayed.ID)
			require.Equal(t, baseline.Status, replayed.Status)
			require.Equal(t, "pending", replayed.Status)
			require.Equal(t, baseline.Comparisons, replayed.Comparisons)
			require.Equal(t, "missing_local", replayed.Comparisons[0].Status)
			require.Equal(t, "unknown", replayed.Comparisons[0].AmountStatus)
			require.Nil(t, replayed.Comparisons[0].EstimatedAmount)
			require.Equal(t, baseline.Attempts, replayed.Attempts)
			require.Equal(t, baseline.UnresolvedAttempts, replayed.UnresolvedAttempts)
			require.Zero(t, replayed.UserReservations)
			require.Zero(t, replayed.InternalAttempts)
			require.Equal(t, "none", replayed.VerificationScope)
			result := replayed.RetrospectiveResult
			require.NotNil(t, result)
			require.Equal(t, "2.04700896", result.Amount)
			require.Equal(t, "0", result.Difference)
			require.Equal(t, "arithmetic_agrees", result.ArithmeticStatus)
			require.False(t, result.ExecutionJoinVerified)
			require.True(t, result.OwnerArithmeticReplayed)
			require.Equal(t, 203, result.Records)
			require.Equal(t, 201, result.ByPurpose["user"].Records)
			require.Equal(t, "2.04696096", result.ByPurpose["user"].Amount)
			require.Equal(t, 2, result.ByPurpose["internal_test"].Records)
			require.Equal(t, "0.000048", result.ByPurpose["internal_test"].Amount)
			require.Equal(t, 106, result.ByBand["off_peak"].Records)
			require.Equal(t, 97, result.ByBand["peak"].Records)
			require.EqualValues(t, 319242, result.Totals.UncachedInputTokens)
			require.EqualValues(t, 30420736, result.Totals.CacheReadTokens)
			require.EqualValues(t, 92728, result.Totals.OutputTokens)
			require.NoError(t, repo.StoreVendorReconciliation(ctx, &replayed))
			require.NoError(t, repo.StoreVendorReconciliation(ctx, &replayed))

			for _, tamper := range []func(*biz.VendorRetrospectiveResult){
				func(r *biz.VendorRetrospectiveResult) { r.Amount = "0" },
				func(r *biz.VendorRetrospectiveResult) { r.ExecutionJoinVerified = true },
				func(r *biz.VendorRetrospectiveResult) { r.Provenance = "owner_verified" },
			} {
				forged := replayed
				copy := *result
				tamper(&copy)
				forged.RetrospectiveResult = &copy
				require.Error(t, repo.StoreVendorReconciliation(ctx, &forged))
			}
			forged := replayed
			forged.RetrospectiveResult = nil
			require.Error(t, repo.StoreVendorReconciliation(ctx, &forged))
			forged = baseline
			forged.RetrospectiveResult = result
			require.Error(t, repo.StoreVendorReconciliation(ctx, &forged))

			var reportCount, runCount int64
			require.NoError(t, db.Model(&vendorReportModel{}).Count(&reportCount).Error)
			require.NoError(t, db.Model(&vendorRunModel{}).Count(&runCount).Error)
			require.EqualValues(t, 1, reportCount)
			require.EqualValues(t, 2, runCount)
			var preserved vendorReportModel
			require.NoError(t, db.Where("id = ?", baseline.ID).First(&preserved).Error)
			require.Equal(t, original.Payload, preserved.Payload)
			var preservedRun vendorRunModel
			require.NoError(t, db.Where("id = ?", firstRun.ID).First(&preservedRun).Error)
			require.Equal(t, firstRun.Payload, preservedRun.Payload)
			latest, err := repo.ListVendorReconciliations(ctx, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), 10)
			require.NoError(t, err)
			require.Len(t, latest, 1)
			require.Equal(t, result, latest[0].RetrospectiveResult)
			raw, err := jsonx.Marshal(latest[0])
			require.NoError(t, err)
			t.Logf("report=%s reports=%d runs=%d archive_records=%d CNY=%s difference=%s status=%s bytes=%d", replayed.ID, reportCount, runCount, result.Records, result.Amount, result.Difference, replayed.Status, len(raw))

			// The maintainer's subsequent no-correction confirmation changes
			// supplier completeness. It must create a successor report/version,
			// even though its original-currency amount has not changed.
			final, err := biz.ReconcileVendor(finalManifest, rows, local)
			require.NoError(t, err)
			require.NotEqual(t, baseline.ID, final.ID)
			require.Equal(t, finalManifest.RetrospectiveEvidence.ReportID, final.ID)
			require.Equal(t, baseline.ID, final.Manifest.Supersedes)
			require.True(t, final.Manifest.Complete)
			require.Equal(t, "maintainer_confirmation", final.Manifest.FinalityAttestation.Kind)
			require.Equal(t, "pending", final.Status, "finality does not manufacture missing local coverage")
			require.Equal(t, baseline.Comparisons, final.Comparisons)
			require.Equal(t, "none", final.VerificationScope)
			require.NoError(t, repo.StoreVendorReconciliation(ctx, &final))
			require.Equal(t, "0.000000000000000000", *final.CorrectionDelta)
			require.NoError(t, repo.StoreVendorReconciliation(ctx, &final))
			require.NoError(t, db.Model(&vendorReportModel{}).Count(&reportCount).Error)
			require.NoError(t, db.Model(&vendorRunModel{}).Count(&runCount).Error)
			require.EqualValues(t, 2, reportCount)
			require.EqualValues(t, 3, runCount)
			latest, err = repo.ListVendorReconciliations(ctx, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), 10)
			require.NoError(t, err)
			require.Len(t, latest, 1)
			require.Equal(t, final.ID, latest[0].ID)
			require.NoError(t, db.Where("id = ?", baseline.ID).First(&preserved).Error)
			require.Equal(t, original.Payload, preserved.Payload)
			t.Logf("successor=%s reports=%d runs=%d finality=%s correction_delta=%s status=%s", final.ID, reportCount, runCount, final.Manifest.FinalityAttestation.Kind, *final.CorrectionDelta, final.Status)
		})
	}
}
