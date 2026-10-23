package biz

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"micro-one-api/pkg/jsonx"
)

func retrospectiveCount(value int64) *int64 { return &value }

func retrospectiveFixture() (VendorManifest, []VendorBillRow) {
	m, rows, _ := vendorFixture()
	m.Scope.Currency = "CNY"
	m.Scope.Model = "deepseek-flash"
	m.CostBasis = "provider_reported"
	m.Complete = false
	m.LocalWatermark = time.Time{}
	m.VendorWatermark = time.Time{}
	m.CoverageEvidenceID = ""
	amount := "0.000044"
	rows = []VendorBillRow{{Amount: &amount}}
	e := &VendorRetrospectiveEvidence{
		ReportID: VendorReportID(m, rows), SupplierEvidenceID: m.EvidenceID, Scope: m.Scope,
		SourceKind: m.Mappings[0].SourceKind, SourceID: m.Mappings[0].SourceID, Model: m.Scope.Model,
		MappingRevision: m.Mappings[0].Revision, MappingEvidenceID: m.Mappings[0].EvidenceID,
		SnapshotEvidenceID: "sha256:" + strings.Repeat("1", 64), RecordsEvidenceID: "sha256:" + strings.Repeat("2", 64), TariffEvidenceID: "sha256:" + strings.Repeat("3", 64), ExternalEvidenceID: "sha256:" + strings.Repeat("4", 64),
		TariffWindows: []VendorRetrospectiveTariffWindow{
			{Start: m.Scope.Start, End: m.Scope.Start.Add(12 * time.Hour), Band: "off_peak", UncachedInputRate: "1", CacheReadRate: "0.02", OutputRate: "4"},
			{Start: m.Scope.Start.Add(12 * time.Hour), End: m.Scope.End, Band: "peak", UncachedInputRate: "2", CacheReadRate: "0.04", OutputRate: "8"},
		},
		Records: []VendorRetrospectiveRecord{
			{ID: "1", Purpose: "user", TimeBasis: "request_handler_and_settlement_interval", StartedAt: m.Scope.Start.Add(time.Hour), FinishedAt: m.Scope.Start.Add(time.Hour + time.Second), EndExclusive: m.Scope.Start.Add(time.Hour + 2*time.Second), UncachedInputTokens: retrospectiveCount(10), CacheReadTokens: retrospectiveCount(100), OutputTokens: retrospectiveCount(2)},
			{ID: "2", Purpose: "internal_test", TimeBasis: "external_direct_call_interval", StartedAt: m.Scope.Start.Add(13 * time.Hour), FinishedAt: m.Scope.Start.Add(13*time.Hour + time.Second), EndExclusive: m.Scope.Start.Add(13*time.Hour + 2*time.Second), UncachedInputTokens: retrospectiveCount(8), CacheReadTokens: retrospectiveCount(0), OutputTokens: retrospectiveCount(1)},
		},
	}
	m.RetrospectiveEvidence = e
	return m, rows
}

func TestVendorRetrospectiveExactArithmeticAndProvenance(t *testing.T) {
	m, rows := retrospectiveFixture()
	before, err := jsonx.Marshal(m.RetrospectiveEvidence)
	require.NoError(t, err)
	result, err := ReplayVendorRetrospective(m, rows)
	require.NoError(t, err)
	require.Equal(t, "retrospective_tariff_recomputation", result.Basis)
	require.Equal(t, "operator_submitted_archive", result.Provenance)
	require.False(t, result.ExecutionJoinVerified)
	require.True(t, result.OwnerArithmeticReplayed)
	require.Equal(t, VendorRetrospectiveRevision, result.Revision)
	require.Equal(t, VendorReportID(m, rows), result.ReportID)
	require.Regexp(t, `^sha256:[0-9a-f]{64}$`, result.EvidenceID)
	require.Equal(t, "CNY", result.Currency)
	require.Equal(t, "currency", result.Unit)
	require.Equal(t, "0.000044", result.Amount)
	require.Equal(t, "0.000044", result.SupplierAmount)
	require.Equal(t, "0", result.Difference)
	require.Equal(t, "arithmetic_agrees", result.ArithmeticStatus)
	require.Equal(t, 2, result.Records)
	require.Equal(t, VendorRetrospectiveTotals{Records: 2, UncachedInputTokens: 18, CacheReadTokens: 100, OutputTokens: 3, Amount: "0.000044"}, result.Totals)
	require.Equal(t, "0.00002", result.ByPurpose["user"].Amount)
	require.Equal(t, "0.000024", result.ByPurpose["internal_test"].Amount)
	require.Equal(t, result.ByPurpose["user"], result.ByBand["off_peak"])
	require.Equal(t, result.ByPurpose["internal_test"], result.ByBand["peak"])
	after, err := jsonx.Marshal(m.RetrospectiveEvidence)
	require.NoError(t, err)
	require.Equal(t, before, after, "replay must not mutate the submitted archive")
	replayed, err := ReplayVendorRetrospective(m, rows)
	require.NoError(t, err)
	require.Equal(t, result, replayed)
}

func TestVendorRetrospectiveDoesNotPromoteOwnerFacts(t *testing.T) {
	m, rows := retrospectiveFixture()
	without := m
	without.RetrospectiveEvidence = nil
	baseline, err := ReconcileVendor(without, rows, nil)
	require.NoError(t, err)
	with, err := ReconcileVendor(m, rows, nil)
	require.NoError(t, err)
	require.Equal(t, baseline.ID, with.ID, "local supplementation preserves the supplier identity")
	require.Equal(t, "pending", with.Status)
	require.Equal(t, "none", with.VerificationScope)
	require.Equal(t, "unknown", with.Comparisons[0].AmountStatus)
	require.Zero(t, with.UserReservations)
	require.Zero(t, with.InternalAttempts)
	require.Empty(t, with.Attempts)
	require.NotNil(t, with.RetrospectiveResult)
	require.Equal(t, "arithmetic_agrees", with.RetrospectiveResult.ArithmeticStatus)
	with.Manifest.RetrospectiveEvidence = nil
	with.RetrospectiveResult = nil
	require.Equal(t, baseline, with, "all ordinary owner facts must be unchanged")
}

func TestVendorRetrospectiveOmittedUsageIsNotAnObservedZero(t *testing.T) {
	for _, bucket := range []string{"uncached", "cache", "output"} {
		t.Run(bucket, func(t *testing.T) {
			m, rows := retrospectiveFixture()
			switch bucket {
			case "uncached":
				m.RetrospectiveEvidence.Records[0].UncachedInputTokens = nil
			case "cache":
				m.RetrospectiveEvidence.Records[0].CacheReadTokens = nil
			case "output":
				m.RetrospectiveEvidence.Records[0].OutputTokens = nil
			}
			_, err := ReplayVendorRetrospective(m, rows)
			require.ErrorContains(t, err, "all three explicitly observed")
		})
	}
	m, rows := retrospectiveFixture()
	for i := range m.RetrospectiveEvidence.Records {
		record := &m.RetrospectiveEvidence.Records[i]
		record.UncachedInputTokens, record.CacheReadTokens, record.OutputTokens = retrospectiveCount(0), retrospectiveCount(0), retrospectiveCount(0)
	}
	result, err := ReplayVendorRetrospective(m, rows)
	require.NoError(t, err)
	require.Equal(t, "0", result.Amount)
	require.Equal(t, "0.000044", result.Difference)
	require.Equal(t, "arithmetic_differs", result.ArithmeticStatus)
}

func TestVendorRetrospectiveRejectsUnboundAndMalformedInputs(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*VendorManifest, *[]VendorBillRow)
	}{
		{"report", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.ReportID = strings.Repeat("0", 64)
		}},
		{"supplier_evidence", func(m *VendorManifest, _ *[]VendorBillRow) { m.RetrospectiveEvidence.SupplierEvidenceID = "other-bill" }},
		{"scope_account", func(m *VendorManifest, _ *[]VendorBillRow) { m.RetrospectiveEvidence.Scope.Account = "other-account" }},
		{"scope_currency", func(m *VendorManifest, _ *[]VendorBillRow) { m.RetrospectiveEvidence.Scope.Currency = "USD" }},
		{"scope_window", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.Scope.End = m.Scope.End.Add(time.Hour)
		}},
		{"model", func(m *VendorManifest, _ *[]VendorBillRow) { m.RetrospectiveEvidence.Model = "other-model" }},
		{"source_id", func(m *VendorManifest, _ *[]VendorBillRow) { m.RetrospectiveEvidence.SourceID++ }},
		{"source_kind", func(m *VendorManifest, _ *[]VendorBillRow) { m.RetrospectiveEvidence.SourceKind = "subscription" }},
		{"mapping_revision", func(m *VendorManifest, _ *[]VendorBillRow) { m.RetrospectiveEvidence.MappingRevision = "newer" }},
		{"mapping_evidence", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.MappingEvidenceID = "other-mapping"
		}},
		{"mapping_binding", func(m *VendorManifest, _ *[]VendorBillRow) { m.Mappings[0].Account = "other-account" }},
		{"mapping_span", func(m *VendorManifest, _ *[]VendorBillRow) { m.Mappings[0].Start = m.Scope.Start.Add(2 * time.Hour) }},
		{"snapshot_digest", func(m *VendorManifest, _ *[]VendorBillRow) { m.RetrospectiveEvidence.SnapshotEvidenceID = "not-a-hash" }},
		{"records_digest", func(m *VendorManifest, _ *[]VendorBillRow) { m.RetrospectiveEvidence.RecordsEvidenceID = "" }},
		{"tariff_digest", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.TariffEvidenceID = "sha256:" + strings.Repeat("A", 64)
		}},
		{"analysis_digest", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.AnalysisEvidenceID = "operator-says-verified"
		}},
		{"external_digest", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.ExternalEvidenceID = "external-call"
		}},
		{"external_provenance", func(m *VendorManifest, _ *[]VendorBillRow) { m.RetrospectiveEvidence.ExternalEvidenceID = "" }},
		{"duplicate_record", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.Records[1].ID = m.RetrospectiveEvidence.Records[0].ID
		}},
		{"missing_record_id", func(m *VendorManifest, _ *[]VendorBillRow) { m.RetrospectiveEvidence.Records[0].ID = "" }},
		{"purpose", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.Records[0].Purpose = "owner_verified_user"
		}},
		{"time_basis", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.Records[0].TimeBasis = "upstream_send_event"
		}},
		{"external_user", func(m *VendorManifest, _ *[]VendorBillRow) { m.RetrospectiveEvidence.Records[1].Purpose = "user" }},
		{"negative_usage", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.Records[0].OutputTokens = retrospectiveCount(-1)
		}},
		{"finish_before_start", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.Records[0].FinishedAt = m.Scope.Start
		}},
		{"empty_exclusive_tail", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.Records[0].EndExclusive = m.RetrospectiveEvidence.Records[0].FinishedAt
		}},
		{"outside_scope", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.Records[0].StartedAt = m.Scope.Start.Add(-time.Second)
		}},
		{"empty_records", func(m *VendorManifest, _ *[]VendorBillRow) { m.RetrospectiveEvidence.Records = nil }},
		{"too_many_records", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.Records = make([]VendorRetrospectiveRecord, 10001)
		}},
		{"too_many_windows", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.TariffWindows = make([]VendorRetrospectiveTariffWindow, 1001)
		}},
		{"no_windows", func(m *VendorManifest, _ *[]VendorBillRow) { m.RetrospectiveEvidence.TariffWindows = nil }},
		{"tariff_gap", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.TariffWindows[1].Start = m.Scope.Start.Add(12*time.Hour + time.Nanosecond)
		}},
		{"tariff_overlap", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.TariffWindows[1].Start = m.Scope.Start.Add(12*time.Hour - time.Nanosecond)
		}},
		{"tariff_missing_head", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.TariffWindows[0].Start = m.Scope.Start.Add(time.Second)
		}},
		{"tariff_missing_tail", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.TariffWindows[1].End = m.Scope.End.Add(-time.Second)
		}},
		{"negative_rate", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.TariffWindows[0].OutputRate = "-1"
		}},
		{"missing_rate", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.TariffWindows[0].CacheReadRate = ""
		}},
		{"float_exponent", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.TariffWindows[0].UncachedInputRate = "1e-7"
		}},
		{"precision_over_12", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.TariffWindows[0].UncachedInputRate = "0.0000000000001"
		}},
		{"inconsistent_band", func(m *VendorManifest, _ *[]VendorBillRow) {
			m.RetrospectiveEvidence.TariffWindows[1].Band = "off_peak"
		}},
		{"unknown_supplier_amount", func(m *VendorManifest, rows *[]VendorBillRow) {
			(*rows)[0].Amount = nil
			m.RetrospectiveEvidence.ReportID = VendorReportID(*m, *rows)
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			m, rows := retrospectiveFixture()
			test.mutate(&m, &rows)
			_, err := ReplayVendorRetrospective(m, rows)
			require.Error(t, err)
		})
	}
}

func TestVendorRetrospectiveHalfOpenBoundaryAndAmbiguity(t *testing.T) {
	m, rows := retrospectiveFixture()
	boundary := m.RetrospectiveEvidence.TariffWindows[0].End
	record := &m.RetrospectiveEvidence.Records[0]
	record.StartedAt, record.FinishedAt, record.EndExclusive = boundary.Add(-2*time.Second), boundary.Add(-time.Second), boundary
	result, err := ReplayVendorRetrospective(m, rows)
	require.NoError(t, err)
	require.Equal(t, "0.00002", result.ByBand["off_peak"].Amount)
	// The exclusive end at the boundary is safe; a single nanosecond beyond it
	// makes the complete observation interval ambiguous, even if finish is earlier.
	record.EndExclusive = boundary.Add(time.Nanosecond)
	_, err = ReplayVendorRetrospective(m, rows)
	require.ErrorContains(t, err, "crosses a tariff boundary")
	record.StartedAt, record.FinishedAt, record.EndExclusive = boundary, boundary.Add(time.Second), boundary.Add(2*time.Second)
	result, err = ReplayVendorRetrospective(m, rows)
	require.NoError(t, err)
	require.NotContains(t, result.ByBand, "off_peak")
	require.Equal(t, "0.000064", result.Amount)
	// Millisecond timestamp truncation can collapse a short handler duration.
	// The preserved exclusive upper bound still describes a nonempty interval.
	record.FinishedAt = record.StartedAt
	result, err = ReplayVendorRetrospective(m, rows)
	require.NoError(t, err)
	require.Equal(t, "0.000064", result.Amount)
}

func TestVendorRetrospectiveDirectReplayDocumentBound(t *testing.T) {
	m, rows := retrospectiveFixture()
	m.RetrospectiveEvidence.Records[0].ID = strings.Repeat("a", 2<<20)
	_, err := ReplayVendorRetrospective(m, rows)
	require.ErrorContains(t, err, "2 MiB document limit")
}

func TestVendorRetrospectiveOverflowAndExactSmallestSupportedDecimal(t *testing.T) {
	m, rows := retrospectiveFixture()
	m.RetrospectiveEvidence.Records[0].UncachedInputTokens = retrospectiveCount(math.MaxInt64)
	m.RetrospectiveEvidence.Records[1].UncachedInputTokens = retrospectiveCount(1)
	_, err := ReplayVendorRetrospective(m, rows)
	require.ErrorContains(t, err, "overflows")
	m, rows = retrospectiveFixture()
	m.RetrospectiveEvidence.Records = m.RetrospectiveEvidence.Records[:1]
	m.RetrospectiveEvidence.Records[0].UncachedInputTokens = retrospectiveCount(1)
	m.RetrospectiveEvidence.Records[0].CacheReadTokens = retrospectiveCount(0)
	m.RetrospectiveEvidence.Records[0].OutputTokens = retrospectiveCount(0)
	m.RetrospectiveEvidence.TariffWindows[0].UncachedInputRate = "0.000000000001"
	result, err := ReplayVendorRetrospective(m, rows)
	require.NoError(t, err)
	require.Equal(t, "0.000000000000000001", result.Amount)
	require.Equal(t, "0.000043999999999999", result.Difference)
}

func TestValidateVendorRetrospectiveResultRejectsForgedDerivedFacts(t *testing.T) {
	require.Error(t, ValidateVendorRetrospectiveResult(nil))
	m, rows := retrospectiveFixture()
	result, err := ReplayVendorRetrospective(m, rows)
	require.NoError(t, err)
	report := &VendorReconciliation{Manifest: m, Rows: rows, RetrospectiveResult: result}
	require.NoError(t, ValidateVendorRetrospectiveResult(report))
	for _, test := range []struct {
		name   string
		mutate func(*VendorRetrospectiveResult)
	}{
		{"amount", func(r *VendorRetrospectiveResult) { r.Amount = "0" }},
		{"difference", func(r *VendorRetrospectiveResult) { r.Difference = "1" }},
		{"status", func(r *VendorRetrospectiveResult) { r.ArithmeticStatus = "matched" }},
		{"join_verified", func(r *VendorRetrospectiveResult) { r.ExecutionJoinVerified = true }},
		{"provenance", func(r *VendorRetrospectiveResult) { r.Provenance = "owner_verified" }},
		{"purpose_total", func(r *VendorRetrospectiveResult) { r.ByPurpose["user"] = VendorRetrospectiveTotals{Records: 201} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fresh, err := ReplayVendorRetrospective(m, rows)
			require.NoError(t, err)
			test.mutate(fresh)
			report.RetrospectiveResult = fresh
			require.ErrorContains(t, ValidateVendorRetrospectiveResult(report), "does not match owner arithmetic")
		})
	}
	report.RetrospectiveResult = nil
	require.Error(t, ValidateVendorRetrospectiveResult(report))
	report.Manifest.RetrospectiveEvidence = nil
	require.NoError(t, ValidateVendorRetrospectiveResult(report))
	report.RetrospectiveResult = result
	require.Error(t, ValidateVendorRetrospectiveResult(report))
	result, err = ReplayVendorRetrospective(report.Manifest, rows)
	require.NoError(t, err)
	require.Nil(t, result)
}
